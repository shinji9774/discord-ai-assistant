package voicesummary_test

import (
	"testing"

	"discord-ai-assistant/internal/voicesummary"
)

// transcribeLimitMinutes は、Amazon Transcribe が扱える音声の長さ（8 時間）。
const transcribeLimitMinutes = 8 * 60

// validConfig は、config.yaml の初期値と同じ形の、問題のない設定。
func validConfig() voicesummary.Config {
	return voicesummary.Config{
		CraigBaseURL:        "https://craig.horse",
		MaxRecordingMinutes: 180,
		MaxOutputTokens:     4096,
		SystemPrompt:        "文字起こしを要約してください。",
	}
}

// 条件: craig_base_url だけを変える（http / https の絶対 URL、空、scheme なし、ほかの scheme、host なし、解釈できない URL）。
// 内容: http か https で host を持つ URL だけを受け入れ、それ以外はキーを名指ししたエラーになる。
func TestValidate_CraigBaseURL_AcceptsOnlyAbsoluteHTTPURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"https", "https://craig.horse", "<nil>"},
		{"http with port", "http://localhost:8080", "<nil>"},
		{"empty", "", "craig_base_url: must not be empty"},
		{"spaces only", "  ", "craig_base_url: must not be empty"},
		{"no scheme", "craig.horse", `craig_base_url: must be an absolute http or https URL (got "craig.horse")`},
		{"other scheme", "ftp://craig.horse", `craig_base_url: must be an absolute http or https URL (got "ftp://craig.horse")`},
		{"no host", "https://", `craig_base_url: must be an absolute http or https URL (got "https://")`},
		{"invalid escape", "https://craig.horse/%zz", `craig_base_url: must be an absolute http or https URL (got "https://craig.horse/%zz")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.CraigBaseURL = tt.url

			got := errorText(cfg.Validate())

			if got != tt.want {
				t.Errorf("Validate = %q, want %q", got, tt.want)
			}
		})
	}
}

// 条件: max_recording_minutes を 0、1、Transcribe の上限ちょうど、上限 + 1 にする。
// 内容: 1 以上、上限（480 分）以下だけを受け入れる。
func TestValidate_MaxRecordingMinutes_AcceptsOneUpToTranscribeLimit(t *testing.T) {
	tests := []struct {
		name    string
		minutes int
		want    string
	}{
		{"zero", 0, "max_recording_minutes: must be 1 or greater (got 0)"},
		{"one", 1, "<nil>"},
		{"at transcribe limit", transcribeLimitMinutes, "<nil>"},
		{"over transcribe limit", transcribeLimitMinutes + 1, "max_recording_minutes: must be 480 or less (got 481)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.MaxRecordingMinutes = tt.minutes

			got := errorText(cfg.Validate())

			if got != tt.want {
				t.Errorf("Validate = %q, want %q", got, tt.want)
			}
		})
	}
}
