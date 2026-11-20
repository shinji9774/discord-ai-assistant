package craig_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"discord-ai-assistant/internal/voicesummary/craig"
	"discord-ai-assistant/test/craigfake"
)

// testPollInterval はテストで使うジョブの確認間隔。
const testPollInterval = time.Millisecond

const (
	completedFlacZip = "reusedoutput0000000000000000.flac.zip"
	runningMp3Zip    = "otheroutput00000000000000000.mp3.zip"
)

// newDownloadClient は craigfake に向け、ジョブの確認間隔を縮めた Client を作る。
func newDownloadClient(t *testing.T) (*craig.Client, *craigfake.Server) {
	t.Helper()
	client, fake, _ := newClient(t)
	craig.SetPollInterval(client, testPollInterval)
	return client, fake
}

// trackContents は visit が受けたトラック番号 → ファイルの中身。visit の中で読む（visit が返ると消えるため）。
type trackContents map[int]string

// collect は受けたトラックを contents に記録する visit を返す。
func (contents trackContents) collect(t *testing.T) func(craig.TrackFile) error {
	t.Helper()
	return func(f craig.TrackFile) error {
		body, err := os.ReadFile(f.Path)
		if err != nil {
			return err
		}
		contents[f.Track] = string(body)
		return nil
	}
}

// meetingTracks は meeting() の全トラックの中身。
var meetingTracks = trackContents{1: "flac-1", 2: "flac-2", 3: "flac-3"}

// assertEmptyDir は dir にファイルが残っていないことを確かめる。
func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("dir has %d entries, want empty", len(entries))
	}
}

// 条件: 完了済みの zip+flac のジョブがある。
// 内容: ジョブを作らずに、その zip から全トラックを visit に渡す。
func TestClientDownload_CompletedFlacJob_ReusesJob(t *testing.T) {
	client, fake := newDownloadClient(t)
	fake.AddRecording(meeting())
	fake.SetJob(recordingID, craigfake.Job{Status: "complete", OutputFileName: completedFlacZip})
	got := trackContents{}

	err := client.Download(context.Background(), link, t.TempDir(), got.collect(t))

	if err != nil || fake.JobCreations() != 0 || !reflect.DeepEqual(got, meetingTracks) {
		t.Errorf("Download err = %v, JobCreations = %d, tracks = %v; want nil, 0, %v", err, fake.JobCreations(), got, meetingTracks)
	}
}

// 条件: ジョブがない。
// 内容: zip+flac のジョブを 1 回作り、完了を待ってから全トラックを visit に渡す。
func TestClientDownload_NoJob_CreatesJobAndWaits(t *testing.T) {
	client, fake := newDownloadClient(t)
	fake.AddRecording(meeting())
	got := trackContents{}

	err := client.Download(context.Background(), link, t.TempDir(), got.collect(t))

	if err != nil || fake.JobCreations() != 1 || !reflect.DeepEqual(got, meetingTracks) {
		t.Errorf("Download err = %v, JobCreations = %d, tracks = %v; want nil, 1, %v", err, fake.JobCreations(), got, meetingTracks)
	}
}

// 条件: 作り直しが要るジョブ（失敗、取り消し、ほかの形式で完了）がある。
// 内容: zip+flac のジョブを 1 回作り、全トラックを visit に渡す。
func TestClientDownload_UnusableJob_CreatesJob(t *testing.T) {
	tests := []struct {
		name string
		job  craigfake.Job
	}{
		{"失敗", craigfake.Job{Status: "error", OutputFileName: completedFlacZip}},
		{"取り消し", craigfake.Job{Status: "cancelled", OutputFileName: completedFlacZip}},
		{"ほかの形式で完了", craigfake.Job{Status: "complete", OutputFileName: runningMp3Zip}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, fake := newDownloadClient(t)
			fake.AddRecording(meeting())
			fake.SetJob(recordingID, tt.job)
			got := trackContents{}

			err := client.Download(context.Background(), link, t.TempDir(), got.collect(t))

			if err != nil || fake.JobCreations() != 1 || !reflect.DeepEqual(got, meetingTracks) {
				t.Errorf("Download err = %v, JobCreations = %d, tracks = %v; want nil, 1, %v", err, fake.JobCreations(), got, meetingTracks)
			}
		})
	}
}

// 条件: zip+flac のジョブが稼働中である。
// 内容: ジョブを作らずに完了を待ち、全トラックを visit に渡す。
func TestClientDownload_RunningFlacJob_WaitsWithoutCreating(t *testing.T) {
	client, fake := newDownloadClient(t)
	fake.AddRecording(meeting())
	fake.SetJob(recordingID, craigfake.Job{Status: "running", OutputFileName: completedFlacZip})
	got := trackContents{}

	err := client.Download(context.Background(), link, t.TempDir(), got.collect(t))

	if err != nil || fake.JobCreations() != 0 || !reflect.DeepEqual(got, meetingTracks) {
		t.Errorf("Download err = %v, JobCreations = %d, tracks = %v; want nil, 0, %v", err, fake.JobCreations(), got, meetingTracks)
	}
}

