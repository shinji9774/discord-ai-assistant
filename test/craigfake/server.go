// Package craigfake は Craig のダウンロード用 API の HTTP fake を提供する。
// 形は Craig のソース（apps/ferret）から読み取ったもの（research.md）に合わせる。
package craigfake

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	recordingsPrefix = "/api/v1/recordings/"
	downloadPrefix   = "/dl/"
	durationSegment  = "duration"
	jobSegment       = "job"
	keyQueryName     = "key"
	deleteQueryName  = "delete"
	contentType      = "application/json"
	zipContentType   = "application/zip"

	statusQueued   = "queued"
	statusIdle     = "idle"
	statusRunning  = "running"
	statusComplete = "complete"
	statusError    = "error"

	codeNoKey      = "no_key"
	codeInvalidKey = "invalid_key"
	codeNoRec      = "no_rec"
	codeJobExists  = "job_exists"
	codeNoJob      = "no_job"
	codeRecNoData  = "rec_no_data"
	// codeInvalidDeleteKey は fake 独自のコード。Client はコードに依存しない。
	codeInvalidDeleteKey = "invalid_delete_key"

	// DeletionTargetJob と DeletionTargetRecording は Deletion.Target の値。
	DeletionTargetJob       = "job"
	DeletionTargetRecording = "recording"

	// createdFileNameFormat は作ったジョブの出力ファイル名。Craig は 30 文字の乱数 + 拡張子にする。
	createdFileNameFormat = "fakeoutput%024d.flac.zip"
	infoFileName          = "info.txt"
	infoFileContent       = "Recording fake\n"
)

// User は録音の参加者。
type User struct {
	Track      int
	Username   string
	GlobalName string
	Bot        bool
}

// Recording は fake が持つ録音。
type Recording struct {
	ID              string
	Key             string
	DeleteKey       string
	GuildID         string
	StartTime       time.Time
	DurationSeconds float64
	Live            bool
	Users           []User
	Tracks          map[int][]byte // トラック番号 → FLAC の中身（zip に "{track}-{username}.flac" で入れる）

	deleted bool // 録音の削除が済んでいる（DELETE 後の状態。外から仕込まない）
}

// Job は録音の変換ジョブ。
type Job struct {
	Status         string // idle|running|complete|error|cancelled|queued
	OutputFileName string
}

// Deletion は fake が受け付けた削除の記録。Target は "job" か "recording"。
type Deletion struct {
	RecordingID string
	Target      string
}

type apiError struct {
	status int
	code   string
}

type recordingBody struct {
	StartTime time.Time `json:"startTime"`
	Guild     guildBody `json:"guild"`
}

type guildBody struct {
	ID string `json:"id"`
}

type userBody struct {
	Track         int    `json:"track"`
	Username      string `json:"username"`
	Discriminator string `json:"discriminator"`
	GlobalName    string `json:"globalName,omitempty"`
	Bot           bool   `json:"bot,omitempty"`
}

type jobBody struct {
	Status         string `json:"status"`
	OutputFileName string `json:"outputFileName"`
}

type createJobBody struct {
	Type    string `json:"type"`
	Options struct {
		Container string `json:"container"`
		Format    string `json:"format"`
	} `json:"options"`
}

// Server は /api/v1/recordings/{id}（GET・DELETE）、.../duration（GET）、.../job（GET・POST・DELETE）、/dl/{file}（GET）に応える http.Handler。
//
// ジョブは GET .../job のたびに 1 段進む（queued・idle → running → complete）。
// トラックの無い録音のジョブは、running の次に error で終わる（変換の失敗を再現する）。
type Server struct {
	mu           sync.Mutex
	recordings   map[string]Recording
	jobs         map[string]Job
	outputs      map[string]string // 出力ファイル名 → 録音 ID
	failure      *apiError
	jobCreations int
	deletions    []Deletion
}

