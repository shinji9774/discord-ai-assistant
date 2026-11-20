// voicefailure は、やり直しを使い切った音声要約の依頼を通知する Lambda のエントリポイント。依存の組み立てだけを行う。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"discord-ai-assistant/internal/bottoken"
	"discord-ai-assistant/internal/discordapi"
	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/voicesummary"
)

const (
	envBotTokenParameter   = "BOT_TOKEN_PARAMETER_NAME"
	envVoiceBucket         = "VOICE_BUCKET_NAME"
	msgBotTokenLoadFailed  = "bot token load failed"
	logKeyParameter        = "parameter"
	logKeyError            = "error"
	exitCodeStartupFailure = 1
)

// invocationRecord は、Lambda の非同期呼び出しの失敗時の宛先に渡る記録のうち、使う項目。
type invocationRecord struct {
	RequestPayload events.EventBridgeEvent `json:"requestPayload"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		fmt.Fprintln(os.Stderr, "voicefailure:", err)
		os.Exit(exitCodeStartupFailure)
	}
}

func run(logger *slog.Logger) error {
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

	notifier := voicesummary.NewFailureNotifier(
		voicesummary.NewS3Store(s3.NewFromConfig(awsConfig, voicesummary.ConfigureS3Client), bucket),
		reply.NewDiscordChannelPoster(token, os.Getenv(discordapi.EnvBaseURL)),
		logger,
	)
	lambda.Start(newFailureHandler(notifier))
	return nil
}

// newFailureHandler は、失敗の記録からジョブ名を取り出して notifier に渡す Lambda のハンドラを返す。
func newFailureHandler(notifier *voicesummary.FailureNotifier) func(context.Context, invocationRecord) error {
	return func(ctx context.Context, record invocationRecord) error {
		jobName, err := voicesummary.TranscriptionJobNameOf(record.RequestPayload.Detail)
		if err != nil {
			return err
		}
		return notifier.Notify(ctx, jobName)
	}
}

func requireEnv(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	return value, nil
}
