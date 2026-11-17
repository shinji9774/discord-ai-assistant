package ingress_test

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/ingress"
	"discord-ai-assistant/test/lambdafake"
)

const (
	testRegion       = "ap-northeast-1"
	testFunctionName = "worker"
)

func newInvoker(t *testing.T) (*ingress.LambdaInvoker, *lambdafake.Server) {
	t.Helper()
	fake := lambdafake.New()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := lambda.New(lambda.Options{
		Region:           testRegion,
		BaseEndpoint:     &server.URL,
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		RetryMaxAttempts: 1,
	})
	return ingress.NewLambdaInvoker(client, testFunctionName), fake
}

var testInvocation = command.Invocation{
	ApplicationID:    "app1",
	InteractionToken: "tok1",
	ChannelID:        "ch1",
	CommandName:      "translate",
	Options:          map[string]string{"text": "Hello"},
}

// 条件: Lambda が 202 を返す。
// 内容: Invocation の JSON が Event 型で、指定の関数名に送られる。
func TestInvokeAsync_Success_SendsInvocationAsEvent(t *testing.T) {
	invoker, fake := newInvoker(t)

	err := invoker.InvokeAsync(context.Background(), testInvocation)

	if err != nil {
		t.Fatalf("InvokeAsync() error = %v", err)
	}
	want := []lambdafake.Invocation{{
		FunctionName:   "worker",
		InvocationType: "Event",
		Payload:        `{"application_id":"app1","interaction_token":"tok1","channel_id":"ch1","command_name":"translate","options":{"text":"Hello"}}`,
	}}
	if got := fake.Invocations(); !reflect.DeepEqual(got, want) {
		t.Errorf("Invocations() = %+v, want %+v", got, want)
	}
}

// 条件: Lambda が 400 を返す。
// 内容: エラーが返る。
func TestInvokeAsync_LambdaFails_ReturnsError(t *testing.T) {
	invoker, fake := newInvoker(t)
	fake.FailWith(400)

	err := invoker.InvokeAsync(context.Background(), testInvocation)

	if err == nil {
		t.Errorf("InvokeAsync() error = nil, want error")
	}
}
