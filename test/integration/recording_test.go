//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"discord-ai-assistant/test/discordfake"
	"discord-ai-assistant/test/ecsfake"
)

const (
	// recordVoiceChannelID は、実行者がいるボイスチャンネル。
	recordVoiceChannelID = "1200000000000000002"
	// recordStopInteractionID は /record-stop の interaction の ID。止める録音の依頼 ID は voiceInteractionID。
	recordStopInteractionID = "1400000000000000002"
	// botCanRecord は VIEW_CHANNEL（1<<10）と CONNECT（1<<20）の和。@everyone のロールに付ける。
	botCanRecord = "1049600"
	// voiceChannelType は Discord のチャンネルの種類のうち、サーバのボイスチャンネル（GUILD_VOICE）。
	voiceChannelType = 2
	// recorderContainer は、録音タスクのコンテナ名（cfn のタスク定義と同じ）。
	recorderContainer = "recorder"
	// recordingFailureHandled は、recordingfailure がエラーを返さなかったときの RIE の応答。
	recordingFailureHandled = "null"

	recordStartAccepted = "録音の開始を受け付けました。準備に 1 分ほどかかります。録音が始まったら、このチャンネルでお知らせします。"
	recordStopAccepted  = "録音の停止を受け付けました。"
	recordNotStarted    = "録音を始められませんでした。もう一度 `/record-start` を実行してください。"
)

// recordingLockKey は、voiceGuildID のサーバの録音のロック。
const recordingLockKey = "recordings/" + voiceGuildID + "/active.json"

// recordingEndKey は、voiceInteractionID の録音の終了マーカー。
const recordingEndKey = "recordings/" + voiceGuildID + "/" + voiceInteractionID + "/end.json"

// recordBody は署名前の /record-* の interaction の本文を作る。
// サーバのテキストチャンネルで、結果を投稿できる権限を bot に付け、実行者を voiceRequesterID にする。
func recordBody(t *testing.T, commandName, interactionID string) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(interactionBody(t, map[string]any{"name": commandName})), &body); err != nil {
		t.Fatal(err)
	}
	body["id"] = interactionID
	body["guild_id"] = voiceGuildID
	body["channel_id"] = voiceChannelID
	body["channel"] = map[string]any{"id": voiceChannelID, "type": textChannelType}
	body["app_permissions"] = botCanPostResults
	body["member"] = map[string]any{"user": map[string]any{"id": voiceRequesterID}, "permissions": "0"}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// arrangeRecordableVoiceChannel は、実行者が recordVoiceChannelID にいて、
// bot がそのチャンネルを見られて接続できるサーバを、fake の Discord に返させる。
func arrangeRecordableVoiceChannel() {
	discord.AddJSON("/guilds/"+voiceGuildID+"/voice-states/"+voiceRequesterID, map[string]any{
		"guild_id": voiceGuildID, "channel_id": recordVoiceChannelID, "user_id": voiceRequesterID, "session_id": "session",
		"deaf": false, "mute": false, "self_deaf": false, "self_mute": false, "suppress": false,
	})
	discord.AddJSON("/guilds/"+voiceGuildID, map[string]any{
		"id": voiceGuildID, "name": "it", "owner_id": "1900000000000000009",
		"roles": []map[string]any{{"id": voiceGuildID, "name": "@everyone", "permissions": botCanRecord}},
	})
	discord.AddJSON("/channels/"+recordVoiceChannelID, map[string]any{
		"id": recordVoiceChannelID, "type": voiceChannelType, "guild_id": voiceGuildID, "name": "会議",
		"permission_overwrites": []map[string]any{},
	})
	discord.AddJSON("/guilds/"+voiceGuildID+"/members/"+testApplicationID, map[string]any{
		"user":  map[string]any{"id": testApplicationID, "username": "minutes-bot", "global_name": "議事録 bot", "bot": true},
		"nick":  nil,
		"roles": []string{},
	})
}

