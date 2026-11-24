package voicerecording_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/voicerecording"
)

// 仕様（設計書「設定設計」）の時間の見込みと、テストの設定の録音の上限。
const (
	specStartupAllowance = 5 * time.Minute
	specJoinTimeout      = 60 * time.Second
	specShutdownBudget   = 10 * time.Minute
	testMaxMinutes       = 180
)

const (
	startAcceptedText = "録音の開始を受け付けました。準備に 1 分ほどかかります。録音が始まったら、このチャンネルでお知らせします。"
	notInVoiceText    = "ボイスチャンネルに参加してから、もう一度 `/record-start` を実行してください。"
	activeText        = "このサーバでは録音中、または録音の終了処理中です。終わってからもう一度実行してください。"
)

// startOutcome は、StartCommand の実行で外から見える結果。
type startOutcome struct {
	Result   command.Result
	Lock     *voicerecording.Session
	Launched []map[string]string
}

func newStartCommand(f recordingFixture) *voicerecording.StartCommand {
	return voicerecording.NewStartCommand(f.deps, voicerecording.Config{MaxRecordingMinutes: testMaxMinutes})
}

// 条件: 開始者がボイスチャンネルにいて、bot の権限が足り、録音中でない
// 内容: 受付の文を返し、期限つきのロックを取り、録音タスクを 1 つ起動する
func TestStartCommandExecute_Ready_AcquiresLockAndLaunchesTask(t *testing.T) {
	f := newRecordingFixture(t)
	allowRecording(f.discord)

	result, err := newStartCommand(f).Execute(context.Background(), commandInvocation("record-start", testStarterID))

	got := startOutcome{Result: result, Lock: f.activeLock(t), Launched: f.launchedEnvironments()}
	want := startOutcome{
		Result: command.Result{Text: startAcceptedText},
		Lock: &voicerecording.Session{
			RequestID: testRequestID, GuildID: testGuildID, VoiceChannelID: testVoiceChannelID, TextChannelID: testTextChannelID,
			StarterID: testStarterID, BotUserID: testBotUserID, AcceptedAt: testAcceptedAt,
			Deadline: testAcceptedAt.Add(specStartupAllowance + specJoinTimeout + testMaxMinutes*time.Minute + specShutdownBudget),
		},
		Launched: []map[string]string{{"GUILD_ID": testGuildID, "REQUEST_ID": testRequestID}},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Execute = %+v, %v; want %+v, nil", got, err, want)
	}
}

// 条件: 開始者がボイスチャンネルにいない（Discord が Unknown Voice State を返す）
// 内容: 参加を促す案内を返し、ロックも録音タスクも作らない（1.3）
func TestStartCommandExecute_StarterNotInVoice_GuidesToJoin(t *testing.T) {
	f := newRecordingFixture(t)
	f.discord.RejectPath(http.MethodGet, "/guilds/"+testGuildID+"/voice-states/"+testStarterID, http.StatusNotFound, codeUnknownVoiceState)

	result, err := newStartCommand(f).Execute(context.Background(), commandInvocation("record-start", testStarterID))

	got := startOutcome{Result: result, Lock: f.activeLock(t), Launched: f.launchedEnvironments()}
	want := startOutcome{Result: command.Guided(notInVoiceText, "not_in_voice")}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Execute = %+v, %v; want %+v, nil", got, err, want)
	}
}

