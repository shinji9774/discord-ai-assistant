// Package discordfake は Discord REST の HTTP fake を提供する。
package discordfake

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
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
	applicationsSeg  = "applications"
	currentAppSeg    = "@me"
	fakeSnowflake    = "1"
	fakeVersion      = "1"
	rejectedBody     = `{"message":"rejected by fake"}`
	authorizationKey = "Authorization"

	channelsSegment           = "channels"
	historyRejectedBodyFormat = `{"message":"rejected by fake","code":%d}`
	beforeQuery               = "before"
	limitQuery                = "limit"
)

// FakeApplicationID は GET /applications/@me が返す application id。disgo は snowflake を要求するため数字だけにする。
const FakeApplicationID = "123456789012345678"

// Response は応答の更新（PATCH .../webhooks/{app}/{token}/messages/@original）。
type Response struct {
	ApplicationID string
	Token         string
	Content       string
	Files         map[string]string // ファイル名 → 中身。添付が無ければ空の map
}

// ChannelMessage はチャンネルへの投稿（POST .../channels/{id}/messages）。
type ChannelMessage struct {
	ChannelID      string
	Authorization  string // Authorization ヘッダの値
	Content        string
	AllowedUserIDs []string          // allowed_mentions.users。指定が無ければ nil
	Files          map[string]string // ファイル名 → 中身。添付が無ければ空の map
}

// Registration はコマンドの一括登録（PUT .../applications/{app}/commands）。
type Registration struct {
	ApplicationID string
	Authorization string // Authorization ヘッダの値
	CommandNames  []string
}

// HistoryMessage はチャンネルの会話履歴（GET .../channels/{id}/messages）の 1 件。Discord API の Message のうち、bot が読む項目だけを持つ。
type HistoryMessage struct {
	ID        string        `json:"id"`
	Type      int           `json:"type"`
	Content   string        `json:"content"`
	Timestamp time.Time     `json:"timestamp"`
	Author    HistoryUser   `json:"author"`
	Mentions  []HistoryUser `json:"mentions"`
	WebhookID string        `json:"webhook_id,omitempty"`
	Flags     int           `json:"flags"`
}

// HistoryUser は投稿者とメンションされた利用者。
type HistoryUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name,omitempty"`
	Bot        bool   `json:"bot,omitempty"`
}

// historyRejection は履歴の取得を拒むときの status と Discord のエラーコード。
type historyRejection struct {
	status int
	code   int
}

// messagePayload は、送られたメッセージの JSON のうち fake が記録する項目。
type messagePayload struct {
	Content         string `json:"content"`
	AllowedMentions struct {
		Users []string `json:"users"`
	} `json:"allowed_mentions"`
}

// Server は Discord REST のうち応答の更新とコマンド登録、application 情報の取得、チャンネルの会話履歴の取得とチャンネルへの投稿、
// および添付ファイルのダウンロード（GET）に応える http.Handler。
type Server struct {
	mu                     sync.Mutex
	failStatus             int
	registrationFailStatus int // 0 以外ならコマンド登録だけをこの status で失敗させる
	responses              []Response
	nextUnread             int           // WaitForResponse がまだ渡していない最初の応答の位置
	responseAdded          chan struct{} // 応答が届くたびに閉じて作り直す
	channelMessages        []ChannelMessage
	nextUnreadMessage      int           // WaitForChannelMessage がまだ渡していない最初の投稿の位置
	channelMessageAdded    chan struct{} // 投稿が届くたびに閉じて作り直す
	registrations          []Registration
	attachments            map[string]string           // URL のパス → 中身
	history                map[string][]HistoryMessage // チャンネル ID → 投稿
	historyRejections      map[string]historyRejection // チャンネル ID → 拒み方
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
	case r.Method == http.MethodGet && isCurrentApplicationPath(segments):
		s.serveCurrentApplication(w, r)
	case r.Method == http.MethodPost && isHistoryPath(segments): // 投稿は履歴と同じ .../channels/{id}/messages
		s.serveChannelMessage(w, r, segments[len(segments)-2])
	case r.Method == http.MethodGet && isHistoryPath(segments):
		s.serveHistory(w, r, segments[len(segments)-2])
	case r.Method == http.MethodGet:
		s.serveAttachment(w, r)
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

// パスの末尾 .../applications/@me で判定する。
func isCurrentApplicationPath(segments []string) bool {
	n := len(segments)
	return n >= 2 && segments[n-2] == applicationsSeg && segments[n-1] == currentAppSeg
}

// パスの末尾 .../channels/{channel}/messages で判定する。
func isHistoryPath(segments []string) bool {
	n := len(segments)
	return n >= 3 && segments[n-3] == channelsSegment && segments[n-1] == messagesSegment
}

// serveHistory は before より前の投稿を新しい順に最大 limit 件返す。拒む設定のチャンネルは Discord と同じ形のエラーを返す。
func (s *Server) serveHistory(w http.ResponseWriter, r *http.Request, channelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rejection, ok := s.historyRejections[channelID]; ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rejection.status)
		fmt.Fprintf(w, historyRejectedBodyFormat, rejection.code)
		return
	}
	before, err := strconv.ParseUint(r.URL.Query().Get(beforeQuery), 10, 64)
	if err != nil {
		http.Error(w, "before must be a snowflake", http.StatusBadRequest)
		return
	}
	limit, err := strconv.Atoi(r.URL.Query().Get(limitQuery))
	if err != nil || limit <= 0 {
		http.Error(w, "limit must be a positive number", http.StatusBadRequest)
		return
	}
	writeJSON(w, olderMessages(s.history[channelID], before, limit))
}

