package craig_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"discord-ai-assistant/internal/voicesummary/craig"
	"discord-ai-assistant/test/craigfake"
)

const (
	recordingID  = "AbCdEfGhIjKl"
	recordingKey = "SecretKey123"
	guildID      = "1100000000000000001"
)

var startedAt = time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)

var link = craig.Link{RecordingID: recordingID, Key: recordingKey}

// newClient は craigfake に向けた Client を作る。
func newClient(t *testing.T) (*craig.Client, *craigfake.Server, *httptest.Server) {
	t.Helper()
	fake := craigfake.New()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return craig.NewClient(srv.URL, srv.Client()), fake, srv
}

// meeting は 3 人（うち 1 人は表示名なし、1 人は bot）が 90.5 秒話した録音。
func meeting() craigfake.Recording {
	return craigfake.Recording{
		ID: recordingID, Key: recordingKey, GuildID: guildID, StartTime: startedAt, DurationSeconds: 90.5,
		Users: []craigfake.User{
			{Track: 1, Username: "sato", GlobalName: "佐藤"},
			{Track: 2, Username: "tanaka"},
			{Track: 3, Username: "music-bot", GlobalName: "音楽", Bot: true},
		},
		Tracks: map[int][]byte{1: []byte("flac-1"), 2: []byte("flac-2"), 3: []byte("flac-3")},
	}
}

// 条件: 3 人の録音（表示名あり、表示名なし、bot）がある。
// 内容: サーバ、開始時刻、duration の秒数の長さ、録音中か、話者（表示名を優先した名前と bot か）を返す。
func TestClientDescribe_Recording_ReturnsRecordingWithSpeakers(t *testing.T) {
	client, fake, _ := newClient(t)
	fake.AddRecording(meeting())

	got, err := client.Describe(context.Background(), link)

	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	want := craig.Recording{
		GuildID: guildID, StartedAt: startedAt, Duration: 90*time.Second + 500*time.Millisecond,
		Speakers: []craig.Speaker{{Track: 1, Name: "佐藤"}, {Track: 2, Name: "tanaka"}, {Track: 3, Name: "音楽", Bot: true}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Describe =\n%+v\nwant\n%+v", got, want)
	}
}

// 条件: 録音中の録音がある。
// 内容: Live が真になる。
func TestClientDescribe_LiveRecording_ReturnsLive(t *testing.T) {
	client, fake, _ := newClient(t)
	rec := meeting()
	rec.Live = true
	fake.AddRecording(rec)

	got, err := client.Describe(context.Background(), link)

	if err != nil || !got.Live {
		t.Errorf("Describe = %+v, %v; want Live", got, err)
	}
}

// 条件: Craig が録音の不在・鍵の誤りを表すエラーを返す。
// 内容: ErrRecordingUnavailable を返す。
func TestClientDescribe_UnavailableRecording_ReturnsErrRecordingUnavailable(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
	}{
		{"鍵がない", http.StatusBadRequest, "no_key"},
		{"鍵が違う", http.StatusUnauthorized, "invalid_key"},
		{"録音がない", http.StatusNotFound, "no_rec"},
		{"削除された", http.StatusNotFound, "recording_deleted"},
		{"データがない", http.StatusNotFound, "rec_no_data"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, fake, _ := newClient(t)
			fake.FailWith(tt.status, tt.code)

			_, err := client.Describe(context.Background(), link)

			if !errors.Is(err, craig.ErrRecordingUnavailable) {
				t.Errorf("err = %v, want %v", err, craig.ErrRecordingUnavailable)
			}
		})
	}
}

// 条件: 鍵が録音と一致しない（fake が 401 invalid_key を返す）。
// 内容: ErrRecordingUnavailable を返す。
func TestClientDescribe_WrongKey_ReturnsErrRecordingUnavailable(t *testing.T) {
	client, fake, _ := newClient(t)
	fake.AddRecording(meeting())

	_, err := client.Describe(context.Background(), craig.Link{RecordingID: recordingID, Key: "WrongKey"})

	if !errors.Is(err, craig.ErrRecordingUnavailable) {
		t.Errorf("err = %v, want %v", err, craig.ErrRecordingUnavailable)
	}
}

// 条件: Craig が 429 rate_limited（利用の制限）または 503 kitchen_down（サーバの不調）を返す。
// 内容: ErrRecordingUnavailable ではない、操作と status とコードだけのエラーを返す（URL・鍵・録音 ID を含めない）。
func TestClientDescribe_RateLimitOrServerError_ReturnsStatusAndCodeOnly(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		want   string
	}{
		{"利用が制限された", http.StatusTooManyRequests, "rate_limited", `craig describe recording: status 429, code "rate_limited"`},
		{"サーバが不調", http.StatusServiceUnavailable, "kitchen_down", `craig describe recording: status 503, code "kitchen_down"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, fake, srv := newClient(t)
			fake.FailWith(tt.status, tt.code)

			_, err := client.Describe(context.Background(), link)

			if err == nil || err.Error() != tt.want || errors.Is(err, craig.ErrRecordingUnavailable) {
				t.Errorf("err = %v, want %q (not %v)", err, tt.want, craig.ErrRecordingUnavailable)
			}
			assertNoSecrets(t, err, srv.URL)
		})
	}
}

// 条件: Craig に接続できない（サーバを止めてある）。
// 内容: エラー文に URL・鍵・録音 ID を含めない。
func TestClientDescribe_ConnectionFailure_OmitsURLAndSecrets(t *testing.T) {
	client, _, srv := newClient(t)
	srv.Close()

	_, err := client.Describe(context.Background(), link)

	assertNoSecrets(t, err, srv.URL)
}

// assertNoSecrets は、err があり、その文に URL・鍵・録音 ID が無いことを確かめる。
func assertNoSecrets(t *testing.T, err error, baseURL string) {
	t.Helper()
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	for _, secret := range []string{baseURL, recordingKey, recordingID} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("err = %q, contains %q", err, secret)
		}
	}
}

// 条件: baseURL の末尾に / がある。
// 内容: 末尾の / が無いときと同じ URL を呼び、録音の情報を返す。
func TestNewClient_BaseURLWithTrailingSlash_DescribesRecording(t *testing.T) {
	fake := craigfake.New()
	fake.AddRecording(meeting())
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	client := craig.NewClient(srv.URL+"/", srv.Client())

	got, err := client.Describe(context.Background(), link)

	if err != nil || got.GuildID != guildID {
		t.Errorf("Describe = %+v, %v; want GuildID %s", got, err, guildID)
	}
}
