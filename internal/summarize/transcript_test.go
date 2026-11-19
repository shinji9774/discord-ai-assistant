package summarize_test

import (
	"testing"
	"time"

	"discord-ai-assistant/internal/summarize"
	"discord-ai-assistant/internal/summarize/summarizetest"
)

// 条件: UTC の時刻を持つ 2 件の投稿（2 件目は複数行の本文）。
// 内容: 日本時間の「[日時] 表示名: 本文」を改行でつなぐ。本文の改行はそのまま残す。
func TestRenderTranscript_TwoPosts_RendersLinesInJapanTime(t *testing.T) {
	messages := []summarize.Message{
		summarizetest.Post(time.Date(2026, 11, 19, 4, 5, 0, 0, time.UTC), "佐藤", "会議は 10 時から"),
		summarizetest.Post(time.Date(2026, 11, 19, 4, 7, 0, 0, time.UTC), "鈴木", "了解です\n資料を用意します"),
	}

	got := summarize.RenderTranscript(messages)

	want := "[2026-11-19 13:05] 佐藤: 会議は 10 時から\n" +
		"[2026-11-19 13:07] 鈴木: 了解です\n資料を用意します"
	if got != want {
		t.Errorf("transcript =\n%s\nwant\n%s", got, want)
	}
}
