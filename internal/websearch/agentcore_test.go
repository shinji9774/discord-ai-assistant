package websearch_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"

	"discord-ai-assistant/internal/websearch"
	"discord-ai-assistant/test/websearchfake"
)

const (
	testRegion       = "ap-northeast-1"
	searchQuery      = "Go 最新版"
	resultsPerSearch = 5
	maxQueryRunes    = 200
	webSearchToolID  = "web-search.v1"
	multibyteRune    = "あ"
)

func newSearcher(t *testing.T) (*websearch.AgentCoreSearcher, *websearchfake.Server) {
	t.Helper()
	fake := websearchfake.New()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := bedrockagentcore.New(bedrockagentcore.Options{
		Region:           testRegion,
		BaseEndpoint:     &server.URL,
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		RetryMaxAttempts: 1,
	})
	return websearch.NewAgentCoreSearcher(client), fake
}

// 条件: 検索文と件数を指定して検索する。
// 内容: 検索サービスに、Web 検索ツールの ID・検索文・件数が届く。
func TestSearch_Query_SendsQueryCountAndToolID(t *testing.T) {
	searcher, fake := newSearcher(t)

	_, err := searcher.Search(context.Background(), searchQuery, resultsPerSearch)

	want := []websearchfake.Request{{WebSearchID: webSearchToolID, Query: searchQuery, MaxResults: resultsPerSearch}}
	if got := fake.Requests(); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %+v, %v, want %+v", got, err, want)
	}
}

// 条件: 検索結果に、URL が無いもの、http・https でない URL のもの、タイトルが無いものが混ざっている。
// 内容: http か https の URL を持つページだけが返る。タイトルが無いページは、URL がタイトルになる。
func TestSearch_ResultsWithoutWebURL_DropsThemAndFillsMissingTitle(t *testing.T) {
	searcher, fake := newSearcher(t)
	fake.RespondWith(
		websearchfake.Result{Title: "A", URL: "https://a.example/", Text: "a"},
		websearchfake.Result{Title: "URL なし", Text: "x"},
		websearchfake.Result{Title: "FTP", URL: "ftp://b.example/", Text: "y"},
		websearchfake.Result{URL: "https://c.example/", Text: "c"},
	)

	got, err := searcher.Search(context.Background(), searchQuery, resultsPerSearch)

	want := []websearch.Page{
		{Title: "A", URL: "https://a.example/", Text: "a"},
		{Title: "https://c.example/", URL: "https://c.example/", Text: "c"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Search() = %+v, %v, want %+v", got, err, want)
	}
}

// 条件: 検索サービスの上限（200 文字）より 1 文字長い検索文で検索する。
// 内容: 先頭の 200 文字だけが、検索サービスに届く。
func TestSearch_QueryOverLimit_SendsFirst200Runes(t *testing.T) {
	searcher, fake := newSearcher(t)
	longQuery := strings.Repeat(multibyteRune, maxQueryRunes+1)

	_, err := searcher.Search(context.Background(), longQuery, resultsPerSearch)

	want := []websearchfake.Request{{WebSearchID: webSearchToolID, Query: strings.Repeat(multibyteRune, maxQueryRunes), MaxResults: resultsPerSearch}}
	if got := fake.Requests(); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %+v, %v, want %+v", got, err, want)
	}
}

// 条件: 検索サービスが失敗を返す。
// 内容: エラーが返り、ページは返らない。
func TestSearch_ServiceFails_ReturnsError(t *testing.T) {
	searcher, fake := newSearcher(t)
	fake.FailWith(http.StatusBadRequest)

	got, err := searcher.Search(context.Background(), searchQuery, resultsPerSearch)

	if err == nil || got != nil {
		t.Errorf("Search() = %+v, %v, want nil and an error", got, err)
	}
}

// 条件: 空白だけの検索文で検索する。
// 内容: ErrEmptyQuery が返り、検索サービスには依頼しない。
func TestSearch_BlankQuery_ReturnsErrEmptyQueryWithoutRequest(t *testing.T) {
	searcher, fake := newSearcher(t)

	_, err := searcher.Search(context.Background(), " \n ", resultsPerSearch)

	if requests := fake.Requests(); !errors.Is(err, websearch.ErrEmptyQuery) || len(requests) != 0 {
		t.Errorf("Search() error = %v, requests = %+v, want ErrEmptyQuery and no requests", err, requests)
	}
}

// 条件: 検索結果が 1 件も無い。
// 内容: 空の結果が、エラーなしで返る。
func TestSearch_NoResults_ReturnsEmptyWithoutError(t *testing.T) {
	searcher, _ := newSearcher(t)

	got, err := searcher.Search(context.Background(), searchQuery, resultsPerSearch)

	if err != nil || len(got) != 0 {
		t.Errorf("Search() = %+v, %v, want empty and no error", got, err)
	}
}
