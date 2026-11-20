package summarize

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"discord-ai-assistant/internal/discordapi"
)

// 履歴の取得の失敗を表す文言。レスポンス本文と URL は会話や秘密を含みうるため、元のエラーの文言は入れない。
const (
	errorMessageGetMessagesRequest = "get channel messages: request failed"
	errorDetailGetMessagesHTTP     = "get channel messages: status %d, code %d: %s"
	errorFormatGetMessagesRequest  = errorMessageGetMessagesRequest + ": %w"
)

// メンションの書式。ユーザーのメンションは <@ID> と、ニックネーム形式の <@!ID> がある。
const (
	mentionPrefix         = "<@"
	mentionNicknamePrefix = "<@!"
	mentionSuffix         = ">"
	mentionDisplayPrefix  = "@"
)

// DiscordHistoryReader は Discord REST API でチャンネルの会話履歴を読む。
type DiscordHistoryReader struct {
	channels rest.Channels
}

var _ HistoryReader = (*DiscordHistoryReader)(nil)

// NewDiscordHistoryReader は botToken で認証する読み手を作る。apiBaseURL が空なら Discord の本番の API を使う。
func NewDiscordHistoryReader(botToken, apiBaseURL string) *DiscordHistoryReader {
	return &DiscordHistoryReader{channels: rest.New(rest.NewClient(botToken, discordapi.ClientOpts(apiBaseURL)...))}
}

// ReadBefore は before より前の投稿を新しい順に最大 PageSize 件読む。
func (r *DiscordHistoryReader) ReadBefore(ctx context.Context, channelID, before snowflake.ID) ([]Message, error) {
	page, err := r.channels.GetMessages(channelID, 0, before, 0, PageSize, rest.WithCtx(ctx))
	if err != nil {
		return nil, redactHistoryError(err)
	}
	messages := make([]Message, 0, len(page))
	for _, m := range page {
		messages = append(messages, toMessage(m))
	}
	return messages, nil
}

// redactHistoryError は、403 を HistoryForbiddenError に、その他の HTTP エラーを status・Discord のコードと文言だけにする。
// HTTP のレスポンスが無い失敗は、URL を除いた内側のエラーだけを残す。元のエラーの文言には URL が入りうる。
func redactHistoryError(err error) error {
	var restErr *rest.Error
	if errors.As(err, &restErr) && restErr.Response != nil {
		return redactRestError(restErr)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf(errorFormatGetMessagesRequest, urlErr.Err)
	}
	return errors.New(errorMessageGetMessagesRequest)
}

// redactRestError は Discord の HTTP エラーから、レスポンス本文と URL を除く。
func redactRestError(restErr *rest.Error) error {
	if restErr.Response.StatusCode == http.StatusForbidden {
		return &HistoryForbiddenError{Code: int(restErr.Code), Message: restErr.Message}
	}
	return fmt.Errorf(errorDetailGetMessagesHTTP, restErr.Response.StatusCode, restErr.Code, restErr.Message)
}

// toMessage は Discord の投稿を、要約で扱う投稿に変換する。
func toMessage(m discord.Message) Message {
	return Message{
		ID:         m.ID,
		SentAt:     m.CreatedAt,
		AuthorName: m.Author.EffectiveName(),
		FromBot:    m.Author.Bot || m.WebhookID != nil,
		System:     m.Type.System(),
		InProgress: m.Flags.Has(discord.MessageFlagLoading),
		Content:    replaceUserMentions(m.Content, m.Mentions),
	}
}

// replaceUserMentions は、本文のユーザーのメンション（<@ID> と <@!ID>）を @表示名 に置き換える。
// モデルが ID ではなく名前で「誰が」を書けるようにするため。
func replaceUserMentions(content string, mentions []discord.User) string {
	replacements := make([]string, 0, len(mentions)*4)
	for _, u := range mentions {
		id := u.ID.String()
		name := mentionDisplayPrefix + u.EffectiveName()
		replacements = append(replacements, mentionPrefix+id+mentionSuffix, name, mentionNicknamePrefix+id+mentionSuffix, name)
	}
	return strings.NewReplacer(replacements...).Replace(content)
}
