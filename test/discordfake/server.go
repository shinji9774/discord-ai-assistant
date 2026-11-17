// Package discordfake は Discord REST の HTTP fake を提供する。
package discordfake

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	payloadJSONPart  = "payload_json"
	multipartMedia   = "multipart/form-data"
	originalMessage  = "@original"
	messagesSegment  = "messages"
	commandsSegment  = "commands"
	fakeSnowflake    = "1"
	fakeVersion      = "1"
	rejectedBody     = `{"message":"rejected by fake"}`
	authorizationKey = "Authorization"
)

// Response は応答の更新（PATCH .../webhooks/{app}/{token}/messages/@original）。
type Response struct {
	ApplicationID string
	Token         string
	Content       string
	Files         map[string]string // ファイル名 → 中身。添付が無ければ空の map
}

// Registration はコマンドの一括登録（PUT .../applications/{app}/commands）。
type Registration struct {
	ApplicationID string
	Authorization string // Authorization ヘッダの値
	CommandNames  []string
}

type messagePayload struct {
	Content string `json:"content"`
}

// Server は Discord REST のうち応答の更新とコマンド登録に応える http.Handler。
type Server struct {
	mu            sync.Mutex
	failStatus    int
	responses     []Response
	nextUnread    int           // WaitForResponse がまだ渡していない最初の応答の位置
	responseAdded chan struct{} // 応答が届くたびに閉じて作り直す
	registrations []Registration
}

func New() *Server {
	s := &Server{}
	s.Reset()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodPatch && isResponseUpdatePath(segments):
		s.serveResponseUpdate(w, r, segments)
	case r.Method == http.MethodPut && isRegistrationPath(segments):
		s.serveRegistration(w, r, segments)
	default:
		http.NotFound(w, r)
	}
}

// パスの末尾 .../{app}/{token}/messages/@original で判定する。
func isResponseUpdatePath(segments []string) bool {
	n := len(segments)
	return n >= 4 && segments[n-2] == messagesSegment && segments[n-1] == originalMessage
}

// パスの末尾 .../{app}/commands で判定する。
func isRegistrationPath(segments []string) bool {
	n := len(segments)
	return n >= 2 && segments[n-1] == commandsSegment
}

func (s *Server) serveResponseUpdate(w http.ResponseWriter, r *http.Request, segments []string) {
	n := len(segments)
	resp, err := parseResponse(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp.ApplicationID, resp.Token = segments[n-4], segments[n-3]

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rejectIfFailing(w) {
		return
	}
	s.responses = append(s.responses, resp)
	close(s.responseAdded)
	s.responseAdded = make(chan struct{})
	writeJSON(w, map[string]any{
		"id":         fakeSnowflake,
		"channel_id": fakeSnowflake,
		"content":    resp.Content,
	})
}

func (s *Server) serveRegistration(w http.ResponseWriter, r *http.Request, segments []string) {
	var definitions []map[string]any
	if err := json.NewDecoder(r.Body).Decode(&definitions); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reg := Registration{
		ApplicationID: segments[len(segments)-2],
		Authorization: r.Header.Get(authorizationKey),
		CommandNames:  commandNames(definitions),
	}
	for _, def := range definitions {
		def["id"], def["application_id"], def["version"] = fakeSnowflake, reg.ApplicationID, fakeVersion
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rejectIfFailing(w) {
		return
	}
	s.registrations = append(s.registrations, reg)
	writeJSON(w, definitions)
}

// rejectIfFailing は FailWith 中なら失敗を書き込んで真を返す。s.mu を保持して呼ぶ。
func (s *Server) rejectIfFailing(w http.ResponseWriter) bool {
	if s.failStatus == 0 {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s.failStatus)
	_, _ = io.WriteString(w, rejectedBody)
	return true
}

func commandNames(definitions []map[string]any) []string {
	names := make([]string, 0, len(definitions))
	for _, def := range definitions {
		name, _ := def["name"].(string)
		names = append(names, name)
	}
	return names
}

// parseResponse は JSON 本体と multipart（payload_json とファイル部）の両方を解釈する。
func parseResponse(r *http.Request) (Response, error) {
	resp := Response{Files: map[string]string{}}
	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != multipartMedia {
		var payload messagePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return Response{}, err
		}
		resp.Content = payload.Content
		return resp, nil
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return resp, nil
		}
		if err != nil {
			return Response{}, err
		}
		body, err := io.ReadAll(part)
		if err != nil {
			return Response{}, err
		}
		if part.FormName() == payloadJSONPart {
			var payload messagePayload
			if err := json.Unmarshal(body, &payload); err != nil {
				return Response{}, err
			}
			resp.Content = payload.Content
			continue
		}
		resp.Files[part.FileName()] = string(body)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) Responses() []Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Response(nil), s.responses...)
}

// WaitForResponse はまだ渡していない最初の応答を、届いた順に 1 件返す。
// 記録済みなら待たずに返し、無ければ届くかタイムアウトするまで待つ。
func (s *Server) WaitForResponse(timeout time.Duration) (Response, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		s.mu.Lock()
		if s.nextUnread < len(s.responses) {
			resp := s.responses[s.nextUnread]
			s.nextUnread++
			s.mu.Unlock()
			return resp, true
		}
		added := s.responseAdded
		s.mu.Unlock()

		select {
		case <-added:
		case <-timer.C:
			return Response{}, false
		}
	}
}

func (s *Server) Registrations() []Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Registration(nil), s.registrations...)
}

// FailWith は以後の応答を指定の status の失敗にする。失敗した依頼は記録しない。
func (s *Server) FailWith(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus = status
}

// Reset は記録と設定を初期化する。
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus, s.responses, s.nextUnread, s.registrations = 0, nil, 0, nil
	if s.responseAdded == nil {
		s.responseAdded = make(chan struct{})
	}
}
