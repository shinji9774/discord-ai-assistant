package ingress_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/help"
	"discord-ai-assistant/internal/ingress"
)

const (
	pingBody    = `{"type":1}`
	commandBody = `{"type":2,"application_id":"app1","token":"tok1","channel_id":"ch1","data":{"name":"translate","options":[{"name":"text","type":3,"value":"Hello"},{"name":"to","type":3,"value":"英語"}]}}`

	fileCommandBody = `{"type":2,"application_id":"app1","token":"tok1","channel_id":"ch1","data":{"name":"translate",` +
		`"options":[{"name":"file","type":11,"value":"att1"},{"name":"to","type":3,"value":"en"}],` +
		`"resolved":{"attachments":{"att1":{"id":"att1","filename":"memo.txt","size":12,"url":"https://cdn.example/att1/memo.txt","content_type":"text/plain; charset=utf-8"}}}}}`

	privateCommandBody = `{"type":2,"application_id":"app1","token":"tok1","channel_id":"ch1","data":{"name":"image","options":[{"name":"prompt","type":3,"value":"cat"},{"name":"visibility","type":3,"value":"private"}]}}`
	publicCommandBody  = `{"type":2,"application_id":"app1","token":"tok1","channel_id":"ch1","data":{"name":"image","options":[{"name":"prompt","type":3,"value":"cat"},{"name":"visibility","type":3,"value":"public"}]}}`

	permissionsCommandBody = `{"type":2,"application_id":"app1","token":"tok1","channel_id":"ch1",` +
		`"app_permissions":"66560","member":{"permissions":"65536"},` +
		`"data":{"name":"summarize","options":[{"name":"since","type":3,"value":"2026-11-18"}]}}`

	helpCommandBody = `{"type":2,"application_id":"app1","token":"tok1","channel_id":"ch1","data":{"name":"help"}}`

	// 依頼 ID・サーバ・実行者・チャンネルの種類（2 はボイスチャンネル）を持つ interaction。
	contextCommandBody = `{"type":2,"id":"1400000000000000001","application_id":"app1","token":"tok1","guild_id":"1500000000000000001",` +
		`"channel_id":"ch1","channel":{"id":"ch1","type":2},"member":{"user":{"id":"1600000000000000001"}},` +
		`"data":{"name":"translate","options":[{"name":"text","type":3,"value":"Hello"}]}}`

	// 入力に秘密を含むコマンド。visibility の指定は無い。
	secretCommandName = "summarize-voice"
	secretCommandBody = `{"type":2,"application_id":"app1","token":"tok1","channel_id":"ch1",` +
		`"data":{"name":"summarize-voice","options":[{"name":"link","type":3,"value":"https://craig.horse/rec/AbCdEf1234?key=secret"}]}}`
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

// secretCommands は、名前の集合で答える SecretInputs の fake。
type secretCommands map[string]bool

func (s secretCommands) HasSecretInput(commandName string) bool {
	return s[commandName]
}

// cmd/ingress が渡す *command.Registry が SecretInputs を満たすことを、コンパイル時に確かめる。
var _ ingress.SecretInputs = (*command.Registry)(nil)

// testSecretInputs は、テストの Handler が秘密入力として扱うコマンド。
var testSecretInputs = secretCommands{secretCommandName: true}

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
	return ingress.NewHandler(publicKey, invoker, testSecretInputs, logger), privateKey
}

// newLoggedHandler は newHandler と同じ Handler を、JSON のログを buffer に書く logger で作る。
func newLoggedHandler(t *testing.T, invoker ingress.WorkerInvoker) (*ingress.Handler, ed25519.PrivateKey, *bytes.Buffer) {
	t.Helper()
	publicKey, privateKey := newKeyPair(t)
	logs := &bytes.Buffer{}
	return ingress.NewHandler(publicKey, invoker, testSecretInputs, slog.New(slog.NewJSONHandler(logs, nil))), privateKey, logs
}

// assertLogRecords は JSON Lines のログから time を除いて全体を比較する。
func assertLogRecords(t *testing.T, logs *bytes.Buffer, want []map[string]any) {
	t.Helper()
	got := []map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		record := map[string]any{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid log line %q: %v", line, err)
		}
		delete(record, "time")
		got = append(got, record)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("log records = %v, want %v", got, want)
	}
}

