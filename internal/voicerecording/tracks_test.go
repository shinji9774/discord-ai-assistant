package voicerecording_test

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	flacpcm "github.com/tphakala/go-flac/pcm"
	"github.com/tphakala/go-opus/opus"

	"discord-ai-assistant/internal/voicerecording"
)

const (
	speakerA         = "1500000000000000001"
	speakerB         = "1500000000000000002"
	samplesPerSecond = 16000                         // 仕様: 16 kHz・モノラル
	thresholdSamples = 100 * samplesPerSecond / 1000 // 仕様: 100 ms 以上の隙間だけを無音で埋める
	windowDuration   = 20 * time.Millisecond         // 音の有無を調べる窓の幅。Opus の 1 フレームと同じ
	windowSamples    = 20 * samplesPerSecond / 1000
	sineFrequencyHz  = 440
	sineAmplitude    = 10000
	opusPacketBuffer = 1276 // go-opus の仕様: 1 フレーム（20 ms 以下）の VBR パケットが収まる大きさ
)

var (
	recordingStart = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	// opusSilenceFrame は、Discord が送る 20 ms の Opus の無音フレーム。
	opusSilenceFrame = []byte{0xF8, 0xFF, 0xFE}
)

type trackLength struct {
	UserID  string
	Samples int64
}

// toneFrame は、復号すると 0 以外のサンプルになる 20 ms の Opus フレーム。
// 440 Hz の正弦波を go-opus のエンコーダで符号化して作る。
func toneFrame(t *testing.T) []byte {
	t.Helper()
	encoder, err := opus.NewEncoder(opus.EncoderConfig{SampleRate: samplesPerSecond, Channels: 1})
	if err != nil {
		t.Fatalf("create opus encoder: %v", err)
	}
	pcm := make([]int16, windowSamples)
	for i := range pcm {
		pcm[i] = int16(sineAmplitude * math.Sin(2*math.Pi*sineFrequencyHz*float64(i)/samplesPerSecond))
	}
	buf := make([]byte, opusPacketBuffer)
	n, err := encoder.Encode(pcm, buf)
	if err != nil {
		t.Fatalf("encode tone: %v", err)
	}
	return buf[:n]
}

// soundWindows は、トラックを 20 ms ごとの窓に区切り、0 以外のサンプルを含む窓の番号を返す。
func soundWindows(t *testing.T, path string) []int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read track: %v", err)
	}
	pcm, _, err := flacpcm.DecodeInterleaved(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode track: %v", err)
	}
	windows := []int{}
	windowBytes := windowSamples * 2 // 16 bit
	for start := 0; start < len(pcm); start += windowBytes {
		end := min(start+windowBytes, len(pcm))
		if bytes.Count(pcm[start:end], []byte{0}) != end-start {
			windows = append(windows, start/windowBytes)
		}
	}
	return windows
}

func at(offset time.Duration) time.Time {
	return recordingStart.Add(offset)
}

func trackLengths(t *testing.T, tracks []voicerecording.Track) []trackLength {
	t.Helper()
	lengths := make([]trackLength, 0, len(tracks))
	for _, track := range tracks {
		lengths = append(lengths, trackLength{UserID: track.UserID, Samples: flacSamples(t, track.Path)})
	}
	return lengths
}

func flacSamples(t *testing.T, path string) int64 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read track: %v", err)
	}
	_, info, err := flacpcm.DecodeInterleaved(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode track: %v", err)
	}
	return int64(info.TotalSamples)
}

