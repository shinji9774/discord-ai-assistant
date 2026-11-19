// Package catalog は提供するコマンドの一覧を組み立てる。
package catalog

import (
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/ask"
	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/config"
	"discord-ai-assistant/internal/help"
	"discord-ai-assistant/internal/imagegen"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/summarize"
	"discord-ai-assistant/internal/translate"
	"discord-ai-assistant/internal/websearch"
)

// Dependencies はコマンドが必要とする依存。
type Dependencies struct {
	Generator      llm.TextGenerator
	ToolGenerator  llm.ToolGenerator
	Searcher       websearch.Searcher
	Logger         *slog.Logger
	ImageGenerator imagegen.Generator
	Translator     translate.Translator
	Fetcher        translate.AttachmentFetcher
	History        summarize.HistoryReader // /summarize が会話履歴を読む
	BotToken       string                  // 土台のコマンドは使わない。後続 spec のコマンドが受け取る
}

// Definitions は Discord へ登録する全コマンドの定義を、translate、ask、image、summarize、help の順に返す。
// help は ingress が応答するため Registry には含めず、ここで足す。
func Definitions(cfg config.Config) []discord.ApplicationCommandCreate {
	return append(New(Dependencies{}, cfg).Definitions(), help.Definition())
}

// New は translate、ask、image、summarize の順にコマンドを登録した Registry を返す。
func New(deps Dependencies, cfg config.Config) *command.Registry {
	return command.NewRegistry(
		translate.New(deps.Translator, deps.Fetcher, cfg.Translate),
		ask.New(deps.ToolGenerator, deps.Searcher, cfg.Ask, time.Now, deps.Logger),
		imagegen.New(deps.Generator, deps.ImageGenerator, deps.Fetcher, cfg.Image),
		summarize.New(deps.Generator, deps.History, cfg.Summarize, time.Now),
	)
}
