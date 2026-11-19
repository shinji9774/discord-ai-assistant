// Package summarize は、実行したチャンネルの会話を要約する /summarize コマンドを提供する。
package summarize

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
)

// コマンドとオプションの名前と説明。利用者向けの文言のため、設定ではなく定数で持つ。
const (
	commandName            = "summarize"
	commandDescription     = "このチャンネルの会話を要約する"
	optionSince            = "since"
	sinceDescription       = "対象期間の開始（例: 2026-11-18 09:00。日本時間）"
	optionUntil            = "until"
	untilDescription       = "対象期間の終了（例: 2026-11-19。日本時間）"
	optionIncludeBots      = "include_bots"
	includeBotsDescription = "bot の投稿も要約に含める"

	// 案内文。失敗ではなく、利用者に指定や権限を見直してもらう返信。
	invalidDateTimeGuidance  = "since と until は `YYYY-MM-DD` または `YYYY-MM-DD HH:MM`（日本時間）で指定してください。"
	invertedPeriodGuidance   = "期間の指定が正しくありません。since は until より前の日時にしてください。"
	noConversationGuidance   = "指定した期間に、要約する会話がありませんでした。"
	botCannotReadGuidance    = "bot にこのチャンネルの会話履歴を読む権限がないため、要約できません。サーバの管理者に相談してください。"
	memberCannotReadGuidance = "このチャンネルの会話履歴を読む権限がないため、要約できません。"

	// includeBotsTrue は、真偽のオプションを ingress が文字列にしたときの真の値。
	includeBotsTrue = "true"
)

// Clock は今の時刻を返す。テストで時刻を固定するために注入する。
type Clock func() time.Time

// Command は /summarize。
type Command struct {
	gen     llm.TextGenerator
	history HistoryReader
	cfg     Config
	now     Clock
}

// New は /summarize を作る。依存は Execute のときにだけ使う。
func New(gen llm.TextGenerator, history HistoryReader, cfg Config, now Clock) *Command {
	return &Command{gen: gen, history: history, cfg: cfg, now: now}
}

// Definition は Discord に登録するコマンドの定義を返す。会話履歴はサーバのチャンネルにしか無いため、サーバでだけ使える。
func (c *Command) Definition() discord.SlashCommandCreate {
	return discord.SlashCommandCreate{
		Name:        commandName,
		Description: commandDescription,
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: optionSince, Description: sinceDescription},
			discord.ApplicationCommandOptionString{Name: optionUntil, Description: untilDescription},
			discord.ApplicationCommandOptionBool{Name: optionIncludeBots, Description: includeBotsDescription},
			command.VisibilityOption(),
		},
		Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild},
	}
}

// Execute は、期間と権限を確かめてから、チャンネルの会話を集めて 1 回だけ要約する。
// 指定や権限の誤り、会話が無い場合は案内を返し、履歴の取得やモデルの生成が失敗した場合はエラーを返す。
func (c *Command) Execute(ctx context.Context, inv command.Invocation) (command.Result, error) {
	defaultLength := time.Duration(c.cfg.DefaultPeriodHours) * time.Hour
	period, err := resolvePeriod(inv.Options[optionSince], inv.Options[optionUntil], c.now(), defaultLength)
	if err != nil {
		return periodGuidance(err), nil
	}
	if guidance, denied := permissionGuidance(inv); denied {
		return guidance, nil
	}
	return c.summarizeChannel(ctx, inv, period)
}

// summarizeChannel は期間中の会話を集め、モデルで 1 回だけ要約する。
func (c *Command) summarizeChannel(ctx context.Context, inv command.Invocation, period Period) (command.Result, error) {
	channelID, err := snowflake.Parse(inv.ChannelID)
	if err != nil {
		return command.Result{}, fmt.Errorf("parse channel id: %w", err)
	}
	includeBots := inv.Options[optionIncludeBots] == includeBotsTrue
	conversation, err := collect(ctx, c.history, channelID, period, includeBots, c.cfg.MaxMessages)
	if errors.Is(err, ErrHistoryForbidden) {
		return guidance(botCannotReadGuidance), nil
	}
	if err != nil {
		return command.Result{}, err
	}
	if len(conversation.Messages) == 0 {
		return guidance(noConversationGuidance), nil
	}
	summary, err := c.gen.Generate(ctx, llm.Request{
		System:          c.cfg.SystemPrompt,
		UserText:        renderTranscript(conversation.Messages),
		MaxOutputTokens: c.cfg.MaxOutputTokens,
	})
	if err != nil {
		return command.Result{}, fmt.Errorf("generate summary: %w", err)
	}
	return command.Result{
		Text:         formatReport(period, conversation, summary, c.cfg.MaxMessages),
		Format:       command.TextFormatMarkdown,
		Confidential: true,
	}, nil
}

// periodGuidance は期間の誤りを案内に変える。since が今以後なら、期間に会話が無いのと同じ案内にする。
func periodGuidance(err error) command.Result {
	switch {
	case errors.Is(err, ErrSinceInFuture):
		return guidance(noConversationGuidance)
	case errors.Is(err, ErrInvertedPeriod):
		return guidance(invertedPeriodGuidance)
	default:
		return guidance(invalidDateTimeGuidance)
	}
}

// permissionGuidance は、bot か実行者がチャンネルの会話履歴を読めないときの案内を返す。
// 実行者の権限を確かめるのは、bot を通して読めない会話を要約させないため。
func permissionGuidance(inv command.Invocation) (command.Result, bool) {
	if !inv.AppPermissions.Has(discord.PermissionViewChannel, discord.PermissionReadMessageHistory) {
		return guidance(botCannotReadGuidance), true
	}
	if !inv.MemberPermissions.Has(discord.PermissionReadMessageHistory) {
		return guidance(memberCannotReadGuidance), true
	}
	return command.Result{}, false
}

func guidance(text string) command.Result {
	return command.Result{Text: text}
}
