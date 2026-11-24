package help_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"discord-ai-assistant/internal/catalog"
	"discord-ai-assistant/internal/config/configtest"
	"discord-ai-assistant/internal/help"
)

const (
	discordMessageLengthLimit = 2000

	// docsIndexURL は、/help から案内する詳細ページの目次（GitHub Pages の公開 URL）。
	docsIndexURL = "https://shinji9774.github.io/discord-ai-assistant/"
	// urlSchemeSeparator は、本文中の URL を数えるための目印。
	urlSchemeSeparator = "://"
)

// 条件: 登録されている全コマンド（catalog.Definitions()）。
// 内容: すべてのコマンドについて、"/" + 名前 が Usage に含まれる。漏れたコマンド名の一覧は空になる。
func TestUsage_RegisteredCommands_AllDocumented(t *testing.T) {
	definitions := catalog.Definitions(configtest.Load(t))

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

// 条件: 埋め込まれた Usage。
// 内容: URL は詳細ページの目次の 1 つだけで、Discord のリンクプレビューを抑えるため <> で囲まれている（1.4）。
func TestUsage_DocsLink_IsSingleSuppressedIndexURL(t *testing.T) {
	usage := help.Usage

	urlCount := strings.Count(usage, urlSchemeSeparator)

	if urlCount != 1 || !strings.Contains(usage, "<"+docsIndexURL+">") {
		t.Errorf("Usage has %d URLs, want only <%s>", urlCount, docsIndexURL)
	}
}
