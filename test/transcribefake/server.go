// Package transcribefake は Amazon Transcribe の StartTranscriptionJob・GetTranscriptionJob の HTTP fake を提供する。
// aws-sdk-go-v2 の transcribe クライアント（v1.67.0）は Smithy RPCv2 CBOR で話すため、本文は CBOR で読み書きする。
// smithy-go の cbor パッケージは SDK 実行基盤向けで API が安定しないが、fake に限った利用なので許容する。
package transcribefake

import (
	"io"
	"net/http"
	"sync"

	"github.com/aws/smithy-go/encoding/cbor"
)

const (
	startJobPath = "/service/Transcribe/operation/StartTranscriptionJob"
	getJobPath   = "/service/Transcribe/operation/GetTranscriptionJob"
	contentType  = "application/cbor"

	fieldJobName          = "TranscriptionJobName"
	fieldJobStatus        = "TranscriptionJobStatus"
	fieldTranscriptionJob = "TranscriptionJob"
	fieldLanguageCode     = "LanguageCode"
	fieldMediaFormat      = "MediaFormat"
	fieldMedia            = "Media"
	fieldMediaFileURI     = "MediaFileUri"
	fieldOutputBucket     = "OutputBucketName"
	fieldOutputKey        = "OutputKey"
	fieldErrorType        = "__type"
	fieldErrorMessage     = "message"

	jobNotFoundErrorType = "BadRequestException"
	jobNotFoundMessage   = "The requested job couldn't be found. Check the job name and try your request again."

	conflictErrorType = "ConflictException"
	conflictMessage   = "The requested job name already exists. Use a different job name."

	internalFailureErrorType = "InternalFailureException"
	internalFailureMessage   = "Internal failure injected by the fake."

	statusInProgress = "IN_PROGRESS"
	statusCompleted  = "COMPLETED"
	statusFailed     = "FAILED"
)

// ObjectWriter は、完了したジョブの結果を書く先（s3fake.Server）。
type ObjectWriter interface {
	PutObject(bucket, key string, body []byte)
}

// Job は fake が受けた文字起こしのジョブ。
type Job struct {
	Name         string
	LanguageCode string
	MediaFormat  string
	MediaURI     string
	OutputBucket string
	OutputKey    string
	Status       string // IN_PROGRESS|COMPLETED|FAILED
}

// Server は POST /service/Transcribe/operation/{StartTranscriptionJob,GetTranscriptionJob} に応える http.Handler。
type Server struct {
	mu      sync.Mutex
	results ObjectWriter
	jobs    []Job
	// failedStarts は、StartTranscriptionJob を 500 で失敗させるジョブ名。
	failedStarts map[string]bool
}

func New(results ObjectWriter) *Server {
	return &Server{results: results}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || (r.URL.Path != startJobPath && r.URL.Path != getJobPath) {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fields, err := decodeFields(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", contentType)
	if r.URL.Path == startJobPath {
		s.serveStart(w, fields)
		return
	}
	s.serveGet(w, fields)
}

// serveStart は StartTranscriptionJob に応える。同じ名前のジョブが既にあれば、実際の Transcribe と同じく ConflictException。s.mu を保持して呼ぶ。
func (s *Server) serveStart(w http.ResponseWriter, fields cbor.Map) {
	media, _ := fields[fieldMedia].(cbor.Map)
	job := Job{
		Name:         stringField(fields, fieldJobName),
		LanguageCode: stringField(fields, fieldLanguageCode),
		MediaFormat:  stringField(fields, fieldMediaFormat),
		MediaURI:     stringField(media, fieldMediaFileURI),
		OutputBucket: stringField(fields, fieldOutputBucket),
		OutputKey:    stringField(fields, fieldOutputKey),
		Status:       statusInProgress,
	}
	if s.failedStarts[job.Name] {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(cbor.Encode(cbor.Map{
			fieldErrorType:    cbor.String(internalFailureErrorType),
			fieldErrorMessage: cbor.String(internalFailureMessage),
		}))
		return
	}
	if s.find(job.Name) != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(cbor.Encode(cbor.Map{
			fieldErrorType:    cbor.String(conflictErrorType),
			fieldErrorMessage: cbor.String(conflictMessage),
		}))
		return
	}
	s.jobs = append(s.jobs, job)
	_, _ = w.Write(encodeJob(job))
}

// serveGet は GetTranscriptionJob に応える。開始されていないジョブは、実際の Transcribe と同じく BadRequestException。s.mu を保持して呼ぶ。
func (s *Server) serveGet(w http.ResponseWriter, fields cbor.Map) {
	job := s.find(stringField(fields, fieldJobName))
	if job == nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(cbor.Encode(cbor.Map{
			fieldErrorType:    cbor.String(jobNotFoundErrorType),
			fieldErrorMessage: cbor.String(jobNotFoundMessage),
		}))
		return
	}
	_, _ = w.Write(encodeJob(*job))
}

func encodeJob(job Job) []byte {
	return cbor.Encode(cbor.Map{fieldTranscriptionJob: cbor.Map{
		fieldJobName:   cbor.String(job.Name),
		fieldJobStatus: cbor.String(job.Status),
	}})
}

// find は名前の一致するジョブを返す。無ければ nil。s.mu を保持して呼ぶ。
func (s *Server) find(name string) *Job {
	for i := range s.jobs {
		if s.jobs[i].Name == name {
			return &s.jobs[i]
		}
	}
	return nil
}

func decodeFields(body []byte) (cbor.Map, error) {
	value, err := cbor.Decode(body)
	if err != nil {
		return nil, err
	}
	fields, _ := value.(cbor.Map)
	return fields, nil
}

func stringField(fields cbor.Map, name string) string {
	text, _ := fields[name].(cbor.String)
	return string(text)
}

// Jobs は受けたジョブを開始順に返す。
func (s *Server) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Job(nil), s.jobs...)
}

// Complete は結果を OutputBucket/OutputKey に書き、ジョブを COMPLETED にする。開始されていないジョブは無視する。
func (s *Server) Complete(jobName string, transcriptJSON []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.find(jobName)
	if job == nil {
		return
	}
	s.results.PutObject(job.OutputBucket, job.OutputKey, transcriptJSON)
	job.Status = statusCompleted
}

// Fail はジョブを FAILED にする。開始されていないジョブは無視する。
func (s *Server) Fail(jobName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job := s.find(jobName); job != nil {
		job.Status = statusFailed
	}
}

// FailStart は、以後その名前のジョブの開始を 500 で失敗させる。Reset まで続く。
func (s *Server) FailStart(jobName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failedStarts == nil {
		s.failedStarts = map[string]bool{}
	}
	s.failedStarts[jobName] = true
}

// Reset は受けたジョブと FailStart の指定を消す。
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = nil
	s.failedStarts = nil
}
