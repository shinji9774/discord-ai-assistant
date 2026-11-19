package discordfake_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"discord-ai-assistant/test/discordfake"
)

const historyChannelID = "1300000000000000001"

// getHistory は fake に GET /channels/{id}/messages を送り、status と本文を返す。
func getHistory(t *testing.T, fake *discordfake.Server, query string) (int, []byte) {
	t.Helper()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/api/v10/channels/" + historyChannelID + "/messages?" + query)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, body
}

// 条件: ID が 101、102、103 の 3 件を足し、before=103、limit=1 で読む。
// 内容: before より前のうち、最も新しい 102 の 1 件だけが返る。
func TestServer_GetHistory_ReturnsOlderMessagesNewestFirstUpToLimit(t *testing.T) {
	sentAt := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	author := discordfake.HistoryUser{ID: "2001", Username: "sato", GlobalName: "佐藤"}
	fake := discordfake.New()
	fake.AddHistory(historyChannelID,
		discordfake.HistoryMessage{ID: "101", Content: "一", Timestamp: sentAt, Author: author},
		discordfake.HistoryMessage{ID: "103", Content: "三", Timestamp: sentAt, Author: author},
		discordfake.HistoryMessage{ID: "102", Content: "二", Timestamp: sentAt, Author: author},
	)

	status, body := getHistory(t, fake, "before=103&limit=1")

	var got []discordfake.HistoryMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	want := []discordfake.HistoryMessage{{ID: "102", Content: "二", Timestamp: sentAt, Author: author}}
	if status != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Errorf("status %d, messages %+v; want 200, %+v", status, got, want)
	}
}

// 条件: チャンネルの履歴を 403、コード 50001 で拒む設定にして読む。
// 内容: 403 と、Discord と同じ形のエラー本文（code を含む）が返る。
func TestServer_GetRejectedHistory_ReturnsStatusAndErrorCode(t *testing.T) {
	fake := discordfake.New()
	fake.RejectHistory(historyChannelID, http.StatusForbidden, 50001)

	status, body := getHistory(t, fake, "before=103&limit=100")

	want := `{"message":"rejected by fake","code":50001}`
	if status != http.StatusForbidden || string(body) != want {
		t.Errorf("status %d, body %s; want 403, %s", status, body, want)
	}
}

// 条件: 履歴と拒否を仕込んだ後に Reset する。
// 内容: 履歴は空になり、拒否も消える（200 と空の配列）。
func TestServer_ResetAfterHistorySetup_ReturnsEmptyHistory(t *testing.T) {
	fake := discordfake.New()
	fake.AddHistory(historyChannelID, discordfake.HistoryMessage{ID: "101", Content: "一"})
	fake.RejectHistory(historyChannelID, http.StatusForbidden, 50001)
	fake.Reset()

	status, body := getHistory(t, fake, "before=103&limit=100")

	if status != http.StatusOK || string(body) != "[]\n" {
		t.Errorf("status %d, body %q; want 200, %q", status, body, "[]\n")
	}
}

// 条件: limit に 0 以下の -1 を指定して読む。
// 内容: panic せず 400 が返る。
func TestServer_GetHistoryWithNonPositiveLimit_ReturnsBadRequest(t *testing.T) {
	fake := discordfake.New()

	status, _ := getHistory(t, fake, "before=103&limit=-1")

	if status != http.StatusBadRequest {
		t.Errorf("status %d; want 400", status)
	}
}
