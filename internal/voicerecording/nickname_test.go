package voicerecording_test

import (
	"strings"
	"testing"

	"discord-ai-assistant/internal/voicerecording"
)

const (
	// recordingMark は、仕様の録音中の印（6 文字）。
	recordingMark = "[録音中] "
	// maxNicknameRunes は、Discord の表示名の上限（32 文字）。
	maxNicknameRunes = 32
)

// 条件: 元の表示名に印を付ける
// 内容: 32 文字に収まればそのまま付け、超えれば末尾を切って 32 文字にする
func TestRecordingNickname_DisplayNameLength_PrefixesMarkWithin32Runes(t *testing.T) {
	markRunes := len([]rune(recordingMark))
	cases := []struct {
		name        string
		displayName string
		want        string
	}{
		{"短い表示名", "録音bot", recordingMark + "録音bot"},
		{"印を付けてちょうど 32 文字", strings.Repeat("あ", maxNicknameRunes-markRunes), recordingMark + strings.Repeat("あ", maxNicknameRunes-markRunes)},
		{"印を付けると 32 文字を超える", strings.Repeat("あ", 30), recordingMark + strings.Repeat("あ", maxNicknameRunes-markRunes)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := voicerecording.RecordingNickname(tc.displayName)

			if got != tc.want {
				t.Errorf("RecordingNickname(%q) = %q, want %q", tc.displayName, got, tc.want)
			}
		})
	}
}

// 条件: 録音の開始前に読んだ bot の表示名から、元の表示名を求める
// 内容: 前に残った印だけを外し、それ以外はそのまま返す
func TestOriginalNickname_CurrentNickname_StripsLeftoverMarkOnly(t *testing.T) {
	cases := []struct {
		name string
		nick string
		want string
	}{
		{"表示名なし", "", ""},
		{"印のない表示名", "議事録 bot", "議事録 bot"},
		{"前回の録音の印が残った表示名", recordingMark + "議事録 bot", "議事録 bot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := voicerecording.OriginalNickname(tc.nick)

			if got != tc.want {
				t.Errorf("OriginalNickname(%q) = %q, want %q", tc.nick, got, tc.want)
			}
		})
	}
}
