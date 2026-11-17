package command_test

import (
	"testing"

	"discord-ai-assistant/internal/command"
)

// 条件: 1 行の入力、truncated が偽。
// 内容: 入力が引用として回答の上に置かれる。
func TestFormatAnswer_SingleLineInput_QuotesInputAboveAnswer(t *testing.T) {
	quoted, answer := "Hello", "こんにちは"

	got := command.FormatAnswer(quoted, answer, false)

	if want := "> Hello\n\nこんにちは"; got != want {
		t.Errorf("FormatAnswer() = %q, want %q", got, want)
	}
}

// 条件: 複数行の入力、truncated が偽。
// 内容: すべての行の先頭に "> " が付く。
func TestFormatAnswer_MultiLineInput_QuotesEveryLine(t *testing.T) {
	quoted, answer := "a\nb", "c"

	got := command.FormatAnswer(quoted, answer, false)

	if want := "> a\n> b\n\nc"; got != want {
		t.Errorf("FormatAnswer() = %q, want %q", got, want)
	}
}

// 条件: truncated が真。
// 内容: 回答の後ろに空行を挟んで途中打ち切りの注記が付く。
func TestFormatAnswer_Truncated_AppendsNotice(t *testing.T) {
	quoted, answer := "Hello", "こんに"

	got := command.FormatAnswer(quoted, answer, true)

	if want := "> Hello\n\nこんに\n\n（上限に達したため、途中までの結果です）"; got != want {
		t.Errorf("FormatAnswer() = %q, want %q", got, want)
	}
}
