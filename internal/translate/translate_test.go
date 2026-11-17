package translate_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/llm/llmtest"
	"discord-ai-assistant/internal/translate"
)

func invocation(options map[string]string) command.Invocation {
	return command.Invocation{CommandName: "translate", Options: options}
}

// 条件: text と to（英語）を指定して実行する。
// 内容: 英語への翻訳を依頼する request が 1 件、生成器へ渡る。
func TestExecute_ToSpecified_RequestsTranslationIntoThatLanguage(t *testing.T) {
	gen := &llmtest.Generator{}
	cmd := translate.New(gen)

	_, _ = cmd.Execute(context.Background(), invocation(map[string]string{"text": "こんにちは", "to": "英語"}))

	want := []llm.Request{{
		System:          "あなたは翻訳者です。ユーザーの文章を英語に翻訳してください。翻訳文だけを出力し、説明や前置きは書かないでください。",
		UserText:        "こんにちは",
		MaxOutputTokens: 16000,
	}}
	if !reflect.DeepEqual(gen.Requests, want) {
		t.Errorf("Requests = %+v, want %+v", gen.Requests, want)
	}
}

// 条件: text だけを指定し、to を省略して実行する。
// 内容: 日本語への翻訳を依頼する request が 1 件、生成器へ渡る。
func TestExecute_ToOmitted_RequestsTranslationIntoJapanese(t *testing.T) {
	gen := &llmtest.Generator{}
	cmd := translate.New(gen)

	_, _ = cmd.Execute(context.Background(), invocation(map[string]string{"text": "Hello"}))

	want := []llm.Request{{
		System:          "あなたは翻訳者です。ユーザーの文章を日本語に翻訳してください。翻訳文だけを出力し、説明や前置きは書かないでください。",
		UserText:        "Hello",
		MaxOutputTokens: 16000,
	}}
	if !reflect.DeepEqual(gen.Requests, want) {
		t.Errorf("Requests = %+v, want %+v", gen.Requests, want)
	}
}

// 条件: 生成が成功し、結果が「こんにちは」。
// 内容: 原文の引用の下に翻訳文が置かれた Result が返る。
func TestExecute_Success_ReturnsOriginalQuotedAboveTranslation(t *testing.T) {
	gen := &llmtest.Generator{Result: llm.Result{Text: "こんにちは"}}
	cmd := translate.New(gen)

	got, _ := cmd.Execute(context.Background(), invocation(map[string]string{"text": "Hello"}))

	if want := (command.Result{Text: "> Hello\n\nこんにちは"}); !reflect.DeepEqual(got, want) {
		t.Errorf("Execute() = %+v, want %+v", got, want)
	}
}

// 条件: 生成結果が Truncated。
// 内容: 翻訳文の後ろに途中打ち切りの注記が付く。
func TestExecute_TruncatedResult_AppendsNotice(t *testing.T) {
	gen := &llmtest.Generator{Result: llm.Result{Text: "こんに", Truncated: true}}
	cmd := translate.New(gen)

	got, _ := cmd.Execute(context.Background(), invocation(map[string]string{"text": "Hello"}))

	if want := (command.Result{Text: "> Hello\n\nこんに\n\n（上限に達したため、途中までの結果です）"}); !reflect.DeepEqual(got, want) {
		t.Errorf("Execute() = %+v, want %+v", got, want)
	}
}

// 条件: 生成器がエラーを返す。
// 内容: そのエラーがそのまま返る。
func TestExecute_GeneratorFails_ReturnsError(t *testing.T) {
	failure := errors.New("generate failed")
	gen := &llmtest.Generator{Err: failure}
	cmd := translate.New(gen)

	_, err := cmd.Execute(context.Background(), invocation(map[string]string{"text": "Hello"}))

	if !errors.Is(err, failure) {
		t.Errorf("Execute() error = %v, want %v", err, failure)
	}
}

// 条件: 定義を取得する。
// 内容: サーバ内専用で、text は必須・最大 6000 文字、to は任意の定義になる。
func TestDefinition_Always_IsGuildOnlyWithInputLimit(t *testing.T) {
	cmd := translate.New(&llmtest.Generator{})

	got, err := json.Marshal(cmd.Definition())

	want := `{"type":1,"name":"translate","description":"文章を指定した言語に翻訳します",` +
		`"options":[` +
		`{"type":3,"name":"text","description":"翻訳する文章","required":true,"max_length":6000},` +
		`{"type":3,"name":"to","description":"翻訳先の言語（省略時は日本語）"}],` +
		`"contexts":[0]}`
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	assertJSONEqual(t, string(got), want)
}

func assertJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("got is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("want is not JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}