func New() *Server {
	s := &Server{}
	s.Reset()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name, ok := strings.CutPrefix(r.URL.Path, downloadPrefix); ok && r.Method == http.MethodGet {
		s.serveDownload(w, name)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, recordingsPrefix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if s.failure != nil {
		writeError(w, *s.failure)
		return
	}
	id, segment, _ := strings.Cut(rest, "/")
	rec, failure := s.authorize(id, r.URL.Query().Get(keyQueryName))
	if failure != nil {
		writeError(w, *failure)
		return
	}
	switch {
	case segment == "" && r.Method == http.MethodGet:
		s.serveRecording(w, rec)
	case segment == durationSegment && r.Method == http.MethodGet:
		writeJSON(w, map[string]float64{"duration": rec.DurationSeconds})
	case segment == jobSegment && r.Method == http.MethodGet:
		s.serveJob(w, rec)
	case segment == jobSegment && r.Method == http.MethodPost:
		s.createJob(w, r, rec)
	case segment == jobSegment && r.Method == http.MethodDelete:
		s.deleteJob(w, rec)
	case segment == "" && r.Method == http.MethodDelete:
		s.deleteRecording(w, r, rec)
	default:
		http.NotFound(w, r)
	}
}

// authorize は録音と鍵を照らし合わせる。s.mu を保持して呼ぶ。
func (s *Server) authorize(id, key string) (Recording, *apiError) {
	if key == "" {
		return Recording{}, &apiError{status: http.StatusBadRequest, code: codeNoKey}
	}
	rec, ok := s.recordings[id]
	if !ok {
		return Recording{}, &apiError{status: http.StatusNotFound, code: codeNoRec}
	}
	if rec.Key != key {
		return Recording{}, &apiError{status: http.StatusUnauthorized, code: codeInvalidKey}
	}
	return rec, nil
}

func (s *Server) serveRecording(w http.ResponseWriter, rec Recording) {
	users := make([]userBody, 0, len(rec.Users))
	for _, u := range rec.Users {
		users = append(users, userBody{Track: u.Track, Username: u.Username, Discriminator: "0", GlobalName: u.GlobalName, Bot: u.Bot})
	}
	writeJSON(w, map[string]any{
		"recording": recordingBody{StartTime: rec.StartTime, Guild: guildBody{ID: rec.GuildID}},
		"users":     users,
		"live":      rec.Live,
	})
}

// serveJob は今のジョブを返し、次の段へ進める。s.mu を保持して呼ぶ。
func (s *Server) serveJob(w http.ResponseWriter, rec Recording) {
	job, ok := s.jobs[rec.ID]
	if !ok {
		writeJSON(w, map[string]*jobBody{"job": nil})
		return
	}
	writeJSON(w, map[string]*jobBody{"job": {Status: job.Status, OutputFileName: job.OutputFileName}})
	s.jobs[rec.ID] = advance(job, len(rec.Tracks) > 0)
}

func advance(job Job, convertible bool) Job {
	switch job.Status {
	case statusQueued, statusIdle:
		job.Status = statusRunning
	case statusRunning:
		job.Status = statusComplete
		if !convertible {
			job.Status = statusError
		}
	}
	return job
}

// createJob は zip+flac 以外の依頼を 400 で断る。稼働中のジョブがあれば job_exists。s.mu を保持して呼ぶ。
func (s *Server) createJob(w http.ResponseWriter, r *http.Request, rec Recording) {
	var in createJobBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Type != "recording" ||
		in.Options.Container != "zip" || in.Options.Format != "flac" {
		http.Error(w, "unsupported job options", http.StatusBadRequest)
		return
	}
	if job, ok := s.jobs[rec.ID]; ok && isActive(job.Status) {
		writeError(w, apiError{status: http.StatusBadRequest, code: codeJobExists})
		return
	}
	s.jobCreations++
	job := Job{Status: statusQueued, OutputFileName: fmt.Sprintf(createdFileNameFormat, s.jobCreations)}
	s.setJob(rec.ID, job)
	writeJSON(w, map[string]*jobBody{"job": {Status: job.Status, OutputFileName: job.OutputFileName}})
}

// deleteJob はジョブと出力を消す。ジョブが無ければ no_job。s.mu を保持して呼ぶ。
func (s *Server) deleteJob(w http.ResponseWriter, rec Recording) {
	job, ok := s.jobs[rec.ID]
	if !ok {
		writeError(w, apiError{status: http.StatusBadRequest, code: codeNoJob})
		return
	}
	delete(s.outputs, job.OutputFileName)
	delete(s.jobs, rec.ID)
	s.deletions = append(s.deletions, Deletion{RecordingID: rec.ID, Target: DeletionTargetJob})
	writeJSON(w, map[string]bool{"ok": true})
}

// deleteRecording は削除の鍵が合えば録音を消し済みにする。s.mu を保持して呼ぶ。
func (s *Server) deleteRecording(w http.ResponseWriter, r *http.Request, rec Recording) {
	if rec.deleted {
		writeError(w, apiError{status: http.StatusNotFound, code: codeRecNoData})
		return
	}
	if r.URL.Query().Get(deleteQueryName) != rec.DeleteKey {
		writeError(w, apiError{status: http.StatusUnauthorized, code: codeInvalidDeleteKey})
		return
	}
	rec.deleted = true
	s.recordings[rec.ID] = rec
	s.deletions = append(s.deletions, Deletion{RecordingID: rec.ID, Target: DeletionTargetRecording})
	writeJSON(w, map[string]bool{"ok": true})
}

func isActive(status string) bool {
	return status == statusQueued || status == statusIdle || status == statusRunning
}

// serveDownload は出力ファイル名に対応する録音の zip を返す。s.mu を保持して呼ぶ。
func (s *Server) serveDownload(w http.ResponseWriter, name string) {
	id, ok := s.outputs[name]
	if !ok || s.jobs[id].Status != statusComplete {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	archive, err := buildArchive(s.recordings[id])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", zipContentType)
	_, _ = w.Write(archive)
}

// buildArchive は Craig と同じく "{track}-{username}.flac" と info.txt を入れた zip を作る。
func buildArchive(rec Recording) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeEntry(zw, infoFileName, []byte(infoFileContent)); err != nil {
		return nil, err
	}
	for _, u := range rec.Users {
		audio, ok := rec.Tracks[u.Track]
		if !ok {
			continue
		}
		if err := writeEntry(zw, fmt.Sprintf("%d-%s.flac", u.Track, u.Username), audio); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeEntry(zw *zip.Writer, name string, content []byte) error {
	f, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = f.Write(content)
	return err
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", contentType)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, e apiError) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(e.status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": e.code, "code": e.code})
}

// setJob はジョブを置き、出力ファイル名から録音を引けるようにする。s.mu を保持して呼ぶ。
func (s *Server) setJob(recordingID string, job Job) {
	s.jobs[recordingID] = job
	if job.OutputFileName != "" {
		s.outputs[job.OutputFileName] = recordingID
	}
}

// AddRecording は録音を足す。
func (s *Server) AddRecording(r Recording) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordings[r.ID] = r
}

// SetJob は既存のジョブを仕込む。
func (s *Server) SetJob(recordingID string, job Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setJob(recordingID, job)
}

// FailWith は以後の API 応答（/api/v1/recordings/ 以下）を、このエラーにする。
func (s *Server) FailWith(status int, code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failure = &apiError{status: status, code: code}
}

// JobCreations は POST .../job を受け付けた回数を返す。
func (s *Server) JobCreations() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobCreations
}

// Deletions は受け付けた削除の記録のコピーを返す。
func (s *Server) Deletions() []Deletion {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Deletion(nil), s.deletions...)
}

// ExpireOutputs は出力ファイルを /dl/ から取れなくする（Craig の保存期間が過ぎた状況を再現する。ジョブの情報は残る）。
func (s *Server) ExpireOutputs() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outputs = map[string]string{}
}

// Reset は録音、ジョブ、失敗の設定、回数、削除の記録を初期化する。
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordings, s.jobs, s.outputs = map[string]Recording{}, map[string]Job{}, map[string]string{}
	s.failure, s.jobCreations, s.deletions = nil, 0, nil
}
