package voicerecording

import "strings"

const (
	// recordingMark は、録音中の bot の表示名の前に付ける印。
	recordingMark = "[録音中] "
	// maxNicknameRunes は、Discord のサーバでの表示名の上限の文字数。
	maxNicknameRunes = 32
)

// recordingNickname は、録音中の bot の表示名を返す。上限を超える分は末尾を切る。
func recordingNickname(displayName string) string {
	runes := []rune(recordingMark + displayName)
	if len(runes) <= maxNicknameRunes {
		return string(runes)
	}
	return string(runes[:maxNicknameRunes])
}

// originalNickname は、録音の開始前の bot の表示名から、録音の後に戻す表示名を返す。
// 前回の録音が後始末できずに残した印は外す。
func originalNickname(nick string) string {
	return strings.TrimPrefix(nick, recordingMark)
}
