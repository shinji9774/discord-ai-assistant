package voicerecording

import (
	"context"
	"errors"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
)

const (
	OptOutCommandName = "record-optout"
	optOutDescription = "自分の音声を、今の録音の文字起こしと要約から除きます"

	optOutAcceptedText = "録音からの除外を受け付けました。この録音の文字起こしと要約に、あなたの音声は含まれません。"
)

// OptOutCommand は /record-optout。実行者の音声を、今の録音から除く。
type OptOutCommand struct {
	deps Dependencies
}

func NewOptOutCommand(deps Dependencies) *OptOutCommand {
	return &OptOutCommand{deps: deps}
}

func (c *OptOutCommand) Definition() discord.SlashCommandCreate {
	return discord.SlashCommandCreate{
		Name:        OptOutCommandName,
		Description: optOutDescription,
		Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
	}
}

// RepliesPrivately は、受付の返信と案内を実行者だけに見せることを示す。
func (c *OptOutCommand) RepliesPrivately() {}

// Execute は除外の印を置く。印を置いたあとに終了マーカーがあれば、
// 録音タスクが除外を読む前に終わった可能性があるため、録音していない案内を返す。
func (c *OptOutCommand) Execute(ctx context.Context, inv command.Invocation) (command.Result, error) {
	session, err := c.deps.Sessions.Active(ctx, inv.GuildID)
	if errors.Is(err, ErrNoRecording) {
		return command.Guided(notRecordingText, reasonNotRecording), nil
	}
	if err != nil {
		return command.Result{}, err
	}
	if err := c.deps.Sessions.OptOut(ctx, session, inv.UserID); err != nil {
		return command.Result{}, err
	}
	_, err = c.deps.Sessions.Ended(ctx, session)
	if err == nil {
		return command.Guided(notRecordingText, reasonNotRecording), nil
	}
	if !errors.Is(err, ErrNotEnded) {
		return command.Result{}, err
	}
	return command.Result{Text: optOutAcceptedText}, nil
}
