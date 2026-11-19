package imagegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"discord-ai-assistant/internal/llm"
)

const (
	verdictAllow  = "allow"
	verdictReject = "reject"
)

// screening は審査の結果。
type screening struct {
	Allowed       bool
	EnglishPrompt string // Allowed が true のときだけ意味を持つ
}

type screeningResponse struct {
	Verdict       string `json:"verdict"`
	EnglishPrompt string `json:"english_prompt"`
}

// screen は文章を 1 回だけ文章生成モデルに渡し、許可か拒否かと英語の指示文を得る。
// error の文言には、利用者の文章とモデルの応答を含めない。
func screen(ctx context.Context, text llm.TextGenerator, cfg ScreeningConfig, prompt string) (screening, error) {
	result, err := text.Generate(ctx, llm.Request{
		System:          cfg.Instruction,
		UserText:        prompt,
		MaxOutputTokens: cfg.MaxOutputTokens,
	})
	if err != nil {
		return screening{}, fmt.Errorf("screening request: %w", err)
	}
	if result.Truncated {
		return screening{}, errors.New("screening response was truncated")
	}
	response, err := parseScreeningResponse(result.Text)
	if err != nil {
		return screening{}, err
	}
	switch response.Verdict {
	case verdictAllow:
		if response.EnglishPrompt == "" {
			return screening{}, errors.New("screening allowed without an English prompt")
		}
		return screening{Allowed: true, EnglishPrompt: response.EnglishPrompt}, nil
	case verdictReject:
		return screening{}, nil
	default:
		return screening{}, errors.New("screening verdict is neither allow nor reject")
	}
}

// parseScreeningResponse は最初の { から最後の } までを JSON として読む。
func parseScreeningResponse(text string) (screeningResponse, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return screeningResponse{}, errors.New("screening response has no JSON object")
	}
	var response screeningResponse
	if err := json.Unmarshal([]byte(text[start:end+1]), &response); err != nil {
		return screeningResponse{}, errors.New("screening response is not valid JSON")
	}
	return response, nil
}
