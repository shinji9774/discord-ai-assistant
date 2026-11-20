// Package summarize は、実行したチャンネルの会話を要約する /summarize コマンドを提供する。
package summarize

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
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

	// 案内の理由。worker が診断のためにログへ出す。
	reasonBotCannotRead    = "bot_cannot_read"
	reasonMemberCannotRead = "member_cannot_read"
	reasonInvalidDateTime  = "invalid_datetime"
	reasonSinceInFuture    = "since_in_future"
	reasonInvertedPeriod   = "inverted_period"
	reasonNoConversation   = "no_conversation"

	// 案内の診断値のキーと、権限の確認の種類。
	attrKeyCheck                = "check"
	attrKeyAppPermissions       = "app_permissions"
	attrKeyMemberPermissions    = "member_permissions"
	attrKeyMissingPermissions   = "missing_permissions"
	attrKeyDiscordStatus        = "discord_status"
	attrKeyDiscordCode          = "discord_code"
	attrKeyDiscordMessage       = "discord_message"
	attrKeyFetchedMessages      = "fetched_messages"
	attrKeyEmptyContentMessages = "empty_content_messages"
	checkPrecheck               = "precheck"
	checkHistoryAPI             = "history_api"

	// permissionsBase は、権限のビット列を文字列にするときの基数。
	permissionsBase = 10
	// missingPermissionsSeparator は、欠けている権限の名前の区切り。
	missingPermissionsSeparator = ", "

	// includeBotsTrue は、真偽のオプションを ingress が文字列にしたときの真の値。
	includeBotsTrue = "true"
)

// 会話履歴を読むのに必要な権限。
var (
	botRequiredPermissions    = []discord.Permissions{discord.PermissionViewChannel, discord.PermissionReadMessageHistory}
	memberRequiredPermissions = []discord.Permissions{discord.PermissionReadMessageHistory}
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
		return command.Guided(botCannotReadGuidance, reasonBotCannotRead, historyForbiddenAttrs(inv, err)...), nil
	}
	if err != nil {
		return command.Result{}, err
	}
	if len(conversation.Messages) == 0 {
		return command.Guided(noConversationGuidance, reasonNoConversation,
			slog.Int(attrKeyFetchedMessages, conversation.FetchedMessages),
			slog.Int(attrKeyEmptyContentMessages, conversation.EmptyContentMessages),
		), nil
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
		return command.Guided(noConversationGuidance, reasonSinceInFuture)
	case errors.Is(err, ErrInvertedPeriod):
		return command.Guided(invertedPeriodGuidance, reasonInvertedPeriod)
	default:
		return command.Guided(invalidDateTimeGuidance, reasonInvalidDateTime)
	}
}

// permissionGuidance は、bot か実行者がチャンネルの会話履歴を読めないときの案内を返す。
// 実行者の権限を確かめるのは、bot を通して読めない会話を要約させないため。
func permissionGuidance(inv command.Invocation) (command.Result, bool) {
	if !inv.AppPermissions.Has(botRequiredPermissions...) {
		missing := missingPermissionNames(inv.AppPermissions, botRequiredPermissions)
		return command.Guided(botCannotReadGuidance, reasonBotCannotRead, precheckAttrs(inv, missing)...), true
	}
	if !inv.MemberPermissions.Has(memberRequiredPermissions...) {
		missing := missingPermissionNames(inv.MemberPermissions, memberRequiredPermissions)
		return command.Guided(memberCannotReadGuidance, reasonMemberCannotRead, precheckAttrs(inv, missing)...), true
	}
	return command.Result{}, false
}

// precheckAttrs は、権限の事前チェックで案内したときの診断値。
func precheckAttrs(inv command.Invocation, missing string) []slog.Attr {
	return append(permissionAttrs(checkPrecheck, inv), slog.String(attrKeyMissingPermissions, missing))
}

// historyForbiddenAttrs は、履歴の取得が 403 で拒まれて案内したときの診断値。
// Discord のコードと文言は、err が HistoryForbiddenError のときだけ加える。
func historyForbiddenAttrs(inv command.Invocation, err error) []slog.Attr {
	attrs := permissionAttrs(checkHistoryAPI, inv)
	var forbidden *HistoryForbiddenError
	if !errors.As(err, &forbidden) {
		return attrs
	}
	return append(attrs,
		slog.Int(attrKeyDiscordStatus, http.StatusForbidden),
		slog.Int(attrKeyDiscordCode, forbidden.Code),
		slog.String(attrKeyDiscordMessage, forbidden.Message),
	)
}

// permissionAttrs は、権限の確認の種類と、bot・実行者の権限（10 進数）の診断値。
func permissionAttrs(check string, inv command.Invocation) []slog.Attr {
	return []slog.Attr{
		slog.String(attrKeyCheck, check),
		slog.String(attrKeyAppPermissions, strconv.FormatInt(int64(inv.AppPermissions), permissionsBase)),
		slog.String(attrKeyMemberPermissions, strconv.FormatInt(int64(inv.MemberPermissions), permissionsBase)),
	}
}

// missingPermissionNames は、required のうち have に無い権限の名前を、required の順に区切って返す。
func missingPermissionNames(have discord.Permissions, required []discord.Permissions) string {
	var names []string
	for _, permission := range required {
		if !have.Has(permission) {
			names = append(names, permission.String())
		}
	}
	return strings.Join(names, missingPermissionsSeparator)
}
