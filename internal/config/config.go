// Package config は、bot の調整値とモデルへの指示文を、同梱の YAML から読む。
package config

import (
	"bytes"
	_ "embed"
	"fmt"

	"go.yaml.in/yaml/v3"

	"discord-ai-assistant/internal/ask"
	"discord-ai-assistant/internal/config/check"
	"discord-ai-assistant/internal/imagegen"
	"discord-ai-assistant/internal/summarize"
	"discord-ai-assistant/internal/translate"
	"discord-ai-assistant/internal/voicesummary"
)

const (
	parseErrorFormat    = "parse config: %w"
	invalidConfigFormat = "invalid config:\n%w"
)

//go:embed config.yaml
var embedded []byte

// Config は bot 全体の設定。
type Config struct {
	TextModelID         string              `yaml:"text_model_id" validate:"notblank"`
	ReplyReserveSeconds int                 `yaml:"reply_reserve_seconds" validate:"min=1"`
	Ask                 ask.Config          `yaml:"ask"`
	Translate           translate.Config    `yaml:"translate"`
	Image               imagegen.Config     `yaml:"image"`
	Summarize           summarize.Config    `yaml:"summarize"`
	SummarizeVoice      voicesummary.Config `yaml:"summarize_voice"`
}

// Load は、同梱した config.yaml を読んで検証する。
func Load() (Config, error) {
	return Parse(embedded)
}

// Parse は、YAML のバイト列を読んで検証する（テストから使う）。
func Parse(data []byte) (Config, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf(parseErrorFormat, err)
	}
	if err := check.Struct(cfg); err != nil {
		return Config{}, fmt.Errorf(invalidConfigFormat, err)
	}
	return cfg, nil
}
