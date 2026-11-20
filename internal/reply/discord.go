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
		files = append(files, discord.NewFile(attachmentFileName(result.Format), "", bytes.NewReader([]byte(result.Text))))
	}
	for _, f := range result.Files {
		files = append(files, discord.NewFile(f.Name, "", bytes.NewReader(f.Content)))
	}
	return discord.NewMessageUpdate().WithContent(content).WithFiles(files...).WithAllowedMentions(&noAllowedMentions)
}

// attachmentFileName は、本文の記法に合うファイル名を返す。markdown は拡張子を .md にして、閲覧時に整形されるようにする。
func attachmentFileName(format command.TextFormat) string {
	if format == command.TextFormatMarkdown {
		return MarkdownAttachmentFileName
	}
	return AttachmentFileName
}

func utf16Length(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// redactRequestURL は interaction token を含む URL とレスポンス本文をエラー文から除く。Discord のコードと文言は残す。
func redactRequestURL(err error) error {
	var restErr *rest.Error
	if errors.As(err, &restErr) && restErr.Response != nil {
		return fmt.Errorf("discord rejected the response update: status %d, code %d: %s", restErr.Response.StatusCode, restErr.Code, restErr.Message)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("discord response update request failed: %w", urlErr.Err)
	}
	return errors.New("discord response update failed")
}
