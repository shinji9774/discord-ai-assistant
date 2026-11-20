package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/catalog"
	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/config/configtest"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/llm/llmtest"
	"discord-ai-assistant/internal/reply/replytest"
	"discord-ai-assistant/internal/worker"
)

const (
	failureText  = "処理に失敗しました。時間をおいてもう一度実行してください。"
	answerText   = "> Go とは？\n\nプログラミング言語です"
	replyReserve = 10 * time.Second // テストの Handler が、返信のために残す時間
)

var errSend = errors.New("send failed")

func askInvocation() command.Invocation {
	return command.Invocation{
		ApplicationID:    "app1",
		InteractionToken: "tok1",
		CommandName:      "ask",
		Options:          map[string]string{"question": "Go とは？"},
	}
}

func newHandler(t *testing.T, gen *llmtest.Generator, responder *replytest.Responder, logs *bytes.Buffer) *worker.Handler {
	t.Helper()
	registry := catalog.New(catalog.Dependencies{Generator: gen, ToolGenerator: gen}, configtest.Load(t))
	return worker.NewHandler(registry, responder, slog.New(slog.NewJSONHandler(logs, nil)), replyReserve)
}

func replyTexts(responder *replytest.Responder) []string {
	texts := []string{}
	for _, call := range responder.Calls {
		texts = append(texts, call.Result.Text)
	}
	return texts
}

func assertReplyTexts(t *testing.T, responder *replytest.Responder, want []string) {
	t.Helper()
	if got := replyTexts(responder); !reflect.DeepEqual(got, want) {
		t.Errorf("replies = %q, want %q", got, want)
	}
}

// assertLogRecords は JSON Lines のログから time と duration_ms を除いて全体を比較する。
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
		delete(record, "duration_ms")
		got = append(got, record)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("log records = %v, want %v", got, want)
	}
}

// 条件: Registry に無いコマンド名で呼ぶ。
// 内容: 未対応メッセージだけを返信し、エラーは返さない。
func TestHandle_UnknownCommand_RepliesUnsupportedMessage(t *testing.T) {
	gen := &llmtest.Generator{}
	responder := &replytest.Responder{}
	logs := &bytes.Buffer{}
	h := newHandler(t, gen, responder, logs)
	inv := askInvocation()
	inv.CommandName = "unknown"

	err := h.Handle(context.Background(), inv)

	assertReplyTexts(t, responder, []string{"対応していないコマンドです。"})
	if err != nil {
		t.Errorf("Handle() = %v, want nil", err)
	}
}

// 条件: Registry に無いコマンド名で呼び、未対応メッセージの返信が失敗する。
// 内容: 未対応の Warn に続けて返信の失敗を Error で記録し、返信のエラーを返す。
func TestHandle_UnknownCommandReplyFails_LogsErrorAndReturnsIt(t *testing.T) {
	gen := &llmtest.Generator{}
	responder := &replytest.Responder{Errs: []error{errSend}}
	logs := &bytes.Buffer{}
	h := newHandler(t, gen, responder, logs)
	inv := askInvocation()
	inv.CommandName = "unknown"

	err := h.Handle(context.Background(), inv)

	assertLogRecords(t, logs, []map[string]any{
		{"level": "WARN", "msg": "unsupported command", "command": "unknown"},
		{"level": "ERROR", "msg": "unsupported command notification failed", "command": "unknown", "error": "send failed"},
	})
	if !errors.Is(err, errSend) {
		t.Errorf("Handle() = %v, want %v", err, errSend)
	}
}

// 条件: 生成が成功する。
// 内容: コマンドの結果を返信し、エラーは返さない。
func TestHandle_Success_RepliesCommandResult(t *testing.T) {
	gen := &llmtest.Generator{Result: llm.Result{Text: "プログラミング言語です"}}
	responder := &replytest.Responder{}
	logs := &bytes.Buffer{}
	h := newHandler(t, gen, responder, logs)

	err := h.Handle(context.Background(), askInvocation())

	assertReplyTexts(t, responder, []string{answerText})
	if err != nil {
		t.Errorf("Handle() = %v, want nil", err)
	}
}

