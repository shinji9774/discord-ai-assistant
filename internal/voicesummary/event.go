package voicesummary

import (
	"encoding/json"
	"fmt"
)

// transcriptionJobStateChange は、Transcribe の状態変化のイベントの detail のうち、使う項目。
// 状態は読まない（Reporter が Transcribe に問い合わせる）。
type transcriptionJobStateChange struct {
	TranscriptionJobName string `json:"TranscriptionJobName"`
}

// TranscriptionJobNameOf は、Transcribe の状態変化のイベントの detail からジョブ名を取り出す。
// cmd/voicereporter と cmd/voicefailure が共有する。
func TranscriptionJobNameOf(detail json.RawMessage) (string, error) {
	var change transcriptionJobStateChange
	if err := json.Unmarshal(detail, &change); err != nil {
		return "", fmt.Errorf("decode transcription job state change: %w", err)
	}
	return change.TranscriptionJobName, nil
}
