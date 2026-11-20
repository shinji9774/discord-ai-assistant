package craig

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// trackEntryPattern は zip の中のトラックのファイル名（"{track}-{name}.flac"）。info.txt と raw.dat は対象にしない。
var trackEntryPattern = regexp.MustCompile(`^(\d+)-.*\.flac$`)

const trackFileFormat = "track-%d.flac"

// extractTracks は zip のトラックを 1 つずつ dir に取り出して visit に渡し、visit が返ったら消す。
// 一度に 1 トラックだけを展開し、/tmp の容量を抑える。
func extractTracks(archivePath, dir string, visit func(TrackFile) error) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("craig open archive: %w", err)
	}
	defer r.Close()
	for _, entry := range r.File {
		match := trackEntryPattern.FindStringSubmatch(entry.Name)
		if match == nil {
			continue
		}
		track, err := strconv.Atoi(match[1])
		if err != nil {
			return fmt.Errorf("craig archive track number: %w", err)
		}
		if err := visitTrack(entry, track, dir, visit); err != nil {
			return err
		}
	}
	return nil
}

// visitTrack は 1 トラックを取り出して visit に渡し、成否にかかわらず消す。
// 保存先の名前はトラック番号だけで作り、zip の中の名前（話者名を含む）をパスに使わない。
func visitTrack(entry *zip.File, track int, dir string, visit func(TrackFile) error) error {
	path := filepath.Join(dir, fmt.Sprintf(trackFileFormat, track))
	if err := extractEntry(entry, path); err != nil {
		_ = os.Remove(path)
		return err
	}
	defer os.Remove(path)
	return visit(TrackFile{Track: track, Path: path})
}

func extractEntry(entry *zip.File, path string) error {
	src, err := entry.Open()
	if err != nil {
		return fmt.Errorf("craig open archive entry: %w", err)
	}
	defer src.Close()
	dst, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("craig create track file: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("craig extract track: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("craig close track file: %w", err)
	}
	return nil
}
