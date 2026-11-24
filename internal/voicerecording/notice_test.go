package voicerecording_test

import (
	"reflect"
	"testing"
	"time"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/voicerecording"
)

// 条件: 4 つの終了の理由と、上限 180 分
// 内容: 理由ごとの日本語の表記を返し、上限への到達には分を入れる
func TestEndReasonLabel_EachReason_ReturnsJapaneseLabel(t *testing.T) {
	const maxDuration = 180 * time.Minute
	cases := []struct {
		reason voicerecording.EndReason
		want   string
	}{
		{voicerecording.EndReasonStopCommand, "停止コマンド"},
		{voicerecording.EndReasonEveryoneLeft, "参加者全員の退出"},
		{voicerecording.EndReasonTimeLimit, "録音の上限（180 分）への到達"},
		{voicerecording.EndReasonDisconnected, "ボイスチャンネルからの切断"},
	}
	for _, tc := range cases {
		t.Run(string(tc.reason), func(t *testing.T) {
			got := voicerecording.EndReasonLabel(tc.reason, maxDuration)

			if got != tc.want {
				t.Errorf("EndReasonLabel(%s) = %q, want %q", tc.reason, got, tc.want)
			}
		})
	}
}

// 条件: 録音の開始・終了・失敗の各場面
// 内容: 録音を開始したテキストチャンネルへ、開始した人に通知して、場面ごとの文言を投稿する
func TestNotices_EachSituation_PostsToTextChannelNotifyingStarter(t *testing.T) {
	session := testSession(testRequestID, testAcceptedAt)
	cases := []struct {
		name     string
		notice   func(voicerecording.Session) reply.ChannelPost
		wantText string
	}{
		{
			"開始",
			voicerecording.StartNotice,
			"ボイスチャンネル <#" + testVoiceChannelID + "> の録音を開始しました（開始: <@" + testStarterID + ">）。自分の音声を録音から除きたい人は `/record-optout` を実行してください。",
		},
		{
			"終了（対象の音声あり）",
			func(s voicerecording.Session) reply.ChannelPost {
				return voicerecording.EndNotice(s, "停止コマンド", true)
			},
			"録音を終了しました（理由: 停止コマンド）。文字起こしと要約ができたら、このチャンネルに投稿します。",
		},
		{
			"終了（対象の音声なし）",
			func(s voicerecording.Session) reply.ChannelPost {
				return voicerecording.EndNotice(s, "参加者全員の退出", false)
			},
			"録音を終了しました（理由: 参加者全員の退出）。文字起こしの対象になる音声がなかったため、要約は投稿しません。",
		},
		{
			"開始の投稿の前に失敗",
			func(s voicerecording.Session) reply.ChannelPost { return voicerecording.FailureNotice(s, false) },
			"録音を始められませんでした。もう一度 `/record-start` を実行してください。",
		},
		{
			"開始の投稿の後に失敗",
			func(s voicerecording.Session) reply.ChannelPost { return voicerecording.FailureNotice(s, true) },
			"録音が失敗しました。この録音の文字起こしと要約は行いません。",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.notice(session)

			want := reply.ChannelPost{ChannelID: testTextChannelID, NotifyUserID: testStarterID, Result: command.Result{Text: tc.wantText}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v,\nwant %+v", got, want)
			}
		})
	}
}
