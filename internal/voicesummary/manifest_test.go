package voicesummary_test

import (
	"testing"

	"discord-ai-assistant/internal/voicesummary"
)

// requestID は依頼 ID の例。interaction ID と同じく 10 進の snowflake。
const requestID = "1441234567890123456"

// parsedJobName は requestIDOf の 2 つの戻り値。まとめて比べるために使う。
type parsedJobName struct {
	requestID string
	ok        bool
}

// 条件: 依頼 ID とトラック番号。
// 内容: "summarize-voice-{依頼 ID}-{トラック番号}" になる。
func TestJobName_RequestIDAndTrack_JoinsAfterPrefix(t *testing.T) {
	got := voicesummary.JobName(requestID, 3)

	if want := "summarize-voice-1441234567890123456-3"; got != want {
		t.Errorf("JobName = %q, want %q", got, want)
	}
}

// 条件: jobName で作ったジョブ名（トラック番号は 1 桁と 2 桁）。
// 内容: requestIDOf は元の依頼 ID を返し、ok は true になる。
func TestRequestIDOf_NameFromJobName_ReturnsRequestID(t *testing.T) {
	tests := []struct {
		name  string
		track int
	}{
		{"single digit track", 1},
		{"two digit track", 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := voicesummary.JobName(requestID, tt.track)

			gotID, gotOK := voicesummary.RequestIDOf(name)

			got, want := parsedJobName{gotID, gotOK}, parsedJobName{requestID, true}
			if got != want {
				t.Errorf("RequestIDOf(%q) = %+v, want %+v", name, got, want)
			}
		})
	}
}

// 条件: この機能のジョブ名の規則に合わない名前（ほかの接頭辞、依頼 ID やトラック番号の欠け、数字でないトラック番号）。
// 内容: 依頼 ID は空、ok は false になる。
func TestRequestIDOf_OtherName_ReturnsNotOK(t *testing.T) {
	tests := []struct {
		name    string
		jobName string
	}{
		{"empty", ""},
		{"other feature", "summarize-text-1441234567890123456-1"},
		{"prefix only", "summarize-voice-"},
		{"no track", "summarize-voice-1441234567890123456"},
		{"empty request id", "summarize-voice--1"},
		{"empty track", "summarize-voice-1441234567890123456-"},
		{"letter track", "summarize-voice-1441234567890123456-a"},
		{"signed track", "summarize-voice-1441234567890123456-+1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotOK := voicesummary.RequestIDOf(tt.jobName)

			if got := (parsedJobName{gotID, gotOK}); got != (parsedJobName{}) {
				t.Errorf("RequestIDOf(%q) = %+v, want %+v", tt.jobName, got, parsedJobName{})
			}
		})
	}
}
