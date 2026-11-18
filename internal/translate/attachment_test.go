package translate_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"discord-ai-assistant/internal/translate"
)

// 条件: サーバが 200 で「hello」を返す。
// 内容: 本文がそのまま返る。
func TestFetch_OkResponse_ReturnsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(server.Close)
	fetcher := translate.NewHTTPAttachmentFetcher(server.Client())

	got, err := fetcher.Fetch(context.Background(), server.URL)

	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if want := []byte("hello"); !reflect.DeepEqual(got, want) {
		t.Errorf("Fetch() = %q, want %q", got, want)
	}
}

// 条件: サーバが 404 を返す。
// 内容: エラーが返る。
func TestFetch_NotFound_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	fetcher := translate.NewHTTPAttachmentFetcher(server.Client())

	_, err := fetcher.Fetch(context.Background(), server.URL)

	if err == nil {
		t.Error("Fetch() error = nil, want error")
	}
}

// 条件: キャンセル済みの ctx で取得する。
// 内容: エラーが返る。
func TestFetch_CanceledContext_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(server.Close)
	fetcher := translate.NewHTTPAttachmentFetcher(server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := fetcher.Fetch(ctx, server.URL)

	if err == nil {
		t.Error("Fetch() error = nil, want error")
	}
}
