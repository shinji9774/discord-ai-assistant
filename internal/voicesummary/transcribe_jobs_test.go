package voicesummary_test

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"

	"discord-ai-assistant/internal/voicesummary"
	"discord-ai-assistant/test/s3fake"
	"discord-ai-assistant/test/transcribefake"
)

// awsTrack3JobName は、トラック 3 の初回のジョブ名（"summarize-voice-{依頼 ID}-{トラック番号}-{試行番号}"）。
const awsTrack3JobName = voicesummary.JobNamePrefix + awsRequestID + "-3-1"

// awsTrack3Job は、トラック 3 の初回のジョブ。
var awsTrack3Job = voicesummary.JobRef{RequestID: awsRequestID, Track: 3, Attempt: voicesummary.FirstAttempt}

// newTranscribeJobs は transcribefake に向けた TranscribeJobs を作る。結果は s3fake に書かれる。
func newTranscribeJobs(t *testing.T) (*voicesummary.TranscribeJobs, *transcribefake.Server, *s3fake.Server) {
	t.Helper()
	results := s3fake.New()
	fake := transcribefake.New(results)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	client := transcribe.New(transcribe.Options{
		Region:           awsTestRegion,
		BaseEndpoint:     aws.String(srv.URL),
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		RetryMaxAttempts: 1,
	})
	return voicesummary.NewTranscribeJobs(client, awsTestBucket), fake, results
}

// 条件: トラック 3 の文字起こしを開始する。
// 内容: 規則どおりの名前、ja-JP、flac、音声のキー、結果のキーでジョブを 1 つ開始する。
func TestTranscribeJobsStart_Track_StartsJobWithKeys(t *testing.T) {
	jobs, fake, _ := newTranscribeJobs(t)

	err := jobs.Start(context.Background(), awsTrack3Job)

	want := []transcribefake.Job{{
		Name: awsTrack3JobName, LanguageCode: "ja-JP", MediaFormat: "flac",
		MediaURI:     "s3://voice-bucket/requests/req-1/audio/3.flac",
		OutputBucket: awsTestBucket, OutputKey: "requests/req-1/transcripts/3.json",
		Status: "IN_PROGRESS",
	}}
	if got := fake.Jobs(); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Start err = %v, jobs =\n%+v\nwant\n%+v", err, got, want)
	}
}

// 条件: ジョブが未開始・処理中・完了・失敗のいずれかである。
// 内容: 未開始と処理中は JobPending、完了は JobCompleted、失敗は JobFailed を返す。
func TestTranscribeJobsStatus_JobState_ReturnsJobStatus(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(jobs *voicesummary.TranscribeJobs, fake *transcribefake.Server) error
		want    voicesummary.JobStatus
	}{
		{"未開始", func(*voicesummary.TranscribeJobs, *transcribefake.Server) error { return nil }, voicesummary.JobPending},
		{"処理中", func(jobs *voicesummary.TranscribeJobs, _ *transcribefake.Server) error {
			return jobs.Start(context.Background(), awsTrack3Job)
		}, voicesummary.JobPending},
		{"完了", func(jobs *voicesummary.TranscribeJobs, fake *transcribefake.Server) error {
			err := jobs.Start(context.Background(), awsTrack3Job)
			fake.Complete(awsTrack3JobName, []byte(`{}`))
			return err
		}, voicesummary.JobCompleted},
		{"失敗", func(jobs *voicesummary.TranscribeJobs, fake *transcribefake.Server) error {
			err := jobs.Start(context.Background(), awsTrack3Job)
			fake.Fail(awsTrack3JobName)
			return err
		}, voicesummary.JobFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs, fake, _ := newTranscribeJobs(t)
			if err := tt.arrange(jobs, fake); err != nil {
				t.Fatalf("arrange: %v", err)
			}

			got, err := jobs.Status(context.Background(), awsTrack3Job)

			if err != nil || got != tt.want {
				t.Errorf("Status = %v, %v; want %v, nil", got, err, tt.want)
			}
		})
	}
}

// 条件: 開始したジョブを fake が完了させる。
// 内容: 結果の JSON が、ジョブに渡した結果のキーに書かれる。
func TestTranscribeJobsStart_ThenComplete_WritesTranscriptKey(t *testing.T) {
	jobs, fake, results := newTranscribeJobs(t)
	if err := jobs.Start(context.Background(), awsTrack3Job); err != nil {
		t.Fatalf("Start: %v", err)
	}

	fake.Complete(awsTrack3JobName, []byte(`{"results":{}}`))

	got, ok := results.Object(awsTestBucket, "requests/req-1/transcripts/3.json")
	if !ok || string(got) != `{"results":{}}` {
		t.Errorf("transcript = %q (exists %t), want %q", got, ok, `{"results":{}}`)
	}
}

// 条件: 同じジョブを 2 回開始する（開き直しを複数の実行が同時に試みる）。
// 内容: 2 回目も nil を返す。
func TestTranscribeJobsStart_SameJobTwice_ReturnsNil(t *testing.T) {
	jobs, _, _ := newTranscribeJobs(t)
	job := voicesummary.JobRef{RequestID: awsRequestID, Track: 1, Attempt: voicesummary.RetryAttempt}
	if err := jobs.Start(context.Background(), job); err != nil {
		t.Fatalf("first Start() = %v", err)
	}

	err := jobs.Start(context.Background(), job)

	if err != nil {
		t.Errorf("Start() = %v; want nil", err)
	}
}
