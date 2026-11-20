package voicesummary_test

import (
	"testing"
	"time"

	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/voicesummary"
)

// reportStartedAt は UTC で持つ録音開始の日時。見出しが日本時間に直すことを確かめるため。
var reportStartedAt = time.Date(2026, 11, 18, 5, 0, 0, 0, time.UTC)

// manifestOf は、names の順にトラック 1 から話者を並べた依頼の記録を返す。
func manifestOf(duration time.Duration, names ...string) voicesummary.Manifest {
	speakers := make([]voicesummary.Speaker, 0, len(names))
	for i, name := range names {
		speakers = append(speakers, voicesummary.Speaker{Track: i + 1, Name: name})
	}
	return voicesummary.Manifest{
		RequestID:   requestID,
		ChannelID:   "1440000000000000001",
		RequesterID: "1440000000000000002",
		StartedAt:   reportStartedAt,
		Duration:    duration,
		Speakers:    speakers,
	}
}

// 条件: 1 時間 23 分 45 秒の、2 人の録音。要約は途中で終わっていない。
// 内容: 日本時間の開始日時、「1 時間 23 分」（秒は切り捨て）、話者の一覧の見出し、空行、要約の順になる。
func TestFormatReport_CompleteSummary_HeadingThenSummary(t *testing.T) {
	m := manifestOf(1*time.Hour+23*time.Minute+45*time.Second, "たろう", "はなこ")
	summary := llm.Result{Text: "## 予定\n- たろう: 金曜に決める"}

	got := voicesummary.FormatReport(m, summary)

	want := "**録音開始**: 2026-11-18 14:00（日本時間） / **長さ**: 1 時間 23 分 / **話者**: たろう、はなこ\n\n" +
		"## 予定\n- たろう: 金曜に決める"
	if got != want {
		t.Errorf("report =\n%s\nwant\n%s", got, want)
	}
}

// 条件: 42 分の、1 人の録音。要約は出力の上限で途中で終わった（Truncated）。
// 内容: 長さは「42 分」（1 時間未満は時間を書かない）になり、要約の後に空行を挟んで途中終了の注記が続く。
func TestFormatReport_TruncatedSummaryUnderAnHour_MinutesOnlyAndNotice(t *testing.T) {
	m := manifestOf(42*time.Minute, "はなこ")
	summary := llm.Result{Text: "## 予定", Truncated: true}

	got := voicesummary.FormatReport(m, summary)

	want := "**録音開始**: 2026-11-18 14:00（日本時間） / **長さ**: 42 分 / **話者**: はなこ\n\n" +
		"## 予定\n\n" +
		"（上限に達したため、途中までの結果です）"
	if got != want {
		t.Errorf("report =\n%s\nwant\n%s", got, want)
	}
}

// 条件: ちょうど 2 時間の、2 人の録音で、発話が 1 つもない。
// 内容: 長さは「2 時間 0 分」になり、見出し、空行、「発話なし」の文の順になる（4.5）。
func TestFormatNoSpeechReport_ExactHours_HeadingThenNoSpeechMessage(t *testing.T) {
	m := manifestOf(2*time.Hour, "たろう", "はなこ")

	got := voicesummary.FormatNoSpeechReport(m)

	want := "**録音開始**: 2026-11-18 14:00（日本時間） / **長さ**: 2 時間 0 分 / **話者**: たろう、はなこ\n\n" +
		"この録音には、要約する発話がありませんでした。"
	if got != want {
		t.Errorf("report =\n%s\nwant\n%s", got, want)
	}
}

// 条件: ちょうど 2 時間の録音で、対象の話者が 1 人もいない（全員 bot）。
// 内容: 話者の欄は空にならず「なし」になり、見出し、空行、「発話なし」の文の順になる。
func TestFormatNoSpeechReport_NoTargetSpeakers_ShowsNoneAsSpeakers(t *testing.T) {
	m := manifestOf(2 * time.Hour)

	got := voicesummary.FormatNoSpeechReport(m)

	want := "**録音開始**: 2026-11-18 14:00（日本時間） / **長さ**: 2 時間 0 分 / **話者**: なし\n\n" +
		"この録音には、要約する発話がありませんでした。"
	if got != want {
		t.Errorf("report =\n%s\nwant\n%s", got, want)
	}
}
