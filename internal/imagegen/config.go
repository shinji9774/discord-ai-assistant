package imagegen

// Config は /image の調整値と、モデルへの指示文。
type Config struct {
	MaxPromptLength    int                `yaml:"max_prompt_length" validate:"min=1"`
	BedrockRegion      string             `yaml:"bedrock_region" validate:"notblank"`
	CoreModelID        string             `yaml:"core_model_id" validate:"notblank"`
	SketchModelID      string             `yaml:"sketch_model_id" validate:"notblank"`
	DiagramStyleSuffix string             `yaml:"diagram_style_suffix" validate:"notblank"`
	NegativePrompt     string             `yaml:"negative_prompt" validate:"notblank"`
	Source             SourceConfig       `yaml:"source"`
	PromptWriter       PromptWriterConfig `yaml:"prompt_writer"`
}

// SourceConfig は、元にする画像を受け付ける条件と、縮小の調整値。
type SourceConfig struct {
	MaxBytes           int     `yaml:"max_bytes" validate:"min=1"`
	MinSide            int     `yaml:"min_side" validate:"min=1"`
	MaxPixels          int     `yaml:"max_pixels" validate:"min=1"`
	MaxModelPixels     int     `yaml:"max_model_pixels" validate:"min=1"`
	MaxAspect          float64 `yaml:"max_aspect" validate:"min=1"`
	ResizedJPEGQuality int     `yaml:"resized_jpeg_quality" validate:"min=1"`
}

// PromptWriterConfig は、プロンプトを書かせる調整値と指示文。
type PromptWriterConfig struct {
	MaxOutputTokens int32  `yaml:"max_output_tokens" validate:"min=1"`
	Instruction     string `yaml:"instruction" validate:"notblank"`
}
