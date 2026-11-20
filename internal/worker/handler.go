// Package worker はコマンドを実行して結果を返信し、失敗時は通知とログを残す。
package worker

import (
	"context"
	"log/slog"
	"time"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/reply"
)

const UnsupportedCommandMessage = "対応していないコマンドです。"

const (
	msgUnsupportedCommand = "unsupported command"
	msgCommandSucceeded   = "command succeeded"
	msgCommandFailed      = "command failed"
	msgFailureNotifyError = "failure notification failed"

	logKeyCommand    = "command"
	logKeyDurationMs = "duration_ms"
	logKeyError      = "error"
	logKeyInput      = "input"
	logKeyAnswer     = "answer"
)

// 案内（Guidance）と、未対応コマンドの返信失敗のログ。
const (
	msgCommandGuided          = "command guided"
	msgUnsupportedNotifyError = "unsupported command notification failed"

	logKeyReason = "reason"
)

// Handler は Invocation 1 回分のコマンドを実行して返信する。
type Handler struct {
	registry     *command.Registry
	responder    reply.Responder
	logger       *slog.Logger
	replyReserve time.Duration // 失敗通知の返信のために残す時間
}

func NewHandler(registry *command.Registry, responder reply.Responder, logger *slog.Logger, replyReserve time.Duration) *Handler {
	return &Handler{registry: registry, responder: responder, logger: logger, replyReserve: replyReserve}
}

// Handle はコマンドを実行して結果を返信する。実行か返信に失敗したら失敗メッセージを返信し、元のエラーを返す。
func (h *Handler) Handle(ctx context.Context, inv command.Invocation) error {
	cmd, ok := h.registry.Find(inv.CommandName)
	if !ok {
		h.logger.Warn(msgUnsupportedCommand, logKeyCommand, inv.CommandName)
		return h.replyUnsupported(ctx, inv)
	}

	start := time.Now()
	result, err := h.executeAndReply(ctx, cmd, inv)
	if err != nil {
		h.notifyFailure(ctx, inv, result, err)
		return err
	}
	h.logResult(ctx, inv, result, time.Since(start))
	return nil
}

// logResult は成功したコマンドを記録する。案内で終えたときは、理由と診断値つきの Warn にする。
func (h *Handler) logResult(ctx context.Context, inv command.Invocation, result command.Result, elapsed time.Duration) {
	if result.Guidance == nil {
		h.logger.Info(msgCommandSucceeded,
			logKeyCommand, inv.CommandName,
			logKeyDurationMs, elapsed.Milliseconds(),
		)
		return
	}
	attrs := []slog.Attr{
		slog.String(logKeyCommand, inv.CommandName),
		slog.String(logKeyReason, result.Guidance.Reason),
	}
	attrs = append(attrs, result.Guidance.Attrs...)
	attrs = append(attrs, slog.Int64(logKeyDurationMs, elapsed.Milliseconds()))
	h.logger.LogAttrs(ctx, slog.LevelWarn, msgCommandGuided, attrs...)
}

// replyUnsupported は未対応のメッセージを返信する。返信に失敗したら、ログに残して元のエラーを返す。
func (h *Handler) replyUnsupported(ctx context.Context, inv command.Invocation) error {
	err := h.responder.Respond(ctx, inv, command.Result{Text: UnsupportedCommandMessage})
	if err != nil {
		h.logger.Error(msgUnsupportedNotifyError, logKeyCommand, inv.CommandName, logKeyError, err.Error())
	}
	return err
}

// executeAndReply は返信の時間を残した ctx でコマンドを実行し、結果を返信する。
func (h *Handler) executeAndReply(ctx context.Context, cmd command.Command, inv command.Invocation) (command.Result, error) {
	execCtx, cancel := withReplyReserve(ctx, h.replyReserve)
	defer cancel()
	if execCtx.Err() != nil {
		return command.Result{}, execCtx.Err()
	}

	result, err := cmd.Execute(execCtx, inv)
	if err != nil {
		return result, err
	}
	return result, h.responder.Respond(ctx, inv, result)
}

// notifyFailure は失敗をログに残し、元の ctx で失敗メッセージを返信する。
// 機密の結果は本文を、入力に秘密を含むコマンドは入力を、ログに出さない。
func (h *Handler) notifyFailure(ctx context.Context, inv command.Invocation, result command.Result, cause error) {
	attrs := []any{
		logKeyCommand, inv.CommandName,
		logKeyError, cause.Error(),
	}
	if !h.registry.HasSecretInput(inv.CommandName) {
		attrs = append(attrs, logKeyInput, inv.Options)
	}
	if result.Guidance != nil {
		attrs = append(attrs, logKeyReason, result.Guidance.Reason)
	}
	if !result.Confidential {
		attrs = append(attrs, logKeyAnswer, result.Text)
	}
	h.logger.Error(msgCommandFailed, attrs...)
	if err := h.responder.Respond(ctx, inv, command.Result{Text: command.FailureMessage}); err != nil {
		h.logger.Error(msgFailureNotifyError, logKeyCommand, inv.CommandName, logKeyError, err.Error())
	}
}

// withReplyReserve は ctx の deadline の reserve 前で切れる ctx を返す。deadline が無ければ ctx のまま。
func withReplyReserve(ctx context.Context, reserve time.Duration) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, deadline.Add(-reserve))
}