// recordingSession は、voiceRequesterID が voiceChannelID で始めた、期限内の録音のロックの本文。
func recordingSession(t *testing.T) []byte {
	t.Helper()
	acceptedAt := time.Now().UTC()
	session, err := json.Marshal(map[string]any{
		"request_id": voiceInteractionID, "guild_id": voiceGuildID, "voice_channel_id": recordVoiceChannelID,
		"text_channel_id": voiceChannelID, "starter_id": voiceRequesterID, "bot_user_id": testApplicationID,
		"accepted_at": acceptedAt, "deadline": acceptedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

// recordingLocked は、voiceGuildID のサーバの録音のロックがあるかを返す。
func recordingLocked() bool {
	_, ok := s3F.Object(voiceBucketName, recordingLockKey)
	return ok
}

// invokeRecordingFailure は、録音タスク（guildID・requestID を上書きで渡したもの）の停止のイベントを、
// recordingfailure の RIE へ送り、応答の本文を返す。
func invokeRecordingFailure(t *testing.T, guildID, requestID string) string {
	t.Helper()
	detail, err := json.Marshal(map[string]any{
		"clusterArn": recorderClusterARN,
		"lastStatus": "STOPPED",
		"stopCode":   "TaskFailedToStart",
		"overrides": map[string]any{
			"containerOverrides": []map[string]any{{
				"name": recorderContainer,
				"environment": []map[string]string{
					{"name": "GUILD_ID", "value": guildID},
					{"name": "REQUEST_ID", "value": requestID},
				},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(events.EventBridgeEvent{
		Version:    "0",
		Source:     "aws.ecs",
		DetailType: "ECS Task State Change",
		Region:     "ap-northeast-1",
		Detail:     detail,
	})
	if err != nil {
		t.Fatal(err)
	}
	return invokeRIE(t, recordingFailureURL, payload)
}

// 条件: ボイスチャンネルにいる実行者が、bot がそのボイスチャンネルを見られて接続できるサーバのテキストチャンネルで /record-start を実行する。
// 内容: 処理中の表示と受け付けの返信が実行者だけに見える形で返り、サーバ ID と依頼 ID を渡した録音タスクが 1 つ起動され、サーバのロックが置かれる。
func TestRecordStart_InVoiceChannel_LaunchesRecorderAndHoldsLock(t *testing.T) {
	resetFakes()
	arrangeRecordableVoiceChannel()

	response := invokeIngress(t, recordBody(t, "record-start", voiceInteractionID), ingressPrivate)

	type startFacts struct {
		IngressStatus int
		IngressBody   string
		Reply         discordfake.Response
		Launches      []ecsfake.RunTaskRequest
		Locked        bool
	}
	want := startFacts{
		IngressStatus: http.StatusOK,
		IngressBody:   privateDeferredResponse,
		Reply:         discordReply(recordStartAccepted, map[string]string{}),
		Launches: []ecsfake.RunTaskRequest{{
			Cluster:        recorderClusterARN,
			TaskDefinition: recorderTaskDefinitionARN,
			LaunchType:     "FARGATE",
			Count:          1,
			Subnets:        strings.Split(recorderSubnetIDs, ","),
			SecurityGroups: []string{recorderSecurityGroupID},
			AssignPublicIP: "ENABLED",
			Overrides: []ecsfake.ContainerOverride{{
				Name:        recorderContainer,
				Environment: map[string]string{"GUILD_ID": voiceGuildID, "REQUEST_ID": voiceInteractionID},
			}},
		}},
		Locked: true,
	}
	got := startFacts{
		IngressStatus: response.StatusCode,
		IngressBody:   response.Body,
		Reply:         waitForDiscordResponse(t),
		Launches:      ecsF.Requests(),
		Locked:        recordingLocked(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("開始 = %+v,\nwant %+v", got, want)
	}
}

// 条件: 録音中のサーバで、録音を始めた人が /record-stop を実行する。
// 内容: 処理中の表示と受け付けの返信が実行者だけに見える形で返り、録音の終了マーカーが置かれる（ロックは録音タスクが解除するので残る）。
func TestRecordStop_ByStarter_WritesEndMarker(t *testing.T) {
	resetFakes()
	arrangeRecordableVoiceChannel()
	s3F.PutObject(voiceBucketName, recordingLockKey, recordingSession(t))

	response := invokeIngress(t, recordBody(t, "record-stop", recordStopInteractionID), ingressPrivate)

	type stopFacts struct {
		IngressStatus int
		IngressBody   string
		Reply         discordfake.Response
		StoredKeys    []string
	}
	want := stopFacts{
		IngressStatus: http.StatusOK,
		IngressBody:   privateDeferredResponse,
		Reply:         discordReply(recordStopAccepted, map[string]string{}),
		StoredKeys:    []string{recordingEndKey, recordingLockKey}, // 昇順（依頼 ID の数字は "active" より前）
	}
	got := stopFacts{
		IngressStatus: response.StatusCode,
		IngressBody:   response.Body,
		Reply:         waitForDiscordResponse(t),
		StoredKeys:    s3F.Keys(voiceBucketName),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("停止 = %+v,\nwant %+v", got, want)
	}
}

// 条件: /record-start で起動した録音タスクが、録音を始める前に止まった（起動の失敗）。
// 内容: recordingfailure が、録音を始めたチャンネルに、開始者へのメンションつきで 7.1 の文を投稿し、ロックを解除する。
func TestRecordingFailure_StoppedBeforeStart_PostsNotStartedAndReleasesLock(t *testing.T) {
	resetFakes()
	arrangeRecordableVoiceChannel()
	invokeIngress(t, recordBody(t, "record-start", voiceInteractionID), ingressPrivate)
	waitForDiscordResponse(t)

	response := invokeRecordingFailure(t, voiceGuildID, voiceInteractionID)

	type failureFacts struct {
		Response string
		Posts    []discordfake.ChannelMessage
		Locked   bool
	}
	want := failureFacts{
		Response: recordingFailureHandled,
		Posts: []discordfake.ChannelMessage{{
			ChannelID:      voiceChannelID,
			Authorization:  "Bot " + botTokenValue,
			Content:        "<@" + voiceRequesterID + ">\n" + recordNotStarted,
			AllowedUserIDs: []string{voiceRequesterID},
			Files:          map[string]string{},
		}},
		Locked: false,
	}
	got := failureFacts{Response: response, Posts: discord.ChannelMessages(), Locked: recordingLocked()}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("後始末 = %+v,\nwant %+v", got, want)
	}
}
