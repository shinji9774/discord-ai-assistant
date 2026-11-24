package voicerecordingtest_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"discord-ai-assistant/internal/voicerecording"
	"discord-ai-assistant/internal/voicerecording/voicerecordingtest"
)

const (
	guildID   = "1100000000000000001"
	channelID = "1200000000000000002"
	speakerA  = "1500000000000000001"
	speakerB  = "1500000000000000002"
)

var (
	origin = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	frameA = voicerecordingtest.Frame{UserID: speakerA, Opus: []byte{0x01}, ReceivedAt: origin}
	frameB = voicerecordingtest.Frame{UserID: speakerB, Opus: []byte{0x02}, ReceivedAt: origin.Add(20 * time.Millisecond)}
)

// collectingSink は、受け取ったフレームを順に覚える FrameSink。
type collectingSink struct {
	frames []voicerecordingtest.Frame
}

func (s *collectingSink) WriteFrame(userID string, opus []byte, receivedAt time.Time) {
	s.frames = append(s.frames, voicerecordingtest.Frame{UserID: userID, Opus: opus, ReceivedAt: receivedAt})
}

var _ voicerecording.FrameSink = (*collectingSink)(nil)
var _ voicerecording.VoiceConnection = (*voicerecordingtest.Voice)(nil)

// receivedCounts は、channel にすでに届いている参加者数をすべて読む。
func receivedCounts(counts <-chan int) []int {
	var got []int
	for {
		select {
		case n := <-counts:
			got = append(got, n)
		default:
			return got
		}
	}
}

// 条件: 参加してから受信を始める
// 内容: 台本のフレームを順にすべて渡し、参加先と受信中であることを覚える
func TestVoiceReceive_AfterJoin_DeliversScriptedFramesInOrder(t *testing.T) {
	voice := voicerecordingtest.NewVoice(voicerecordingtest.Script{Frames: []voicerecordingtest.Frame{frameA, frameB}})
	mustJoin(t, voice)
	sink := &collectingSink{}

	voice.Receive(sink)

	type outcome struct {
		Frames []voicerecordingtest.Frame
		State  voicerecordingtest.State
	}
	got := outcome{Frames: sink.frames, State: voice.State()}
	want := outcome{
		Frames: []voicerecordingtest.Frame{frameA, frameB},
		State:  voicerecordingtest.State{GuildID: guildID, ChannelID: channelID, Receiving: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// 条件: 参加者数の台本を持つ接続に参加する
// 内容: 参加の直後に、台本の参加者数が順に届いている
func TestVoiceHumanCount_AfterJoin_YieldsScriptedCounts(t *testing.T) {
	voice := voicerecordingtest.NewVoice(voicerecordingtest.Script{HumanCounts: []int{2, 1, 0}})
	mustJoin(t, voice)

	got := receivedCounts(voice.HumanCount())

	if want := []int{2, 1, 0}; !reflect.DeepEqual(got, want) {
		t.Errorf("counts = %v, want %v", got, want)
	}
}

// 条件: 受信で切断を起こす台本で、受信を始める
// 内容: Disconnected が閉じる
func TestVoiceDisconnected_DisconnectOnReceive_ClosesAfterReceive(t *testing.T) {
	voice := voicerecordingtest.NewVoice(voicerecordingtest.Script{DisconnectOnReceive: true})
	mustJoin(t, voice)

	voice.Receive(&collectingSink{})

	select {
	case <-voice.Disconnected():
	default:
		t.Error("Disconnected is not closed")
	}
}

// 条件: 台本どおりに切断しない接続で、受信を始める
// 内容: Disconnected は閉じない
func TestVoiceDisconnected_NoDisconnectScripted_StaysOpen(t *testing.T) {
	voice := voicerecordingtest.NewVoice(voicerecordingtest.Script{})
	mustJoin(t, voice)

	voice.Receive(&collectingSink{})

	select {
	case <-voice.Disconnected():
		t.Error("Disconnected is closed")
	default:
	}
}

// 条件: 終わった context、または台本のエラーで参加する
// 内容: そのエラーを返し、参加先を覚えない
func TestVoiceJoin_CanceledContextOrScriptedError_ReturnsErrorWithoutJoining(t *testing.T) {
	errJoin := errors.New("join failed")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name   string
		ctx    context.Context
		script voicerecordingtest.Script
		want   error
	}{
		{"context が終わっている", canceled, voicerecordingtest.Script{}, context.Canceled},
		{"台本のエラー", context.Background(), voicerecordingtest.Script{JoinErr: errJoin}, errJoin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			voice := voicerecordingtest.NewVoice(tc.script)

			err := voice.Join(tc.ctx, guildID, channelID)

			if !errors.Is(err, tc.want) || voice.State() != (voicerecordingtest.State{}) {
				t.Errorf("Join = %v (state %+v), want %v with empty state", err, voice.State(), tc.want)
			}
		})
	}
}

// 条件: 受信中の接続から退出する
// 内容: 退出したことを覚える
func TestVoiceLeave_WhileReceiving_MarksLeft(t *testing.T) {
	voice := voicerecordingtest.NewVoice(voicerecordingtest.Script{})
	mustJoin(t, voice)
	voice.Receive(&collectingSink{})

	voice.Leave(context.Background())

	want := voicerecordingtest.State{GuildID: guildID, ChannelID: channelID, Receiving: true, Left: true}
	if got := voice.State(); got != want {
		t.Errorf("State = %+v, want %+v", got, want)
	}
}

func mustJoin(t *testing.T, voice *voicerecordingtest.Voice) {
	t.Helper()
	if err := voice.Join(context.Background(), guildID, channelID); err != nil {
		t.Fatalf("Join: %v", err)
	}
}
