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
	testUserPrompt   = "ログインの流れを図にして"
	testEnglishDraft = "A flowchart of a login flow."
)

// 条件: モデルが許可と英語の指示文を JSON で返す。
// 内容: 許可され、英語の指示文が返る。
func TestScreen_AllowVerdict_ReturnsEnglishPrompt(t *testing.T) {
	model := &llmtest.Generator{Result: llm.Result{Text: `{"verdict":"allow","english_prompt":"` + testEnglishDraft + `"}`}}

	got, err := screen(context.Background(), model, testConfig.Screening, testUserPrompt)

	if err != nil {
		t.Fatalf("screen() error = %v", err)
	}
	if want := (screening{Allowed: true, EnglishPrompt: testEnglishDraft}); got != want {
		t.Errorf("screen() = %+v, want %+v", got, want)
	}
}

// 条件: モデルが拒否を JSON で返す。
// 内容: 許可されず、エラーにもならない。
func TestScreen_RejectVerdict_ReturnsNotAllowed(t *testing.T) {
	model := &llmtest.Generator{Result: llm.Result{Text: `{"verdict":"reject","english_prompt":""}`}}

	got, err := screen(context.Background(), model, testConfig.Screening, "猫の写真を撮って")

	if err != nil {
		t.Fatalf("screen() error = %v", err)
	}
	if want := (screening{Allowed: false}); got != want {
		t.Errorf("screen() = %+v, want %+v", got, want)
	}
}

// 条件: モデルが JSON をコードブロックで囲んで返す。
// 内容: 最初の { から最後の } までが読まれ、許可される。
func TestScreen_JSONInCodeBlock_IsParsed(t *testing.T) {
	text := "```json\n{\"verdict\":\"allow\",\"english_prompt\":\"" + testEnglishDraft + "\"}\n```"
	model := &llmtest.Generator{Result: llm.Result{Text: text}}

	got, err := screen(context.Background(), model, testConfig.Screening, testUserPrompt)

	if err != nil {
		t.Fatalf("screen() error = %v", err)
	}
	if want := (screening{Allowed: true, EnglishPrompt: testEnglishDraft}); got != want {
		t.Errorf("screen() = %+v, want %+v", got, want)
	}
}

// 条件: 結果を得られない 4 種の応答（途中で切れた、JSON でない、判定が不明、許可なのに指示文が空）。
// 内容: error を返し、その文言に利用者の文章とモデルの応答を含まない。
func TestScreen_UnusableResponse_ReturnsErrorWithoutLeakingContent(t *testing.T) {
	const modelLeak = "MODEL-RESPONSE-MARKER"
	tests := []struct {
		name   string
		result llm.Result
	}{
		{"Truncated", llm.Result{Text: `{"verdict":"allow","english_prompt":"` + modelLeak + `"}`, Truncated: true}},
		{"NotJSON", llm.Result{Text: modelLeak}},
		{"UnknownVerdict", llm.Result{Text: `{"verdict":"` + modelLeak + `","english_prompt":"x"}`}},
		{"AllowWithoutEnglishPrompt", llm.Result{Text: `{"verdict":"allow","english_prompt":"","note":"` + modelLeak + `"}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &llmtest.Generator{Result: tt.result}

			_, err := screen(context.Background(), model, testConfig.Screening, testUserPrompt)

			if err == nil {
				t.Fatal("screen() error = nil, want error")
			}
			if strings.Contains(err.Error(), modelLeak) || strings.Contains(err.Error(), testUserPrompt) {
				t.Errorf("screen() error = %q, must not contain the user text or the model response", err)
			}
		})
	}
}

// 条件: 文章生成モデルの呼び出しが失敗する。
// 内容: 元の error をたどれる error を返す。
func TestScreen_ModelFails_ReturnsWrappedError(t *testing.T) {
	failure := errors.New("model down")
	model := &llmtest.Generator{Err: failure}

	_, err := screen(context.Background(), model, testConfig.Screening, testUserPrompt)

	if !errors.Is(err, failure) {
		t.Errorf("screen() error = %v, want it to wrap %v", err, failure)
	}
}

// 条件: 許可の応答を返すモデルに利用者の文章を渡す。
// 内容: 依頼は 1 回で、System が設定の instruction、UserText が利用者の文章だけ、出力上限が設定の値。
func TestScreen_Request_SeparatesInstructionFromUserText(t *testing.T) {
	model := &llmtest.Generator{Result: llm.Result{Text: `{"verdict":"allow","english_prompt":"x"}`}}

	_, err := screen(context.Background(), model, testConfig.Screening, testUserPrompt)

	if err != nil {
		t.Fatalf("screen() error = %v", err)
	}
	want := []llm.Request{{System: testConfig.Screening.Instruction, UserText: testUserPrompt, MaxOutputTokens: testConfig.Screening.MaxOutputTokens}}
	if len(model.Requests) != 1 || model.Requests[0] != want[0] {
		t.Errorf("model.Requests = %+v, want %+v", model.Requests, want)
	}
}
