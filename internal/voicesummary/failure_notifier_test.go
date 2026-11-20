package voicesummary_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/reply/replytest"
	"discord-ai-assistant/internal/voicesummary"
	"discord-ai-assistant/internal/voicesummary/voicesummarytest"
)

// failureScenario は、acceptedManifest の依頼を受け付けた状態（claim なし）の FailureNotifier の依存。
type failureScenario struct {
	store  *voicesummarytest.Store
	poster *replytest.ChannelPoster
	logs   *bytes.Buffer
}

func newFailureScenario(t *testing.T) *failureScenario {
	t.Helper()
	ctx := context.Background()
	store := &voicesummarytest.Store{}
	if err := store.CreateManifest(ctx, acceptedManifest()); err != nil {
		t.Fatalf("CreateManifest: %v", err)
	}
	for track, audio := range map[int]string{1: "たろうの音声", 2: "はなこの音声"} {
		if err := store.PutAudio(ctx, requestID, track, strings.NewReader(audio)); err != nil {
			t.Fatalf("PutAudio: %v", err)
		}
	}
	return &failureScenario{store: store, poster: &replytest.ChannelPoster{}, logs: &bytes.Buffer{}}
}

// notify は、いまの依存で作った FailureNotifier に、jobName のジョブの失敗を 1 回伝える。
func (s *failureScenario) notify(jobName string) error {
	logger := slog.New(slog.NewJSONHandler(s.logs, nil))
	return voicesummary.NewFailureNotifier(s.store, s.poster, logger).Notify(context.Background(), jobName)
}

// failureOutcome は、Notify の終わり方と、置き場・投稿・ログに残ったもの。
type failureOutcome struct {
	Failed bool // Notify がエラーを返したか
	Keys   []string
	Posts  []reply.ChannelPost
	Logs   []reportLog
}

func (s *failureScenario) verify(t *testing.T, err error, want failureOutcome) {
	t.Helper()
	got := failureOutcome{Failed: err != nil, Keys: s.store.Keys(), Posts: s.poster.Posts, Logs: parseReportLogs(t, s.logs)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("outcome =\n%+v\nwant\n%+v\n(err = %v)", got, want, err)
	}
}

// acceptedManifestKeys は、受け付けた直後の依頼（文字起こし前）が置き場に持つもの。
func acceptedManifestKeys() []string {
	return []string{requestID + "/audio/1.flac", requestID + "/audio/2.flac", requestID + "/manifest.json"}
}

// claimedManifestKeys は、acceptedManifestKeys に、投稿の権利（claim）が加わったもの。
func claimedManifestKeys() []string {
	return []string{requestID + "/audio/1.flac", requestID + "/audio/2.flac", requestID + "/manifest.json", requestID + "/report.claim"}
}

// 条件: 再試行を使い切った依頼（マニフェストがあり、claim が無い）。
// 内容: 依頼者への通知つきで失敗を 1 回投稿し、依頼のデータを消す（claim は残る）。
func TestNotify_UnclaimedRequest_PostsFailureOnceAndDiscards(t *testing.T) {
	s := newFailureScenario(t)

	err := s.notify(reportJobName)

	s.verify(t, err, failureOutcome{Keys: reportedRequestKeys(), Posts: []reply.ChannelPost{failurePost()}})
}

// 条件: 既に投稿した依頼（claim がある）と、知らない依頼（マニフェストが無い）。
// 内容: 何もしない。
func TestNotify_ClosedOrUnknownRequest_DoesNothing(t *testing.T) {
	tests := []struct {
		name     string
		arrange  func(t *testing.T, s *failureScenario)
		jobName  string
		wantKeys []string
	}{
		{"already claimed", func(t *testing.T, s *failureScenario) {
			if err := s.store.ClaimReport(context.Background(), requestID); err != nil {
				t.Fatalf("ClaimReport: %v", err)
			}
		}, reportJobName, claimedManifestKeys()},
		{"request not found", func(*testing.T, *failureScenario) {}, "summarize-voice-1449999999999999999-1-1", acceptedManifestKeys()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newFailureScenario(t)
			tt.arrange(t, s)

			err := s.notify(tt.jobName)

			s.verify(t, err, failureOutcome{Keys: tt.wantKeys})
		})
	}
}

// 条件: 失敗の投稿が Discord に断られる。
// 内容: ログを出し、データを消して nil を返す。
func TestNotify_PostFails_LogsAndDiscards(t *testing.T) {
	s := newFailureScenario(t)
	s.poster.Errs = []error{fmt.Errorf("post: %w", reply.ErrPostRejected)}

	err := s.notify(reportJobName)

	s.verify(t, err, failureOutcome{
		Keys:  reportedRequestKeys(),
		Posts: []reply.ChannelPost{failurePost()},
		Logs:  []reportLog{failureLog("notify failure")},
	})
}
