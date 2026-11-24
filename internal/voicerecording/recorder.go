package voicerecording

import (
	"context"
	"time"
)

// VoiceConnection は、ボイスチャンネルへの 1 本の接続を表す。
type VoiceConnection interface {
	// Join は、ボイスチャンネルに参加し、音声を受信できる状態になったら戻る。
	Join(ctx context.Context, guildID, channelID string) error
	// Receive は、受信した音声を frames に渡し始める。frames は 1 つの goroutine から呼ぶ。
	Receive(frames FrameSink)
	// HumanCount は、bot ではない参加者の数を、参加の直後と、変わるたびに流す。
	HumanCount() <-chan int
	// Disconnected は、Leave を呼ぶ前に接続が切れたら閉じる。
	Disconnected() <-chan struct{}
	// Leave は、ボイスチャンネルから退出する。戻ったあとは frames を呼ばない。
	Leave(ctx context.Context)
}

// FrameSink は、1 人の話者の Opus フレームを 1 つ受け取る。
type FrameSink interface {
	WriteFrame(userID string, opus []byte, receivedAt time.Time)
}

var _ FrameSink = (*Tracks)(nil)
