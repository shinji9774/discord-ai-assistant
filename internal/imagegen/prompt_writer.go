package imagegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"discord-ai-assistant/internal/llm"
)

// promptMessage は文章生成モデルとやりとりする JSON。入力では利用者の文章（任意の言語）、出力では英語のプロンプトを持つ。
type promptMessage struct {
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negative_prompt"`
}

// writePrompt は利用者の文章を 1 回だけ文章生成モデルに渡し、画像生成向けの英語のプロンプトを得る。
// error の文言には、利用者の文章とモデルの応答を含めない。
func writePrompt(ctx context.Context, text llm.TextGenerator, cfg PromptWriterConfig, prompt, negativePrompt string) (Prompt, error) {
	userText, err := json.Marshal(promptMessage{Prompt: prompt, NegativePrompt: negativePrompt})
	if err != nil {
		return Prompt{}, fmt.Errorf("encode prompt writer request: %w", err)
	}
	result, err := text.Generate(ctx, llm.Request{
		System:          cfg.Instruction,
		UserText:        string(userText),
		MaxOutputTokens: cfg.MaxOutputTokens,
	})
	if err != nil {
		return Prompt{}, fmt.Errorf("prompt writer request: %w", err)
	}
	if result.Truncated {
		return Prompt{}, errors.New("prompt writer response was truncated")
	}
	response, err := parsePromptResponse(result.Text)
	if err != nil {
		return Prompt{}, err
	}
	if response.Prompt == "" {
		return Prompt{}, errors.New("prompt writer returned an empty prompt")
	}
	return Prompt{Positive: response.Prompt, Negative: response.NegativePrompt}, nil
}

// parsePromptResponse は最初の { から最後の } までを JSON として読む。
func parsePromptResponse(text string) (promptMessage, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return promptMessage{}, errors.New("prompt writer response has no JSON object")
	}
	var response promptMessage
	if err := json.Unmarshal([]byte(text[start:end+1]), &response); err != nil {
		return promptMessage{}, errors.New("prompt writer response is not valid JSON")
	}
	return response, nil
}
