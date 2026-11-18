// Package bedrockfake は Bedrock Converse API と InvokeModel の HTTP fake を提供する。
package bedrockfake

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const (
	pathPrefix       = "/model/"
	converseSuffix   = "/converse"
	invokeSuffix     = "/invoke"
	errorTypeHeader  = "X-Amzn-Errortype"
	validationError  = "ValidationException"
	rejectedBody     = `{"message":"rejected by fake"}`
	defaultText      = "fake response"
	defaultStopCause = "end_turn"

	authorizationHeader = "Authorization"
	credentialKey       = "Credential="
	// credential scope は AKID/日付/リージョン/サービス/aws4_request の形
	credentialScopeRegionIndex = 2
	defaultImage               = "fake image"
	defaultImageSeed           = 1
)

// Request は fake が受けた Converse 依頼のうち検証に使う部分。
type Request struct {
	ModelID   string
	System    string
	UserText  string
	MaxTokens int
}

// InvokeRequest は fake が受けた InvokeModel 依頼。Region は署名の credential scope から読む。
type InvokeRequest struct {
	ModelID string
	Region  string
	Body    string
}

type invokeResponse struct {
	Images        []string  `json:"images"`
	Seeds         []int     `json:"seeds"`
	FinishReasons []*string `json:"finish_reasons"`
}

type converseRequest struct {
	System   []textBlock `json:"system"`
	Messages []struct {
		Content []textBlock `json:"content"`
	} `json:"messages"`
	InferenceConfig struct {
		MaxTokens int `json:"maxTokens"`
	} `json:"inferenceConfig"`
}

type textBlock struct {
	Text string `json:"text"`
}

type converseResponse struct {
	Output     responseOutput `json:"output"`
	StopReason string         `json:"stopReason"`
	Usage      map[string]int `json:"usage"`
	Metrics    map[string]int `json:"metrics"`
}

type responseOutput struct {
	Message responseMessage `json:"message"`
}

type responseMessage struct {
	Role    string      `json:"role"`
	Content []textBlock `json:"content"`
}

// Server は POST /model/{modelId}/converse と /invoke に応える http.Handler。
type Server struct {
	mu             sync.Mutex
	text           string
	stopReason     string
	failStatus     int
	requests       []Request
	image          []byte
	finishReason   *string
	invokeRequests []InvokeRequest
}

func New() *Server {
	s := &Server{}
	s.Reset()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if modelID, ok := modelIDFromPath(r.URL.EscapedPath(), invokeSuffix); ok {
		s.serveInvoke(w, r, modelID)
		return
	}
	modelID, ok := modelIDFromPath(r.URL.EscapedPath(), converseSuffix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req converseRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, toRequest(modelID, req))
	if s.failStatus != 0 {
		w.Header().Set(errorTypeHeader, validationError)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.failStatus)
		_, _ = io.WriteString(w, rejectedBody)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.successResponse())
}

func (s *Server) serveInvoke(w http.ResponseWriter, r *http.Request, modelID string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.invokeRequests = append(s.invokeRequests, InvokeRequest{
		ModelID: modelID,
		Region:  regionFromAuthorization(r.Header.Get(authorizationHeader)),
		Body:    string(body),
	})
	if s.failStatus != 0 {
		w.Header().Set(errorTypeHeader, validationError)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.failStatus)
		_, _ = io.WriteString(w, rejectedBody)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.invokeSuccessResponse())
}

// RespondWith は以後の成功応答の本文と stopReason を設定する。
func (s *Server) RespondWith(text, stopReason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.text, s.stopReason, s.failStatus = text, stopReason, 0
}

// RespondWithImage は以後の InvokeModel の成功応答を、指定の画像と null の finish_reasons にする。
func (s *Server) RespondWithImage(image []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.image, s.finishReason, s.failStatus = image, nil, 0
}

// RespondWithFinishReason は以後の InvokeModel の成功応答を、画像なしで指定の終了理由にする。
func (s *Server) RespondWithFinishReason(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.image, s.finishReason, s.failStatus = nil, &reason, 0
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

func (s *Server) InvokeRequests() []InvokeRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]InvokeRequest(nil), s.invokeRequests...)
}

// Reset は記録と設定を初期化する。
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.text, s.stopReason, s.failStatus, s.requests = defaultText, defaultStopCause, 0, nil
	s.image, s.finishReason, s.invokeRequests = []byte(defaultImage), nil, nil
}

func (s *Server) successResponse() converseResponse {
	return converseResponse{
		Output: responseOutput{Message: responseMessage{
			Role:    "assistant",
			Content: []textBlock{{Text: s.text}},
		}},
		StopReason: s.stopReason,
		Usage:      map[string]int{"inputTokens": 1, "outputTokens": 1, "totalTokens": 2},
		Metrics:    map[string]int{"latencyMs": 1},
	}
}

func (s *Server) invokeSuccessResponse() invokeResponse {
	resp := invokeResponse{Images: []string{}, Seeds: []int{defaultImageSeed}, FinishReasons: []*string{s.finishReason}}
	if s.image != nil {
		resp.Images = []string{base64.StdEncoding.EncodeToString(s.image)}
	}
	return resp
}

func modelIDFromPath(escapedPath, suffix string) (string, bool) {
	if !strings.HasPrefix(escapedPath, pathPrefix) || !strings.HasSuffix(escapedPath, suffix) {
		return "", false
	}
	escaped := strings.TrimSuffix(strings.TrimPrefix(escapedPath, pathPrefix), suffix)
	modelID, err := url.PathUnescape(escaped)
	if err != nil {
		return "", false
	}
	return modelID, true
}

func toRequest(modelID string, req converseRequest) Request {
	got := Request{ModelID: modelID, MaxTokens: req.InferenceConfig.MaxTokens}
	if len(req.System) > 0 {
		got.System = req.System[0].Text
	}
	if len(req.Messages) > 0 && len(req.Messages[0].Content) > 0 {
		got.UserText = req.Messages[0].Content[0].Text
	}
	return got
}

// regionFromAuthorization は SigV4 の Authorization ヘッダの credential scope からリージョンを読む。
func regionFromAuthorization(authorization string) string {
	_, afterKey, found := strings.Cut(authorization, credentialKey)
	if !found {
		return ""
	}
	credential, _, _ := strings.Cut(afterKey, ",")
	scope := strings.Split(credential, "/")
	if len(scope) <= credentialScopeRegionIndex {
		return ""
	}
	return scope[credentialScopeRegionIndex]
}