// olderMessages は ID が before より小さい投稿を新しい順に最大 limit 件返す。
func olderMessages(messages []HistoryMessage, before uint64, limit int) []HistoryMessage {
	older := []HistoryMessage{}
	for _, m := range messages {
		if messageID(m) < before {
			older = append(older, m)
		}
	}
	slices.SortFunc(older, func(a, b HistoryMessage) int { return cmp.Compare(messageID(b), messageID(a)) })
	return older[:min(len(older), limit)]
}

// messageID は投稿の ID を数として返す。仕込む側が数字の ID を渡す前提。
func messageID(m HistoryMessage) uint64 {
	id, _ := strconv.ParseUint(m.ID, 10, 64)
	return id
}

func (s *Server) serveResponseUpdate(w http.ResponseWriter, r *http.Request, segments []string) {
	n := len(segments)
	payload, files, err := parseMessage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp := Response{ApplicationID: segments[n-4], Token: segments[n-3], Content: payload.Content, Files: files}

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

// serveChannelMessage はチャンネルへの投稿を記録し、作ったメッセージを返す。FailWith 中は記録しない。
func (s *Server) serveChannelMessage(w http.ResponseWriter, r *http.Request, channelID string) {
	payload, files, err := parseMessage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	msg := ChannelMessage{
		ChannelID:      channelID,
		Authorization:  r.Header.Get(authorizationKey),
		Content:        payload.Content,
		AllowedUserIDs: payload.AllowedMentions.Users,
		Files:          files,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rejectIfFailing(w) {
		return
	}
	s.channelMessages = append(s.channelMessages, msg)
	close(s.channelMessageAdded)
	s.channelMessageAdded = make(chan struct{})
	writeJSON(w, map[string]any{
		"id":         fakeSnowflake,
		"channel_id": channelID,
		"content":    msg.Content,
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
	if s.rejectIfFailing(w) || s.rejectRegistrationIfFailing(w) {
		return
	}
	s.registrations = append(s.registrations, reg)
	writeJSON(w, definitions)
}

// serveCurrentApplication は Bot トークンの持ち主の application として FakeApplicationID を返す。
func (s *Server) serveCurrentApplication(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rejectIfFailing(w) {
		return
	}
	writeJSON(w, map[string]any{"id": FakeApplicationID})
}

// serveAttachment は AddAttachment で登録したパスの中身を返す。未登録なら 404。
func (s *Server) serveAttachment(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	content, ok := s.attachments[r.URL.Path]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = io.WriteString(w, content)
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

// rejectRegistrationIfFailing は FailRegistrationWith 中なら失敗を書き込んで真を返す。s.mu を保持して呼ぶ。
func (s *Server) rejectRegistrationIfFailing(w http.ResponseWriter) bool {
	if s.registrationFailStatus == 0 {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s.registrationFailStatus)
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

// parseMessage は JSON 本体と multipart（payload_json とファイル部）の両方を解釈し、JSON の項目と添付（ファイル名 → 中身）を返す。
func parseMessage(r *http.Request) (messagePayload, map[string]string, error) {
	var payload messagePayload
	files := map[string]string{}
	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != multipartMedia {
		err := json.NewDecoder(r.Body).Decode(&payload)
		return payload, files, err
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return payload, files, nil
		}
		if err != nil {
			return messagePayload{}, nil, err
		}
		body, err := io.ReadAll(part)
		if err != nil {
			return messagePayload{}, nil, err
		}
		if part.FormName() != payloadJSONPart {
			files[part.FileName()] = string(body)
			continue
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return messagePayload{}, nil, err
		}
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

func (s *Server) ChannelMessages() []ChannelMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ChannelMessage(nil), s.channelMessages...)
}

// WaitForChannelMessage はまだ渡していない最初の投稿を、届いた順に 1 件返す。
// 記録済みなら待たずに返し、無ければ届くかタイムアウトするまで待つ。
func (s *Server) WaitForChannelMessage(timeout time.Duration) (ChannelMessage, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		s.mu.Lock()
		if s.nextUnreadMessage < len(s.channelMessages) {
			msg := s.channelMessages[s.nextUnreadMessage]
			s.nextUnreadMessage++
			s.mu.Unlock()
			return msg, true
		}
		added := s.channelMessageAdded
		s.mu.Unlock()

		select {
		case <-added:
		case <-timer.C:
			return ChannelMessage{}, false
		}
	}
}

// AddAttachment は GET path に返す添付ファイルの中身を登録する。
func (s *Server) AddAttachment(path, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attachments[path] = content
}

// AddHistory は channelID のチャンネルに投稿を足す。
func (s *Server) AddHistory(channelID string, messages ...HistoryMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history[channelID] = append(s.history[channelID], messages...)
}

// RejectHistory は channelID のチャンネルの履歴の取得を、status と Discord のエラーコード code で拒ませる。
func (s *Server) RejectHistory(channelID string, status, code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.historyRejections[channelID] = historyRejection{status: status, code: code}
}

// FailWith は以後の応答を指定の status の失敗にする。失敗した依頼は記録しない。
func (s *Server) FailWith(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus = status
}

// FailRegistrationWith は以後のコマンド登録だけを指定の status の失敗にする。他の依頼には影響しない。
func (s *Server) FailRegistrationWith(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registrationFailStatus = status
}

// Reset は記録と設定を初期化する。
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus, s.registrationFailStatus, s.responses, s.nextUnread, s.registrations, s.attachments = 0, 0, nil, 0, nil, map[string]string{}
	s.channelMessages, s.nextUnreadMessage = nil, 0
	s.history = map[string][]HistoryMessage{}
	s.historyRejections = map[string]historyRejection{}
	if s.responseAdded == nil {
		s.responseAdded = make(chan struct{})
	}
	if s.channelMessageAdded == nil {
		s.channelMessageAdded = make(chan struct{})
	}
}
