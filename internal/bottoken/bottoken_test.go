package bottoken_test

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"discord-ai-assistant/internal/bottoken"
	"discord-ai-assistant/test/ssmfake"
)

const (
	testRegion        = "ap-northeast-1"
	testParameterName = "/discord-ai-assistant/bot-token"
)

func newClient(t *testing.T) (*ssm.Client, *ssmfake.Server) {
	t.Helper()
	fake := ssmfake.New()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := ssm.New(ssm.Options{
		Region:           testRegion,
		BaseEndpoint:     &server.URL,
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		RetryMaxAttempts: 1,
	})
	return client, fake
}

// 条件: SecureString のパラメータが設定されている。
// 内容: パラメータの値がそのまま返る。
func TestLoad_ParameterExists_ReturnsValue(t *testing.T) {
	client, fake := newClient(t)
	fake.SetParameter(testParameterName, "token1")

	got, err := bottoken.Load(context.Background(), client, testParameterName)

	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != "token1" {
		t.Errorf("Load() = %q, want %q", got, "token1")
	}
}

// 条件: SecureString のパラメータが設定されている。
// 内容: GetParameter が WithDecryption: true で 1 回だけ呼ばれる。
func TestLoad_Always_RequestsDecryption(t *testing.T) {
	client, fake := newClient(t)
	fake.SetParameter(testParameterName, "token1")

	_, err := bottoken.Load(context.Background(), client, testParameterName)

	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []ssmfake.Request{{Name: testParameterName, WithDecryption: true}}
	if got := fake.Requests(); !reflect.DeepEqual(got, want) {
		t.Errorf("Requests() = %+v, want %+v", got, want)
	}
}

// 条件: パラメータを設定していない。
// 内容: ParameterNotFound がエラーとして返る。
func TestLoad_ParameterNotFound_ReturnsError(t *testing.T) {
	client, _ := newClient(t)

	_, err := bottoken.Load(context.Background(), client, testParameterName)

	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}
