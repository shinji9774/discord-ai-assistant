package reply

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"unicode/utf16"

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
		return redactRequestURL(err)
	}
	return nil
}

func buildMessageUpdate(result command.Result) discord.MessageUpdate {
	content := result.Text
	var files []*discord.File
	if utf16Length(result.Text) > DiscordContentLimit {
		content = AttachmentNotice
		files = append(files, discord.NewFile(AttachmentFileName, "", bytes.NewReader([]byte(result.Text))))
	}
	for _, f := range result.Files {
		files = append(files, discord.NewFile(f.Name, "", bytes.NewReader(f.Content)))
	}
	return discord.NewMessageUpdate().WithContent(content).WithFiles(files...).WithAllowedMentions(&noAllowedMentions)
}

func utf16Length(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// redactRequestURL は interaction token を含む URL をエラー文から除く。
func redactRequestURL(err error) error {
	var restErr *rest.Error
	if errors.As(err, &restErr) && restErr.Response != nil {
		return fmt.Errorf("discord rejected the response update: status %d", restErr.Response.StatusCode)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("discord response update request failed: %w", urlErr.Err)
	}
	return errors.New("discord response update failed")
}
