// Package catalog は提供するコマンドの一覧を組み立てる。
package catalog

import (
	"discord-ai-assistant/internal/ask"
	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/translate"
)

// Dependencies はコマンドが必要とする依存。
type Dependencies struct {
	Generator llm.TextGenerator
	BotToken  string // 土台のコマンドは使わない。後続 spec のコマンドが受け取る
}

// New は translate、ask の順にコマンドを登録した Registry を返す。
func New(deps Dependencies) *command.Registry {
	return command.NewRegistry(
		translate.New(deps.Generator),
		ask.New(deps.Generator),
	)
}
