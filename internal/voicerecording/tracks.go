// Package voicerecording は、bot 自身によるボイスチャンネルの録音（/record-start、/record-stop、/record-optout と録音タスク）を扱う。
package voicerecording

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	flacpcm "github.com/tphakala/go-flac/pcm"
	"github.com/tphakala/go-opus/opus"
)

const (
	sampleRate           = 16000
	bitDepth             = 16
	channelCount         = 1
	bytesPerSample       = bitDepth / 8
	maxOpusFrameDuration = 120 * time.Millisecond
	maxFrameSamples      = int(sampleRate * maxOpusFrameDuration / time.Second)
	silenceGapThreshold  = 100 * time.Millisecond
	trackFileExtension   = ".flac"
)

// silence は、無音を書くときに使う 1 フレーム分のゼロ。書き換えない。
var silence = make([]byte, maxFrameSamples*bytesPerSample)

// Track は、1 人の話者の録音ファイル。
type Track struct {
	UserID string
	Path   string
}

// Tracks は、話者ごとの音声を、録音の原点からの同じ時間軸にそろえて FLAC に書く。
// WriteFrame は 1 つの goroutine から呼ばれる前提で、排他しない。
type Tracks struct {
	dir       string
	startedAt time.Time
	order     []string
	speakers  map[string]*speakerTrack
	pcm       []int16
	err       error
}

func NewTracks(dir string, startedAt time.Time) *Tracks {
	return &Tracks{dir: dir, startedAt: startedAt, speakers: map[string]*speakerTrack{}, pcm: make([]int16, maxFrameSamples)}
}

// WriteFrame は、1 人の話者の Opus フレームを 1 つ復号して書く。復号できないフレームは捨てる（次のフレームの前に無音が補われる）。
func (t *Tracks) WriteFrame(userID string, frame []byte, receivedAt time.Time) {
	if t.err != nil {
		return
	}
	speaker, err := t.speaker(userID)
	if err != nil {
		t.err = err
		return
	}
	samples, err := speaker.decoder.Decode(frame, t.pcm)
	if err != nil {
		return
	}
	offset := samplesOf(elapsed(t.startedAt, receivedAt))
	t.err = speaker.write(paddingSamples(speaker.written, offset), t.pcm[:samples])
}

// Close は、全トラックを endedAt まで無音で埋めて閉じ、初めて発話した順に返す。
func (t *Tracks) Close(endedAt time.Time) ([]Track, error) {
	total := samplesOf(elapsed(t.startedAt, endedAt))
	errs := []error{t.err}
	tracks := make([]Track, 0, len(t.order))
	for _, userID := range t.order {
		speaker := t.speakers[userID]
		errs = append(errs, speaker.finish(total))
		tracks = append(tracks, Track{UserID: userID, Path: speaker.path})
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return tracks, nil
}

func (t *Tracks) speaker(userID string) (*speakerTrack, error) {
	if speaker, ok := t.speakers[userID]; ok {
		return speaker, nil
	}
	speaker, err := newSpeakerTrack(filepath.Join(t.dir, userID+trackFileExtension))
	if err != nil {
		return nil, err
	}
	t.speakers[userID] = speaker
	t.order = append(t.order, userID)
	return speaker, nil
}

// elapsed は、録音の原点からの経過時間を返す。原点より前の時刻は原点として扱う。
func elapsed(startedAt, at time.Time) time.Duration {
	return max(at.Sub(startedAt), 0)
}

func samplesOf(d time.Duration) int64 {
	return int64(d) * sampleRate / int64(time.Second)
}

// paddingSamples は、次のフレームの前に書く無音のサンプル数を返す。隙間がしきい値未満なら埋めない。
func paddingSamples(written, offset int64) int64 {
	gap := offset - written
	if gap < samplesOf(silenceGapThreshold) {
		return 0
	}
	return gap
}

type speakerTrack struct {
	path    string
	file    *os.File
	encoder *flacpcm.Encoder
	decoder *opus.Decoder
	written int64
	buf     []byte
}

func newSpeakerTrack(path string) (*speakerTrack, error) {
	decoder, err := opus.NewDecoder(sampleRate, channelCount)
	if err != nil {
		return nil, fmt.Errorf("create opus decoder: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create track file: %w", err)
	}
	encoder, err := flacpcm.NewEncoder(file, flacpcm.Config{SampleRate: sampleRate, BitDepth: bitDepth, Channels: channelCount})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create flac encoder: %w", err), file.Close())
	}
	return &speakerTrack{path: path, file: file, encoder: encoder, decoder: decoder}, nil
}

func (s *speakerTrack) write(silenceSamples int64, pcm []int16) error {
	if err := s.writeSilence(silenceSamples); err != nil {
		return err
	}
	s.buf = s.buf[:0]
	for _, sample := range pcm {
		s.buf = binary.LittleEndian.AppendUint16(s.buf, uint16(sample))
	}
	if _, err := s.encoder.Write(s.buf); err != nil {
		return fmt.Errorf("encode track: %w", err)
	}
	s.written += int64(len(pcm))
	return nil
}

func (s *speakerTrack) writeSilence(samples int64) error {
	for samples > 0 {
		chunk := min(samples, int64(maxFrameSamples))
		if _, err := s.encoder.Write(silence[:chunk*bytesPerSample]); err != nil {
			return fmt.Errorf("encode silence: %w", err)
		}
		s.written += chunk
		samples -= chunk
	}
	return nil
}

// finish は、トラックを totalSamples まで無音で埋めて閉じる。
func (s *speakerTrack) finish(totalSamples int64) error {
	return errors.Join(s.writeSilence(totalSamples-s.written), s.encoder.Close(), s.file.Close())
}
