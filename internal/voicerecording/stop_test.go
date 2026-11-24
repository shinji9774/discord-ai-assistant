package voicerecording_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/voicerecording"
)

const (
	// stopperID は、開始者ではない参加者。
	stopperID = "1500000000000000005"
	// otherVoiceChannelID は、録音していないボイスチャンネル。
	otherVoiceChannelID = "1700000000000000007"

	stopAcceptedText = "録音の停止を受け付けました。"
	notAllowedText   = "録音を停止できるのは、録音を開始した人と、録音中のボイスチャンネルにいる人だけです。"
	notRecordingText = "このサーバでは録音していません。"
)

// stopOutcome は、StopCommand の実行で外から見える結果。EndReason は終了マーカーの理由（無ければ空）。
type stopOutcome struct {
	Result    command.Result
	EndReason voicerecording.EndReason
}

// endReason は、録音の終了マーカーの理由を返す。無ければ空。
func (f recordingFixture) endReason(t *testing.T, session voicerecording.Session) voicerecording.EndReason {
	t.Helper()
	reason, err := f.store.Ended(context.Background(), session)
	if errors.Is(err, voicerecording.ErrNotEnded) {
		return ""
	}
	if err != nil {
		t.Fatalf("read end marker: %v", err)
	}
	return reason
}

// recording は、テストの録音のロックを置いた状態にする。
func (f recordingFixture) recording(t *testing.T) voicerecording.Session {
	t.Helper()
	session := testSession(testRequestID, testAcceptedAt)
	mustDo(t, f.store.Acquire(context.Background(), session))
	return session
}

// 条件: 録音を開始した本人が停止する（ボイスチャンネルにいなくてよい）
// 内容: 受付の文を返し、停止コマンドを理由に終了マーカーを書く（2.1）
func TestStopCommandExecute_Starter_WritesStopMarker(t *testing.T) {
	f := newRecordingFixture(t)
	session := f.recording(t)

	result, err := voicerecording.NewStopCommand(f.deps).Execute(context.Background(), commandInvocation("record-stop", testStarterID))

	got := stopOutcome{Result: result, EndReason: f.endReason(t, session)}
	want := stopOutcome{Result: command.Result{Text: stopAcceptedText}, EndReason: voicerecording.EndReasonStopCommand}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Execute = %+v, %v; want %+v, nil", got, err, want)
	}
}

// 条件: 開始者ではないが、録音中のボイスチャンネルにいる人が停止する
// 内容: 受付の文を返し、終了マーカーを書く（2.1）
func TestStopCommandExecute_ParticipantInRecordedChannel_WritesStopMarker(t *testing.T) {
	f := newRecordingFixture(t)
	session := f.recording(t)
	addVoiceState(f.discord, stopperID, testVoiceChannelID)

	result, err := voicerecording.NewStopCommand(f.deps).Execute(context.Background(), commandInvocation("record-stop", stopperID))

	got := stopOutcome{Result: result, EndReason: f.endReason(t, session)}
	want := stopOutcome{Result: command.Result{Text: stopAcceptedText}, EndReason: voicerecording.EndReasonStopCommand}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Execute = %+v, %v; want %+v, nil", got, err, want)
	}
}

// 条件: 開始者ではない人が、別のボイスチャンネルにいる、またはどこにもいない
// 内容: 停止できる人を示す案内を返し、終了マーカーを書かない（2.3）
func TestStopCommandExecute_NotStarterAndNotInRecordedChannel_GuidesWhoCanStop(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(f recordingFixture)
	}{
		{"別のボイスチャンネルにいる", func(f recordingFixture) { addVoiceState(f.discord, stopperID, otherVoiceChannelID) }},
		{"ボイスチャンネルにいない", func(f recordingFixture) {
			f.discord.RejectPath(http.MethodGet, "/guilds/"+testGuildID+"/voice-states/"+stopperID, http.StatusNotFound, codeUnknownVoiceState)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecordingFixture(t)
			session := f.recording(t)
			tc.arrange(f)

			result, err := voicerecording.NewStopCommand(f.deps).Execute(context.Background(), commandInvocation("record-stop", stopperID))

			got := stopOutcome{Result: result, EndReason: f.endReason(t, session)}
			want := stopOutcome{Result: command.Guided(notAllowedText, "not_allowed_to_stop")}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("Execute = %+v, %v; want %+v, nil", got, err, want)
			}
		})
	}
}

// 条件: サーバで録音していない（ロックが無い）
// 内容: 録音していない案内を返す（2.4）
func TestStopCommandExecute_NoRecording_GuidesNotRecording(t *testing.T) {
	f := newRecordingFixture(t)

	result, err := voicerecording.NewStopCommand(f.deps).Execute(context.Background(), commandInvocation("record-stop", testStarterID))

	if want := command.Guided(notRecordingText, "not_recording"); err != nil || !reflect.DeepEqual(result, want) {
		t.Errorf("Execute = %+v, %v; want %+v, nil", result, err, want)
	}
}

// 条件: 参加者全員の退出で、すでに終了マーカーが書かれている（終了処理中）
// 内容: 受付の文を返し、最初の理由を変えない
func TestStopCommandExecute_AlreadyEnded_AcceptsAndKeepsFirstReason(t *testing.T) {
	f := newRecordingFixture(t)
	session := f.recording(t)
	_, err := f.store.End(context.Background(), session, voicerecording.EndReasonEveryoneLeft)
	mustDo(t, err)

	result, err := voicerecording.NewStopCommand(f.deps).Execute(context.Background(), commandInvocation("record-stop", testStarterID))

	got := stopOutcome{Result: result, EndReason: f.endReason(t, session)}
	want := stopOutcome{Result: command.Result{Text: stopAcceptedText}, EndReason: voicerecording.EndReasonEveryoneLeft}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Execute = %+v, %v; want %+v, nil", got, err, want)
	}
}

// 条件: コマンドの定義を取る
// 内容: 名前は record-stop、オプションなし、サーバの中でだけ使える（1.6）
func TestStopCommandDefinition_Always_GuildOnlyWithoutOptions(t *testing.T) {
	f := newRecordingFixture(t)

	got := voicerecording.NewStopCommand(f.deps).Definition()

	want := definitionShape{Name: "record-stop", Options: 0, Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild}}
	if shape := shapeOf(got); !reflect.DeepEqual(shape, want) || got.Description == "" {
		t.Errorf("Definition = %+v (description %q), want %+v with description", shape, got.Description, want)
	}
}

// StopCommand は返信を常に非公開にする（2.2）。
var _ command.PrivateReply = (*voicerecording.StopCommand)(nil)
