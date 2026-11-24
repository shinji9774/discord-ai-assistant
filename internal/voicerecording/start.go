package voicerecording

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/voicesummary"
)

const (
	StartCommandName = "record-start"
	startDescription = "あなたがいるボイスチャンネルの録音を始めます。終わると文字起こしと要約をこのチャンネルに投稿します"

	startAcceptedText        = "録音の開始を受け付けました。準備に 1 分ほどかかります。録音が始まったら、このチャンネルでお知らせします。"
	notInVoiceText           = "ボイスチャンネルに参加してから、もう一度 `/record-start` を実行してください。"
	recordingActiveText      = "このサーバでは録音中、または録音の終了処理中です。終わってからもう一度実行してください。"
	missingPermissionsFormat = "bot の権限が足りないため、録音を始められません（%s）。サーバの管理者に権限の追加を依頼してください。"

	// 案内で終えた理由（worker のログ用）。
	reasonNotInVoice         = "not_in_voice"
	reasonRecordingActive    = "recording_active"
	reasonMissingPermissions = "missing_permissions"
)

// Dependencies は、録音の 3 つのコマンドが使う依存。
type Dependencies struct {
	Sessions *SessionStore
	Guild    *DiscordGuild
	Launcher TaskLauncher
	Now      func() time.Time
}

// TaskLauncher は、録音タスクを 1 つ起動する。起動を受け付けられなければエラーを返す。
type TaskLauncher interface {
	Launch(ctx context.Context, session Session) error
}

// StartCommand は /record-start。実行者がいるボイスチャンネルの録音を始める。
type StartCommand struct {
	deps Dependencies
	cfg  Config
}

func NewStartCommand(deps Dependencies, cfg Config) *StartCommand {
	return &StartCommand{deps: deps, cfg: cfg}
}

func (c *StartCommand) Definition() discord.SlashCommandCreate {
	return discord.SlashCommandCreate{
		Name:        StartCommandName,
		Description: startDescription,
		Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
	}
}

// RepliesPrivately は、受付の返信と案内を実行者だけに見せることを示す。周知は録音タスクがチャンネルへ投稿する。
func (c *StartCommand) RepliesPrivately() {}

// Execute は、参加、権限、ロックの順に確かめてから録音タスクを起動し、受付の文を返す。
func (c *StartCommand) Execute(ctx context.Context, inv command.Invocation) (command.Result, error) {
	voiceChannelID, err := c.deps.Guild.VoiceChannelOf(ctx, inv.GuildID, inv.UserID)
	if errors.Is(err, ErrNotInVoice) {
		return command.Guided(notInVoiceText, reasonNotInVoice), nil
	}
	if err != nil {
		return command.Result{}, err
	}
	missing, err := c.missingPermissions(ctx, inv, voiceChannelID)
	if err != nil {
		return command.Result{}, err
	}
	if missing != "" {
		return command.Guided(fmt.Sprintf(missingPermissionsFormat, missing), reasonMissingPermissions), nil
	}
	session := c.newSession(inv, voiceChannelID)
	err = c.deps.Sessions.Acquire(ctx, session)
	if errors.Is(err, ErrRecordingActive) {
		return command.Guided(recordingActiveText, reasonRecordingActive), nil
	}
	if err != nil {
		return command.Result{}, err
	}
	if err := c.launch(ctx, session); err != nil {
		return command.Result{}, err
	}
	return command.Result{Text: startAcceptedText}, nil
}

// missingPermissions は、実行されたチャンネルと対象のボイスチャンネルで、bot に足りない権限の案内を返す。
func (c *StartCommand) missingPermissions(ctx context.Context, inv command.Invocation, voiceChannelID string) (string, error) {
	voicePermissions, err := c.deps.Guild.BotPermissionsIn(ctx, inv.GuildID, voiceChannelID, inv.ApplicationID)
	if err != nil {
		return "", err
	}
	return describeMissing([]permissionPlace{
		{label: thisChannelLabel, required: voicesummary.RequiredPostPermissions(inv.ChannelType), granted: inv.AppPermissions},
		{label: voiceChannelLabel, required: voiceChannelPermissions, granted: voicePermissions},
	}), nil
}

// newSession は、受付の時刻から期限を決めた録音を作る。bot のユーザー ID はアプリケーション ID と同じ値。
func (c *StartCommand) newSession(inv command.Invocation, voiceChannelID string) Session {
	acceptedAt := c.deps.Now()
	return Session{
		RequestID:      inv.InteractionID,
		GuildID:        inv.GuildID,
		VoiceChannelID: voiceChannelID,
		TextChannelID:  inv.ChannelID,
		StarterID:      inv.UserID,
		BotUserID:      inv.ApplicationID,
		AcceptedAt:     acceptedAt,
		Deadline:       acceptedAt.Add(c.cfg.sessionLength()),
	}
}

// launch は録音タスクを起動する。起動できなければロックを外し、次の /record-start を待たせない。
func (c *StartCommand) launch(ctx context.Context, session Session) error {
	launchErr := c.deps.Launcher.Launch(ctx, session)
	if launchErr == nil {
		return nil
	}
	if err := c.deps.Sessions.Release(ctx, session); err != nil {
		return errors.Join(launchErr, err)
	}
	return launchErr
}
