// Package register は slash command を Discord へ一括登録する。
package register

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"

	"discord-ai-assistant/internal/discordapi"
)

const (
	EnvBotToken = "DISCORD_BOT_TOKEN"

	actionCommandRegistration = "command registration"
	actionApplicationLookup   = "application lookup"
)

// Config は登録に必要な設定。
type Config struct {
	BotToken   string
	APIBaseURL string // 空なら本番の URL
}

// Run は bot トークンから Application ID を引き、全コマンドをグローバルコマンドとして一括で置き換え、登録した名前を 1 行ずつ stdout に書く。
func Run(ctx context.Context, cfg Config, definitions []discord.ApplicationCommandCreate, stdout io.Writer) error {
	if cfg.BotToken == "" {
		return errors.New(EnvBotToken + " is not set")
	}
	client := rest.New(rest.NewClient(cfg.BotToken, discordapi.ClientOpts(cfg.APIBaseURL)...))
	application, err := client.GetCurrentApplication(rest.WithCtx(ctx))
	if err != nil {
		return redactRequest(actionApplicationLookup, err)
	}
	if _, err := client.SetGlobalCommands(application.ID, definitions, rest.WithCtx(ctx)); err != nil {
		return redactRequest(actionCommandRegistration, err)
	}
	for _, definition := range definitions {
		if _, err := fmt.Fprintln(stdout, definition.CommandName()); err != nil {
			return fmt.Errorf("write registered command name: %w", err)
		}
	}
	return nil
}

// redactRequest は Authorization ヘッダを持つリクエストをエラー文に含めない。
func redactRequest(action string, err error) error {
	var restErr *rest.Error
	if errors.As(err, &restErr) && restErr.Response != nil {
		return fmt.Errorf("discord rejected the %s: status %d", action, restErr.Response.StatusCode)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("discord %s request failed: %w", action, urlErr.Err)
	}
	return fmt.Errorf("discord %s failed", action)
}
