package voicesummary_test

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"discord-ai-assistant/internal/voicesummary"
	"discord-ai-assistant/test/s3fake"
)

const (
	awsTestBucket = "voice-bucket"
	awsTestRegion = "ap-northeast-1"
	awsRequestID  = "req-1"
)

// newS3Client は srv に向けた S3 クライアントを、本番と同じ ConfigureS3Client で作る。
func newS3Client(srv *httptest.Server) *s3.Client {
	cfg := aws.Config{
		Region:           awsTestRegion,
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		BaseEndpoint:     aws.String(srv.URL),
		RetryMaxAttempts: 1,
	}
	return s3.NewFromConfig(cfg, voicesummary.ConfigureS3Client)
}

// newS3Store は s3fake に向けた S3Store を作る。
func newS3Store(t *testing.T) (*voicesummary.S3Store, *s3fake.Server) {
	t.Helper()
	fake := s3fake.New()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return voicesummary.NewS3Store(newS3Client(srv), awsTestBucket), fake
}

// s3StoreManifest はトラック 1・3 の 2 人の依頼。トラック番号が連番でないことも扱う。
func s3StoreManifest() voicesummary.Manifest {
	return voicesummary.Manifest{
		RequestID: awsRequestID, ChannelID: "2200000000000000002", RequesterID: "3300000000000000003",
		StartedAt: time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC), Duration: 90 * time.Minute,
		Speakers: []voicesummary.Speaker{{Track: 1, Name: "佐藤"}, {Track: 3, Name: "tanaka"}},
	}
}

// 条件: 依頼を記録する。
// 内容: ReadManifest が同じ内容を返す。
func TestS3Store_CreateThenReadManifest_ReturnsSameManifest(t *testing.T) {
	store, _ := newS3Store(t)
	ctx := context.Background()
	if err := store.CreateManifest(ctx, s3StoreManifest()); err != nil {
		t.Fatalf("CreateManifest: %v", err)
	}

	got, err := store.ReadManifest(ctx, awsRequestID)

	if err != nil || !reflect.DeepEqual(got, s3StoreManifest()) {
		t.Errorf("ReadManifest = %+v, %v; want %+v, nil", got, err, s3StoreManifest())
	}
}

// 条件: 同じ依頼 ID の記録が既にある。
// 内容: ErrRequestExists を返し、先の記録を上書きしない。
func TestS3StoreCreateManifest_SameRequestID_ReturnsErrRequestExists(t *testing.T) {
	store, fake := newS3Store(t)
	ctx := context.Background()
	if err := store.CreateManifest(ctx, s3StoreManifest()); err != nil {
		t.Fatalf("CreateManifest: %v", err)
	}
	before, _ := fake.Object(awsTestBucket, "requests/req-1/manifest.json")
	second := s3StoreManifest()
	second.ChannelID = "9900000000000000009"

	err := store.CreateManifest(ctx, second)

	after, _ := fake.Object(awsTestBucket, "requests/req-1/manifest.json")
	if !errors.Is(err, voicesummary.ErrRequestExists) || !bytes.Equal(before, after) {
		t.Errorf("err = %v, manifest changed = %t; want %v, false", err, !bytes.Equal(before, after), voicesummary.ErrRequestExists)
	}
}

// 条件: 依頼の記録がない。
// 内容: ErrRequestNotFound を返す。
func TestS3StoreReadManifest_Missing_ReturnsErrRequestNotFound(t *testing.T) {
	store, _ := newS3Store(t)

	_, err := store.ReadManifest(context.Background(), awsRequestID)

	if !errors.Is(err, voicesummary.ErrRequestNotFound) {
		t.Errorf("err = %v, want %v", err, voicesummary.ErrRequestNotFound)
	}
}

// 条件: トラック 3 の音声を置く。
// 内容: requests/{依頼 ID}/audio/3.flac に中身を書く。
func TestS3StorePutAudio_Track_WritesAudioKey(t *testing.T) {
	store, fake := newS3Store(t)

	err := store.PutAudio(context.Background(), awsRequestID, 3, bytes.NewReader([]byte("flac-3")))

	got, ok := fake.Object(awsTestBucket, "requests/req-1/audio/3.flac")
	if err != nil || !ok || string(got) != "flac-3" {
		t.Errorf("PutAudio err = %v, object = %q (exists %t); want nil, %q", err, got, ok, "flac-3")
	}
}

