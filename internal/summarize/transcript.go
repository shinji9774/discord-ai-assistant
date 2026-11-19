package summarize

import (
	"fmt"
	"strings"
)

// transcriptLineFormat はモデルに渡す会話の 1 行。日時は日本時間。
const transcriptLineFormat = "[%s] %s: %s"

// renderTranscript は古い順の投稿を、モデルに渡す会話の記録にする。
func renderTranscript(messages []Message) string {
	lines := make([]string, 0, len(messages))
	for _, m := range messages {
		lines = append(lines, fmt.Sprintf(transcriptLineFormat, m.SentAt.In(japanTime).Format(dateTimeLayout), m.AuthorName, m.Content))
	}
	return strings.Join(lines, "\n")
}
