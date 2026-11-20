package reply

import (
	"context"
	"errors"
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/discordapi"
)

const (
	mentionFormat           = "<@%s>\n"         // 依頼者への通知。結果の本文の前に置く
	channelMessageOperation = "channel message" // エラー文に入れる操作の名前
)

// ErrPostRejected は、Discord が応答を返して投稿を断ったことを示す（投稿されていない）。
// 通信の失敗や時間切れ（投稿されたか分からない）は、これで包まない。
var ErrPostRejected = errors.New("discord rejected the post")

// ChannelPost は、チャンネルへの投稿 1 件。
type ChannelPost struct {
	ChannelID    string
	NotifyUserID string // この利用者にだけ通知する
	Result       command.Result
}

// ChannelPoster は、interaction の応答とは別に、チャンネルへ結果を投稿する。
type ChannelPoster interface {
	Post(ctx context.Context, post ChannelPost) error
}

// DiscordChannelPoster は disgo の REST で、bot としてチャンネルへメッセージを投稿する。
type DiscordChannelPoster struct {
	rest rest.Rest
}

// NewDiscordChannelPoster は apiBaseURL が空なら Discord の本番 URL を使う。
func NewDiscordChannelPoster(botToken string, apiBaseURL string) *DiscordChannelPoster {
	client := rest.NewClient(botToken, discordapi.ClientOpts(apiBaseURL)...)
	return &DiscordChannelPoster{rest: rest.New(client)}
}

// Post は、依頼者へのメンションに続けて結果を投稿する。通知が届くのは依頼者だけにする。
func (p *DiscordChannelPoster) Post(ctx context.Context, post ChannelPost) error {
	channelID, err := snowflake.Parse(post.ChannelID)
	if err != nil {
		return fmt.Errorf("invalid channel id: %w", err)
	}
	notifyUserID, err := snowflake.Parse(post.NotifyUserID)
	if err != nil {
		return fmt.Errorf("invalid notify user id: %w", err)
	}
	if _, err := p.rest.CreateMessage(channelID, buildChannelMessage(notifyUserID, post.Result), rest.WithCtx(ctx)); err != nil {
		return redactRESTError(err, channelMessageOperation)
	}
	return nil
}

// buildChannelMessage は、notifyUserID だけを通知先にした投稿を作る。本文中のほかのメンション（@everyone、ロール、別の利用者）は解釈させない。
func buildChannelMessage(notifyUserID snowflake.ID, result command.Result) discord.MessageCreate {
	layout := layoutMessage(fmt.Sprintf(mentionFormat, notifyUserID), result)
	onlyRequester := discord.AllowedMentions{
		Parse: []discord.AllowedMentionType{},
		Roles: []snowflake.ID{},
		Users: []snowflake.ID{notifyUserID},
	}
	return discord.NewMessageCreate().WithContent(layout.content).WithFiles(layout.files...).WithAllowedMentions(&onlyRequester)
}