// 条件: Transcribe がトラック 3 の結果を requests/{依頼 ID}/transcripts/3.json に書いてある。
// 内容: ReadTranscript がその中身を返す。
func TestS3StoreReadTranscript_Written_ReturnsBody(t *testing.T) {
	store, fake := newS3Store(t)
	fake.PutObject(awsTestBucket, "requests/req-1/transcripts/3.json", []byte(`{"results":{}}`))

	got, err := store.ReadTranscript(context.Background(), awsRequestID, 3)

	if err != nil || string(got) != `{"results":{}}` {
		t.Errorf("ReadTranscript = %q, %v; want %q, nil", got, err, `{"results":{}}`)
	}
}

// 条件: 同じ依頼で ClaimReport を 2 回呼ぶ。
// 内容: 1 回目は権利を取り、2 回目は ErrReportClaimed を返す。
func TestS3StoreClaimReport_Twice_SecondReturnsErrReportClaimed(t *testing.T) {
	store, _ := newS3Store(t)
	ctx := context.Background()
	first := store.ClaimReport(ctx, awsRequestID)

	second := store.ClaimReport(ctx, awsRequestID)

	if first != nil || !errors.Is(second, voicesummary.ErrReportClaimed) {
		t.Errorf("ClaimReport = %v, %v; want nil, %v", first, second, voicesummary.ErrReportClaimed)
	}
}

// 条件: 依頼の記録、全話者の音声と結果、report.claim、ほかの依頼の記録がある。
// 内容: その依頼の音声、結果、記録を消し、report.claim とほかの依頼は残す。
func TestS3StoreDiscard_Request_KeepsClaimAndOtherRequests(t *testing.T) {
	store, fake := newS3Store(t)
	for _, key := range []string{
		"requests/req-1/manifest.json", "requests/req-1/report.claim",
		"requests/req-1/audio/1.flac", "requests/req-1/audio/3.flac",
		"requests/req-1/transcripts/1.json", "requests/req-1/transcripts/3.json",
		"requests/req-2/manifest.json",
	} {
		fake.PutObject(awsTestBucket, key, []byte("x"))
	}

	err := store.Discard(context.Background(), s3StoreManifest())

	want := []string{"requests/req-1/report.claim", "requests/req-2/manifest.json"}
	if got := fake.Keys(awsTestBucket); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Discard err = %v, keys = %v; want nil, %v", err, got, want)
	}
}

// 条件: 文字起こしの結果がまだ無い（受け付けの途中で失敗した）依頼。
// 内容: 無いキーは飛ばして、音声と記録を消す。
func TestS3StoreDiscard_TranscriptsMissing_DeletesRest(t *testing.T) {
	store, fake := newS3Store(t)
	fake.PutObject(awsTestBucket, "requests/req-1/manifest.json", []byte("x"))
	fake.PutObject(awsTestBucket, "requests/req-1/audio/1.flac", []byte("x"))

	err := store.Discard(context.Background(), s3StoreManifest())

	if got := fake.Keys(awsTestBucket); err != nil || len(got) != 0 {
		t.Errorf("Discard err = %v, keys = %v; want nil, []", err, got)
	}
}

// s3Settings は ConfigureS3Client が決める設定。
type s3Settings struct {
	UsePathStyle               bool
	RequestChecksumCalculation aws.RequestChecksumCalculation
	ResponseChecksumValidation aws.ResponseChecksumValidation
}

// 条件: エンドポイントの差し替えがある・ない。
// 内容: チェックサムはどちらも必要時だけにし、差し替えがあるときだけ path-style にする。
func TestConfigureS3Client_Endpoint_SetsPathStyleOnlyWhenOverridden(t *testing.T) {
	tests := []struct {
		name         string
		baseEndpoint *string
		want         s3Settings
	}{
		{"差し替えなし", nil, s3Settings{false, aws.RequestChecksumCalculationWhenRequired, aws.ResponseChecksumValidationWhenRequired}},
		{"差し替えあり", aws.String("http://s3fake:8080"), s3Settings{true, aws.RequestChecksumCalculationWhenRequired, aws.ResponseChecksumValidationWhenRequired}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := s3.Options{BaseEndpoint: tt.baseEndpoint}

			voicesummary.ConfigureS3Client(&o)

			got := s3Settings{o.UsePathStyle, o.RequestChecksumCalculation, o.ResponseChecksumValidation}
			if got != tt.want {
				t.Errorf("settings = %+v, want %+v", got, tt.want)
			}
		})
	}
}
