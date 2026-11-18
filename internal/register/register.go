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
	"github.com/disgoorg/snowflake/v2"

	"discord-ai-assistant/internal/discordapi"
)

const (
	EnvApplicationID = "DISCORD_APPLICATION_ID"
	EnvBotToken      = "DISCORD_BOT_TOKEN"
)

// Config は登録に必要な設定。
type Config struct {
	ApplicationID string
	BotToken      string
	APIBaseURL    string // 空なら本番の URL
}

// Run は全コマンドをグローバルコマンドとして一括で置き換え、登録した名前を 1 行ずつ stdout に書く。
func Run(ctx context.Context, cfg Config, definitions []discord.ApplicationCommandCreate, stdout io.Writer) error {
	if cfg.ApplicationID == "" {
		return errors.New(EnvApplicationID + " is not set")
	}
	if cfg.BotToken == "" {
		return errors.New(EnvBotToken + " is not set")
	}
	applicationID, err := snowflake.Parse(cfg.ApplicationID)
	if err != nil {
		return fmt.Errorf("invalid application id: %w", err)
	}

	client := rest.New(rest.NewClient(cfg.BotToken, discordapi.ClientOpts(cfg.APIBaseURL)...))
	if _, err := client.SetGlobalCommands(applicationID, definitions, rest.WithCtx(ctx)); err != nil {
		return redactRequest(err)
	}
	for _, definition := range definitions {
		if _, err := fmt.Fprintln(stdout, definition.CommandName()); err != nil {
			return fmt.Errorf("write registered command name: %w", err)
		}
	}
	return nil
}

// redactRequest は Authorization ヘッダを持つリクエストをエラー文に含めない。
func redactRequest(err error) error {
	var restErr *rest.Error
	if errors.As(err, &restErr) && restErr.Response != nil {
		return fmt.Errorf("discord rejected the command registration: status %d", restErr.Response.StatusCode)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("discord command registration request failed: %w", urlErr.Err)
	}
	return errors.New("discord command registration failed")
}