// 条件: 生成が成功する。
// 内容: コマンド名だけを Info で記録し、入力・回答・token は記録しない。
func TestHandle_Success_LogsCommandNameWithoutBodies(t *testing.T) {
	gen := &llmtest.Generator{Result: llm.Result{Text: "プログラミング言語です"}}
	responder := &replytest.Responder{}
	logs := &bytes.Buffer{}
	h := newHandler(t, gen, responder, logs)

	_ = h.Handle(context.Background(), askInvocation())

	assertLogRecords(t, logs, []map[string]any{
		{"level": "INFO", "msg": "command succeeded", "command": "ask"},
	})
}

// 条件: 生成が失敗する。
// 内容: 失敗メッセージを返信し、生成のエラーを返す。
func TestHandle_GeneratorFails_RepliesFailureMessageAndReturnsError(t *testing.T) {
	boom := errors.New("boom")
	gen := &llmtest.Generator{Err: boom}
	responder := &replytest.Responder{}
	logs := &bytes.Buffer{}
	h := newHandler(t, gen, responder, logs)

	err := h.Handle(context.Background(), askInvocation())

	assertReplyTexts(t, responder, []string{failureText})
	if !errors.Is(err, boom) {
		t.Errorf("Handle() = %v, want %v", err, boom)
	}
}

// 条件: 生成が失敗する（エラー文は boom）。
// 内容: コマンド名・エラー・入力を Error で記録し、回答は空文字。
func TestHandle_GeneratorFails_LogsInputText(t *testing.T) {
	gen := &llmtest.Generator{Err: errors.New("boom")}
	responder := &replytest.Responder{}
	logs := &bytes.Buffer{}
	h := newHandler(t, gen, responder, logs)

	_ = h.Handle(context.Background(), askInvocation())

	assertLogRecords(t, logs, []map[string]any{
		{
			"level":   "ERROR",
			"msg":     "command failed",
			"command": "ask",
			"error":   "boom",
			"input":   map[string]any{"question": "Go とは？"},
			"answer":  "",
		},
	})
}

// 条件: 生成は成功するが、1 回目の返信が失敗する。
// 内容: 2 回目に失敗メッセージを返信し、得られていた回答をログに記録する。
func TestHandle_ReplyFails_LogsInputAndAnswerThenRepliesFailureMessage(t *testing.T) {
	gen := &llmtest.Generator{Result: llm.Result{Text: "プログラミング言語です"}}
	responder := &replytest.Responder{Errs: []error{errSend}}
	logs := &bytes.Buffer{}
	h := newHandler(t, gen, responder, logs)

	_ = h.Handle(context.Background(), askInvocation())

	assertReplyTexts(t, responder, []string{answerText, failureText})
	assertLogRecords(t, logs, []map[string]any{
		{
			"level":   "ERROR",
			"msg":     "command failed",
			"command": "ask",
			"error":   "send failed",
			"input":   map[string]any{"question": "Go とは？"},
			"answer":  answerText,
		},
	})
}

const confidentialAnswer = "佐藤: 会議は 10 時から"

// confidentialCommand は、機密の印つきの結果を返すテスト用のコマンド。
type confidentialCommand struct{}

func (confidentialCommand) Definition() discord.SlashCommandCreate {
	return discord.SlashCommandCreate{Name: "confidential", Description: "test"}
}

func (confidentialCommand) Execute(context.Context, command.Invocation) (command.Result, error) {
	return command.Result{Text: confidentialAnswer, Confidential: true}, nil
}

// 条件: 機密の印つきの結果を返すコマンドで、最初の返信が失敗する。
// 内容: 失敗ログにコマンド名・エラー・入力は出るが、回答（answer）は出ない（NFR: 本文をログに残さない）。
func TestHandle_ReplyFailsForConfidentialResult_LogsWithoutAnswer(t *testing.T) {
	responder := &replytest.Responder{Errs: []error{errSend}}
	logs := &bytes.Buffer{}
	h := worker.NewHandler(command.NewRegistry(confidentialCommand{}), responder, slog.New(slog.NewJSONHandler(logs, nil)), replyReserve)
	inv := command.Invocation{ApplicationID: "app1", InteractionToken: "tok1", CommandName: "confidential", Options: map[string]string{"since": "2026-11-18"}}

	_ = h.Handle(context.Background(), inv)

	assertReplyTexts(t, responder, []string{confidentialAnswer, failureText})
	assertLogRecords(t, logs, []map[string]any{
		{
			"level":   "ERROR",
			"msg":     "command failed",
			"command": "confidential",
			"error":   "send failed",
			"input":   map[string]any{"since": "2026-11-18"},
		},
	})
}

