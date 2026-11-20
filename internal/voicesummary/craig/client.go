package craig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var ErrRecordingUnavailable = errors.New("craig recording unavailable")

const (
	recordingsPath = "/api/v1/recordings/"
	durationPath   = "/duration"

	opDescribeRecording = "describe recording"
	opGetDuration       = "get recording duration"
)

// unavailableCodes は、録音が無い・鍵が違うことを表す Craig のエラーコード。利用者の再実行では直らない。
var unavailableCodes = map[string]bool{
	"no_key":            true,
	"invalid_key":       true,
	"no_rec":            true,
	"recording_deleted": true,
	"rec_no_data":       true,
}

type Recording struct {
	GuildID   string
	StartedAt time.Time
	Duration  time.Duration
	Live      bool
	Speakers  []Speaker
}

type Speaker struct {
	Track int
	Name  string
	Bot   bool
}

type TrackFile struct {
	Track int
	Path  string
}

// Client は Craig のダウンロード用 API を呼ぶ。接続先は常に設定の baseURL で、録音リンクの host は使わない。
type Client struct {
	baseURL      string
	httpClient   *http.Client
	pollInterval time.Duration
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimSuffix(baseURL, "/"), httpClient: httpClient, pollInterval: defaultPollInterval}
}

type recordingResponse struct {
	Recording struct {
		StartTime time.Time `json:"startTime"`
		Guild     struct {
			ID string `json:"id"`
		} `json:"guild"`
	} `json:"recording"`
	Users []struct {
		Track      int    `json:"track"`
		Username   string `json:"username"`
		GlobalName string `json:"globalName"`
		Bot        bool   `json:"bot"`
	} `json:"users"`
	Live bool `json:"live"`
}

type durationResponse struct {
	Duration float64 `json:"duration"`
}

type errorResponse struct {
	Code string `json:"code"`
}

// Describe は録音の情報と長さを返す。録音が無い・鍵が違うときは ErrRecordingUnavailable。
func (c *Client) Describe(ctx context.Context, link Link) (Recording, error) {
	var rec recordingResponse
	if err := c.getJSON(ctx, opDescribeRecording, c.recordingURL(link, ""), &rec); err != nil {
		return Recording{}, err
	}
	var dur durationResponse
	if err := c.getJSON(ctx, opGetDuration, c.recordingURL(link, durationPath), &dur); err != nil {
		return Recording{}, err
	}
	speakers := make([]Speaker, 0, len(rec.Users))
	for _, u := range rec.Users {
		speakers = append(speakers, Speaker{Track: u.Track, Name: displayName(u.GlobalName, u.Username), Bot: u.Bot})
	}
	return Recording{
		GuildID:   rec.Recording.Guild.ID,
		StartedAt: rec.Recording.StartTime,
		Duration:  time.Duration(dur.Duration * float64(time.Second)),
		Live:      rec.Live,
		Speakers:  speakers,
	}, nil
}

// displayName は Discord の表示名を優先する。表示名を設定していない利用者は username だけを持つ。
func displayName(globalName, username string) string {
	if globalName != "" {
		return globalName
	}
	return username
}

// recordingURL は録音の API の URL を作る。鍵を含むので、エラー文に入れない。
func (c *Client) recordingURL(link Link, suffix string) string {
	query := url.Values{keyQueryName: {link.Key}}
	return c.baseURL + recordingsPath + url.PathEscape(link.RecordingID) + suffix + "?" + query.Encode()
}

func (c *Client) getJSON(ctx context.Context, op, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("craig %s: build request: %w", op, stripURL(err))
	}
	return c.doJSON(op, req, out)
}

// doJSON は req を送り、成功なら本文を out に読む。失敗の応答は Craig のエラーコードで分類する。
func (c *Client) doJSON(op string, req *http.Request, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("craig %s: %w", op, stripURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return responseError(op, resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("craig %s: decode response: %w", op, err)
	}
	return nil
}

func responseError(op string, resp *http.Response) error {
	var body errorResponse
	_ = json.NewDecoder(io.LimitReader(resp.Body, maxErrorBodyBytes)).Decode(&body)
	if unavailableCodes[body.Code] {
		return fmt.Errorf("craig %s: code %s: %w", op, body.Code, ErrRecordingUnavailable)
	}
	return fmt.Errorf("craig %s: status %d, code %q", op, resp.StatusCode, body.Code)
}

// maxErrorBodyBytes はエラー応答から読む上限。エラー本文は {error, code} だけの小さな JSON である。
const maxErrorBodyBytes = 4096

// stripURL は *url.Error から URL（鍵とファイル名を含む）を除く。
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// defaultPollInterval はジョブの状態を確かめる間隔。Craig の公式の画面と同じ。
const defaultPollInterval = 3 * time.Second

const (
	jobPath      = "/job"
	downloadPath = "/dl/"
	// flacZipSuffix は zip+flac のジョブの出力ファイル名の末尾。ほかの形式のジョブと見分けるために使う。
	flacZipSuffix   = ".flac.zip"
	archivePattern  = "craig-*.zip"
	jsonContentType = "application/json"

	jobStatusComplete  = "complete"
	jobStatusError     = "error"
	jobStatusCancelled = "cancelled"

	opGetJob    = "get job"
	opCreateJob = "create job"
	opDownload  = "download"
)

// createJobBody は話者ごとの FLAC を入れた zip を作る依頼。有料機能の判定がない形式に固定する。
const createJobBody = `{"type":"recording","options":{"container":"zip","format":"flac"}}`

// errJobBusy は、ほかの形式のジョブが稼働中で、zip+flac のジョブを作れないことを表す。
var errJobBusy = errors.New("craig job of another format is running")

type job struct {
	Status         string `json:"status"`
	OutputFileName string `json:"outputFileName"`
}

type jobResponse struct {
	Job *job `json:"job"`
}

// Download は zip+flac の変換を済ませて zip を dir に保存し、トラックを 1 つずつ取り出して visit に渡す。
// zip と取り出したファイルは、Download が返るまでに消す。待つ時間の上限は ctx の期限である。
func (c *Client) Download(ctx context.Context, link Link, dir string, visit func(TrackFile) error) error {
	fileName, err := c.completedJob(ctx, link)
	if err != nil {
		return err
	}
	archivePath, err := c.saveArchive(ctx, fileName, dir)
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)
	return extractTracks(archivePath, dir, visit)
}

