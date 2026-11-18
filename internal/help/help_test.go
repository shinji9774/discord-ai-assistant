package help_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"discord-ai-assistant/internal/catalog"
	"discord-ai-assistant/internal/help"
)

const discordMessageLengthLimit = 2000

// 条件: 登録されている全コマンド（catalog.Definitions()）。
// 内容: すべてのコマンドについて、"/" + 名前 が Usage に含まれる。漏れたコマンド名の一覧は空になる。
func TestUsage_RegisteredCommands_AllDocumented(t *testing.T) {
	definitions := catalog.Definitions()

	var undocumented []string
	for _, definition := range definitions {
		if name := "/" + definition.CommandName(); !strings.Contains(help.Usage, name) {
			undocumented = append(undocumented, name)
		}
	}

	if len(undocumented) != 0 {
		t.Errorf("commands missing from Usage = %q, want none", undocumented)
	}
}

// 条件: 埋め込まれた Usage。
// 内容: 文字数が Discord のメッセージ上限（2000）以下になる。
func TestUsage_Length_FitsDiscordMessageLimit(t *testing.T) {
	usage := help.Usage

	got := utf8.RuneCountInString(usage)

	if got > discordMessageLengthLimit {
		t.Errorf("Usage length = %d, want <= %d", got, discordMessageLengthLimit)
	}
}