// guidedCommand は、案内で終える結果を返すテスト用のコマンド。
type guidedCommand struct{}

func (guidedCommand) Definition() discord.SlashCommandCreate {
	return discord.SlashCommandCreate{Name: "guided", Description: "test"}
}

func (guidedCommand) Execute(context.Context, command.Invocation) (command.Result, error) {
	return command.Result{
		Text: "案内です",
		Guidance: &command.Guidance{
			Reason: "bot_cannot_read",
			Attrs:  []slog.Attr{slog.String("check", "precheck"), slog.Int("discord_code", 50001)},
		},
	}, nil
}

func newGuidedHandler(responder *replytest.Responder, logs *bytes.Buffer) *worker.Handler {
	return worker.NewHandler(command.NewRegistry(guidedCommand{}), responder, slog.New(slog.NewJSONHandler(logs, nil)), replyReserve)
}

func guidedInvocation() command.Invocation {
	return command.Invocation{ApplicationID: "app1", InteractionToken: "tok1", CommandName: "guided", Options: map[string]string{"since": "2026-11-18"}}
}

// 条件: コマンドが Guidance つきの結果を返し、返信は成功する。
// 内容: command succeeded ではなく Warn の command guided に、コマンド名・理由・診断値を記録し、入力（Options）は記録しない。
func TestHandle_GuidedResult_LogsWarnWithReasonAndAttrsWithoutInput(t *testing.T) {
	responder := &replytest.Responder{}
	logs := &bytes.Buffer{}
	h := newGuidedHandler(responder, logs)

	_ = h.Handle(context.Background(), guidedInvocation())

	assertLogRecords(t, logs, []map[string]any{
		{
			"level":        "WARN",
			"msg":          "command guided",
			"command":      "guided",
			"reason":       "bot_cannot_read",
			"check":        "precheck",
			"discord_code": float64(50001),
		},
	})
}

// 条件: コマンドが Guidance つきの結果を返すが、最初の返信が失敗する。
// 内容: command failed のログに、従来の属性に加えて reason が付く。
func TestHandle_ReplyFailsForGuidedResult_LogsFailureWithReason(t *testing.T) {
	responder := &replytest.Responder{Errs: []error{errSend}}
	logs := &bytes.Buffer{}
	h := newGuidedHandler(responder, logs)

	_ = h.Handle(context.Background(), guidedInvocation())

	assertLogRecords(t, logs, []map[string]any{
		{
			"level":   "ERROR",
			"msg":     "command failed",
			"command": "guided",
			"error":   "send failed",
			"input":   map[string]any{"since": "2026-11-18"},
			"answer":  "案内です",
			"reason":  "bot_cannot_read",
		},
	})
}

// 条件: deadline が replyReserve + 50ms 後にあり、生成が ctx の終了まで止まる。
// 内容: 失敗通知の時間を残して実行を打ち切り、失敗メッセージを返信して DeadlineExceeded を返す。
func TestHandle_DeadlineNear_StopsExecutionAndRepliesFailureMessage(t *testing.T) {
	gen := &llmtest.Generator{Block: true}
	responder := &replytest.Responder{}
	logs := &bytes.Buffer{}
	h := newHandler(t, gen, responder, logs)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(replyReserve+50*time.Millisecond))
	defer cancel()

	err := h.Handle(ctx, askInvocation())

	assertReplyTexts(t, responder, []string{failureText})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Handle() = %v, want DeadlineExceeded", err)
	}
}

// 条件: deadline が replyReserve より手前にあり、生成は成功する設定。
// 内容: 生成を呼ばずに失敗メッセージを返信し、DeadlineExceeded を返す。
func TestHandle_DeadlineWithinReplyReserve_SkipsExecutionAndRepliesFailureMessage(t *testing.T) {
	gen := &llmtest.Generator{Result: llm.Result{Text: "プログラミング言語です"}}
	responder := &replytest.Responder{}
	logs := &bytes.Buffer{}
	h := newHandler(t, gen, responder, logs)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(replyReserve-time.Second))
	defer cancel()

	err := h.Handle(ctx, askInvocation())

	assertReplyTexts(t, responder, []string{failureText})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Handle() = %v, want DeadlineExceeded", err)
	}
}
