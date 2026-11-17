package command

import "strings"

const quotePrefix = "> "

// FormatAnswer は入力を引用として回答の上に置く。truncated が真なら末尾に注記を付ける。
func FormatAnswer(quoted, answer string, truncated bool) string {
	quotedLines := quotePrefix + strings.ReplaceAll(quoted, "\n", "\n"+quotePrefix)
	formatted := quotedLines + "\n\n" + answer
	if truncated {
		formatted += "\n\n" + TruncatedNotice
	}
	return formatted
}
