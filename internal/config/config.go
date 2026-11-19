// Package config は、bot の調整値とモデルへの指示文を、同梱の YAML から読む。
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"

	"discord-ai-assistant/internal/ask"
	"discord-ai-assistant/internal/config/check"
	"discord-ai-assistant/internal/imagegen"
	"discord-ai-assistant/internal/translate"
)

const (
	parseErrorFormat    = "parse config: %w"
	invalidConfigFormat = "invalid config:\n%w"
)

//go:embed config.yaml
var embedded []byte

// Config は bot 全体の設定。
type Config struct {
	TextModelID         string           `yaml:"text_model_id"`
	ReplyReserveSeconds int              `yaml:"reply_reserve_seconds"`
	Ask                 ask.Config       `yaml:"ask"`
	Translate           translate.Config `yaml:"translate"`
	Image               imagegen.Config  `yaml:"image"`
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
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf(invalidConfigFormat, err)
	}
	return cfg, nil
}

// Validate は値の問題を、キー名つきで全部返す。
func (c Config) Validate() error {
	return errors.Join(
		check.NotEmpty("text_model_id", c.TextModelID),
		check.Positive("reply_reserve_seconds", c.ReplyReserveSeconds),
		check.Under("ask", c.Ask.Validate()),
		check.Under("translate", c.Translate.Validate()),
		check.Under("image", c.Image.Validate()),
	)
}
