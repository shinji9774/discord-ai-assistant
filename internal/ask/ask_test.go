package ask_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"discord-ai-assistant/internal/ask"
	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/llm/llmtest"
)

const askSystem = "あなたは Discord サーバで質問に答えるアシスタントです。質問と同じ言語で、簡潔に答えてください。"

func invocation(question string) command.Invocation {
	return command.Invocation{CommandName: "ask", Options: map[string]string{"question": question}}
}

// 条件: 1 問目を実行した後に 2 問目を実行する。
// 内容: 2 件目の request は 2 問目だけを含み、1 問目の内容を含まない。
func TestExecute_Question_RequestsAnswerWithoutHistory(t *testing.T) {
	gen := &llmtest.Generator{}
	cmd := ask.New(gen)
	_, _ = cmd.Execute(context.Background(), invocation("1 問目"))

	_, _ = cmd.Execute(context.Background(), invocation("2 問目"))

	want := llm.Request{System: askSystem, UserText: "2 問目", MaxOutputTokens: 16000}
	if len(gen.Requests) != 2 || !reflect.DeepEqual(gen.Requests[1], want) {
		t.Errorf("Requests = %+v, want second = %+v", gen.Requests, want)
	}
}

// 条件: 生成が成功し、結果が「プログラミング言語です」。
// 内容: 質問の引用の下に回答が置かれた Result が返る。
func TestExecute_Success_ReturnsQuestionQuotedAboveAnswer(t *testing.T) {
	gen := &llmtest.Generator{Result: llm.Result{Text: "プログラミング言語です"}}
	cmd := ask.New(gen)

	got, _ := cmd.Execute(context.Background(), invocation("Go とは？"))

	if want := (command.Result{Text: "> Go とは？\n\nプログラミング言語です"}); !reflect.DeepEqual(got, want) {
		t.Errorf("Execute() = %+v, want %+v", got, want)
	}
}

// 条件: 生成器がエラーを返す。
// 内容: そのエラーがそのまま返る。
func TestExecute_GeneratorFails_ReturnsError(t *testing.T) {
	failure := errors.New("generate failed")
	gen := &llmtest.Generator{Err: failure}
	cmd := ask.New(gen)

	_, err := cmd.Execute(context.Background(), invocation("Go とは？"))

	if !errors.Is(err, failure) {
		t.Errorf("Execute() error = %v, want %v", err, failure)
	}
}

// 条件: 定義を取得する。
// 内容: サーバ内専用で、question は必須・最大 6000 文字の定義になる。
func TestDefinition_Always_IsGuildOnlyWithInputLimit(t *testing.T) {
	cmd := ask.New(&llmtest.Generator{})

	got, err := json.Marshal(cmd.Definition())

	want := `{"type":1,"name":"ask","description":"質問に回答します",` +
		`"options":[{"type":3,"name":"question","description":"質問の内容","required":true,"max_length":6000}],` +
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
