package ask

import (
	"errors"
	"fmt"
	"strings"

	"discord-ai-assistant/internal/config/check"
)

const (
	todayPlaceholder    = "{today}" // 指示文の中で、実行日の日付に置き換える目印
	maxResultsPerSearch = 25        // 検索サービスが 1 回の検索で返せる件数の上限

	missingTodayFormat = "system_prompt: must contain %s"
	overLimitFormat    = "results_per_search: must be %d or less (got %d)"
)

// Config は /ask の調整値と、モデルへの指示文。
type Config struct {
	MaxInputLength  int             `yaml:"max_input_length"`
	MaxOutputTokens int32           `yaml:"max_output_tokens"`
	SystemPrompt    string          `yaml:"system_prompt"`
	WebSearch       WebSearchConfig `yaml:"web_search"`
}

// WebSearchConfig は Web 検索の調整値と、モデルへ返す文。
type WebSearchConfig struct {
	MaxSearches         int    `yaml:"max_searches"`
	ResultsPerSearch    int32  `yaml:"results_per_search"`
	MaxResultChars      int    `yaml:"max_result_chars"`
	FallbackSourceCount int    `yaml:"fallback_source_count"`
	ToolDescription     string `yaml:"tool_description"`
	LimitReachedMessage string `yaml:"limit_reached_message"`
	SearchFailedMessage string `yaml:"search_failed_message"`
	NoResultsMessage    string `yaml:"no_results_message"`
}

// Validate は値の問題を、キー名つきで全部返す。
func (c Config) Validate() error {
	return errors.Join(
		check.Positive("max_input_length", c.MaxInputLength),
		check.Positive("max_output_tokens", c.MaxOutputTokens),
		validateSystemPrompt(c.SystemPrompt),
		check.Under("web_search", c.WebSearch.Validate()),
	)
}

// Validate は値の問題を、キー名つきで全部返す。
func (c WebSearchConfig) Validate() error {
	return errors.Join(
		check.Positive("max_searches", c.MaxSearches),
		validateResultsPerSearch(c.ResultsPerSearch),
		check.Positive("max_result_chars", c.MaxResultChars),
		check.Positive("fallback_source_count", c.FallbackSourceCount),
		check.NotEmpty("tool_description", c.ToolDescription),
		check.NotEmpty("limit_reached_message", c.LimitReachedMessage),
		check.NotEmpty("search_failed_message", c.SearchFailedMessage),
		check.NotEmpty("no_results_message", c.NoResultsMessage),
	)
}

func validateSystemPrompt(prompt string) error {
	if err := check.NotEmpty("system_prompt", prompt); err != nil {
		return err
	}
	if !strings.Contains(prompt, todayPlaceholder) {
		return fmt.Errorf(missingTodayFormat, todayPlaceholder)
	}
	return nil
}

func validateResultsPerSearch(count int32) error {
	if err := check.Positive("results_per_search", count); err != nil {
		return err
	}
	if count > maxResultsPerSearch {
		return fmt.Errorf(overLimitFormat, maxResultsPerSearch, count)
	}
	return nil
}
