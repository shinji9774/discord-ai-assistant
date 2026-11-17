package llm_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/test/bedrockfake"
)

const (
	testModelID = "global.anthropic.claude-haiku-5-5"
	testRegion  = "ap-northeast-1"
)

// newGenerator は fake サーバに向けた BedrockGenerator を作る。
func newGenerator(t *testing.T) (*llm.BedrockGenerator, *bedrockfake.Server) {
	t.Helper()
	fake := bedrockfake.New()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := bedrockruntime.New(bedrockruntime.Options{
		Region:           testRegion,
		BaseEndpoint:     &server.URL,
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		RetryMaxAttempts: 1,
	})
	return llm.NewBedrockGenerator(client, testModelID), fake
}

// 条件: モデルが end_turn で「こんにちは」を返す。
// 内容: Text がそのまま返り、Truncated は偽になる。
func TestGenerate_NormalResponse_ReturnsTextNotTruncated(t *testing.T) {
	generator, fake := newGenerator(t)
	fake.RespondWith("こんにちは", "end_turn")

	got, err := generator.Generate(context.Background(), llm.Request{UserText: "Hi", MaxOutputTokens: 100})

	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if want := (llm.Result{Text: "こんにちは", Truncated: false}); got != want {
		t.Errorf("Generate() = %+v, want %+v", got, want)
	}
}

// 条件: モデルが max_tokens で「こんに」を返す。
// 内容: Text がそのまま返り、Truncated が真になる。
func TestGenerate_StoppedByMaxTokens_ReturnsTruncated(t *testing.T) {
	generator, fake := newGenerator(t)
	fake.RespondWith("こんに", "max_tokens")

	got, err := generator.Generate(context.Background(), llm.Request{UserText: "Hi", MaxOutputTokens: 100})

	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if want := (llm.Result{Text: "こんに", Truncated: true}); got != want {
		t.Errorf("Generate() = %+v, want %+v", got, want)
	}
}

// 条件: System、UserText、MaxOutputTokens を指定して呼ぶ。
// 内容: モデル ID、system、user のメッセージ、maxTokens がそのまま Bedrock に届く。
func TestGenerate_Always_SendsSystemUserTextAndMaxTokensToModel(t *testing.T) {
	generator, fake := newGenerator(t)
	req := llm.Request{System: "sys", UserText: "Hello", MaxOutputTokens: 16000}

	_, err := generator.Generate(context.Background(), req)

	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	want := []bedrockfake.Request{{ModelID: "global.anthropic.claude-haiku-5-5", System: "sys", UserText: "Hello", MaxTokens: 16000}}
	if got := fake.Requests(); !equalRequests(got, want) {
		t.Errorf("fake.Requests() = %+v, want %+v", got, want)
	}
}

// 条件: Bedrock が 400 で拒否する。
// 内容: Generate がエラーを返す。
func TestGenerate_BedrockRejects_ReturnsError(t *testing.T) {
	generator, fake := newGenerator(t)
	fake.FailWith(400)

	_, err := generator.Generate(context.Background(), llm.Request{UserText: "Hi", MaxOutputTokens: 100})

	if err == nil {
		t.Fatal("Generate() error = nil, want error")
	}
}

func equalRequests(a, b []bedrockfake.Request) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
