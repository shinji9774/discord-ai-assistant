package imagegen

import (
	"context"
	"errors"
	"strings"
	"testing"

	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/llm/llmtest"
)

const (
	testUserPrompt     = "ログインの流れを図にして"
	testUserNegative   = "文字"
	testEnglishPrompt  = "login flow flowchart, boxes and arrows"
	testEnglishNegated = "text"
)

// 条件: モデルが prompt と negative_prompt を JSON で返す。
// 内容: 英語のプロンプトと、避けたい要素が返る。
func TestWritePrompt_JSONResponse_ReturnsPositiveAndNegative(t *testing.T) {
	model := &llmtest.Generator{Result: llm.Result{Text: `{"prompt":"` + testEnglishPrompt + `","negative_prompt":"` + testEnglishNegated + `"}`}}

	got, err := writePrompt(context.Background(), model, testConfig.PromptWriter, testUserPrompt, testUserNegative)

	if err != nil {
		t.Fatalf("writePrompt() error = %v", err)
	}
	if want := (Prompt{Positive: testEnglishPrompt, Negative: testEnglishNegated}); got != want {
		t.Errorf("writePrompt() = %+v, want %+v", got, want)
	}
}

// 条件: モデルが JSON をコードブロックで囲んで返し、negative_prompt は空にする。
// 内容: 最初の { から最後の } までが読まれ、Negative が空のプロンプトが返る。
func TestWritePrompt_JSONInCodeBlock_IsParsed(t *testing.T) {
	text := "```json\n{\"prompt\":\"" + testEnglishPrompt + "\",\"negative_prompt\":\"\"}\n```"
	model := &llmtest.Generator{Result: llm.Result{Text: text}}

	got, err := writePrompt(context.Background(), model, testConfig.PromptWriter, testUserPrompt, "")

	if err != nil {
		t.Fatalf("writePrompt() error = %v", err)
	}
	if want := (Prompt{Positive: testEnglishPrompt}); got != want {
		t.Errorf("writePrompt() = %+v, want %+v", got, want)
	}
}

// 条件: 結果を得られない 3 種の応答（途中で切れた、JSON でない、prompt が空）。
// 内容: error を返し、その文言に利用者の文章とモデルの応答を含まない。
func TestWritePrompt_UnusableResponse_ReturnsErrorWithoutLeakingContent(t *testing.T) {
	const modelLeak = "MODEL-RESPONSE-MARKER"
	tests := []struct {
		name   string
		result llm.Result
	}{
		{"Truncated", llm.Result{Text: `{"prompt":"` + modelLeak + `","negative_prompt":""}`, Truncated: true}},
		{"NotJSON", llm.Result{Text: modelLeak}},
		{"EmptyPrompt", llm.Result{Text: `{"prompt":"","negative_prompt":"","note":"` + modelLeak + `"}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &llmtest.Generator{Result: tt.result}

			_, err := writePrompt(context.Background(), model, testConfig.PromptWriter, testUserPrompt, testUserNegative)

			if err == nil {
				t.Fatal("writePrompt() error = nil, want error")
			}
			if strings.Contains(err.Error(), modelLeak) || strings.Contains(err.Error(), testUserPrompt) {
				t.Errorf("writePrompt() error = %q, must not contain the user text or the model response", err)
			}
		})
	}
}

// 条件: 文章生成モデルの呼び出しが失敗する。
// 内容: 元の error をたどれる error を返す。
func TestWritePrompt_ModelFails_ReturnsWrappedError(t *testing.T) {
	failure := errors.New("model down")
	model := &llmtest.Generator{Err: failure}

	_, err := writePrompt(context.Background(), model, testConfig.PromptWriter, testUserPrompt, "")

	if !errors.Is(err, failure) {
		t.Errorf("writePrompt() error = %v, want it to wrap %v", err, failure)
	}
}

// 条件: 利用者の prompt と negative_prompt に JSON を壊す文字（引用符）を含めて渡す。
// 内容: 依頼は 1 回で、System が設定の instruction、UserText が両方を JSON にしたもの、出力上限が設定の値。
func TestWritePrompt_Request_EncodesUserTextAsJSONSeparatedFromInstruction(t *testing.T) {
	model := &llmtest.Generator{Result: llm.Result{Text: `{"prompt":"x","negative_prompt":""}`}}

	_, err := writePrompt(context.Background(), model, testConfig.PromptWriter, `猫の"絵"`, "文字")

	if err != nil {
		t.Fatalf("writePrompt() error = %v", err)
	}
	want := []llm.Request{{System: testConfig.PromptWriter.Instruction, UserText: `{"prompt":"猫の\"絵\"","negative_prompt":"文字"}`, MaxOutputTokens: testConfig.PromptWriter.MaxOutputTokens}}
	if len(model.Requests) != 1 || model.Requests[0] != want[0] {
		t.Errorf("model.Requests = %+v, want %+v", model.Requests, want)
	}
}
