package voicerecording

import (
	"errors"
	"time"
)

// Session は、1 つのサーバでの 1 回の録音。ロック（active.json）の中身として S3 に置く。
type Session struct {
	RequestID      string    `json:"request_id"`
	GuildID        string    `json:"guild_id"`
	VoiceChannelID string    `json:"voice_channel_id"`
	TextChannelID  string    `json:"text_channel_id"`
	StarterID      string    `json:"starter_id"`
	BotUserID      string    `json:"bot_user_id"`
	AcceptedAt     time.Time `json:"accepted_at"`
	// Deadline は、録音タスクが終わっているはずの時刻。過ぎてから猶予を置くと、ほかの依頼がロックを取り直せる。
	Deadline time.Time `json:"deadline"`
}

// Started は、録音を始めた記録。元の表示名を、表示名を戻すときのために持つ。
type Started struct {
	OriginalNickname string `json:"original_nickname"`
}

// EndReason は、録音が終わった理由。
type EndReason string

const (
	EndReasonStopCommand  EndReason = "stop_command"
	EndReasonEveryoneLeft EndReason = "everyone_left"
	EndReasonTimeLimit    EndReason = "time_limit"
	EndReasonDisconnected EndReason = "disconnected"
)

var (
	ErrRecordingActive = errors.New("recording already active in guild")
	ErrNoRecording     = errors.New("no active recording in guild")
	ErrNotStarted      = errors.New("recording has not started")
	ErrNotEnded        = errors.New("recording has not ended")
)
