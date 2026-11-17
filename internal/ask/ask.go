// Package ask は /ask コマンドを提供する。
package ask

import (
	"context"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
)

const (
	commandName    = "ask"
	optionQuestion = "question"
	systemPrompt   = "あなたは Discord サーバで質問に答えるアシスタントです。質問と同じ言語で、簡潔に答えてください。"
)

// Command は質問に回答する。会話履歴は持たない。
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
		Description: "質問に回答します",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{
				Name:        optionQuestion,
				Description: "質問の内容",
				Required:    true,
				MaxLength:   &maxLength,
			},
		},
		Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild},
	}
}

func (c *Command) Execute(ctx context.Context, inv command.Invocation) (command.Result, error) {
	question := inv.Options[optionQuestion]

	result, err := c.gen.Generate(ctx, llm.Request{
		System:          systemPrompt,
		UserText:        question,
		MaxOutputTokens: command.MaxOutputTokens,
	})
	if err != nil {
		return command.Result{}, err
	}
	return command.Result{Text: command.FormatAnswer(question, result.Text, result.Truncated)}, nil
}