func jsonResponse(status int, body string) events.LambdaFunctionURLResponse {
	return events.LambdaFunctionURLResponse{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       body,
	}
}

// helpResponse は /help の応答のうち、仕様として確認する部分。
type helpResponse struct {
	Type    int
	Flags   int
	Content string
}

func summarizeHelpResponse(t *testing.T, res events.LambdaFunctionURLResponse) helpResponse {
	t.Helper()
	var body struct {
		Type int `json:"type"`
		Data struct {
			Content string `json:"content"`
			Flags   int    `json:"flags"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.Body), &body); err != nil {
		t.Fatalf("unmarshal response body %q: %v", res.Body, err)
	}
	return helpResponse{Type: body.Type, Flags: body.Data.Flags, Content: body.Data.Content}
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
// 内容: 401 が返り、worker は起動されず、理由 mismatch を Warn で記録する。
func TestHandle_InvalidSignature_Returns401WithoutInvokingWorker(t *testing.T) {
	invoker := &recordingInvoker{}
	handler, _, logs := newLoggedHandler(t, invoker)
	_, otherKey := newKeyPair(t)
	req := signedRequest(t, otherKey, commandBody)

	got, _ := handler.Handle(context.Background(), req)

	want := events.LambdaFunctionURLResponse{StatusCode: http.StatusUnauthorized, Body: "invalid request signature"}
	assertResponse(t, got, want)
	if recorded := invoker.recorded(); len(recorded) != 0 {
		t.Errorf("invocations = %+v, want empty", recorded)
	}
	assertLogRecords(t, logs, []map[string]any{
		{"level": "WARN", "msg": "signature verification failed", "reason": "mismatch"},
	})
}

// 条件: 署名ヘッダがない。
// 内容: 401 が返り、理由 missing_header を Warn で記録する。
func TestHandle_MissingSignatureHeaders_Returns401(t *testing.T) {
	handler, _, logs := newLoggedHandler(t, &recordingInvoker{})
	req := events.LambdaFunctionURLRequest{Body: pingBody}

	got, _ := handler.Handle(context.Background(), req)

	want := events.LambdaFunctionURLResponse{StatusCode: http.StatusUnauthorized, Body: "invalid request signature"}
	assertResponse(t, got, want)
	assertLogRecords(t, logs, []map[string]any{
		{"level": "WARN", "msg": "signature verification failed", "reason": "missing_header"},
	})
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

// 条件: 添付ファイル型のオプション（file）と文字列のオプション（to）を持つ translate コマンド。
// 内容: file は resolved から引いた情報が Attachments に入り、Options には to だけが入る。
func TestHandle_CommandWithAttachment_PassesResolvedAttachmentOutsideOptions(t *testing.T) {
	invoker := &recordingInvoker{}
	handler, privateKey := newHandler(t, invoker)
	req := signedRequest(t, privateKey, fileCommandBody)

	_, _ = handler.Handle(context.Background(), req)

	wantInvocations := []command.Invocation{{
		ApplicationID:    "app1",
		InteractionToken: "tok1",
		ChannelID:        "ch1",
		CommandName:      "translate",
		Options:          map[string]string{"to": "en"},
		Attachments: map[string]command.Attachment{"file": {
			Filename:    "memo.txt",
			URL:         "https://cdn.example/att1/memo.txt",
			ContentType: "text/plain; charset=utf-8",
			Size:        12,
		}},
	}}
	if recorded := invoker.recorded(); !reflect.DeepEqual(recorded, wantInvocations) {
		t.Errorf("invocations = %+v, want %+v", recorded, wantInvocations)
	}
}

// 条件: 正しい署名の help コマンド。
// 内容: 200 で type 4・ephemeral の応答が返り、本文が help.Usage と一致し、worker は起動されない。
func TestHandle_HelpCommand_ReturnsEphemeralHelpWithoutInvokingWorker(t *testing.T) {
	invoker := &recordingInvoker{}
	handler, privateKey := newHandler(t, invoker)
	req := signedRequest(t, privateKey, helpCommandBody)

	got, _ := handler.Handle(context.Background(), req)

	want := helpResponse{Type: 4, Flags: 64, Content: help.Usage}
	if gotSummary := summarizeHelpResponse(t, got); !reflect.DeepEqual(gotSummary, want) {
		t.Errorf("help response = %+v, want %+v", gotSummary, want)
	}
	if recorded := invoker.recorded(); len(recorded) != 0 {
		t.Errorf("invocations = %+v, want empty", recorded)
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
// 内容: 400 が返り、type の値を Warn で記録する。
func TestHandle_UnsupportedInteractionType_Returns400(t *testing.T) {
	handler, privateKey, logs := newLoggedHandler(t, &recordingInvoker{})
	req := signedRequest(t, privateKey, `{"type":3}`)

	got, _ := handler.Handle(context.Background(), req)

	want := events.LambdaFunctionURLResponse{StatusCode: http.StatusBadRequest, Body: "unsupported interaction type"}
	assertResponse(t, got, want)
	assertLogRecords(t, logs, []map[string]any{
		{"level": "WARN", "msg": "unsupported interaction type", "type": float64(3)},
	})
}

// 条件: IsBase64Encoded が真で、本文が base64 として不正。
// 内容: 署名を検証できないものとして 401 が返り、worker は起動されず、デコードの失敗を Warn で記録する。
func TestHandle_InvalidBase64Body_Returns401WithoutInvokingWorker(t *testing.T) {
	invoker := &recordingInvoker{}
	handler, privateKey, logs := newLoggedHandler(t, invoker)
	req := signedRequest(t, privateKey, pingBody)
	req.Body = "!!not-base64!!"
	req.IsBase64Encoded = true

	got, _ := handler.Handle(context.Background(), req)

	want := events.LambdaFunctionURLResponse{StatusCode: http.StatusUnauthorized, Body: "invalid request signature"}
	assertResponse(t, got, want)
	if recorded := invoker.recorded(); len(recorded) != 0 {
		t.Errorf("invocations = %+v, want empty", recorded)
	}
	assertLogRecords(t, logs, []map[string]any{
		{"level": "WARN", "msg": "request body decode failed", "error": "illegal base64 data at input byte 0"},
	})
}

// 条件: 正しく署名された、JSON として不正な本文。
// 内容: 400 invalid request body が返り、デコードのエラー文を（本文なしで）Warn で記録する。
func TestHandle_MalformedJSON_Returns400(t *testing.T) {
	handler, privateKey, logs := newLoggedHandler(t, &recordingInvoker{})
	req := signedRequest(t, privateKey, `{"type":`)

	got, _ := handler.Handle(context.Background(), req)

	want := events.LambdaFunctionURLResponse{StatusCode: http.StatusBadRequest, Body: "invalid request body"}
	assertResponse(t, got, want)
	assertLogRecords(t, logs, []map[string]any{
		{"level": "WARN", "msg": "interaction decode failed", "error": "unexpected end of JSON input"},
	})
}

// 条件: visibility に private を指定したコマンド。
// 内容: 200 で flags 64 付きの {"type":5} が返り、worker に Invocation が渡される。
func TestHandle_PrivateVisibility_ReturnsEphemeralDeferred(t *testing.T) {
	invoker := &recordingInvoker{}
	handler, privateKey := newHandler(t, invoker)
	req := signedRequest(t, privateKey, privateCommandBody)

	got, _ := handler.Handle(context.Background(), req)

	assertResponse(t, got, jsonResponse(http.StatusOK, `{"type":5,"data":{"flags":64}}`))
	if recorded := invoker.recorded(); len(recorded) != 1 {
		t.Errorf("invocations = %+v, want 1 item", recorded)
	}
}

// 条件: visibility に public を指定したコマンド。
// 内容: 現状と同じ {"type":5} が返る（flags は付かない）。
func TestHandle_PublicVisibility_ReturnsPlainDeferred(t *testing.T) {
	handler, privateKey := newHandler(t, &recordingInvoker{})
	req := signedRequest(t, privateKey, publicCommandBody)

	got, _ := handler.Handle(context.Background(), req)

	assertResponse(t, got, jsonResponse(http.StatusOK, `{"type":5}`))
}

// 条件: visibility に private を指定し、worker の起動に失敗する。
// 内容: 200 で、失敗メッセージと flags 64 付きの {"type":4} が返る。
func TestHandle_PrivateVisibilityWorkerInvokeFails_ReturnsEphemeralFailureMessage(t *testing.T) {
	handler, privateKey := newHandler(t, &recordingInvoker{err: errors.New("boom")})
	req := signedRequest(t, privateKey, privateCommandBody)

	got, _ := handler.Handle(context.Background(), req)

	want := jsonResponse(http.StatusOK, `{"type":4,"data":{"content":"処理に失敗しました。時間をおいてもう一度実行してください。","flags":64}}`)
	assertResponse(t, got, want)
}

// 条件: bot の権限（チャンネルを見る + メッセージ履歴を読む）と、実行者の権限（メッセージ履歴を読む）を持つ interaction。
// 内容: 両方の権限が Invocation に入って worker に渡る（4.2、4.6）。
func TestHandle_CommandWithPermissions_PassesPermissionsToWorker(t *testing.T) {
	invoker := &recordingInvoker{}
	handler, privateKey := newHandler(t, invoker)
	req := signedRequest(t, privateKey, permissionsCommandBody)

	_, _ = handler.Handle(context.Background(), req)

	wantInvocations := []command.Invocation{{
		ApplicationID:     "app1",
		InteractionToken:  "tok1",
		ChannelID:         "ch1",
		CommandName:       "summarize",
		Options:           map[string]string{"since": "2026-11-18"},
		AppPermissions:    discord.PermissionViewChannel | discord.PermissionReadMessageHistory,
		MemberPermissions: discord.PermissionReadMessageHistory,
	}}
	if recorded := invoker.recorded(); !reflect.DeepEqual(recorded, wantInvocations) {
		t.Errorf("invocations = %+v, want %+v", recorded, wantInvocations)
	}
}

// 条件: 依頼 ID、サーバ、実行者、チャンネルの種類（ボイスチャンネル）を持つ interaction。
// 内容: 4 項目が Invocation に入って worker に渡る。
func TestHandle_CommandWithRequestContext_PassesContextToWorker(t *testing.T) {
	invoker := &recordingInvoker{}
	handler, privateKey := newHandler(t, invoker)
	req := signedRequest(t, privateKey, contextCommandBody)

	_, _ = handler.Handle(context.Background(), req)

	wantInvocations := []command.Invocation{{
		ApplicationID:    "app1",
		InteractionToken: "tok1",
		ChannelID:        "ch1",
		CommandName:      "translate",
		Options:          map[string]string{"text": "Hello"},
		InteractionID:    "1400000000000000001",
		GuildID:          "1500000000000000001",
		UserID:           "1600000000000000001",
		ChannelType:      discord.ChannelTypeGuildVoice,
	}}
	if recorded := invoker.recorded(); !reflect.DeepEqual(recorded, wantInvocations) {
		t.Errorf("invocations = %+v, want %+v", recorded, wantInvocations)
	}
}

// 条件: 入力に秘密を含むコマンド（visibility の指定なし）。
// 内容: 200 で flags 64 付きの {"type":5} が返り、worker に Invocation が 1 件渡される（1.2）。
func TestHandle_SecretInputCommand_ReturnsEphemeralDeferred(t *testing.T) {
	invoker := &recordingInvoker{}
	handler, privateKey := newHandler(t, invoker)
	req := signedRequest(t, privateKey, secretCommandBody)

	got, _ := handler.Handle(context.Background(), req)

	assertResponse(t, got, jsonResponse(http.StatusOK, `{"type":5,"data":{"flags":64}}`))
	if recorded := invoker.recorded(); len(recorded) != 1 {
		t.Errorf("invocations = %+v, want 1 item", recorded)
	}
}

// 条件: 入力に秘密を含むコマンドで、worker の起動に失敗する。
// 内容: 200 で失敗メッセージと flags 64 付きの {"type":4} が返り、失敗ログにはコマンド名とエラーだけが出る（入力は出ない）。
func TestHandle_SecretInputCommandWorkerInvokeFails_ReturnsEphemeralFailureWithoutLoggingInput(t *testing.T) {
	handler, privateKey, logs := newLoggedHandler(t, &recordingInvoker{err: errors.New("boom")})
	req := signedRequest(t, privateKey, secretCommandBody)

	got, _ := handler.Handle(context.Background(), req)

	want := jsonResponse(http.StatusOK, `{"type":4,"data":{"content":"処理に失敗しました。時間をおいてもう一度実行してください。","flags":64}}`)
	assertResponse(t, got, want)
	assertLogRecords(t, logs, []map[string]any{
		{"level": "ERROR", "msg": "worker invoke failed", "command": "summarize-voice", "error": "boom"},
	})
}
