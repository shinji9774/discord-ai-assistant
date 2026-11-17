// Package lambdafake は Lambda Invoke API の HTTP fake を提供する。
package lambdafake

import (
	"io"
	"net/http"
	"strings"
	"sync"
)

const (
	pathPrefix           = "/2015-03-31/functions/"
	pathSuffix           = "/invocations"
	invocationTypeHeader = "X-Amz-Invocation-Type"
)

// Invocation は fake が受けた Invoke 依頼。
type Invocation struct {
	FunctionName   string
	InvocationType string // X-Amz-Invocation-Type ヘッダ
	Payload        string
}

// Server は POST /2015-03-31/functions/{name}/invocations に応える http.Handler。
type Server struct {
	mu          sync.Mutex
	failStatus  int
	forward     func(payload []byte)
	invocations []Invocation
}

func New() *Server {
	return &Server{}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, ok := functionNameFromPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.invocations = append(s.invocations, Invocation{
		FunctionName:   name,
		InvocationType: r.Header.Get(invocationTypeHeader),
		Payload:        string(payload),
	})
	failStatus, forward := s.failStatus, s.forward
	s.mu.Unlock()

	if failStatus != 0 {
		http.Error(w, `{"message":"rejected by fake"}`, failStatus)
		return
	}
	if forward != nil {
		go forward(payload)
	}
	w.WriteHeader(http.StatusAccepted)
}

// OnInvoke は受けた依頼の本文を、別の goroutine で forward に渡すよう設定する。
func (s *Server) OnInvoke(forward func(payload []byte)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forward = forward
}

func (s *Server) Invocations() []Invocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Invocation(nil), s.invocations...)
}

// FailWith は以後の応答を指定の status の失敗にする。
func (s *Server) FailWith(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus = status
}

// Reset は記録と設定を初期化する。
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus, s.forward, s.invocations = 0, nil, nil
}

func functionNameFromPath(path string) (string, bool) {
	if !strings.HasPrefix(path, pathPrefix) || !strings.HasSuffix(path, pathSuffix) {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(path, pathPrefix), pathSuffix), true
}
