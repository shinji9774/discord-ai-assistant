package discordfake_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"discord-ai-assistant/test/discordfake"
)

const postChannelID = "1300000000000000001"

// postChannelMessage は fake に、ファイルなしの JSON の投稿（POST /channels/{id}/messages）を bot トークンつきで送る。
func postChannelMessage(t *testing.T, fake *discordfake.Server, body string) {
	t.Helper()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v10/channels/"+postChannelID+"/messages", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot token1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", resp.StatusCode)
	}
}

// 条件: 依頼者 1 人だけを通知先にした投稿を 1 件送る。
// 内容: WaitForChannelMessage が、チャンネル ID・認可ヘッダ・本文・通知先・空の添付を記録した 1 件を返す。
func TestServer_PostChannelMessage_WaitReturnsRecordedMessage(t *testing.T) {
	fake := discordfake.New()
	postChannelMessage(t, fake, `{"content":"<@1600000000000000001>\n要約です","allowed_mentions":{"parse":[],"roles":[],"users":["1600000000000000001"]}}`)

	got, ok := fake.WaitForChannelMessage(time.Second)

	want := discordfake.ChannelMessage{
		ChannelID:      postChannelID,
		Authorization:  "Bot token1",
		Content:        "<@1600000000000000001>\n要約です",
		AllowedUserIDs: []string{"1600000000000000001"},
		Files:          map[string]string{},
	}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("WaitForChannelMessage() = %+v, %v, want %+v, true", got, ok, want)
	}
}

// 条件: 投稿を 1 件送ったあとで Reset する。
// 内容: 記録した投稿が消え、ChannelMessages が空になる。
func TestServer_ResetAfterChannelMessage_ClearsChannelMessages(t *testing.T) {
	fake := discordfake.New()
	postChannelMessage(t, fake, `{"content":"要約です"}`)

	fake.Reset()

	if got := fake.ChannelMessages(); len(got) != 0 {
		t.Errorf("ChannelMessages() = %+v, want empty", got)
	}
}
