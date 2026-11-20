package craig_test

import (
	"errors"
	"reflect"
	"testing"

	"discord-ai-assistant/internal/voicesummary/craig"
)

const testBaseURL = "https://craig.horse"

// 条件: baseURL と scheme・host が同じで、パスが /rec/{英数字 10〜12 文字}、key がある録音リンク。
// 内容: 録音 ID と鍵を取り出す。delete などほかのクエリ、前後の空白、baseURL の末尾の / は無視する。
func TestParseLink_RecordingLink_ReturnsIDAndKey(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		raw     string
		want    craig.Link
	}{
		{"鍵つき", testBaseURL, "https://craig.horse/rec/AbCdEfGhIjKl?key=XyZ123", craig.Link{RecordingID: "AbCdEfGhIjKl", Key: "XyZ123"}},
		{"delete つき", testBaseURL, "https://craig.horse/rec/AbCdEfGhIjKl?key=XyZ123&delete=Del456", craig.Link{RecordingID: "AbCdEfGhIjKl", Key: "XyZ123"}},
		{"ID が 10 文字", testBaseURL, "https://craig.horse/rec/A1b2C3d4E5?key=k", craig.Link{RecordingID: "A1b2C3d4E5", Key: "k"}},
		{"host の大文字", testBaseURL, "https://CRAIG.horse/rec/AbCdEfGhIjKl?key=XyZ123", craig.Link{RecordingID: "AbCdEfGhIjKl", Key: "XyZ123"}},
		{"baseURL の末尾に /", testBaseURL + "/", "https://craig.horse/rec/AbCdEfGhIjKl?key=XyZ123", craig.Link{RecordingID: "AbCdEfGhIjKl", Key: "XyZ123"}},
		{"前後の空白", testBaseURL, "  https://craig.horse/rec/AbCdEfGhIjKl?key=XyZ123\n", craig.Link{RecordingID: "AbCdEfGhIjKl", Key: "XyZ123"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := craig.ParseLink(tt.raw, tt.baseURL)

			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseLink(%q) = %+v, %v; want %+v, nil", tt.raw, got, err, tt.want)
			}
		})
	}
}

// 条件: 録音リンクの形でない文字列。
// 内容: ErrNotRecordingLink を返す。
func TestParseLink_NotRecordingLink_ReturnsErrNotRecordingLink(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"別の host", "https://example.com/rec/AbCdEfGhIjKl?key=XyZ123"},
		{"http と https の違い", "http://craig.horse/rec/AbCdEfGhIjKl?key=XyZ123"},
		{"/rec/ がない", "https://craig.horse/AbCdEfGhIjKl?key=XyZ123"},
		{"/rec/ の後ろにさらにパス", "https://craig.horse/rec/AbCdEfGhIjKl/extra?key=XyZ123"},
		{"key がない", "https://craig.horse/rec/AbCdEfGhIjKl"},
		{"key が空", "https://craig.horse/rec/AbCdEfGhIjKl?key="},
		{"ID に記号", "https://craig.horse/rec/AbCdEf-hIjKl?key=XyZ123"},
		{"ID が 9 文字", "https://craig.horse/rec/AbCdEfGhI?key=XyZ123"},
		{"ID が 13 文字", "https://craig.horse/rec/AbCdEfGhIjKlM?key=XyZ123"},
		{"URL でない", "録音のリンクです"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := craig.ParseLink(tt.raw, testBaseURL)

			if !errors.Is(err, craig.ErrNotRecordingLink) {
				t.Errorf("ParseLink(%q) err = %v, want %v", tt.raw, err, craig.ErrNotRecordingLink)
			}
		})
	}
}
