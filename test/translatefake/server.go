// Package translatefake は Amazon Translate TranslateText・TranslateDocument の HTTP fake を提供する。
// aws-sdk-go-v2 の translate クライアントは Smithy RPCv2 CBOR で話すため、本文は CBOR で読み書きする。
// smithy-go の cbor パッケージは SDK 実行基盤向けで API が安定しないが、fake に限った利用なので許容する。
package translatefake

import (
	"io"
	"net/http"
	"sync"

	"github.com/aws/smithy-go/encoding/cbor"
)

const (
	translateTextPath     = "/service/AWSShineFrontendService_20170701/operation/TranslateText"
	translateDocumentPath = "/service/AWSShineFrontendService_20170701/operation/TranslateDocument"
	contentType           = "application/cbor"
	defaultText           = "fake translation"
	defaultDocument       = "fake document translation"

	fieldText           = "Text"
	fieldTranslatedText = "TranslatedText"
	fieldDocument       = "Document"
	fieldContent        = "Content"
	fieldContentType    = "ContentType"
	fieldTranslatedDoc  = "TranslatedDocument"
	fieldSourceCode     = "SourceLanguageCode"
	fieldTargetCode     = "TargetLanguageCode"
	fieldErrorType      = "__type"
	fieldErrorMessage   = "message"
	rejectedErrorType   = "InternalServerException"
	rejectedMessage     = "rejected by fake"
)

// Request は fake が受けた TranslateText 依頼。
type Request struct {
	Text               string
	SourceLanguageCode string
	TargetLanguageCode string
}

// DocumentRequest は fake が受けた TranslateDocument 依頼。
type DocumentRequest struct {
	Content            string
	ContentType        string
	SourceLanguageCode string
	TargetLanguageCode string
}

// Server は POST /service/AWSShineFrontendService_20170701/operation/{TranslateText,TranslateDocument} に応える http.Handler。
type Server struct {
	mu               sync.Mutex
	text             string
	document         string
	failStatus       int
	requests         []Request
	documentRequests []DocumentRequest
}

func New() *Server {
	s := &Server{}
	s.Reset()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || (r.URL.Path != translateTextPath && r.URL.Path != translateDocumentPath) {
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
	if r.URL.Path == translateDocumentPath {
		s.serveDocument(w, fields)
		return
	}
	s.serveText(w, fields)
}

// serveText は TranslateText に応える。s.mu を保持して呼ぶ。
func (s *Server) serveText(w http.ResponseWriter, fields cbor.Map) {
	req := Request{
		Text:               stringField(fields, fieldText),
		SourceLanguageCode: stringField(fields, fieldSourceCode),
		TargetLanguageCode: stringField(fields, fieldTargetCode),
	}
	s.requests = append(s.requests, req)
	if s.rejectIfFailing(w) {
		return
	}
	_, _ = w.Write(cbor.Encode(cbor.Map{
		fieldTranslatedText: cbor.String(s.text),
		fieldSourceCode:     cbor.String(req.SourceLanguageCode),
		fieldTargetCode:     cbor.String(req.TargetLanguageCode),
	}))
}

// serveDocument は TranslateDocument に応える。s.mu を保持して呼ぶ。
func (s *Server) serveDocument(w http.ResponseWriter, fields cbor.Map) {
	document, _ := fields[fieldDocument].(cbor.Map)
	content, _ := document[fieldContent].(cbor.Slice)
	req := DocumentRequest{
		Content:            string(content),
		ContentType:        stringField(document, fieldContentType),
		SourceLanguageCode: stringField(fields, fieldSourceCode),
		TargetLanguageCode: stringField(fields, fieldTargetCode),
	}
	s.documentRequests = append(s.documentRequests, req)
	if s.rejectIfFailing(w) {
		return
	}
	_, _ = w.Write(cbor.Encode(cbor.Map{
		fieldTranslatedDoc: cbor.Map{fieldContent: cbor.Slice(s.document)},
		fieldSourceCode:    cbor.String(req.SourceLanguageCode),
		fieldTargetCode:    cbor.String(req.TargetLanguageCode),
	}))
}

// rejectIfFailing は FailWith 中なら失敗を書き込んで真を返す。成功時もヘッダを設定する。s.mu を保持して呼ぶ。
func (s *Server) rejectIfFailing(w http.ResponseWriter) bool {
	w.Header().Set("Content-Type", contentType)
	if s.failStatus == 0 {
		return false
	}
	w.WriteHeader(s.failStatus)
	_, _ = w.Write(cbor.Encode(cbor.Map{
		fieldErrorType:    cbor.String(rejectedErrorType),
		fieldErrorMessage: cbor.String(rejectedMessage),
	}))
	return true
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

// RespondWith は以後の成功応答の訳文を設定する。
func (s *Server) RespondWith(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.text, s.failStatus = text, 0
}

// RespondWithDocument は以後の TranslateDocument の成功応答の中身を設定する。
func (s *Server) RespondWithDocument(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.document, s.failStatus = content, 0
}

// FailWith は以後の応答（TranslateText・TranslateDocument 共通）を指定の status の失敗にする。
func (s *Server) FailWith(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus = status
}

func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func (s *Server) DocumentRequests() []DocumentRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]DocumentRequest(nil), s.documentRequests...)
}

// Reset は記録と設定を初期化する。
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.text, s.document, s.failStatus, s.requests, s.documentRequests = defaultText, defaultDocument, 0, nil, nil
}
