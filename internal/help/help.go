// Package help は /help コマンドの定義と、ヘルプ本文（USAGE.md）を提供する。
package help

import (
	_ "embed"

	"github.com/disgoorg/disgo/discord"
)

const CommandName = "help"

// Usage は /help が返す本文。README からも参照するため、Markdown ファイルとして持つ。
//
//go:embed USAGE.md
var Usage string

// Definition は /help の定義。ingress が応答するため、実行側の Command は持たない。
func Definition() discord.SlashCommandCreate {
	return discord.SlashCommandCreate{
		Name:        CommandName,
		Description: "コマンドの一覧と使い方を表示します",
		Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
	}
}
