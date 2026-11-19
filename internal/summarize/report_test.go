package summarize_test

import (
	"testing"
	"time"

	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/summarize"
	"discord-ai-assistant/internal/summarize/summarizetest"
)

const reportMaxMessages = 1000

// reportPeriod は UTC で持つ期間。見出しが日本時間に直すことを確かめるため。
var reportPeriod = summarize.Period{
	Since: time.Date(2026, 11, 18, 5, 0, 0, 0, time.UTC),
	Until: time.Date(2026, 11, 19, 5, 0, 0, 0, time.UTC),
}

// twoPosts は 2 件の会話。
func twoPosts() summarize.Conversation {
	return summarize.Conversation{Messages: []summarize.Message{
		summarizetest.Post(time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC), "佐藤", "会議は 10 時から"),
		summarizetest.Post(time.Date(2026, 11, 18, 6, 1, 0, 0, time.UTC), "鈴木", "了解です"),
	}}
}

// 条件: 打ち切りも途中終了もない要約。
// 内容: 日本時間の期間と件数の見出し、空行、要約の順になる。
func TestFormatReport_CompleteSummary_HeadingThenSummary(t *testing.T) {
	summary := llm.Result{Text: "## 会議\n- 佐藤: 10 時から"}

	got := summarize.FormatReport(reportPeriod, twoPosts(), summary, reportMaxMessages)

	want := "**対象期間**: 2026-11-18 14:00 〜 2026-11-19 14:00（日本時間） / **メッセージ**: 2 件\n\n" +
		"## 会議\n- 佐藤: 10 時から"
	if got != want {
		t.Errorf("report =\n%s\nwant\n%s", got, want)
	}
}

// 条件: 上限で古い投稿を除き（Clipped）、要約も出力の上限で途中終了した（Truncated）。
// 内容: 要約の後に、打ち切りの注記、途中終了の注記の順で空行を挟んで続く。
func TestFormatReport_ClippedAndTruncated_AppendsBothNotices(t *testing.T) {
	conversation := twoPosts()
	conversation.Clipped = true
	summary := llm.Result{Text: "## 会議", Truncated: true}

	got := summarize.FormatReport(reportPeriod, conversation, summary, reportMaxMessages)

	want := "**対象期間**: 2026-11-18 14:00 〜 2026-11-19 14:00（日本時間） / **メッセージ**: 2 件\n\n" +
		"## 会議\n\n" +
		"（上限の 1000 件を超えたため、古い会話は要約に含まれていません）\n\n" +
		"（上限に達したため、途中までの結果です）"
	if got != want {
		t.Errorf("report =\n%s\nwant\n%s", got, want)
	}
}
