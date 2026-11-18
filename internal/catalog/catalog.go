// Package catalog は提供するコマンドの一覧を組み立てる。
package catalog

import (
	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/ask"
	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/help"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/translate"
)

// Dependencies はコマンドが必要とする依存。
type Dependencies struct {
	Generator  llm.TextGenerator
	Translator translate.Translator
	Fetcher    translate.AttachmentFetcher
	BotToken   string // 土台のコマンドは使わない。後続 spec のコマンドが受け取る
}

// Definitions は Discord へ登録する全コマンドの定義を、translate、ask、help の順に返す。
// help は ingress が応答するため Registry には含めず、ここで足す。
func Definitions() []discord.ApplicationCommandCreate {
	return append(New(Dependencies{}).Definitions(), help.Definition())
}

// New は translate、ask の順にコマンドを登録した Registry を返す。
func New(deps Dependencies) *command.Registry {
	return command.NewRegistry(
		translate.New(deps.Translator, deps.Fetcher),
		ask.New(deps.Generator),
	)
}
