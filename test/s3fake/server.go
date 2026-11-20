// Package s3fake は Amazon S3 の PutObject・GetObject・DeleteObject の HTTP fake を提供する。
// path-style（/{bucket}/{key}）だけに応え、PutObject の If-None-Match: * を条件付き書き込みとして扱う。
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
	headerETag        = "ETag"
	ifNoneMatchAny    = "*"
	fakeETag          = `"fake-etag"`
	xmlContentType    = "application/xml"

	codePreconditionFailed = "PreconditionFailed"
	codeNoSuchKey          = "NoSuchKey"
	errorBodyFormat        = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`
)

// Server は path-style の S3 のオブジェクト操作に応える http.Handler。
type Server struct {
	mu      sync.Mutex
	objects map[string]map[string][]byte // バケット → キー → 中身
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
		delete(s.objects[bucket], key)
		w.WriteHeader(http.StatusNoContent)
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
	s.putObject(bucket, key, body)
	w.Header().Set(headerETag, fakeETag)
	w.WriteHeader(http.StatusOK)
}

// serveGet は GetObject に応える。s.mu を保持して呼ぶ。
func (s *Server) serveGet(w http.ResponseWriter, bucket, key string) {
	body, ok := s.objects[bucket][key]
	if !ok {
		writeError(w, http.StatusNotFound, codeNoSuchKey)
		return
	}
	w.Header().Set(headerETag, fakeETag)
	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", xmlContentType)
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, errorBodyFormat, code, code)
}

// putObject は s.mu を保持して呼ぶ。
func (s *Server) putObject(bucket, key string, body []byte) {
	if s.objects[bucket] == nil {
		s.objects[bucket] = map[string][]byte{}
	}
	s.objects[bucket][key] = append([]byte(nil), body...)
}

// Object はオブジェクトの中身を返す。無ければ ok は false。
func (s *Server) Object(bucket, key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.objects[bucket][key]
	return append([]byte(nil), body...), ok
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

// Reset はすべてのオブジェクトを消す。
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects = map[string]map[string][]byte{}
}
