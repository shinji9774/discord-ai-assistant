package translate

import (
	"errors"

	"discord-ai-assistant/internal/config/check"
)

// Config は /translate の調整値。
type Config struct {
	MaxTextLength    int `yaml:"max_text_length"`
	MaxFileSizeBytes int `yaml:"max_file_size_bytes"`
}

// Validate は値の問題を、キー名つきで全部返す。
func (c Config) Validate() error {
	return errors.Join(
		check.Positive("max_text_length", c.MaxTextLength),
		check.Positive("max_file_size_bytes", c.MaxFileSizeBytes),
	)
}
