package craigfake_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"discord-ai-assistant/test/craigfake"
)

const (
	recordingID = "AbCdEfGhIjKl"
	key         = "SecretKey123"
	deleteKey   = "DeleteSecret456"
)

type response struct {
	Status int
	Body   string
}

func newServer(t *testing.T) (*craigfake.Server, *httptest.Server) {
	t.Helper()
	fake := craigfake.New()
	fake.AddRecording(craigfake.Recording{ID: recordingID, Key: key, DeleteKey: deleteKey})
	fake.SetJob(recordingID, craigfake.Job{Status: "complete", OutputFileName: "output.flac.zip"})
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return fake, srv
}

func send(t *testing.T, method, url string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	return response{Status: resp.StatusCode, Body: string(buf[:n])}
}

func jobURL(srv *httptest.Server) string {
	return srv.URL + "/api/v1/recordings/" + recordingID + "/job?key=" + key
}

func recordingURL(srv *httptest.Server, deleteValue string) string {
	return srv.URL + "/api/v1/recordings/" + recordingID + "?key=" + key + "&delete=" + deleteValue
}

// 条件: ジョブがある録音のジョブを DELETE する。
// 内容: 200 を返し、ジョブの削除を記録する。
func TestServer_DeleteJob_RecordsDeletion(t *testing.T) {
	fake, srv := newServer(t)

	got := send(t, http.MethodDelete, jobURL(srv))

	want := response{Status: http.StatusOK, Body: "{\"ok\":true}\n"}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(fake.Deletions(), []craigfake.Deletion{{RecordingID: recordingID, Target: "job"}}) {
		t.Errorf("DELETE job = %+v, deletions %+v; want %+v with job deletion", got, fake.Deletions(), want)
	}
}

// 条件: ジョブが無い録音のジョブを DELETE する。
// 内容: 400 no_job を返す。
func TestServer_DeleteJobTwice_ReturnsNoJob(t *testing.T) {
	_, srv := newServer(t)
	send(t, http.MethodDelete, jobURL(srv))

	got := send(t, http.MethodDelete, jobURL(srv))

	want := response{Status: http.StatusBadRequest, Body: "{\"code\":\"no_job\",\"error\":\"no_job\"}\n"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DELETE job = %+v; want %+v", got, want)
	}
}

// 条件: 正しい削除の鍵で録音を DELETE し、もう一度 DELETE する。
// 内容: 1 回目は 200、2 回目は 404 rec_no_data で、削除の記録は 1 件。
func TestServer_DeleteRecordingTwice_SecondReturnsRecNoData(t *testing.T) {
	fake, srv := newServer(t)
	first := send(t, http.MethodDelete, recordingURL(srv, deleteKey))

	second := send(t, http.MethodDelete, recordingURL(srv, deleteKey))

	got := []any{first, second, fake.Deletions()}
	want := []any{
		response{Status: http.StatusOK, Body: "{\"ok\":true}\n"},
		response{Status: http.StatusNotFound, Body: "{\"code\":\"rec_no_data\",\"error\":\"rec_no_data\"}\n"},
		[]craigfake.Deletion{{RecordingID: recordingID, Target: "recording"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v; want %+v", got, want)
	}
}

// 条件: 違う削除の鍵で録音を DELETE する。
// 内容: 401 invalid_delete_key を返し、削除を記録しない。
func TestServer_DeleteRecordingWrongDeleteKey_ReturnsInvalidDeleteKey(t *testing.T) {
	fake, srv := newServer(t)

	got := send(t, http.MethodDelete, recordingURL(srv, "wrong"))

	want := []any{response{Status: http.StatusUnauthorized, Body: "{\"code\":\"invalid_delete_key\",\"error\":\"invalid_delete_key\"}\n"}, []craigfake.Deletion(nil)}
	if !reflect.DeepEqual([]any{got, fake.Deletions()}, want) {
		t.Errorf("got %+v, %+v; want %+v", got, fake.Deletions(), want)
	}
}

// 条件: FailWith を設定して DELETE する。
// 内容: 設定したエラーを返す。
func TestServer_DeleteWithFailure_ReturnsConfiguredError(t *testing.T) {
	fake, srv := newServer(t)
	fake.FailWith(http.StatusServiceUnavailable, "kitchen_down")

	got := send(t, http.MethodDelete, jobURL(srv))

	want := response{Status: http.StatusServiceUnavailable, Body: "{\"code\":\"kitchen_down\",\"error\":\"kitchen_down\"}\n"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DELETE job = %+v; want %+v", got, want)
	}
}

// 条件: 削除を記録した後に Reset する。
// 内容: Deletions が空になる。
func TestServer_Reset_ClearsDeletions(t *testing.T) {
	fake, srv := newServer(t)
	send(t, http.MethodDelete, jobURL(srv))

	fake.Reset()

	if got := fake.Deletions(); len(got) != 0 {
		t.Errorf("Deletions() = %+v; want empty", got)
	}
}
