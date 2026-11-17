// register は slash command を Discord へ登録するコマンドのエントリポイント。依存の組み立てだけを行う。
package main

import (
	"context"
	"fmt"
	"os"

	"discord-ai-assistant/internal/catalog"
	"discord-ai-assistant/internal/discordapi"
	"discord-ai-assistant/internal/register"
)

func main() {
	cfg := register.Config{
		ApplicationID: os.Getenv(register.EnvApplicationID),
		BotToken:      os.Getenv(register.EnvBotToken),
		APIBaseURL:    os.Getenv(discordapi.EnvBaseURL),
	}
	if err := register.Run(context.Background(), cfg, catalog.New(catalog.Dependencies{}), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "register:", err)
		os.Exit(1)
	}
}