// 条件: 話者 A が 0 秒・20 ms・1 秒に、話者 B が 500 ms に発話し、2 秒で録音を終える
// 内容: 全トラックが録音の長さ（2 秒 × 16000 サンプル）にそろい、初めて発話した順に並ぶ
func TestTracksClose_FramesWithGaps_PadsEveryTrackToRecordingLength(t *testing.T) {
	tracks := voicerecording.NewTracks(t.TempDir(), recordingStart)
	tracks.WriteFrame(speakerA, opusSilenceFrame, at(0))
	tracks.WriteFrame(speakerA, opusSilenceFrame, at(20*time.Millisecond))
	tracks.WriteFrame(speakerB, opusSilenceFrame, at(500*time.Millisecond))
	tracks.WriteFrame(speakerA, opusSilenceFrame, at(time.Second))

	got, err := tracks.Close(at(2 * time.Second))

	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	want := []trackLength{{UserID: speakerA, Samples: 2 * samplesPerSecond}, {UserID: speakerB, Samples: 2 * samplesPerSecond}}
	if lengths := trackLengths(t, got); !reflect.DeepEqual(lengths, want) {
		t.Errorf("track lengths = %+v, want %+v", lengths, want)
	}
}

// 条件: 話者 A が 0 秒と 1 秒に、話者 B が 500 ms に 20 ms の音声を発話し、2 秒で録音を終える
// 内容: 各音声は、発話した時刻の位置（20 ms 窓の番号）にだけ置かれ、前後は無音になる
func TestTracksClose_ToneFramesWithGaps_PlacesEachFrameAtItsOffset(t *testing.T) {
	frame := toneFrame(t)
	tracks := voicerecording.NewTracks(t.TempDir(), recordingStart)
	tracks.WriteFrame(speakerA, frame, at(0))
	tracks.WriteFrame(speakerB, frame, at(500*time.Millisecond))
	tracks.WriteFrame(speakerA, frame, at(time.Second))

	got, err := tracks.Close(at(2 * time.Second))

	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	want := map[string][]int{
		speakerA: {0, int(time.Second / windowDuration)},
		speakerB: {int(500 * time.Millisecond / windowDuration)},
	}
	gotWindows := map[string][]int{}
	for _, track := range got {
		gotWindows[track.UserID] = soundWindows(t, track.Path)
	}
	if !reflect.DeepEqual(gotWindows, want) {
		t.Errorf("sound windows = %v, want %v", gotWindows, want)
	}
}

// 条件: 誰も発話しないまま録音を終える
// 内容: トラックを 1 つも返さない
func TestTracksClose_NoFrames_ReturnsNoTracks(t *testing.T) {
	tracks := voicerecording.NewTracks(t.TempDir(), recordingStart)

	got, err := tracks.Close(at(time.Second))

	if err != nil || !reflect.DeepEqual(got, []voicerecording.Track{}) {
		t.Errorf("Close = (%+v, %v), want ([], nil)", got, err)
	}
}

// 条件: 書き込み先のディレクトリが存在しない
// 内容: Close がエラーを返す
func TestTracksClose_MissingDirectory_ReturnsError(t *testing.T) {
	tracks := voicerecording.NewTracks(filepath.Join(t.TempDir(), "missing"), recordingStart)
	tracks.WriteFrame(speakerA, opusSilenceFrame, at(0))

	_, err := tracks.Close(at(time.Second))

	if err == nil {
		t.Error("Close error = nil, want error")
	}
}

// 条件: 書き込み済みの長さと、次のフレームの位置の差がいろいろ
// 内容: 差がしきい値（100 ms）以上のときだけ、差の分の無音を返す
func TestPaddingSamples_GapAgainstThreshold_ReturnsSilenceToInsert(t *testing.T) {
	cases := []struct {
		name    string
		written int64
		offset  int64
		want    int64
	}{
		{name: "しきい値未満の隙間は埋めない", written: 0, offset: thresholdSamples - 1, want: 0},
		{name: "しきい値ちょうどの隙間は埋める", written: 0, offset: thresholdSamples, want: thresholdSamples},
		{name: "1 秒の隙間は 1 秒分埋める", written: samplesPerSecond, offset: 2 * samplesPerSecond, want: samplesPerSecond},
		{name: "書き込み済みが先行していれば埋めない", written: 2 * thresholdSamples, offset: thresholdSamples, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := voicerecording.PaddingSamples(tc.written, tc.offset)

			if got != tc.want {
				t.Errorf("PaddingSamples(%d, %d) = %d, want %d", tc.written, tc.offset, got, tc.want)
			}
		})
	}
}
