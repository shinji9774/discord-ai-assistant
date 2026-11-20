package voicesummary_test

import (
	"encoding/json"
	"testing"

	"discord-ai-assistant/internal/voicesummary"
)

// 条件: Transcribe の状態変化のイベントの detail。
// 内容: ジョブ名を取り出す。
func TestTranscriptionJobNameOf_StateChangeDetail_ReturnsJobName(t *testing.T) {
	detail := json.RawMessage(`{"TranscriptionJobName":"summarize-voice-1-1-1","TranscriptionJobStatus":"FAILED"}`)

	got, err := voicesummary.TranscriptionJobNameOf(detail)

	if err != nil || got != "summarize-voice-1-1-1" {
		t.Errorf("TranscriptionJobNameOf() = %q, %v; want summarize-voice-1-1-1, nil", got, err)
	}
}

// 条件: JSON として読めない detail。
// 内容: エラーを返す。
func TestTranscriptionJobNameOf_MalformedDetail_ReturnsError(t *testing.T) {
	detail := json.RawMessage(`{not json`)

	_, err := voicesummary.TranscriptionJobNameOf(detail)

	if err == nil {
		t.Error("TranscriptionJobNameOf() error = nil; want error")
	}
}