// 条件: 実行したチャンネルでファイルを添付できず、ボイスチャンネルで接続できない
// 内容: 不足している権限を場所ごとに並べた案内を返し、ロックも録音タスクも作らない（1.5）
func TestStartCommandExecute_PermissionsMissingInBothPlaces_ListsMissingPerPlace(t *testing.T) {
	f := newRecordingFixture(t)
	addVoiceState(f.discord, testStarterID, testVoiceChannelID)
	addPermissionFixtures(f.discord, discord.PermissionViewChannel|discord.PermissionConnect, discord.PermissionConnect, discord.ChannelTypeGuildVoice, testVoiceChannelID)
	inv := commandInvocation("record-start", testStarterID)
	inv.AppPermissions = discord.PermissionViewChannel | discord.PermissionSendMessages

	result, err := newStartCommand(f).Execute(context.Background(), inv)

	got := startOutcome{Result: result, Lock: f.activeLock(t), Launched: f.launchedEnvironments()}
	want := startOutcome{Result: command.Guided(
		"bot の権限が足りないため、録音を始められません（このチャンネル: ファイルを添付／ボイスチャンネル: 接続）。サーバの管理者に権限の追加を依頼してください。",
		"missing_permissions",
	)}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Execute = %+v, %v; want %+v, nil", got, err, want)
	}
}

// 条件: bot がボイスチャンネルを見られない（チャンネルの取得が Missing Access で拒まれる）
// 内容: ボイスチャンネルで「チャンネルを見る」と「接続」が足りないと案内する（1.5）
func TestStartCommandExecute_VoiceChannelHidden_ListsViewAndConnect(t *testing.T) {
	f := newRecordingFixture(t)
	addVoiceState(f.discord, testStarterID, testVoiceChannelID)
	f.discord.RejectPath(http.MethodGet, "/channels/"+testVoiceChannelID, http.StatusForbidden, codeMissingAccess)

	result, err := newStartCommand(f).Execute(context.Background(), commandInvocation("record-start", testStarterID))

	want := command.Guided(
		"bot の権限が足りないため、録音を始められません（ボイスチャンネル: チャンネルを見る、接続）。サーバの管理者に権限の追加を依頼してください。",
		"missing_permissions",
	)
	if err != nil || !reflect.DeepEqual(result, want) {
		t.Errorf("Execute = %+v, %v; want %+v, nil", result, err, want)
	}
}

// 条件: 期限内のロックがある（ほかの録音が進んでいる）
// 内容: 録音中の案内を返し、既存のロックを残し、録音タスクを起動しない（1.4）
func TestStartCommandExecute_RecordingActive_GuidesToWait(t *testing.T) {
	f := newRecordingFixture(t)
	allowRecording(f.discord)
	existing := testSession("req-0", testAcceptedAt)
	mustDo(t, f.store.Acquire(context.Background(), existing))

	result, err := newStartCommand(f).Execute(context.Background(), commandInvocation("record-start", testStarterID))

	got := startOutcome{Result: result, Lock: f.activeLock(t), Launched: f.launchedEnvironments()}
	want := startOutcome{Result: command.Guided(activeText, "recording_active"), Lock: &existing}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Execute = %+v, %v; want %+v, nil", got, err, want)
	}
}

// 条件: ECS が録音タスクの起動を断る
// 内容: エラーを返し（定型の失敗メッセージになる）、取ったロックを外す
func TestStartCommandExecute_LaunchFails_ReleasesLockAndReturnsError(t *testing.T) {
	f := newRecordingFixture(t)
	allowRecording(f.discord)
	f.ecs.RejectPlacement()

	_, err := newStartCommand(f).Execute(context.Background(), commandInvocation("record-start", testStarterID))

	if lock := f.activeLock(t); err == nil || lock != nil {
		t.Errorf("Execute error = %v, lock %+v; want error, no lock", err, lock)
	}
}

// 条件: コマンドの定義を取る
// 内容: 名前は record-start、オプションなし、サーバの中でだけ使える（1.6）
func TestStartCommandDefinition_Always_GuildOnlyWithoutOptions(t *testing.T) {
	f := newRecordingFixture(t)

	got := newStartCommand(f).Definition()

	want := definitionShape{Name: "record-start", Options: 0, Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild}}
	if shape := shapeOf(got); !reflect.DeepEqual(shape, want) || got.Description == "" {
		t.Errorf("Definition = %+v (description %q), want %+v with description", shape, got.Description, want)
	}
}

// StartCommand は返信を常に非公開にする（1.1）。
var _ command.PrivateReply = (*voicerecording.StartCommand)(nil)
