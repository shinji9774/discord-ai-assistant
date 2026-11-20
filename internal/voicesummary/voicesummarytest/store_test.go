package voicesummarytest_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"discord-ai-assistant/internal/voicesummary"
	"discord-ai-assistant/internal/voicesummary/voicesummarytest"
)

// 条件: 同じ依頼 ID のマニフェストを 2 回作る。
// 内容: 2 回目は ErrRequestExists を返す（S3 の条件つき作成と同じ）。
func TestStoreCreateManifest_SameRequestTwice_ReturnsErrRequestExists(t *testing.T) {
	store := &voicesummarytest.Store{}
	m := voicesummary.Manifest{RequestID: "1441234567890123456"}
	if err := store.CreateManifest(context.Background(), m); err != nil {
		t.Fatalf("first CreateManifest: %v", err)
	}

	err := store.CreateManifest(context.Background(), m)

	if !errors.Is(err, voicesummary.ErrRequestExists) {
		t.Errorf("err = %v, want %v", err, voicesummary.ErrRequestExists)
	}
}

// 条件: マニフェストを作っていない依頼 ID を読む。
// 内容: ErrRequestNotFound を返す。
func TestStoreReadManifest_UnknownRequest_ReturnsErrRequestNotFound(t *testing.T) {
	store := &voicesummarytest.Store{}

	_, err := store.ReadManifest(context.Background(), "1441234567890123456")

	if !errors.Is(err, voicesummary.ErrRequestNotFound) {
		t.Errorf("err = %v, want %v", err, voicesummary.ErrRequestNotFound)
	}
}

// 条件: 同じ依頼の投稿の権利を 2 回取る。
// 内容: 2 回目は ErrReportClaimed を返す（S3 の条件つき作成と同じ）。
func TestStoreClaimReport_SecondClaim_ReturnsErrReportClaimed(t *testing.T) {
	store := &voicesummarytest.Store{}
	if err := store.ClaimReport(context.Background(), "1441234567890123456"); err != nil {
		t.Fatalf("first ClaimReport: %v", err)
	}

	err := store.ClaimReport(context.Background(), "1441234567890123456")

	if !errors.Is(err, voicesummary.ErrReportClaimed) {
		t.Errorf("err = %v, want %v", err, voicesummary.ErrReportClaimed)
	}
}

// 条件: 2 つの依頼がそれぞれマニフェストと音声を持ち、片方は文字起こしと claim も持つ。その片方を消す。
// 内容: 消した依頼のマニフェスト、音声、文字起こしだけが無くなる。消した依頼の claim と、もう一方の依頼のものは残る。
func TestStoreDiscard_TwoRequests_RemovesOnlyDiscardedRequestAndKeepsClaim(t *testing.T) {
	ctx := context.Background()
	store := &voicesummarytest.Store{}
	discarded := voicesummary.Manifest{RequestID: "1441234567890123456"}
	kept := voicesummary.Manifest{RequestID: "1449999999999999999"}
	for _, m := range []voicesummary.Manifest{discarded, kept} {
		if err := store.CreateManifest(ctx, m); err != nil {
			t.Fatalf("CreateManifest: %v", err)
		}
		if err := store.PutAudio(ctx, m.RequestID, 1, strings.NewReader("音声")); err != nil {
			t.Fatalf("PutAudio: %v", err)
		}
	}
	store.PutTranscript(discarded.RequestID, 1, []byte(`{"results":{"audio_segments":[]}}`))
	if err := store.ClaimReport(ctx, discarded.RequestID); err != nil {
		t.Fatalf("ClaimReport: %v", err)
	}

	err := store.Discard(ctx, discarded)

	if err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if got, want := store.Keys(), []string{"1441234567890123456/report.claim", "1449999999999999999/audio/1.flac", "1449999999999999999/manifest.json"}; !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
}
