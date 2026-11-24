// Command recorder は、1 件の録音を行う ECS Fargate のタスク。
// worker が RunTask の上書きで渡したサーバと依頼の ID の録音をし、終わると終了する。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"

	"discord-ai-assistant/internal/bottoken"
	"discord-ai-assistant/internal/config"
	"discord-ai-assistant/internal/discordapi"
	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/voicerecording"
	"discord-ai-assistant/internal/voicerecording/discordvoice"
	"discord-ai-assistant/internal/voicesummary"
)

const (
	envBotTokenParameter = "BOT_TOKEN_PARAMETER_NAME"
	envVoiceBucket       = "VOICE_BUCKET_NAME"

	// workDirPattern は、録音中の FLAC を書く一時ディレクトリの名前の型。
	workDirPattern = "recording-"

	msgBotTokenLoadFailed = "bot token load failed"
	msgRecordingFailed    = "recording failed"
	logKeyGuildID         = "guild_id"
	logKeyRequestID       = "request_id"
	logKeyParameter       = "parameter"
	logKeyError           = "error"

	exitCodeFailure = 1
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		fmt.Fprintln(os.Stderr, "recorder:", err)
		os.Exit(exitCodeFailure)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	env, err := requireEnv(voicerecording.EnvGuildID, voicerecording.EnvRequestID, envBotTokenParameter, envVoiceBucket)
	if err != nil {
		return err
	}
	guildID, requestID, bucket := env[voicerecording.EnvGuildID], env[voicerecording.EnvRequestID], env[envVoiceBucket]
	logger = logger.With(logKeyGuildID, guildID, logKeyRequestID, requestID)

	awsConfig, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}
	token, err := bottoken.Load(ctx, ssm.NewFromConfig(awsConfig), env[envBotTokenParameter])
	if err != nil {
		logger.Error(msgBotTokenLoadFailed, logKeyParameter, env[envBotTokenParameter], logKeyError, err.Error())
		return err
	}
	workDir, err := os.MkdirTemp("", workDirPattern)
	if err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}
	defer os.RemoveAll(workDir)
	voice, err := discordvoice.New(token, logger)
	if err != nil {
		return err
	}

	s3Client := s3.NewFromConfig(awsConfig, voicesummary.ConfigureS3Client)
	discordBaseURL := os.Getenv(discordapi.EnvBaseURL)
	deps := voicerecording.RecorderDependencies{
		Sessions: voicerecording.NewSessionStore(s3Client, bucket),
		Voice:    voice,
		Guild:    voicerecording.NewDiscordGuild(token, discordBaseURL),
		Poster:   reply.NewDiscordChannelPoster(token, discordBaseURL),
		Audio:    voicesummary.NewS3Store(s3Client, bucket),
		Jobs:     voicesummary.NewTranscribeJobs(transcribe.NewFromConfig(awsConfig), bucket),
		Now:      time.Now,
		WorkDir:  workDir,
	}
	recorder := voicerecording.NewRecorder(deps, cfg.Recording.RecorderTiming(), logger)
	if err := recorder.Run(ctx, guildID, requestID); err != nil {
		logger.Error(msgRecordingFailed, logKeyError, err.Error())
		return err
	}
	return nil
}

// requireEnv は、names の環境変数をすべて読む。どれかが空ならエラー。
func requireEnv(names ...string) (map[string]string, error) {
	values := make(map[string]string, len(names))
	for _, name := range names {
		value := os.Getenv(name)
		if value == "" {
			return nil, fmt.Errorf("%s is not set", name)
		}
		values[name] = value
	}
	return values, nil
}
