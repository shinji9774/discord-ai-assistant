// Package websearchfake は、AgentCore Web Search の代わりに使う HTTP サーバを提供する。
package websearchfake

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

const (
	invokePattern     = "POST /web-search/{webSearchId}/invoke"
	pathWebSearchID   = "webSearchId"
	errorTypeHeader   = "X-Amzn-ErrorType"
	validationError   = "ValidationException"
	rejectedBody      = `{"message":"rejected by fake"}`
	contentTypeHeader = "Content-Type"
	contentTypeJSON   = "application/json"
)

// Result は、fake が返す検索結果の 1 件。
type Result struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
	Text  string `json:"text"`
}

// Request は、fake が受けた検索の依頼。
type Request struct {
	WebSearchID string
	Query       string
	MaxResults  int
}

type invokeRequest struct {
	SearchQuery string `json:"searchQuery"`
	MaxResults  int    `json:"maxResults"`
}

type invokeResponse struct {
	Results []Result `json:"results"`
}

// Server は、検索の依頼を記録し、決めた結果か失敗を返す。
type Server struct {
	mux *http.ServeMux

	mu         sync.Mutex
	results    []Result
	failStatus int
	requests   []Request
}

func New() *Server {
	s := &Server{mux: http.NewServeMux()}
	s.mux.HandleFunc(invokePattern, s.serveInvoke)
	s.Reset()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) serveInvoke(w http.ResponseWriter, r *http.Request) {
	var req invokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{WebSearchID: r.PathValue(pathWebSearchID), Query: req.SearchQuery, MaxResults: req.MaxResults})
	w.Header().Set(contentTypeHeader, contentTypeJSON)
	if s.failStatus != 0 {
		w.Header().Set(errorTypeHeader, validationError)
		w.WriteHeader(s.failStatus)
		_, _ = io.WriteString(w, rejectedBody)
		return
	}
	_ = json.NewEncoder(w).Encode(invokeResponse{Results: s.results})
}

// RespondWith は、以降の検索に返す結果を決める。
func (s *Server) RespondWith(results ...Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results, s.failStatus = results, 0
}

// FailWith は、以降の検索を status で失敗させる。5xx は SDK が再試行するので、テストでは 400 を使う。
func (s *Server) FailWith(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus = status
}

// Requests は、受けた検索の依頼を、受けた順に返す。
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Reset は、結果なし・失敗なし・依頼の記録なしの状態に戻す。
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results, s.failStatus, s.requests = []Result{}, 0, nil
}
