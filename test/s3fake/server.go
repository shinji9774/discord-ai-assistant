// Package s3fake は Amazon S3 の PutObject・GetObject・DeleteObject の HTTP fake を提供する。
// path-style（/{bucket}/{key}）だけに応え、PutObject の If-None-Match: * と、PutObject・DeleteObject の If-Match を条件付きの操作として扱う。
package s3fake

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

const (
	headerIfNoneMatch = "If-None-Match"
	headerIfMatch     = "If-Match"
	headerETag        = "ETag"
	ifNoneMatchAny    = "*"
	etagFormat        = `"etag-%d"`
	xmlContentType    = "application/xml"

	codePreconditionFailed = "PreconditionFailed"
	codeNoSuchKey          = "NoSuchKey"
	codeInternalError      = "InternalError"
	errorBodyFormat        = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`
)

type object struct {
	body []byte
	etag string
}

// Server は path-style の S3 のオブジェクト操作に応える http.Handler。
type Server struct {
	mu       sync.Mutex
	objects  map[string]map[string]object // バケット → キー → オブジェクト
	versions int
	// failedDeletes は、DeleteObject を 500 で失敗させるオブジェクト（バケット → キー）。
	failedDeletes map[string]map[string]bool
}

func New() *Server {
	s := &Server{}
	s.Reset()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bucket, key, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if !ok || bucket == "" || key == "" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		s.servePut(w, r, bucket, key)
	case http.MethodGet:
		s.serveGet(w, bucket, key)
	case http.MethodDelete:
		s.serveDelete(w, r, bucket, key)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// servePut は PutObject に応える。s.mu を保持して呼ぶ。
func (s *Server) servePut(w http.ResponseWriter, r *http.Request, bucket, key string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, exists := s.objects[bucket][key]; exists && r.Header.Get(headerIfNoneMatch) == ifNoneMatchAny {
		writeError(w, http.StatusPreconditionFailed, codePreconditionFailed)
		return
	}
	if !s.matchesCondition(w, r, bucket, key) {
		return
	}
	w.Header().Set(headerETag, s.putObject(bucket, key, body))
	w.WriteHeader(http.StatusOK)
}

// serveGet は GetObject に応える。s.mu を保持して呼ぶ。
func (s *Server) serveGet(w http.ResponseWriter, bucket, key string) {
	obj, ok := s.objects[bucket][key]
	if !ok {
		writeError(w, http.StatusNotFound, codeNoSuchKey)
		return
	}
	w.Header().Set(headerETag, obj.etag)
	_, _ = w.Write(obj.body)
}

// serveDelete は DeleteObject に応える。s.mu を保持して呼ぶ。
func (s *Server) serveDelete(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if s.failedDeletes[bucket][key] {
		writeError(w, http.StatusInternalServerError, codeInternalError)
		return
	}
	if !s.matchesCondition(w, r, bucket, key) {
		return
	}
	delete(s.objects[bucket], key)
	w.WriteHeader(http.StatusNoContent)
}

// matchesCondition は If-Match を確かめ、満たさなければエラーを書いて false を返す。s.mu を保持して呼ぶ。
func (s *Server) matchesCondition(w http.ResponseWriter, r *http.Request, bucket, key string) bool {
	want := r.Header.Get(headerIfMatch)
	if want == "" {
		return true
	}
	obj, ok := s.objects[bucket][key]
	if !ok {
		writeError(w, http.StatusNotFound, codeNoSuchKey)
		return false
	}
	if obj.etag != want {
		writeError(w, http.StatusPreconditionFailed, codePreconditionFailed)
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", xmlContentType)
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, errorBodyFormat, code, code)
}

// putObject は新しい ETag でオブジェクトを置き、その ETag を返す。s.mu を保持して呼ぶ。
func (s *Server) putObject(bucket, key string, body []byte) string {
	if s.objects[bucket] == nil {
		s.objects[bucket] = map[string]object{}
	}
	s.versions++
	etag := fmt.Sprintf(etagFormat, s.versions)
	s.objects[bucket][key] = object{body: append([]byte(nil), body...), etag: etag}
	return etag
}

// Object はオブジェクトの中身を返す。無ければ ok は false。
func (s *Server) Object(bucket, key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[bucket][key]
	return append([]byte(nil), obj.body...), ok
}

// Keys はバケットのキーを昇順で返す。
func (s *Server) Keys(bucket string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.objects[bucket]))
	for key := range s.objects[bucket] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// PutObject はオブジェクトを置く。ほかの fake（transcribefake）が結果を書くときと、テストの準備に使う。
func (s *Server) PutObject(bucket, key string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putObject(bucket, key, body)
}

// FailDelete は、以後そのオブジェクトの DeleteObject を 500 で失敗させる。Reset まで続く。
func (s *Server) FailDelete(bucket, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failedDeletes[bucket] == nil {
		s.failedDeletes[bucket] = map[string]bool{}
	}
	s.failedDeletes[bucket][key] = true
}

// Reset はすべてのオブジェクトと FailDelete の指定を消し、ETag の番号を 1 から振り直す。
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects = map[string]map[string]object{}
	s.versions = 0
	s.failedDeletes = map[string]map[string]bool{}
}
