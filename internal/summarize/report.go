package summarize

import (
	"fmt"
	"strings"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
)

// 結果の見出しと注記。利用者向けの文言のため、設定ではなく定数で持つ。
const (
	headingFormat       = "**対象期間**: %s 〜 %s（日本時間） / **メッセージ**: %d 件"
	clippedNoticeFormat = "（上限の %d 件を超えたため、古い会話は要約に含まれていません）"
	sectionSeparator    = "\n\n"
)

// formatReport は、見出し（期間と件数）、要約、注記を空行で区切ってつなぐ。
func formatReport(period Period, conversation Conversation, summary llm.Result, maxMessages int) string {
	sections := []string{
		fmt.Sprintf(headingFormat,
			period.Since.In(japanTime).Format(dateTimeLayout),
			period.Until.In(japanTime).Format(dateTimeLayout),
			len(conversation.Messages)),
		summary.Text,
	}
	if conversation.Clipped {
		sections = append(sections, fmt.Sprintf(clippedNoticeFormat, maxMessages))
	}
	if summary.Truncated {
		sections = append(sections, command.TruncatedNotice)
	}
	return strings.Join(sections, sectionSeparator)
}
