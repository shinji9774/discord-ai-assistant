package summarize

// Config は /summarize の調整値と指示文。
type Config struct {
	DefaultPeriodHours int    `yaml:"default_period_hours" validate:"min=1"` // since と until を省略したときの期間の長さ（時間）
	MaxMessages        int    `yaml:"max_messages" validate:"min=1"`         // 要約に含める投稿の上限。超えた分は古い方から除く
	MaxOutputTokens    int32  `yaml:"max_output_tokens" validate:"min=1"`    // 要約の出力トークンの上限
	SystemPrompt       string `yaml:"system_prompt" validate:"notblank"`     // 要約の指示文
}
