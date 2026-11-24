package discordfake_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"discord-ai-assistant/test/discordfake"
)

const (
	fakeGuildID  = "1100000000000000001"
	memberPath   = "/guilds/" + fakeGuildID + "/members/1400000000000000004"
	nicknamePath = "/guilds/" + fakeGuildID + "/members/@me"
)

type reply struct {
	Status int
	Body   string
}

func call(t *testing.T, fake *discordfake.Server, method, path, body string) reply {
	t.Helper()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	req, err := http.NewRequest(method, srv.URL+"/api/v10"+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return reply{Status: res.StatusCode, Body: strings.TrimSpace(string(got))}
}

// 条件: パスに JSON を登録して GET する（クエリ付き）
// 内容: 登録した JSON を 200 で返す
func TestServerAddJSON_GetRegisteredPath_ReturnsJSON(t *testing.T) {
	fake := discordfake.New()
	fake.AddJSON(memberPath, map[string]string{"nick": "議事録 bot"})

	got := call(t, fake, http.MethodGet, memberPath+"?with_counts=false", "")

	if want := (reply{Status: http.StatusOK, Body: `{"nick":"議事録 bot"}`}); got != want {
		t.Errorf("GET = %+v, want %+v", got, want)
	}
}

// 条件: JSON を登録したパスを、別に拒むよう登録する
// 内容: 拒否が優先され、指定の status と Discord のエラーコードを返す
func TestServerRejectPath_RegisteredPath_ReturnsRejection(t *testing.T) {
	fake := discordfake.New()
	fake.AddJSON(memberPath, map[string]string{"nick": "議事録 bot"})
	fake.RejectPath(http.MethodGet, memberPath, http.StatusForbidden, 50001)

	got := call(t, fake, http.MethodGet, memberPath, "")

	if want := (reply{Status: http.StatusForbidden, Body: `{"message":"rejected by fake","code":50001}`}); got != want {
		t.Errorf("GET = %+v, want %+v", got, want)
	}
}

// 条件: bot の表示名を変え、次に表示名を消す（空文字）
// 内容: 2 回の変更を順に記録し、どちらも 200 を返す
func TestServerNicknameChanges_TwoUpdates_RecordsBothInOrder(t *testing.T) {
	fake := discordfake.New()
	set := call(t, fake, http.MethodPatch, nicknamePath, `{"nick":"[録音中] 議事録 bot"}`)

	cleared := call(t, fake, http.MethodPatch, nicknamePath, `{"nick":""}`)

	want := []discordfake.NicknameChange{{GuildID: fakeGuildID, Nick: "[録音中] 議事録 bot"}, {GuildID: fakeGuildID, Nick: ""}}
	if got := fake.NicknameChanges(); set.Status != http.StatusOK || cleared.Status != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Errorf("statuses = %d, %d; changes = %+v, want %+v", set.Status, cleared.Status, got, want)
	}
}

// 条件: 登録・拒否・表示名の変更のあとで Reset する
// 内容: 登録が消えて GET は 404 になり、変更の記録も空になる
func TestServerReset_AfterRegistrations_ClearsEverything(t *testing.T) {
	fake := discordfake.New()
	fake.AddJSON(memberPath, map[string]string{"nick": "議事録 bot"})
	call(t, fake, http.MethodPatch, nicknamePath, `{"nick":"x"}`)
	fake.Reset()

	got := call(t, fake, http.MethodGet, memberPath, "")

	if got.Status != http.StatusNotFound || len(fake.NicknameChanges()) != 0 {
		t.Errorf("GET = %+v, changes = %+v; want 404 and none", got, fake.NicknameChanges())
	}
}
