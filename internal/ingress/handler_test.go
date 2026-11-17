package ingress_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/ingress"
)

const (
	pingBody    = `{"type":1}`
	commandBody = `{"type":2,"application_id":"app1","token":"tok1","channel_id":"ch1","data":{"name":"translate","options":[{"name":"text","type":3,"value":"Hello"},{"name":"to","type":3,"value":"英語"}]}}`
)

// recordingInvoker は依頼を記録し、設定されたエラーを返す WorkerInvoker の fake。
type recordingInvoker struct {
	mu          sync.Mutex
	err         error
	invocations []command.Invocation
}

func (r *recordingInvoker) InvokeAsync(_ context.Context, inv command.Invocation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.invocations = append(r.invocations, inv)
	return r.err
}

func (r *recordingInvoker) recorded() []command.Invocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]command.Invocation(nil), r.invocations...)
}

func signedRequest(t *testing.T, privateKey ed25519.PrivateKey, body string) events.LambdaFunctionURLRequest {
	t.Helper()
	return events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-Signature-Ed25519":   sign(privateKey, testTimestamp, body),
			"X-Signature-Timestamp": testTimestamp,
		},
		Body: body,
	}
}

func newHandler(t *testing.T, invoker ingress.WorkerInvoker) (*ingress.Handler, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey := newKeyPair(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return ingress.NewHandler(publicKey, invoker, logger), privateKey
}

func jsonResponse(status int, body string) events.LambdaFunctionURLResponse {
	return events.LambdaFunctionURLResponse{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       body,
	}
}

func assertResponse(t *testing.T, got, want events.LambdaFunctionURLResponse) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Handle() = %+v, want %+v", got, want)
	}
}

// 条件: 正しい署名の PING。
// 内容: 200 で {"type":1} が返る。
func TestHandle_ValidPing_ReturnsPong(t *testing.T) {
	handler, privateKey := newHandler(t, &recordingInvoker{})
	req := signedRequest(t, privateKey, pingBody)

	got, _ := handler.Handle(context.Background(), req)

	assertResponse(t, got, jsonResponse(http.StatusOK, `{"type":1}`))
}

// 条件: 別の鍵で署名したコマンド。
// 内容: 401 が返り、worker は起動されない。
func TestHandle_InvalidSignature_Returns401WithoutInvokingWorker(t *testing.T) {
	invoker := &recordingInvoker{}
	handler, _ := newHandler(t, invoker)
	_, otherKey := newKeyPair(t)
	req := signedRequest(t, otherKey, commandBody)

	got, _ := handler.Handle(context.Background(), req)

	want := events.LambdaFunctionURLResponse{StatusCode: http.StatusUnauthorized, Body: "invalid request signature"}
	assertResponse(t, got, want)
	if recorded := invoker.recorded(); len(recorded) != 0 {
		t.Errorf("invocations = %+v, want empty", recorded)
	}
}

// 条件: 署名ヘッダがない。
// 内容: 401 が返る。
func TestHandle_MissingSignatureHeaders_Returns401(t *testing.T) {
	handler, _ := newHandler(t, &recordingInvoker{})
	req := events.LambdaFunctionURLRequest{Body: pingBody}

	got, _ := handler.Handle(context.Background(), req)

	want := events.LambdaFunctionURLResponse{StatusCode: http.StatusUnauthorized, Body: "invalid request signature"}
	assertResponse(t, got, want)
}

// 条件: 正しい署名の translate コマンド。
// 内容: 200 で {"type":5} が返り、worker に Invocation が 1 件渡される。
func TestHandle_Command_InvokesWorkerAndReturnsDeferred(t *testing.T) {
	invoker := &recordingInvoker{}
	handler, privateKey := newHandler(t, invoker)
	req := signedRequest(t, privateKey, commandBody)

	got, _ := handler.Handle(context.Background(), req)

	assertResponse(t, got, jsonResponse(http.StatusOK, `{"type":5}`))
	wantInvocations := []command.Invocation{{
		ApplicationID:    "app1",
		InteractionToken: "tok1",
		ChannelID:        "ch1",
		CommandName:      "translate",
		Options:          map[string]string{"text": "Hello", "to": "英語"},
	}}
	if recorded := invoker.recorded(); !reflect.DeepEqual(recorded, wantInvocations) {
		t.Errorf("invocations = %+v, want %+v", recorded, wantInvocations)
	}
}

// 条件: worker の起動に失敗する。
// 内容: 200 で失敗メッセージ付きの {"type":4} が返る。
func TestHandle_WorkerInvokeFails_ReturnsFailureMessage(t *testing.T) {
	handler, privateKey := newHandler(t, &recordingInvoker{err: errors.New("boom")})
	req := signedRequest(t, privateKey, commandBody)

	got, _ := handler.Handle(context.Background(), req)

	want := jsonResponse(http.StatusOK, `{"type":4,"data":{"content":"処理に失敗しました。時間をおいてもう一度実行してください。"}}`)
	assertResponse(t, got, want)
}

// 条件: 本文が base64 で、IsBase64Encoded が真。
// 内容: デコードした本文で署名が検証され、200 で {"type":1} が返る。
func TestHandle_Base64EncodedBody_VerifiesDecodedBody(t *testing.T) {
	handler, privateKey := newHandler(t, &recordingInvoker{})
	req := signedRequest(t, privateKey, pingBody)
	req.Body = base64.StdEncoding.EncodeToString([]byte(pingBody))
	req.IsBase64Encoded = true

	got, _ := handler.Handle(context.Background(), req)

	assertResponse(t, got, jsonResponse(http.StatusOK, `{"type":1}`))
}

// 条件: 署名は正しいが type が 3。
// 内容: 400 が返る。
func TestHandle_UnsupportedInteractionType_Returns400(t *testing.T) {
	handler, privateKey := newHandler(t, &recordingInvoker{})
	req := signedRequest(t, privateKey, `{"type":3}`)

	got, _ := handler.Handle(context.Background(), req)

	want := events.LambdaFunctionURLResponse{StatusCode: http.StatusBadRequest, Body: "unsupported interaction type"}
	assertResponse(t, got, want)
}

// 条件: IsBase64Encoded が真で、本文が base64 として不正。
// 内容: 署名を検証できないものとして 401 が返り、worker は起動されない。
func TestHandle_InvalidBase64Body_Returns401WithoutInvokingWorker(t *testing.T) {
	invoker := &recordingInvoker{}
	handler, privateKey := newHandler(t, invoker)
	req := signedRequest(t, privateKey, pingBody)
	req.Body = "!!not-base64!!"
	req.IsBase64Encoded = true

	got, _ := handler.Handle(context.Background(), req)

	want := events.LambdaFunctionURLResponse{StatusCode: http.StatusUnauthorized, Body: "invalid request signature"}
	assertResponse(t, got, want)
	if recorded := invoker.recorded(); len(recorded) != 0 {
		t.Errorf("invocations = %+v, want empty", recorded)
	}
}

// 条件: 正しく署名された、JSON として不正な本文。
// 内容: 400 invalid request body が返る。
func TestHandle_MalformedJSON_Returns400(t *testing.T) {
	handler, privateKey := newHandler(t, &recordingInvoker{})
	req := signedRequest(t, privateKey, `{"type":`)

	got, _ := handler.Handle(context.Background(), req)

	want := events.LambdaFunctionURLResponse{StatusCode: http.StatusBadRequest, Body: "invalid request body"}
	assertResponse(t, got, want)
}
