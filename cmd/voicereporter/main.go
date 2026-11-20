// voicereporter は、文字起こしの完了を受けて録音の要約を投稿する Lambda のエントリポイント。依存の組み立てだけを行う。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"

	"discord-ai-assistant/internal/bottoken"
	"discord-ai-assistant/internal/config"
	"discord-ai-assistant/internal/discordapi"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/voicesummary"
)

const (
	envBotTokenParameter   = "BOT_TOKEN_PARAMETER_NAME"
	envVoiceBucket         = "VOICE_BUCKET_NAME"
	msgConfigLoadFailed    = "config load failed"
	msgBotTokenLoadFailed  = "bot token load failed"
	logKeyParameter        = "parameter"
	logKeyError            = "error"
	exitCodeStartupFailure = 1
)

// transcriptionJobStateChange は、EventBridge の "Transcribe Job State Change" の detail のうち、使う項目。
// 状態（COMPLETED / FAILED）は Reporter が Transcribe に問い合わせて確かめるので読まない。
type transcriptionJobStateChange struct {
	TranscriptionJobName string `json:"TranscriptionJobName"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		fmt.Fprintln(os.Stderr, "voicereporter:", err)
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

	reporter := voicesummary.NewReporter(
		voicesummary.NewS3Store(s3.NewFromConfig(awsConfig, voicesummary.ConfigureS3Client), bucket),
		voicesummary.NewTranscribeJobs(transcribe.NewFromConfig(awsConfig), bucket),
		llm.NewBedrockGenerator(bedrockruntime.NewFromConfig(awsConfig), cfg.TextModelID),
		reply.NewDiscordChannelPoster(token, os.Getenv(discordapi.EnvBaseURL)),
		cfg.SummarizeVoice,
		logger,
	)
	lambda.Start(newEventHandler(reporter))
	return nil
}

// newEventHandler は、完了のイベントからジョブ名を取り出して reporter に渡す Lambda のハンドラを返す。
// エラーを返すと、Lambda の非同期呼び出しの再試行に任せることになる（design「Reporter」の事後条件）。
func newEventHandler(reporter *voicesummary.Reporter) func(context.Context, events.EventBridgeEvent) error {
	return func(ctx context.Context, event events.EventBridgeEvent) error {
		var detail transcriptionJobStateChange
		if err := json.Unmarshal(event.Detail, &detail); err != nil {
			return fmt.Errorf("decode transcription job state change: %w", err)
		}
		return reporter.Report(ctx, detail.TranscriptionJobName)
	}
}

func requireEnv(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	return value, nil
}
