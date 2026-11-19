// Package ask は /ask コマンドを提供する。
package ask

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/websearch"
)

const (
	commandName         = "ask"
	optionQuestion      = "question"
	dateLayout          = "2006-01-02"
	jstOffsetSeconds    = 9 * 60 * 60
	turnsBeyondSearches = 2 // 検索の上限を超えた求めに答える 1 回と、最後の回答の 1 回
)

var jst = time.FixedZone("JST", jstOffsetSeconds)

// Clock は現在の時刻を返す。
type Clock func() time.Time

// Command は質問に回答する。会話履歴は持たない。
type Command struct {
	gen      llm.ToolGenerator
	searcher websearch.Searcher
	cfg      Config
	now      Clock
	logger   *slog.Logger
}

func New(gen llm.ToolGenerator, searcher websearch.Searcher, cfg Config, now Clock, logger *slog.Logger) *Command {
	return &Command{gen: gen, searcher: searcher, cfg: cfg, now: now, logger: logger}
}

func (c *Command) Definition() discord.SlashCommandCreate {
	maxLength := c.cfg.MaxInputLength
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
	session := newSearchSession(c.searcher, c.cfg.WebSearch, c.logger)

	result, err := c.gen.GenerateWithTools(ctx, c.toolRequest(question, session.run))
	if err != nil {
		return command.Result{}, err
	}
	return command.Result{Text: command.FormatAnswer(question, session.answerBody(result.Text), result.Truncated)}, nil
}

// toolRequest は、Web 検索のツールを使える、回答の依頼を作る。
func (c *Command) toolRequest(question string, run llm.ToolRunner) llm.ToolRequest {
	return llm.ToolRequest{
		Request: llm.Request{
			System:          c.systemPrompt(),
			UserText:        question,
			MaxOutputTokens: c.cfg.MaxOutputTokens,
		},
		Tools: []llm.Tool{{
			Name:        toolNameWebSearch,
			Description: c.cfg.WebSearch.ToolDescription,
			Parameters:  []llm.ToolParameter{{Name: toolParameterQuery, Description: queryDescription}},
		}},
		Run:      run,
		MaxTurns: c.cfg.WebSearch.MaxSearches + turnsBeyondSearches,
	}
}

// systemPrompt は、指示文の中の日付の目印を、日本時間の今日の日付に置き換える。
func (c *Command) systemPrompt() string {
	return strings.ReplaceAll(c.cfg.SystemPrompt, todayPlaceholder, c.now().In(jst).Format(dateLayout))
}
