// Package voicerecordingtest は、voicerecording.VoiceConnection のインメモリ fake を提供する。
package voicerecordingtest

import (
	"context"
	"sync"
	"time"

	"discord-ai-assistant/internal/voicerecording"
)

// Frame は、1 人の話者の Opus フレーム 1 つ。
type Frame struct {
	UserID     string
	Opus       []byte
	ReceivedAt time.Time
}

// Script は、fake の接続で起きることの台本。
type Script struct {
	Frames              []Frame // Receive が渡すフレーム（この順）
	HumanCounts         []int   // Join の直後に HumanCount へ順に流す参加者数
	DisconnectOnReceive bool    // Receive で切断を起こす
	JoinErr             error   // Join が返すエラー
}

// State は、fake の接続が受けた操作。
type State struct {
	GuildID   string
	ChannelID string
	Receiving bool
	Left      bool
}

// Voice は、台本どおりに振る舞う VoiceConnection。
type Voice struct {
	script       Script
	humans       chan int
	disconnected chan struct{}

	mu    sync.Mutex
	state State
}

var _ voicerecording.VoiceConnection = (*Voice)(nil)

func NewVoice(script Script) *Voice {
	return &Voice{
		script:       script,
		humans:       make(chan int, len(script.HumanCounts)),
		disconnected: make(chan struct{}),
	}
}

// Join は、台本の参加者数を HumanCount に流し、参加先を覚える。
func (v *Voice) Join(ctx context.Context, guildID, channelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if v.script.JoinErr != nil {
		return v.script.JoinErr
	}
	v.mu.Lock()
	v.state.GuildID, v.state.ChannelID = guildID, channelID
	v.mu.Unlock()
	for _, count := range v.script.HumanCounts {
		v.humans <- count
	}
	return nil
}

// Receive は、台本のフレームを順にすべて frames に渡してから戻る。
func (v *Voice) Receive(frames voicerecording.FrameSink) {
	v.mu.Lock()
	v.state.Receiving = true
	v.mu.Unlock()
	for _, frame := range v.script.Frames {
		frames.WriteFrame(frame.UserID, frame.Opus, frame.ReceivedAt)
	}
	if v.script.DisconnectOnReceive {
		close(v.disconnected)
	}
}

func (v *Voice) HumanCount() <-chan int {
	return v.humans
}

func (v *Voice) Disconnected() <-chan struct{} {
	return v.disconnected
}

func (v *Voice) Leave(context.Context) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.state.Left = true
}

// State は、これまでに受けた操作を返す。
func (v *Voice) State() State {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.state
}
