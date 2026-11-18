// worker はコマンドを実行する Lambda のエントリポイント。依存の組み立てだけを行う。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	awstranslate "github.com/aws/aws-sdk-go-v2/service/translate"

	"discord-ai-assistant/internal/bottoken"
	"discord-ai-assistant/internal/catalog"
	"discord-ai-assistant/internal/discordapi"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/translate"
	"discord-ai-assistant/internal/worker"
)

const (
	envBedrockModelID      = "BEDROCK_MODEL_ID"
	envBotTokenParameter   = "BOT_TOKEN_PARAMETER_NAME"
	msgBotTokenLoadFailed  = "bot token load failed"
	logKeyParameter        = "parameter"
	logKeyError            = "error"
	exitCodeStartupFailure = 1
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(exitCodeStartupFailure)
	}
}

func run(logger *slog.Logger) error {
	modelID, err := requireEnv(envBedrockModelID)
	if err != nil {
		return err
	}
	parameterName, err := requireEnv(envBotTokenParameter)
	if err != nil {
		return err
	}
	ctx := context.Background()
	awsConfig, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}
	token, err := bottoken.Load(ctx, ssm.NewFromConfig(awsConfig), parameterName)
	if err != nil {
		logger.Error(msgBotTokenLoadFailed, logKeyParameter, parameterName, logKeyError, err)
		return fmt.Errorf("%s", msgBotTokenLoadFailed)
	}

	generator := llm.NewBedrockGenerator(bedrockruntime.NewFromConfig(awsConfig), modelID)
	translator := translate.NewAmazonTranslator(awstranslate.NewFromConfig(awsConfig))
	fetcher := translate.NewHTTPAttachmentFetcher(http.DefaultClient)
	registry := catalog.New(catalog.Dependencies{Generator: generator, Translator: translator, Fetcher: fetcher, BotToken: token})
	responder := reply.NewDiscordResponder(os.Getenv(discordapi.EnvBaseURL))
	lambda.Start(worker.NewHandler(registry, responder, logger).Handle)
	return nil
}

func requireEnv(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	return value, nil
}
