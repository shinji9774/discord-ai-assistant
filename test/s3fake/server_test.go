package s3fake_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"discord-ai-assistant/test/s3fake"
)

const (
	bucket    = "voice-bucket"
	objectKey = "recordings/g/active.json"
	// otherObjectKey は、FailDelete の対象にしないオブジェクト。
	otherObjectKey = "recordings/g/other.json"
)

type response struct {
	Status int
	ETag   string
}

func send(t *testing.T, srv *httptest.Server, method string, header http.Header) response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+"/"+bucket+"/"+objectKey, strings.NewReader("body"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header = header
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("send %s: %v", method, err)
	}
	defer res.Body.Close()
	return response{Status: res.StatusCode, ETag: res.Header.Get("ETag")}
}

func ifMatch(etag string) http.Header {
	return http.Header{"If-Match": []string{etag}}
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(s3fake.New())
	t.Cleanup(srv.Close)
	return srv
}

// 条件: 同じキーに 2 回書き、それぞれの ETag を条件にして書き直す
// 内容: 最新の ETag なら書け、古い ETag なら 412 になる
func TestServerPut_IfMatch_SucceedsOnlyWithLatestETag(t *testing.T) {
	srv := newServer(t)
	first := send(t, srv, http.MethodPut, http.Header{})
	second := send(t, srv, http.MethodPut, ifMatch(first.ETag))

	stale := send(t, srv, http.MethodPut, ifMatch(first.ETag))

	want := []response{{Status: http.StatusOK, ETag: `"etag-1"`}, {Status: http.StatusOK, ETag: `"etag-2"`}, {Status: http.StatusPreconditionFailed}}
	if got := []response{first, second, stale}; !reflect.DeepEqual(got, want) {
		t.Errorf("responses = %+v, want %+v", got, want)
	}
}

// 条件: 書いたオブジェクトを GET する
// 内容: 書いたときと同じ ETag を返す
func TestServerGet_AfterPut_ReturnsSameETag(t *testing.T) {
	srv := newServer(t)
	put := send(t, srv, http.MethodPut, http.Header{})

	got := send(t, srv, http.MethodGet, http.Header{})

	if want := (response{Status: http.StatusOK, ETag: put.ETag}); got != want {
		t.Errorf("GET = %+v, want %+v", got, want)
	}
}

// 条件: 違う ETag を条件にして消し、次に正しい ETag で消し、最後にもう一度消す
// 内容: 412 → 204 → 404 の順になる（無いオブジェクトへの条件付き削除は 404）
func TestServerDelete_IfMatch_DeletesOnlyMatchingObject(t *testing.T) {
	srv := newServer(t)
	put := send(t, srv, http.MethodPut, http.Header{})
	mismatch := send(t, srv, http.MethodDelete, ifMatch(`"etag-other"`))
	matched := send(t, srv, http.MethodDelete, ifMatch(put.ETag))

	missing := send(t, srv, http.MethodDelete, ifMatch(put.ETag))

	want := []int{http.StatusPreconditionFailed, http.StatusNoContent, http.StatusNotFound}
	if got := []int{mismatch.Status, matched.Status, missing.Status}; !reflect.DeepEqual(got, want) {
		t.Errorf("statuses = %v, want %v", got, want)
	}
}

// 条件: 無いオブジェクトに If-Match を付けて書く
// 内容: 404 になり、オブジェクトは作られない
func TestServerPut_IfMatchOnMissingObject_ReturnsNotFound(t *testing.T) {
	srv := newServer(t)

	got := send(t, srv, http.MethodPut, ifMatch(`"etag-1"`))

	if got.Status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", got.Status, http.StatusNotFound)
	}
}

// 条件: FailDelete したオブジェクトと、していないオブジェクトを消す
// 内容: 指定したほうは 500 で消えずに残り、もう一方は 204 で消える
func TestServerDelete_FailDelete_FailsOnlyTheSpecifiedObject(t *testing.T) {
	fake := s3fake.New()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	fake.PutObject(bucket, objectKey, []byte("body"))
	fake.PutObject(bucket, otherObjectKey, []byte("body"))
	fake.FailDelete(bucket, objectKey)

	failed := deleteObject(t, srv, objectKey)
	deleted := deleteObject(t, srv, otherObjectKey)

	type outcome struct {
		Statuses []int
		Keys     []string
	}
	got := outcome{Statuses: []int{failed, deleted}, Keys: fake.Keys(bucket)}
	want := outcome{Statuses: []int{http.StatusInternalServerError, http.StatusNoContent}, Keys: []string{objectKey}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func deleteObject(t *testing.T, srv *httptest.Server, key string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/"+bucket+"/"+key, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("delete %s: %v", key, err)
	}
	defer res.Body.Close()
	return res.StatusCode
}
