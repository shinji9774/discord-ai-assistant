package transcribefake_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/aws/smithy-go/encoding/cbor"

	"discord-ai-assistant/test/s3fake"
	"discord-ai-assistant/test/transcribefake"
)

const (
	startJobPath   = "/service/Transcribe/operation/StartTranscriptionJob"
	conflictedType = "ConflictException"
	// internalFailureType は、FailStart が返すエラーの種類。
	internalFailureType = "InternalFailureException"
	sampleJobName       = "summarize-voice-req-1-1-1"
	otherJobName        = "summarize-voice-req-1-2-1"
	errorTypeField      = "__type"
	jobNameField        = "TranscriptionJobName"
	cborContentType     = "application/cbor"
)

// startResponse は StartTranscriptionJob への応答のうち、状態コードとエラーの種類。
type startResponse struct {
	Status    int
	ErrorType string
}

// postStart は、ジョブ名だけを持つ StartTranscriptionJob を fake に送る。
func postStart(t *testing.T, fake *transcribefake.Server, name string) startResponse {
	t.Helper()
	body := cbor.Encode(cbor.Map{jobNameField: cbor.String(name)})
	req := httptest.NewRequest(http.MethodPost, startJobPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", cborContentType)
	rec := httptest.NewRecorder()
	fake.ServeHTTP(rec, req)
	value, err := cbor.Decode(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	fields, _ := value.(cbor.Map)
	errorType, _ := fields[errorTypeField].(cbor.String)
	return startResponse{Status: rec.Code, ErrorType: string(errorType)}
}

// 条件: 同じ名前のジョブを 2 回開始する。
// 内容: 1 回目は成功し、2 回目は 400 の ConflictException を返す。ジョブは 1 つだけ残る。
func TestServerStart_SameNameTwice_ReturnsConflictException(t *testing.T) {
	fake := transcribefake.New(s3fake.New())
	first := postStart(t, fake, sampleJobName)

	second := postStart(t, fake, sampleJobName)

	got := []any{first, second, len(fake.Jobs())}
	want := []any{startResponse{Status: http.StatusOK}, startResponse{Status: http.StatusBadRequest, ErrorType: conflictedType}, 1}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("responses and job count = %+v; want %+v", got, want)
	}
}

// 条件: FailStart したジョブ名と、していないジョブ名を開始する。
// 内容: 指定した名前は 500 で失敗してジョブにならず、もう一方は 200 で開始される。
func TestServerStart_FailStart_FailsOnlyTheSpecifiedJob(t *testing.T) {
	fake := transcribefake.New(s3fake.New())
	fake.FailStart(sampleJobName)

	failed := postStart(t, fake, sampleJobName)
	started := postStart(t, fake, otherJobName)

	type outcome struct {
		Responses []startResponse
		Jobs      int
	}
	got := outcome{Responses: []startResponse{failed, started}, Jobs: len(fake.Jobs())}
	want := outcome{
		Responses: []startResponse{{Status: http.StatusInternalServerError, ErrorType: internalFailureType}, {Status: http.StatusOK}},
		Jobs:      1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
