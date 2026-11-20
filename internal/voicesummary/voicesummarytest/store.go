package voicesummarytest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"discord-ai-assistant/internal/voicesummary"
)

// 置いたものを一覧するときの名前。S3 の配置にならうが、テストで比べるためだけに使う。
const (
	manifestKeyFormat   = "%s/manifest.json"
	audioKeyFormat      = "%s/audio/%d.flac"
	transcriptKeyFormat = "%s/transcripts/%d.json"
	claimKeyFormat      = "%s/report.claim"
)

// ErrTranscriptNotFound は、置かれていない文字起こしを読んだときのエラー。
var ErrTranscriptNotFound = errors.New("transcript not found")

// trackKey は、依頼の 1 トラックを指す。
type trackKey struct {
	requestID string
	track     int
}

// Store は voicesummary.AudioStore と voicesummary.ResultStore のインメモリ実装。
// ゼロ値で使える。並行には呼ばれない前提で排他しない。
type Store struct {
	ReadManifestErr   error // nil でなければ ReadManifest はこれを返す
	PutAudioErr       error // nil でなければ PutAudio は音声を置かずにこれを返す
	ReadTranscriptErr error // nil でなければ ReadTranscript はこれを返す
	DiscardErr        error // nil でなければ Discard は何も消さずにこれを返す

	manifests   map[string]voicesummary.Manifest
	audio       map[trackKey][]byte
	transcripts map[trackKey][]byte
	claims      map[string]bool
}

var (
	_ voicesummary.AudioStore  = (*Store)(nil)
	_ voicesummary.ResultStore = (*Store)(nil)
)

func (s *Store) CreateManifest(_ context.Context, m voicesummary.Manifest) error {
	if _, exists := s.manifests[m.RequestID]; exists {
		return voicesummary.ErrRequestExists
	}
	put(&s.manifests, m.RequestID, m)
	return nil
}

func (s *Store) ReadManifest(_ context.Context, requestID string) (voicesummary.Manifest, error) {
	if s.ReadManifestErr != nil {
		return voicesummary.Manifest{}, s.ReadManifestErr
	}
	m, ok := s.manifests[requestID]
	if !ok {
		return voicesummary.Manifest{}, voicesummary.ErrRequestNotFound
	}
	return m, nil
}

func (s *Store) PutAudio(_ context.Context, requestID string, track int, audio io.ReadSeeker) error {
	if s.PutAudioErr != nil {
		return s.PutAudioErr
	}
	content, err := io.ReadAll(audio)
	if err != nil {
		return err
	}
	put(&s.audio, trackKey{requestID: requestID, track: track}, content)
	return nil
}

// PutTranscript は、Transcribe がトラックの文字起こしの結果を書いたことにする。
func (s *Store) PutTranscript(requestID string, track int, transcribeJSON []byte) {
	put(&s.transcripts, trackKey{requestID: requestID, track: track}, transcribeJSON)
}

func (s *Store) ReadTranscript(_ context.Context, requestID string, track int) ([]byte, error) {
	if s.ReadTranscriptErr != nil {
		return nil, s.ReadTranscriptErr
	}
	content, ok := s.transcripts[trackKey{requestID: requestID, track: track}]
	if !ok {
		return nil, ErrTranscriptNotFound
	}
	return content, nil
}

func (s *Store) ClaimReport(_ context.Context, requestID string) error {
	if s.claims[requestID] {
		return voicesummary.ErrReportClaimed
	}
	put(&s.claims, requestID, true)
	return nil
}

// Discard は、依頼のマニフェスト、音声、文字起こしを消す。claim は残す（イベントが再配送されても二度投稿しないため）。
func (s *Store) Discard(_ context.Context, m voicesummary.Manifest) error {
	if s.DiscardErr != nil {
		return s.DiscardErr
	}
	ofRequest := func(k trackKey, _ []byte) bool { return k.requestID == m.RequestID }
	delete(s.manifests, m.RequestID)
	maps.DeleteFunc(s.audio, ofRequest)
	maps.DeleteFunc(s.transcripts, ofRequest)
	return nil
}

// Manifest は、依頼のマニフェストを返す。無ければ ok は false。
func (s *Store) Manifest(requestID string) (voicesummary.Manifest, bool) {
	m, ok := s.manifests[requestID]
	return m, ok
}

// Audio は、依頼に置かれた音声をトラック番号ごとに返す。無ければ nil。
func (s *Store) Audio(requestID string) map[int][]byte {
	var audio map[int][]byte
	for k, content := range s.audio {
		if k.requestID == requestID {
			put(&audio, k.track, content)
		}
	}
	return audio
}

// Keys は、置き場にあるものを S3 のキーにならった名前で、昇順に返す。何も無ければ nil。
func (s *Store) Keys() []string {
	var keys []string
	for requestID := range s.manifests {
		keys = append(keys, fmt.Sprintf(manifestKeyFormat, requestID))
	}
	for k := range s.audio {
		keys = append(keys, fmt.Sprintf(audioKeyFormat, k.requestID, k.track))
	}
	for k := range s.transcripts {
		keys = append(keys, fmt.Sprintf(transcriptKeyFormat, k.requestID, k.track))
	}
	for requestID := range s.claims {
		keys = append(keys, fmt.Sprintf(claimKeyFormat, requestID))
	}
	slices.Sort(keys)
	return keys
}

// put は、m が nil なら作ってから key に value を入れる。Store をゼロ値で使えるようにするため。
func put[K comparable, V any](m *map[K]V, key K, value V) {
	if *m == nil {
		*m = make(map[K]V)
	}
	(*m)[key] = value
}
