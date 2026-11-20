// Package craig は、録音 bot「Craig」のダウンロード用 API との接点を閉じ込める。
// API は非公式で、Craig のソースから読み取った形に合わせている（research.md）。
package craig

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var ErrNotRecordingLink = errors.New("not a craig recording link")

const (
	recordingPathPrefix = "/rec/"
	keyQueryName        = "key"
	deleteQueryName     = "delete"
)

// recordingIDPattern は Craig の録音 ID（英数字 10〜12 文字）。
var recordingIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{10,12}$`)

// Link は、録音リンクから取り出した録音 ID と鍵。
type Link struct {
	RecordingID string
	Key         string
	DeleteKey   string // 削除用のリンクのときだけ入る
}

// ParseLink は、raw が baseURL の録音リンクであれば Link を返す。
// 解釈の失敗は raw を含めずに ErrNotRecordingLink で返す（url.Parse のエラーはリンクを含むため）。
func ParseLink(raw string, baseURL string) (Link, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return Link{}, fmt.Errorf("parse craig base url: %w", err)
	}
	link, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || link.Scheme != base.Scheme || !strings.EqualFold(link.Host, base.Host) {
		return Link{}, ErrNotRecordingLink
	}
	id, ok := strings.CutPrefix(link.Path, recordingPathPrefix)
	if !ok || !recordingIDPattern.MatchString(id) {
		return Link{}, ErrNotRecordingLink
	}
	query := link.Query()
	key := query.Get(keyQueryName)
	if key == "" {
		return Link{}, ErrNotRecordingLink
	}
	return Link{RecordingID: id, Key: key, DeleteKey: query.Get(deleteQueryName)}, nil
}