// 条件: ほかの形式のジョブが稼働中である。
// 内容: ジョブを作らずに errJobBusy を返す。
func TestClientDownload_RunningOtherFormatJob_ReturnsErrJobBusy(t *testing.T) {
	client, fake := newDownloadClient(t)
	fake.AddRecording(meeting())
	fake.SetJob(recordingID, craigfake.Job{Status: "running", OutputFileName: runningMp3Zip})

	err := client.Download(context.Background(), link, t.TempDir(), trackContents{}.collect(t))

	if !errors.Is(err, craig.ErrJobBusy) || fake.JobCreations() != 0 {
		t.Errorf("Download err = %v, JobCreations = %d; want %v, 0", err, fake.JobCreations(), craig.ErrJobBusy)
	}
}

// 条件: 作ったジョブが変換に失敗する（fake ではトラックの無い録音のジョブが error で終わる）。
// 内容: ErrRecordingUnavailable ではない、ジョブの状態を含むエラーを返す。
func TestClientDownload_JobEndsWithError_ReturnsError(t *testing.T) {
	client, fake := newDownloadClient(t)
	rec := meeting()
	rec.Tracks = nil
	fake.AddRecording(rec)

	err := client.Download(context.Background(), link, t.TempDir(), trackContents{}.collect(t))

	if want := "craig get job: job ended with status error"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// 条件: 作ったジョブが終わる前に ctx の期限が切れる（確認間隔を 1 時間にする）。
// 内容: context.DeadlineExceeded を包んだエラーを返す。
func TestClientDownload_DeadlineBeforeCompletion_ReturnsDeadlineExceeded(t *testing.T) {
	client, fake := newDownloadClient(t)
	craig.SetPollInterval(client, time.Hour)
	fake.AddRecording(meeting())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := client.Download(ctx, link, t.TempDir(), trackContents{}.collect(t))

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want %v", err, context.DeadlineExceeded)
	}
}

// 条件: 完了済みのジョブから取り出す。
// 内容: Download が返ったあと、dir に zip も取り出したトラックも残らない。
func TestClientDownload_AfterReturn_LeavesDirEmpty(t *testing.T) {
	client, fake := newDownloadClient(t)
	fake.AddRecording(meeting())
	fake.SetJob(recordingID, craigfake.Job{Status: "complete", OutputFileName: completedFlacZip})
	dir := t.TempDir()

	err := client.Download(context.Background(), link, dir, trackContents{}.collect(t))

	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	assertEmptyDir(t, dir)
}

// 条件: visit が 1 つ目のトラックでエラーを返す。
// 内容: そのエラーを返し、残りのトラックは渡さず、dir に何も残さない。
func TestClientDownload_VisitFails_ReturnsVisitErrorAndCleansUp(t *testing.T) {
	client, fake := newDownloadClient(t)
	fake.AddRecording(meeting())
	fake.SetJob(recordingID, craigfake.Job{Status: "complete", OutputFileName: completedFlacZip})
	dir := t.TempDir()
	errVisit := errors.New("put audio failed")
	visited := 0
	visit := func(craig.TrackFile) error { visited++; return errVisit }

	err := client.Download(context.Background(), link, dir, visit)

	if !errors.Is(err, errVisit) || visited != 1 {
		t.Errorf("Download err = %v, visited = %d; want %v, 1", err, visited, errVisit)
	}
	assertEmptyDir(t, dir)
}

// 条件: ジョブの取得で Craig が 404 no_rec を返す。
// 内容: ErrRecordingUnavailable を返し、エラー文に URL・鍵・録音 ID を含めない。
func TestClientDownload_RecordingGone_ReturnsErrRecordingUnavailableWithoutSecrets(t *testing.T) {
	client, fake, srv := newClient(t)
	fake.FailWith(404, "no_rec")

	err := client.Download(context.Background(), link, t.TempDir(), trackContents{}.collect(t))

	if !errors.Is(err, craig.ErrRecordingUnavailable) {
		t.Errorf("err = %v, want %v", err, craig.ErrRecordingUnavailable)
	}
	assertNoSecrets(t, err, srv.URL)
}

// 条件: 完了済みのジョブの zip が /dl/ から取れず 404 になる（fake の出力を期限切れにする）。
// 内容: ErrRecordingUnavailable ではない、status を含むエラーを返し、エラー文に URL・鍵・録音 ID・出力ファイル名を含めない。
func TestClientDownload_ArchiveNotFound_ReturnsErrorWithoutOutputFileName(t *testing.T) {
	client, fake, srv := newClient(t)
	fake.AddRecording(meeting())
	fake.SetJob(recordingID, craigfake.Job{Status: "complete", OutputFileName: completedFlacZip})
	fake.ExpireOutputs()

	err := client.Download(context.Background(), link, t.TempDir(), trackContents{}.collect(t))

	if want := "craig download: status 404"; err == nil || err.Error() != want || errors.Is(err, craig.ErrRecordingUnavailable) {
		t.Errorf("err = %v, want %q (not %v)", err, want, craig.ErrRecordingUnavailable)
	}
	assertNoSecrets(t, err, srv.URL)
	if err != nil && strings.Contains(err.Error(), completedFlacZip) {
		t.Errorf("err = %q, contains output file name %q", err, completedFlacZip)
	}
}
