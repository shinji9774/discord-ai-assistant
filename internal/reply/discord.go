package reply

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/discordapi"
)

// noAllowedMentions は空の allowed_mentions。parse などを null でなく [] で送り、どの種類のメンションも解釈させない。
var noAllowedMentions = discord.AllowedMentions{
	Parse: []discord.AllowedMentionType{},
	Roles: []snowflake.ID{},
	Users: []snowflake.ID{},
}

// noMention は、応答の更新の本文の先頭に置く文字列。応答の更新は誰にも通知しない。
const noMention = ""

// responseUpdateOperation は、応答の更新の失敗をエラー文で示す操作の名前。
const responseUpdateOperation = "response update"

// DiscordResponder は disgo の REST で interaction の応答を更新する。
// interaction token の経路は認可ヘッダが要らないため、bot トークンは持たない。
type DiscordResponder struct {
	rest rest.Rest
}

// NewDiscordResponder は apiBaseURL が空なら Discord の本番 URL を使う。
func NewDiscordResponder(apiBaseURL string) *DiscordResponder {
	client := rest.NewClient("", discordapi.ClientOpts(apiBaseURL)...)
	return &DiscordResponder{rest: rest.New(client)}
}

func (r *DiscordResponder) Respond(ctx context.Context, inv command.Invocation, result command.Result) error {
	applicationID, err := snowflake.Parse(inv.ApplicationID)
	if err != nil {
		return fmt.Errorf("invalid application id: %w", err)
	}
	update := buildMessageUpdate(result)
	if _, err := r.rest.UpdateInteractionResponse(applicationID, inv.InteractionToken, update, rest.WithCtx(ctx)); err != nil {
		return redactRESTError(err, responseUpdateOperation)
	}
	return nil
}

func buildMessageUpdate(result command.Result) discord.MessageUpdate {
	layout := layoutMessage(noMention, result)
	return discord.NewMessageUpdate().WithContent(layout.content).WithFiles(layout.files...).WithAllowedMentions(&noAllowedMentions)
}

// redactRESTError は、URL（interaction token を含む）・レスポンス本文・送った本文をエラー文から除く。Discord のコードと文言は残す。
// operation は、失敗した操作の名前としてエラー文に入れる。
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
