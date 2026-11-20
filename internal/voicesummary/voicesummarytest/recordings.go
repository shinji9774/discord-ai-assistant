// Package voicesummarytest は voicesummary のテスト用の fake を提供する。
package voicesummarytest

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"discord-ai-assistant/internal/voicesummary"
	"discord-ai-assistant/internal/voicesummary/craig"
)

const (
	// trackFileExtension は、取り出したトラックのファイルの拡張子。
	trackFileExtension = ".flac"
	// trackFileMode は、取り出したトラックのファイルの権限（所有者だけが読み書きできる）。
	trackFileMode = 0o600
)

// Recordings は voicesummary.RecordingSource のインメモリ実装。どのリンクにも同じ録音を返す。
// 並行には呼ばれない前提で排他しない。
type Recordings struct {
	Recording   craig.Recording // Describe が返す録音の情報
	Tracks      map[int][]byte  // トラック番号 → 音声の中身。Download がトラック番号の順に取り出す
	DescribeErr error           // nil でなければ Describe はこれを返す
	DownloadErr error           // nil でなければ Download は何も取り出さずにこれを返す
}

var _ voicesummary.RecordingSource = (*Recordings)(nil)

func (r *Recordings) Describe(_ context.Context, _ craig.Link) (craig.Recording, error) {
	if r.DescribeErr != nil {
		return craig.Recording{}, r.DescribeErr
	}
	return r.Recording, nil
}

// Download は、トラック番号の順に dir/{track}.flac へ書いて visit に渡し、visit が返ったら消す。
// visit がエラーを返したら、そこで止めてそのエラーを返す。
func (r *Recordings) Download(_ context.Context, _ craig.Link, dir string, visit func(craig.TrackFile) error) error {
	if r.DownloadErr != nil {
		return r.DownloadErr
	}
	for _, track := range slices.Sorted(maps.Keys(r.Tracks)) {
		if err := r.visitTrack(dir, track, visit); err != nil {
			return err
		}
	}
	return nil
}

func (r *Recordings) visitTrack(dir string, track int, visit func(craig.TrackFile) error) error {
	path := filepath.Join(dir, strconv.Itoa(track)+trackFileExtension)
	if err := os.WriteFile(path, r.Tracks[track], trackFileMode); err != nil {
		return err
	}
	defer os.Remove(path)
	return visit(craig.TrackFile{Track: track, Path: path})
}
