package ask

const todayPlaceholder = "{today}" // 指示文の中で、実行日の日付に置き換える目印

// Config は /ask の調整値と、モデルへの指示文。
type Config struct {
	MaxInputLength  int             `yaml:"max_input_length" validate:"min=1"`
	MaxOutputTokens int32           `yaml:"max_output_tokens" validate:"min=1"`
	SystemPrompt    string          `yaml:"system_prompt" validate:"notblank,contains={today}"` // contains の値は todayPlaceholder と同じ
	WebSearch       WebSearchConfig `yaml:"web_search"`
}

// WebSearchConfig は Web 検索の調整値と、モデルへ返す文。
type WebSearchConfig struct {
	MaxSearches         int    `yaml:"max_searches" validate:"min=1"`
	ResultsPerSearch    int32  `yaml:"results_per_search" validate:"min=1,max=25"` // 25 は、検索サービスが 1 回の検索で返せる件数の上限
	MaxResultChars      int    `yaml:"max_result_chars" validate:"min=1"`
	FallbackSourceCount int    `yaml:"fallback_source_count" validate:"min=1"`
	ToolDescription     string `yaml:"tool_description" validate:"notblank"`
	LimitReachedMessage string `yaml:"limit_reached_message" validate:"notblank"`
	SearchFailedMessage string `yaml:"search_failed_message" validate:"notblank"`
	NoResultsMessage    string `yaml:"no_results_message" validate:"notblank"`
}
