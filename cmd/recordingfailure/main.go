// Command recordingfailure は、止まった録音タスクの後始末をする Lambda。
// EventBridge の ECS Task State Change（STOPPED）を受け取る。
package main

import (
	"context"
	"errors"
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
	"discord-ai-assistant/internal/voicerecording"
	"discord-ai-assistant/internal/voicesummary"
)

const (
	envBotTokenParameter = "BOT_TOKEN_PARAMETER_NAME"
	envVoiceBucket       = "VOICE_BUCKET_NAME"

	msgBotTokenLoadFailed = "bot token load failed"
	msgNotRecordingTask   = "stopped task is not a recording task"
	logKeyParameter       = "parameter"
	logKeyError           = "error"

	exitCodeStartupFailure = 1
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		fmt.Fprintln(os.Stderr, "recordingfailure:", err)
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
		logger.Error(msgBotTokenLoadFailed, logKeyParameter, parameterName, logKeyError, err.Error())
		return err
	}
	s3Client := s3.NewFromConfig(awsConfig, voicesummary.ConfigureS3Client)
	discordBaseURL := os.Getenv(discordapi.EnvBaseURL)
	handler := voicerecording.NewFailureHandler(voicerecording.FailureDependencies{
		Sessions: voicerecording.NewSessionStore(s3Client, bucket),
		Audio:    voicesummary.NewS3Store(s3Client, bucket),
		Guild:    voicerecording.NewDiscordGuild(token, discordBaseURL),
		Poster:   reply.NewDiscordChannelPoster(token, discordBaseURL),
	}, logger)
	lambda.Start(newStoppedTaskHandler(handler, logger))
	return nil
}

// newStoppedTaskHandler は、停止のイベントから録音の ID を読み、後始末を呼ぶ。
// 録音の ID を持たないタスクのイベントは、ログを残して成功として扱う（再試行しても結果は変わらない）。
func newStoppedTaskHandler(handler *voicerecording.FailureHandler, logger *slog.Logger) func(context.Context, events.EventBridgeEvent) error {
	return func(ctx context.Context, event events.EventBridgeEvent) error {
		task, err := voicerecording.StoppedRecording(event.Detail)
		if errors.Is(err, voicerecording.ErrNotRecordingTask) {
			logger.Warn(msgNotRecordingTask)
			return nil
		}
		if err != nil {
			return err
		}
		return handler.Handle(ctx, task)
	}
}

func requireEnv(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	return value, nil
}
