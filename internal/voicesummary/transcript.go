package voicesummary

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	// secondsUnit は、秒の数の文字列（start_time）を time.ParseDuration で読むために付ける単位。
	// 浮動小数点を経ずに読むので、"1.23" はちょうど 1.23 秒になる。
	secondsUnit = "s"

	transcriptLineFormat    = "[%02d:%02d:%02d] %s: %s"
	transcriptLineSeparator = "\n"
)

// Segment は、1 トラックの中の 1 つの発話。Start は録音の最初の音声からの経過。
type Segment struct {
	Start time.Duration
	Text  string
}

// Utterance は、話者名つきの発話。
type Utterance struct {
	Start   time.Duration
	Speaker string
	Text    string
}

// transcribeResult は、Transcribe の結果の JSON のうち、使う部分。
type transcribeResult struct {
	Results struct {
		AudioSegments []audioSegment `json:"audio_segments"`
	} `json:"results"`
}

type audioSegment struct {
	StartTime  string `json:"start_time"`
	Transcript string `json:"transcript"`
}

// trackedUtterance は、並べ替えの鍵（トラック番号と、トラックの中の順番）を持つ発話。
type trackedUtterance struct {
	Utterance
	track int
	order int
}

// parseSegments は、Transcribe の結果の JSON から発話を取り出す。本文が空白だけの発話は捨てる。
func parseSegments(transcribeJSON []byte) ([]Segment, error) {
	var result transcribeResult
	if err := json.Unmarshal(transcribeJSON, &result); err != nil {
		return nil, fmt.Errorf("decode transcribe result: %w", err)
	}
	var segments []Segment
	for _, s := range result.Results.AudioSegments {
		if strings.TrimSpace(s.Transcript) == "" {
			continue
		}
		start, err := time.ParseDuration(s.StartTime + secondsUnit)
		if err != nil {
			return nil, fmt.Errorf("parse start_time %q: %w", s.StartTime, err)
		}
		segments = append(segments, Segment{Start: start, Text: s.Transcript})
	}
	return segments, nil
}

// mergeUtterances は、話者ごとの発話を、開始時刻の早い順に 1 つにまとめる。
func mergeUtterances(bySpeaker map[Speaker][]Segment) []Utterance {
	var tracked []trackedUtterance
	for speaker, segments := range bySpeaker {
		for i, s := range segments {
			tracked = append(tracked, trackedUtterance{
				Utterance: Utterance{Start: s.Start, Speaker: speaker.Name, Text: s.Text},
				track:     speaker.Track,
				order:     i,
			})
		}
	}
	slices.SortFunc(tracked, compareUtterances)
	utterances := make([]Utterance, 0, len(tracked))
	for _, u := range tracked {
		utterances = append(utterances, u.Utterance)
	}
	return utterances
}

// compareUtterances は、開始時刻、トラック番号、トラックの中の順番の順に比べる。
// map の走査順に結果が左右されないよう、同時刻の発話もトラック番号で決まった順にする。
func compareUtterances(a, b trackedUtterance) int {
	return cmp.Or(
		cmp.Compare(a.Start, b.Start),
		cmp.Compare(a.track, b.track),
		cmp.Compare(a.order, b.order),
	)
}

// renderTranscript は "[HH:MM:SS] 話者名: 本文" を 1 行として連結する。
func renderTranscript(utterances []Utterance) string {
	lines := make([]string, 0, len(utterances))
	for _, u := range utterances {
		lines = append(lines, renderLine(u))
	}
	return strings.Join(lines, transcriptLineSeparator)
}

// renderLine は、経過時刻を秒未満を切り捨てた時・分・秒で表した 1 行を返す。
func renderLine(u Utterance) string {
	hours := int(u.Start / time.Hour)
	minutes := int(u.Start % time.Hour / time.Minute)
	seconds := int(u.Start % time.Minute / time.Second)
	return fmt.Sprintf(transcriptLineFormat, hours, minutes, seconds, u.Speaker, u.Text)
}
