// Package voicesummary は、Craig の録音を文字起こしして要約する /summarize-voice を提供する。
package voicesummary

// Config は /summarize-voice の調整値と指示文。
type Config struct {
	CraigBaseURL        string `yaml:"craig_base_url" validate:"notblank,http_url"`    // 録音リンクの照合と、Craig の API の接続先
	MaxRecordingMinutes int    `yaml:"max_recording_minutes" validate:"min=1,max=480"` // 扱える録音の長さの上限（分）。480 は Amazon Transcribe が 1 つのジョブで扱える音声の長さ（8 時間）
	MaxOutputTokens     int32  `yaml:"max_output_tokens" validate:"min=1"`             // 要約の出力トークンの上限
	SystemPrompt        string `yaml:"system_prompt" validate:"notblank"`              // 要約の指示文
}
