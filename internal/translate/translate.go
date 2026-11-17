// Package translate は /translate コマンドを提供する。
package translate

import (
	"context"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
)

const (
	commandName           = "translate"
	optionText            = "text"
	optionTo              = "to"
	DefaultTargetLanguage = "日本語"
	systemPromptPrefix    = "あなたは翻訳者です。ユーザーの文章を"
	systemPromptSuffix    = "に翻訳してください。翻訳文だけを出力し、説明や前置きは書かないでください。"
)

// Command は文章を指定した言語へ翻訳する。
type Command struct {
	gen llm.TextGenerator
}

func New(gen llm.TextGenerator) *Command {
	return &Command{gen: gen}
}

func (c *Command) Definition() discord.SlashCommandCreate {
	maxLength := command.MaxInputLength
	return discord.SlashCommandCreate{
		Name:        commandName,
		Description: "文章を指定した言語に翻訳します",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{
				Name:        optionText,
				Description: "翻訳する文章",
				Required:    true,
				MaxLength:   &maxLength,
			},
			discord.ApplicationCommandOptionString{
				Name:        optionTo,
				Description: "翻訳先の言語（省略時は日本語）",
			},
		},
		Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild},
	}
}

func (c *Command) Execute(ctx context.Context, inv command.Invocation) (command.Result, error) {
	text := inv.Options[optionText]
	target := inv.Options[optionTo]
	if target == "" {
		target = DefaultTargetLanguage
	}

	result, err := c.gen.Generate(ctx, llm.Request{
		System:          systemPromptPrefix + target + systemPromptSuffix,
		UserText:        text,
		MaxOutputTokens: command.MaxOutputTokens,
	})
	if err != nil {
		return command.Result{}, err
	}
	return command.Result{Text: command.FormatAnswer(text, result.Text, result.Truncated)}, nil
}
