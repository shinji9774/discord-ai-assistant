package voicerecording

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"discord-ai-assistant/internal/discordapi"
)

// Discord のエラーコード（実機確認の第 1 段で確かめた値）。
const (
	// notInVoiceCode は、利用者がボイスチャンネルにいないときの Unknown Voice State。
	notInVoiceCode = 10065
	// channelHiddenCode は、bot がチャンネルを見られないときの Missing Access。
	channelHiddenCode = 50001
)

var ErrNotInVoice = errors.New("user is not in a voice channel")

// DiscordGuild は、録音に使うサーバの情報を Discord REST で読み書きする。
type DiscordGuild struct {
	rest rest.Rest
}

func NewDiscordGuild(botToken, apiBaseURL string) *DiscordGuild {
	client := rest.NewClient(botToken, discordapi.ClientOpts(apiBaseURL)...)
	return &DiscordGuild{rest: rest.New(client)}
}

// VoiceChannelOf は、利用者がいるボイスチャンネルの ID を返す。いなければ ErrNotInVoice。
func (g *DiscordGuild) VoiceChannelOf(ctx context.Context, guildID, userID string) (string, error) {
	gid, uid, err := parseIDPair(guildID, userID)
	if err != nil {
		return "", err
	}
	state, err := g.rest.GetUserVoiceState(gid, uid, rest.WithCtx(ctx))
	if hasDiscordCode(err, notInVoiceCode) {
		return "", ErrNotInVoice
	}
	if err != nil {
		return "", redactRESTError(err, "voice state lookup")
	}
	if state.ChannelID == nil {
		return "", ErrNotInVoice
	}
	return state.ChannelID.String(), nil
}

// BotPermissionsIn は、チャンネルでの bot の権限を返す。bot がチャンネルを見られなければ権限なし（0）。
func (g *DiscordGuild) BotPermissionsIn(ctx context.Context, guildID, channelID, botUserID string) (discord.Permissions, error) {
	gid, bid, err := parseIDPair(guildID, botUserID)
	if err != nil {
		return 0, err
	}
	cid, err := snowflake.Parse(channelID)
	if err != nil {
		return 0, fmt.Errorf("parse channel id: %w", err)
	}
	channel, err := g.rest.GetChannel(cid, rest.WithCtx(ctx))
	if hasDiscordCode(err, channelHiddenCode) || hasStatus(err, http.StatusForbidden) {
		return 0, nil
	}
	if err != nil {
		return 0, redactRESTError(err, "channel lookup")
	}
	guildChannel, ok := channel.(discord.GuildChannel)
	if !ok {
		return 0, fmt.Errorf("channel %s is not a guild channel", channelID)
	}
	guild, err := g.rest.GetGuild(gid, false, rest.WithCtx(ctx))
	if err != nil {
		return 0, redactRESTError(err, "guild lookup")
	}
	member, err := g.rest.GetMember(gid, bid, rest.WithCtx(ctx))
	if err != nil {
		return 0, redactRESTError(err, "member lookup")
	}
	return channelPermissions(*guild, *member, guildChannel.PermissionOverwrites()), nil
}

// BotNickname は、サーバでの bot の表示名を返す。付いていなければ空文字。
func (g *DiscordGuild) BotNickname(ctx context.Context, guildID, botUserID string) (string, error) {
	member, err := g.member(ctx, guildID, botUserID)
	if err != nil {
		return "", err
	}
	if member.Nick == nil {
		return "", nil
	}
	return *member.Nick, nil
}

// SetBotNickname は、サーバでの bot の表示名を変える。空文字なら表示名を消す。
func (g *DiscordGuild) SetBotNickname(ctx context.Context, guildID, nickname string) error {
	gid, err := snowflake.Parse(guildID)
	if err != nil {
		return fmt.Errorf("parse guild id: %w", err)
	}
	if _, err := g.rest.UpdateCurrentMember(gid, discord.CurrentMemberUpdate{Nick: &nickname}, rest.WithCtx(ctx)); err != nil {
		return redactRESTError(err, "nickname update")
	}
	return nil
}

// MemberName は、話者の名前（global_name、無ければ username）を返す。voicesummary の話者名と同じ規則にする。
func (g *DiscordGuild) MemberName(ctx context.Context, guildID, userID string) (string, error) {
	member, err := g.member(ctx, guildID, userID)
	if err != nil {
		return "", err
	}
	return member.User.EffectiveName(), nil
}

func (g *DiscordGuild) member(ctx context.Context, guildID, userID string) (*discord.Member, error) {
	gid, uid, err := parseIDPair(guildID, userID)
	if err != nil {
		return nil, err
	}
	member, err := g.rest.GetMember(gid, uid, rest.WithCtx(ctx))
	if err != nil {
		return nil, redactRESTError(err, "member lookup")
	}
	return member, nil
}

func parseIDPair(guildID, userID string) (snowflake.ID, snowflake.ID, error) {
	gid, err := snowflake.Parse(guildID)
	if err != nil {
		return 0, 0, fmt.Errorf("parse guild id: %w", err)
	}
	uid, err := snowflake.Parse(userID)
	if err != nil {
		return 0, 0, fmt.Errorf("parse user id: %w", err)
	}
	return gid, uid, nil
}

func hasDiscordCode(err error, code int) bool {
	var restErr *rest.Error
	return errors.As(err, &restErr) && int(restErr.Code) == code
}

func hasStatus(err error, status int) bool {
	var restErr *rest.Error
	return errors.As(err, &restErr) && restErr.Response != nil && restErr.Response.StatusCode == status
}

// redactRESTError は、URL・レスポンス本文・送った本文をエラー文から除く。Discord のコードと文言は残す。
func redactRESTError(err error, operation string) error {
	var restErr *rest.Error
	if errors.As(err, &restErr) && restErr.Response != nil {
		return fmt.Errorf("discord rejected the %s: status %d, code %d: %s", operation, restErr.Response.StatusCode, restErr.Code, restErr.Message)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("discord %s request failed: %w", operation, urlErr.Err)
	}
	return fmt.Errorf("discord %s failed", operation)
}
