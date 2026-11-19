package summarize

import (
	"errors"

	"discord-ai-assistant/internal/config/check"
)

// Config は /summarize の調整値と指示文。
type Config struct {
	DefaultPeriodHours int    `yaml:"default_period_hours"` // since と until を省略したときの期間の長さ（時間）
	MaxMessages        int    `yaml:"max_messages"`         // 要約に含める投稿の上限。超えた分は古い方から除く
	MaxOutputTokens    int32  `yaml:"max_output_tokens"`    // 要約の出力トークンの上限
	SystemPrompt       string `yaml:"system_prompt"`        // 要約の指示文
}

// Validate は、数値が 1 以上で指示文が空でないことを確かめる。
func (c Config) Validate() error {
	return errors.Join(
		check.Positive("default_period_hours", c.DefaultPeriodHours),
		check.Positive("max_messages", c.MaxMessages),
		check.Positive("max_output_tokens", c.MaxOutputTokens),
		check.NotEmpty("system_prompt", c.SystemPrompt),
	)
}
