package voicesummarytest_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"

	"discord-ai-assistant/internal/voicesummary/craig"
	"discord-ai-assistant/internal/voicesummary/voicesummarytest"
)

// 条件: トラック 2 と 1 の音声を持つ録音を、一時ディレクトリへ取り出す。
// 内容: トラック番号の順に、中身の入ったファイルが visit に渡り、取り出し終えるとディレクトリに何も残らない。
func TestRecordingsDownload_TwoTracks_VisitsInTrackOrderAndRemovesFiles(t *testing.T) {
	recordings := &voicesummarytest.Recordings{Tracks: map[int][]byte{2: []byte("はなこの音声"), 1: []byte("たろうの音声")}}
	dir := t.TempDir()
	var visited []string
	visit := func(f craig.TrackFile) error {
		content, err := os.ReadFile(f.Path)
		if err != nil {
			return err
		}
		visited = append(visited, fmt.Sprintf("%d:%s", f.Track, content))
		return nil
	}

	err := recordings.Download(context.Background(), craig.Link{}, dir, visit)

	left, readErr := os.ReadDir(dir)
	if err != nil || readErr != nil {
		t.Fatalf("Download: %v, ReadDir: %v", err, readErr)
	}
	if want := []string{"1:たろうの音声", "2:はなこの音声"}; !reflect.DeepEqual(visited, want) || len(left) != 0 {
		t.Errorf("visited = %v (left %d files), want %v (left 0 files)", visited, len(left), want)
	}
}
