package voicerecording

import (
	"context"
	"errors"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
)

const (
	StopCommandName = "record-stop"
	stopDescription = "録音を停止します（録音を開始した人と、録音中のボイスチャンネルにいる人が使えます）"

	stopAcceptedText = "録音の停止を受け付けました。"
	notAllowedText   = "録音を停止できるのは、録音を開始した人と、録音中のボイスチャンネルにいる人だけです。"
	// notRecordingText は、/record-stop と /record-optout が共通で使う、録音していないときの案内。
	notRecordingText = "このサーバでは録音していません。"

	reasonNotAllowedToStop = "not_allowed_to_stop"
	reasonNotRecording     = "not_recording"
)

// StopCommand は /record-stop。録音の終了マーカーを書き、録音タスクに終わりを伝える。
type StopCommand struct {
	deps Dependencies
}

func NewStopCommand(deps Dependencies) *StopCommand {
	return &StopCommand{deps: deps}
}

func (c *StopCommand) Definition() discord.SlashCommandCreate {
	return discord.SlashCommandCreate{
		Name:        StopCommandName,
		Description: stopDescription,
		Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
	}
}

// RepliesPrivately は、受付の返信と案内を実行者だけに見せることを示す。
func (c *StopCommand) RepliesPrivately() {}

// Execute は、停止できる人かを確かめてから終了マーカーを書く。
// すでに終了マーカーがあっても受付の文を返す（録音は終了に向かっている）。
func (c *StopCommand) Execute(ctx context.Context, inv command.Invocation) (command.Result, error) {
	session, err := c.deps.Sessions.Active(ctx, inv.GuildID)
	if errors.Is(err, ErrNoRecording) {
		return command.Guided(notRecordingText, reasonNotRecording), nil
	}
	if err != nil {
		return command.Result{}, err
	}
	allowed, err := c.canStop(ctx, session, inv.UserID)
	if err != nil {
		return command.Result{}, err
	}
	if !allowed {
		return command.Guided(notAllowedText, reasonNotAllowedToStop), nil
	}
	if _, err := c.deps.Sessions.End(ctx, session, EndReasonStopCommand); err != nil {
		return command.Result{}, err
	}
	return command.Result{Text: stopAcceptedText}, nil
}

// canStop は、利用者が録音を開始した本人か、録音中のボイスチャンネルにいるかを返す。
func (c *StopCommand) canStop(ctx context.Context, session Session, userID string) (bool, error) {
	if userID == session.StarterID {
		return true, nil
	}
	channelID, err := c.deps.Guild.VoiceChannelOf(ctx, session.GuildID, userID)
	if errors.Is(err, ErrNotInVoice) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return channelID == session.VoiceChannelID, nil
}
