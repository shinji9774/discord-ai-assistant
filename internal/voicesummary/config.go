// Package voicesummary は、Craig の録音を文字起こしして要約する /summarize-voice を提供する。
package voicesummary

import (
	"errors"
	"fmt"
	"net/url"

	"discord-ai-assistant/internal/config/check"
)

const (
	// maxTranscribeMinutes は、Amazon Transcribe が 1 つのジョブで扱える音声の長さ（8 時間）。
	maxTranscribeMinutes = 8 * 60

	notHTTPURLFormat = "craig_base_url: must be an absolute http or https URL (got %q)"
	overLimitFormat  = "max_recording_minutes: must be %d or less (got %d)"
)

// httpSchemes は、Craig の URL に使える scheme。
var httpSchemes = map[string]bool{"http": true, "https": true}

// Config は /summarize-voice の調整値と指示文。
type Config struct {
	CraigBaseURL        string `yaml:"craig_base_url"`        // 録音リンクの照合と、Craig の API の接続先
	MaxRecordingMinutes int    `yaml:"max_recording_minutes"` // 扱える録音の長さの上限（分）
	MaxOutputTokens     int32  `yaml:"max_output_tokens"`     // 要約の出力トークンの上限
	SystemPrompt        string `yaml:"system_prompt"`         // 要約の指示文
}

// Validate は値の問題を、キー名つきで全部返す。
func (c Config) Validate() error {
	return errors.Join(
		validateCraigBaseURL(c.CraigBaseURL),
		validateMaxRecordingMinutes(c.MaxRecordingMinutes),
		check.Positive("max_output_tokens", c.MaxOutputTokens),
		check.NotEmpty("system_prompt", c.SystemPrompt),
	)
}

func validateCraigBaseURL(raw string) error {
	if err := check.NotEmpty("craig_base_url", raw); err != nil {
		return err
	}
	u, err := url.Parse(raw)
	if err != nil || !httpSchemes[u.Scheme] || u.Host == "" {
		return fmt.Errorf(notHTTPURLFormat, raw)
	}
	return nil
}

func validateMaxRecordingMinutes(minutes int) error {
	if err := check.Positive("max_recording_minutes", minutes); err != nil {
		return err
	}
	if minutes > maxTranscribeMinutes {
		return fmt.Errorf(overLimitFormat, maxTranscribeMinutes, minutes)
	}
	return nil
}
