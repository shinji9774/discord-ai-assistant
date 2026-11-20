package voicesummary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const (
	requestsPrefix = "requests/"
	manifestName   = "/manifest.json"
	audioDir       = "/audio/"
	transcriptsDir = "/transcripts/"
	claimName      = "/report.claim"
	flacExtension  = ".flac"
	jsonExtension  = ".json"

	// ifNoneMatchAny は、キーが無いときだけ書く条件付き書き込みの値。
	ifNoneMatchAny = "*"
)

// conditionalFailureCodes は、条件付き書き込みで先にキーがあったことを表す S3 のエラーコード。
// 412 は既にあるとき、409 は同じキーへの書き込みが同時に進んでいるとき。
var conditionalFailureCodes = map[string]bool{
	"PreconditionFailed":         true,
	"ConditionalRequestConflict": true,
}

// S3Store は依頼の記録・音声・文字起こしの結果を 1 つのバケットに置く。
type S3Store struct {
	client *s3.Client
	bucket string
}

var (
	_ AudioStore  = (*S3Store)(nil)
	_ ResultStore = (*S3Store)(nil)
)

func NewS3Store(client *s3.Client, bucket string) *S3Store {
	return &S3Store{client: client, bucket: bucket}
}

// ConfigureS3Client は s3.NewFromConfig の optFn。チェックサムを必要時だけにし、
// エンドポイントが差し替えられている（o.BaseEndpoint != nil）ときは path-style にする。
// 差し替え先（結合テストの fake）は仮想ホスト形式の名前を解決できず、既定のチェックサムの付け方にも追従しないため。
func ConfigureS3Client(o *s3.Options) {
	o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	o.UsePathStyle = o.BaseEndpoint != nil
}

func manifestKey(requestID string) string {
	return requestsPrefix + requestID + manifestName
}

func audioKey(requestID string, track int) string {
	return requestsPrefix + requestID + audioDir + strconv.Itoa(track) + flacExtension
}

func transcriptKey(requestID string, track int) string {
	return requestsPrefix + requestID + transcriptsDir + strconv.Itoa(track) + jsonExtension
}

func claimKey(requestID string) string {
	return requestsPrefix + requestID + claimName
}

// CreateManifest は依頼を記録する。同じ依頼 ID が既にあれば ErrRequestExists。
func (s *S3Store) CreateManifest(ctx context.Context, m Manifest) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	err = s.putIfAbsent(ctx, manifestKey(m.RequestID), body)
	if isConditionalFailure(err) {
		return ErrRequestExists
	}
	if err != nil {
		return fmt.Errorf("put manifest: %w", err)
	}
	return nil
}

func (s *S3Store) PutAudio(ctx context.Context, requestID string, track int, audio io.ReadSeeker) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(audioKey(requestID, track)),
		Body:   audio,
	})
	if err != nil {
		return fmt.Errorf("put audio of track %d: %w", track, err)
	}
	return nil
}

// ReadManifest は依頼の記録を返す。なければ ErrRequestNotFound。
func (s *S3Store) ReadManifest(ctx context.Context, requestID string) (Manifest, error) {
	body, err := s.getObject(ctx, manifestKey(requestID))
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return Manifest{}, ErrRequestNotFound
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("get manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	return m, nil
}

func (s *S3Store) ReadTranscript(ctx context.Context, requestID string, track int) ([]byte, error) {
	body, err := s.getObject(ctx, transcriptKey(requestID, track))
	if err != nil {
		return nil, fmt.Errorf("get transcript of track %d: %w", track, err)
	}
	return body, nil
}

// ClaimReport は、投稿する権利を取る。既に取られていれば ErrReportClaimed。
func (s *S3Store) ClaimReport(ctx context.Context, requestID string) error {
	err := s.putIfAbsent(ctx, claimKey(requestID), nil)
	if isConditionalFailure(err) {
		return ErrReportClaimed
	}
	if err != nil {
		return fmt.Errorf("put report claim: %w", err)
	}
	return nil
}

// ReleaseClaim は、report.claim だけを消す。
func (s *S3Store) ReleaseClaim(ctx context.Context, requestID string) error {
	return s.deleteObject(ctx, claimKey(requestID))
}

// Discard は、音声、文字起こしの結果、マニフェストの順に消す。マニフェストを最後にするのは、
// 途中で失敗しても再実行で残りを消せるようにするため。report.claim は内容を持たないので消さない（ライフサイクルで消える）。
func (s *S3Store) Discard(ctx context.Context, m Manifest) error {
	for _, key := range discardKeys(m) {
		if err := s.deleteObject(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func discardKeys(m Manifest) []string {
	keys := make([]string, 0, 2*len(m.Speakers)+1)
	for _, sp := range m.Speakers {
		keys = append(keys, audioKey(m.RequestID, sp.Track))
	}
	for _, sp := range m.Speakers {
		keys = append(keys, transcriptKey(m.RequestID, sp.Track))
	}
	return append(keys, manifestKey(m.RequestID))
}

func (s *S3Store) putIfAbsent(ctx context.Context, key string, body []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		IfNoneMatch: aws.String(ifNoneMatchAny),
	})
	return err
}

func (s *S3Store) getObject(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

func (s *S3Store) deleteObject(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

func isConditionalFailure(err error) bool {
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && conditionalFailureCodes[apiErr.ErrorCode()]
}
