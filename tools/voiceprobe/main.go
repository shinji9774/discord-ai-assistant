// voiceprobe は、実機確認の第 1 段で使う手元のプログラム。
// ボイスチャンネルに参加して話者ごとの FLAC を書き、参加者数と切断をログに出す。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"discord-ai-assistant/internal/voicerecording"
	"discord-ai-assistant/internal/voicerecording/discordvoice"
)

const (
	envBotToken     = "DISCORD_BOT_TOKEN"
	envGuildID      = "GUILD_ID"
	envChannelID    = "CHANNEL_ID"
	envOutDir       = "OUT_DIR"
	envDuration     = "PROBE_DURATION" // time.ParseDuration の形式（例: 2m）
	joinTimeout     = 60 * time.Second
	leaveTimeout    = 10 * time.Second
	exitCodeFailure = 1
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("voiceprobe failed", "error", err)
		os.Exit(exitCodeFailure)
	}
}

func run(logger *slog.Logger) error {
	env, err := requireEnv(envBotToken, envGuildID, envChannelID, envOutDir, envDuration)
	if err != nil {
		return err
	}
	duration, err := time.ParseDuration(env[envDuration])
	if err != nil {
		return fmt.Errorf("parse %s: %w", envDuration, err)
	}
	conn, err := discordvoice.New(env[envBotToken], logger)
	if err != nil {
		return err
	}
	joinCtx, cancel := context.WithTimeout(context.Background(), joinTimeout)
	defer cancel()
	if err := conn.Join(joinCtx, env[envGuildID], env[envChannelID]); err != nil {
		return err
	}
	tracks := voicerecording.NewTracks(env[envOutDir], time.Now())
	conn.Receive(tracks)
	logger.Info("recording started")
	waitUntilDone(logger, conn, duration)
	leaveCtx, cancelLeave := context.WithTimeout(context.Background(), leaveTimeout)
	defer cancelLeave()
	conn.Leave(leaveCtx)
	written, err := tracks.Close(time.Now())
	if err != nil {
		return err
	}
	for _, track := range written {
		logger.Info("track written", "user_id", track.UserID, "path", track.Path)
	}
	return nil
}

func waitUntilDone(logger *slog.Logger, conn *discordvoice.Connection, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	for {
		select {
		case count := <-conn.HumanCount():
			logger.Info("human count", "count", count)
		case <-conn.Disconnected():
			logger.Info("disconnected")
			return
		case <-timer.C:
			return
		}
	}
}

func requireEnv(names ...string) (map[string]string, error) {
	values := make(map[string]string, len(names))
	for _, name := range names {
		value := os.Getenv(name)
		if value == "" {
			return nil, fmt.Errorf("environment variable %s is required", name)
		}
		values[name] = value
	}
	return values, nil
}
