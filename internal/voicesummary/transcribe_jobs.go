package voicesummary

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"
	"github.com/aws/aws-sdk-go-v2/service/transcribe/types"
)

// s3URIScheme は Transcribe に音声の場所を渡す URI の接頭辞。
const s3URIScheme = "s3://"

// TranscribeJobs は Amazon Transcribe で 1 トラックずつ文字起こしする。音声と結果は同じバケットに置く。
type TranscribeJobs struct {
	client *transcribe.Client
	bucket string
}

var (
	_ JobStarter    = (*TranscribeJobs)(nil)
	_ JobController = (*TranscribeJobs)(nil)
)

func NewTranscribeJobs(client *transcribe.Client, bucket string) *TranscribeJobs {
	return &TranscribeJobs{client: client, bucket: bucket}
}

// Start は、トラックの音声を日本語で文字起こしし、結果を transcriptKey に書くジョブを開始する。
// 同じジョブが既にあれば（開き直しを複数の実行が同時に試みたとき）何もせず nil を返す。
func (j *TranscribeJobs) Start(ctx context.Context, job JobRef) error {
	_, err := j.client.StartTranscriptionJob(ctx, &transcribe.StartTranscriptionJobInput{
		TranscriptionJobName: aws.String(jobName(job)),
		LanguageCode:         types.LanguageCodeJaJp,
		MediaFormat:          types.MediaFormatFlac,
		Media:                &types.Media{MediaFileUri: aws.String(s3URIScheme + j.bucket + "/" + audioKey(job.RequestID, job.Track))},
		OutputBucketName:     aws.String(j.bucket),
		OutputKey:            aws.String(transcriptKey(job.RequestID, job.Track)),
	})
	var alreadyStarted *types.ConflictException
	if errors.As(err, &alreadyStarted) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("start transcription job of track %d: %w", job.Track, err)
	}
	return nil
}

// Status はトラックのジョブの状態を返す。まだ開始されていないジョブは JobPending とする。
// Transcribe は開始されていないジョブを BadRequestException で返す。ジョブ名は jobName が常に正しい形で作るため、この操作の BadRequestException はジョブが無いことだけを表す。
// ジョブが存在しないとき（NotFoundException）も BadRequestException と同じく、エラーにせず JobPending を返す。
// 完了の通知は全トラックの終了を待たずに届くため、ほかのトラックが未開始のこともある。
func (j *TranscribeJobs) Status(ctx context.Context, job JobRef) (JobStatus, error) {
	out, err := j.client.GetTranscriptionJob(ctx, &transcribe.GetTranscriptionJobInput{
		TranscriptionJobName: aws.String(jobName(job)),
	})
	var notStarted *types.BadRequestException
	var notFound *types.NotFoundException
	if errors.As(err, &notStarted) || errors.As(err, &notFound) {
		return JobPending, nil
	}
	if err != nil {
		return JobPending, fmt.Errorf("get transcription job of track %d: %w", job.Track, err)
	}
	return jobStatusOf(out.TranscriptionJob.TranscriptionJobStatus), nil
}

func jobStatusOf(status types.TranscriptionJobStatus) JobStatus {
	switch status {
	case types.TranscriptionJobStatusCompleted:
		return JobCompleted
	case types.TranscriptionJobStatusFailed:
		return JobFailed
	}
	return JobPending
}
