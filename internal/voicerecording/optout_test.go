package voicerecording_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/voicerecording"
)

const (
	// optOutUserID は、録音から除外を申し出る参加者。
	optOutUserID = "1800000000000000008"

	optOutAcceptedText = "録音からの除外を受け付けました。この録音の文字起こしと要約に、あなたの音声は含まれません。"
)

// optOutOutcome は、OptOutCommand の実行で外から見える結果。
type optOutOutcome struct {
	Result   command.Result
	OptedOut bool
}

func (f recordingFixture) optedOut(t *testing.T, session voicerecording.Session, userID string) bool {
	t.Helper()
	optedOut, err := f.store.OptedOut(context.Background(), session, userID)
	if err != nil {
		t.Fatalf("read optout: %v", err)
	}
	return optedOut
}

// 条件: 録音中（終了マーカーなし）に参加者が除外を申し出る
// 内容: 受付の文を返し、その参加者の除外の印を置く（4.1、4.2）
func TestOptOutCommandExecute_Recording_MarksUserOptedOut(t *testing.T) {
	f := newRecordingFixture(t)
	session := f.recording(t)

	result, err := voicerecording.NewOptOutCommand(f.deps).Execute(context.Background(), commandInvocation("record-optout", optOutUserID))

	got := optOutOutcome{Result: result, OptedOut: f.optedOut(t, session, optOutUserID)}
	want := optOutOutcome{Result: command.Result{Text: optOutAcceptedText}, OptedOut: true}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Execute = %+v, %v; want %+v, nil", got, err, want)
	}
}

// 条件: 同じ参加者が 2 回申し出る
// 内容: 2 回目も受付の文を返す（何度実行しても同じ結果。4.5）
func TestOptOutCommandExecute_Twice_AcceptsAgain(t *testing.T) {
	f := newRecordingFixture(t)
	f.recording(t)
	optOut := voicerecording.NewOptOutCommand(f.deps)
	_, err := optOut.Execute(context.Background(), commandInvocation("record-optout", optOutUserID))
	mustDo(t, err)

	result, err := optOut.Execute(context.Background(), commandInvocation("record-optout", optOutUserID))

	if want := (command.Result{Text: optOutAcceptedText}); err != nil || !reflect.DeepEqual(result, want) {
		t.Errorf("Execute = %+v, %v; want %+v, nil", result, err, want)
	}
}

// 条件: サーバで録音していない（ロックが無い）
// 内容: 録音していない案内を返す（4.6）
func TestOptOutCommandExecute_NoRecording_GuidesNotRecording(t *testing.T) {
	f := newRecordingFixture(t)

	result, err := voicerecording.NewOptOutCommand(f.deps).Execute(context.Background(), commandInvocation("record-optout", optOutUserID))

	if want := command.Guided(notRecordingText, "not_recording"); err != nil || !reflect.DeepEqual(result, want) {
		t.Errorf("Execute = %+v, %v; want %+v, nil", result, err, want)
	}
}

// 条件: 終了マーカーが書かれたあと（終了処理中）に申し出る
// 内容: 録音していない案内を返す（除外が文字起こしに間に合うとは言えないため。4.6）
func TestOptOutCommandExecute_AfterEndMarker_GuidesNotRecording(t *testing.T) {
	f := newRecordingFixture(t)
	session := f.recording(t)
	_, err := f.store.End(context.Background(), session, voicerecording.EndReasonStopCommand)
	mustDo(t, err)

	result, err := voicerecording.NewOptOutCommand(f.deps).Execute(context.Background(), commandInvocation("record-optout", optOutUserID))

	if want := command.Guided(notRecordingText, "not_recording"); err != nil || !reflect.DeepEqual(result, want) {
		t.Errorf("Execute = %+v, %v; want %+v, nil", result, err, want)
	}
}

// 条件: コマンドの定義を取る
// 内容: 名前は record-optout、オプションなし、サーバの中でだけ使える（1.6）
func TestOptOutCommandDefinition_Always_GuildOnlyWithoutOptions(t *testing.T) {
	f := newRecordingFixture(t)

	got := voicerecording.NewOptOutCommand(f.deps).Definition()

	want := definitionShape{Name: "record-optout", Options: 0, Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild}}
	if shape := shapeOf(got); !reflect.DeepEqual(shape, want) || got.Description == "" {
		t.Errorf("Definition = %+v (description %q), want %+v with description", shape, got.Description, want)
	}
}

// OptOutCommand は返信を常に非公開にする（4.1）。
var _ command.PrivateReply = (*voicerecording.OptOutCommand)(nil)
