package imagegen

// testConfig は、内部テストで使う設定。値は移行前の定数と同じ。
var testConfig = Config{
	MaxPromptLength:    1000,
	BedrockRegion:      "us-west-2",
	CoreModelID:        "stability.stable-image-core-v1:1",
	SketchModelID:      "us.stability.stable-image-control-sketch-v1:0",
	DiagramStyleSuffix: ", clean flat diagram with simple shapes, clear lines and labels, white background",
	NegativePrompt:     "photograph, photorealistic, painting, oil painting, watercolor, 3d render",
	Source: SourceConfig{
		MaxBytes:           10_000_000,
		MinSide:            64,
		MaxPixels:          25_000_000,
		MaxModelPixels:     9_437_184,
		MaxAspect:          2.5,
		ResizedJPEGQuality: 90,
	},
	PromptWriter: PromptWriterConfig{
		MaxOutputTokens: 2048,
		Instruction:     "write the prompt",
	},
}