// completedJob は完了済みの zip+flac のジョブの出力ファイル名を返す。無ければジョブを作って完了を待つ。
func (c *Client) completedJob(ctx context.Context, link Link) (string, error) {
	current, err := c.getJob(ctx, link)
	if err != nil {
		return "", err
	}
	if current != nil && isActive(current.Status) && !isFlacZip(current) {
		return "", fmt.Errorf("craig %s: %w", opCreateJob, errJobBusy)
	}
	if needsNewJob(current) {
		if err := c.createJob(ctx, link); err != nil {
			return "", err
		}
	}
	return c.waitForJob(ctx, link)
}

// needsNewJob は、使えるジョブも待てるジョブも無いときに真を返す。完了済みのジョブは新しい POST で置き換わる。
func needsNewJob(current *job) bool {
	if current == nil {
		return true
	}
	switch current.Status {
	case jobStatusError, jobStatusCancelled:
		return true
	case jobStatusComplete:
		return !isFlacZip(current)
	}
	return false
}

func isActive(status string) bool {
	return status != jobStatusComplete && status != jobStatusError && status != jobStatusCancelled
}

func isFlacZip(j *job) bool {
	return strings.HasSuffix(j.OutputFileName, flacZipSuffix)
}

// waitForJob は pollInterval ごとにジョブを確かめ、完了したら出力ファイル名を返す。
func (c *Client) waitForJob(ctx context.Context, link Link) (string, error) {
	for {
		current, err := c.getJob(ctx, link)
		if err != nil {
			return "", err
		}
		if current == nil {
			return "", fmt.Errorf("craig %s: job disappeared", opGetJob)
		}
		switch current.Status {
		case jobStatusComplete:
			return current.OutputFileName, nil
		case jobStatusError, jobStatusCancelled:
			return "", fmt.Errorf("craig %s: job ended with status %s", opGetJob, current.Status)
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("craig wait for job: %w", ctx.Err())
		case <-time.After(c.pollInterval):
		}
	}
}

func (c *Client) getJob(ctx context.Context, link Link) (*job, error) {
	var resp jobResponse
	if err := c.getJSON(ctx, opGetJob, c.recordingURL(link, jobPath), &resp); err != nil {
		return nil, err
	}
	return resp.Job, nil
}

func (c *Client) createJob(ctx context.Context, link Link) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.recordingURL(link, jobPath), strings.NewReader(createJobBody))
	if err != nil {
		return fmt.Errorf("craig %s: build request: %w", opCreateJob, stripURL(err))
	}
	req.Header.Set("Content-Type", jsonContentType)
	var resp jobResponse
	return c.doJSON(opCreateJob, req, &resp)
}

// saveArchive は出力ファイルを dir に保存し、そのパスを返す。ファイル名は秘密なので、保存先の名前には使わない。
func (c *Client) saveArchive(ctx context.Context, fileName, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+downloadPath+url.PathEscape(fileName), nil)
	if err != nil {
		return "", fmt.Errorf("craig %s: build request: %w", opDownload, stripURL(err))
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("craig %s: %w", opDownload, stripURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("craig %s: status %d", opDownload, resp.StatusCode)
	}
	return writeTempFile(dir, resp.Body)
}

// writeTempFile は body を dir の一時ファイルに書く。失敗したらファイルを消す。
func writeTempFile(dir string, body io.Reader) (string, error) {
	f, err := os.CreateTemp(dir, archivePattern)
	if err != nil {
		return "", fmt.Errorf("craig %s: create archive file: %w", opDownload, err)
	}
	_, copyErr := io.Copy(f, body)
	closeErr := f.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("craig %s: save archive: %w", opDownload, stripURL(err))
	}
	return f.Name(), nil
}
