// worker はコマンドを実行する Lambda のエントリポイント。依存の組み立てだけを行う。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"
	awstranslate "github.com/aws/aws-sdk-go-v2/service/translate"

	"discord-ai-assistant/internal/bottoken"
	"discord-ai-assistant/internal/catalog"
	"discord-ai-assistant/internal/config"
	"discord-ai-assistant/internal/discordapi"
	"discord-ai-assistant/internal/imagegen"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/summarize"
	"discord-ai-assistant/internal/translate"
	"discord-ai-assistant/internal/voicesummary"
	"discord-ai-assistant/internal/voicesummary/craig"
	"discord-ai-assistant/internal/websearch"
	"discord-ai-assistant/internal/worker"
)

const (
	envBotTokenParameter   = "BOT_TOKEN_PARAMETER_NAME"
	envVoiceBucket         = "VOICE_BUCKET_NAME"
	envCraigBaseURL        = "CRAIG_BASE_URL" // 結合テストが Craig の fake へ向けるために使う。本番では設定しない
	msgConfigLoadFailed    = "config load failed"
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
	cfg, err := config.Load()
	if err != nil {
		logger.Error(msgConfigLoadFailed, logKeyError, err.Error())
		return fmt.Errorf("%s", msgConfigLoadFailed)
	}
	parameterName, err := requireEnv(envBotTokenParameter)
	if err != nil {
		return err
	}
	bucket, err := requireEnv(envVoiceBucket)
	if err != nil {
		return err
	}
	if baseURL := os.Getenv(envCraigBaseURL); baseURL != "" {
		cfg.SummarizeVoice.CraigBaseURL = baseURL
	}
	ctx := context.Background()
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}
	token, err := bottoken.Load(ctx, ssm.NewFromConfig(awsConfig), parameterName)
	if err != nil {
		logger.Error(msgBotTokenLoadFailed, logKeyParameter, parameterName, logKeyError, err)
		return fmt.Errorf("%s", msgBotTokenLoadFailed)
	}

	generator := llm.NewBedrockGenerator(bedrockruntime.NewFromConfig(awsConfig), cfg.TextModelID)
	searcher := websearch.NewAgentCoreSearcher(bedrockagentcore.NewFromConfig(awsConfig))
	imageClient := bedrockruntime.NewFromConfig(awsConfig, func(o *bedrockruntime.Options) {
		o.Region = cfg.Image.BedrockRegion
	})
	imageGenerator := imagegen.NewStabilityGenerator(imageClient, cfg.Image)
	translator := translate.NewAmazonTranslator(awstranslate.NewFromConfig(awsConfig))
	fetcher := translate.NewHTTPAttachmentFetcher(http.DefaultClient)
	discordBaseURL := os.Getenv(discordapi.EnvBaseURL)
	history := summarize.NewDiscordHistoryReader(token, discordBaseURL)
	voice := voicesummary.Dependencies{
		Recordings: craig.NewClient(cfg.SummarizeVoice.CraigBaseURL, http.DefaultClient),
		Store:      voicesummary.NewS3Store(s3.NewFromConfig(awsConfig, voicesummary.ConfigureS3Client), bucket),
		Jobs:       voicesummary.NewTranscribeJobs(transcribe.NewFromConfig(awsConfig), bucket),
		Poster:     reply.NewDiscordChannelPoster(token, discordBaseURL),
	}
	registry := catalog.New(catalog.Dependencies{Generator: generator, ToolGenerator: generator, Searcher: searcher, Logger: logger, ImageGenerator: imageGenerator, Translator: translator, Fetcher: fetcher, History: history, Voice: voice, BotToken: token}, cfg)
	responder := reply.NewDiscordResponder(discordBaseURL)
	replyReserve := time.Duration(cfg.ReplyReserveSeconds) * time.Second
	lambda.Start(worker.NewHandler(registry, responder, logger, replyReserve).Handle)
	return nil
}

func requireEnv(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	return value, nil
}
