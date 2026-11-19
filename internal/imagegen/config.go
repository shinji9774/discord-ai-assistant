package imagegen

import (
	"errors"

	"discord-ai-assistant/internal/config/check"
)

// Config は /image の調整値と、モデルへの指示文。
type Config struct {
	MaxPromptLength    int                `yaml:"max_prompt_length"`
	BedrockRegion      string             `yaml:"bedrock_region"`
	CoreModelID        string             `yaml:"core_model_id"`
	SketchModelID      string             `yaml:"sketch_model_id"`
	DiagramStyleSuffix string             `yaml:"diagram_style_suffix"`
	NegativePrompt     string             `yaml:"negative_prompt"`
	Source             SourceConfig       `yaml:"source"`
	PromptWriter       PromptWriterConfig `yaml:"prompt_writer"`
}

// SourceConfig は、元にする画像を受け付ける条件と、縮小の調整値。
type SourceConfig struct {
	MaxBytes           int     `yaml:"max_bytes"`
	MinSide            int     `yaml:"min_side"`
	MaxPixels          int     `yaml:"max_pixels"`
	MaxModelPixels     int     `yaml:"max_model_pixels"`
	MaxAspect          float64 `yaml:"max_aspect"`
	ResizedJPEGQuality int     `yaml:"resized_jpeg_quality"`
}

// PromptWriterConfig は、プロンプトを書かせる調整値と指示文。
type PromptWriterConfig struct {
	MaxOutputTokens int32  `yaml:"max_output_tokens"`
	Instruction     string `yaml:"instruction"`
}

// Validate は値の問題を、キー名つきで全部返す。
func (c Config) Validate() error {
	return errors.Join(
		check.Positive("max_prompt_length", c.MaxPromptLength),
		check.NotEmpty("bedrock_region", c.BedrockRegion),
		check.NotEmpty("core_model_id", c.CoreModelID),
		check.NotEmpty("sketch_model_id", c.SketchModelID),
		check.NotEmpty("diagram_style_suffix", c.DiagramStyleSuffix),
		check.NotEmpty("negative_prompt", c.NegativePrompt),
		check.Under("source", c.Source.Validate()),
		check.Under("prompt_writer", c.PromptWriter.Validate()),
	)
}

// Validate は値の問題を、キー名つきで全部返す。
func (c SourceConfig) Validate() error {
	return errors.Join(
		check.Positive("max_bytes", c.MaxBytes),
		check.Positive("min_side", c.MinSide),
		check.Positive("max_pixels", c.MaxPixels),
		check.Positive("max_model_pixels", c.MaxModelPixels),
		check.Positive("max_aspect", c.MaxAspect),
		check.Positive("resized_jpeg_quality", c.ResizedJPEGQuality),
	)
}

// Validate は値の問題を、キー名つきで全部返す。
func (c PromptWriterConfig) Validate() error {
	return errors.Join(
		check.Positive("max_output_tokens", c.MaxOutputTokens),
		check.NotEmpty("instruction", c.Instruction),
	)
}
