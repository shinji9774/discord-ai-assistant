// Package ssmfake は SSM GetParameter の HTTP fake を提供する。
package ssmfake

import (
	"encoding/json"
	"net/http"
	"sync"
)

const (
	targetHeader       = "X-Amz-Target"
	getParameterTarget = "AmazonSSM.GetParameter"
	contentType        = "application/x-amz-json-1.1"
	secureString       = "SecureString"
	parameterVersion   = 1
	notFoundBody       = `{"__type":"ParameterNotFound","message":"parameter not found"}`
	rejectedBody       = `{"__type":"InternalServerError","message":"rejected by fake"}`
)

// Request は fake が受けた GetParameter 依頼。
type Request struct {
	Name           string
	WithDecryption bool
}

type getParameterInput struct {
	Name           string `json:"Name"`
	WithDecryption bool   `json:"WithDecryption"`
}

type parameter struct {
	Name    string `json:"Name"`
	Type    string `json:"Type"`
	Value   string `json:"Value"`
	Version int    `json:"Version"`
}

// Server は POST / （X-Amz-Target: AmazonSSM.GetParameter）に応える http.Handler。
type Server struct {
	mu         sync.Mutex
	failStatus int
	parameters map[string]string
	requests   []Request
}

func New() *Server {
	return &Server{parameters: map[string]string{}}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get(targetHeader) != getParameterTarget {
		http.NotFound(w, r)
		return
	}
	var in getParameterInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{Name: in.Name, WithDecryption: in.WithDecryption})
	w.Header().Set("Content-Type", contentType)
	if s.failStatus != 0 {
		w.WriteHeader(s.failStatus)
		_, _ = w.Write([]byte(rejectedBody))
		return
	}
	value, ok := s.parameters[in.Name]
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(notFoundBody))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]parameter{
		"Parameter": {Name: in.Name, Type: secureString, Value: value, Version: parameterVersion},
	})
}

// SetParameter は以後、この名前に SecureString の値を返すようにする。
func (s *Server) SetParameter(name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.parameters[name] = value
}

// FailWith は以後の応答を指定の status の失敗にする。
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
