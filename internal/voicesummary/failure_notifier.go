package voicesummary

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/reply"
)

// ClaimStore は、依頼を閉じる側が使う置き場。Reporter と FailureNotifier が使う。
type ClaimStore interface {
	// ReadManifest は依頼の記録を返す。なければ ErrRequestNotFound。
	ReadManifest(ctx context.Context, requestID string) (Manifest, error)
	// ClaimReport は、投稿する権利を取る。既に取られていれば ErrReportClaimed。
	ClaimReport(ctx context.Context, requestID string) error
	Discard(ctx context.Context, m Manifest) error
}

// FailureNotifier は、voicereporter のやり直しを使い切った依頼に、失敗を 1 回だけ投稿して、データを消す。
type FailureNotifier struct {
	store  ClaimStore
	poster reply.ChannelPoster
	logger *slog.Logger
}

func NewFailureNotifier(store ClaimStore, poster reply.ChannelPoster, logger *slog.Logger) *FailureNotifier {
	return &FailureNotifier{store: store, poster: poster, logger: logger}
}

// Notify は、jobName のジョブが属する依頼に、失敗を 1 回だけ投稿する。
// この機能のジョブでない、依頼の記録が無い、既に投稿の権利が取られている場合は、何もせず nil を返す。
func (n *FailureNotifier) Notify(ctx context.Context, jobName string) error {
	requestID, ok := requestIDOf(jobName)
	if !ok {
		return nil
	}
	m, err := n.store.ReadManifest(ctx, requestID)
	if errors.Is(err, ErrRequestNotFound) {
		return nil
	}
	if err != nil {
		err = fmt.Errorf("read manifest: %w", err)
		logFailure(n.logger, requestID, stageManifest, err)
		return err
	}
	err = n.store.ClaimReport(ctx, requestID)
	if errors.Is(err, ErrReportClaimed) {
		return nil
	}
	if err != nil {
		err = fmt.Errorf("claim report: %w", err)
		logFailure(n.logger, requestID, stageClaim, err)
		return err
	}
	n.notifyFailure(ctx, m)
	return nil
}

// notifyFailure は、claim を取った依頼に失敗を投稿し、投稿の成否にかかわらずデータを消す。
func (n *FailureNotifier) notifyFailure(ctx context.Context, m Manifest) {
	if err := n.poster.Post(ctx, requesterPost(m, command.Result{Text: command.FailureMessage})); err != nil {
		logFailure(n.logger, m.RequestID, stageNotifyFailure, fmt.Errorf("post failure: %w", err))
	}
	n.discard(ctx, m)
}

// discard は、依頼のデータを消す。消せなければログを出し、残りはライフサイクルに任せる。
func (n *FailureNotifier) discard(ctx context.Context, m Manifest) {
	if err := n.store.Discard(ctx, m); err != nil {
		logFailure(n.logger, m.RequestID, stageDiscard, fmt.Errorf("discard request: %w", err))
	}
}
