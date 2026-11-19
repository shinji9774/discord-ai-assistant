// Package websearch は Web 検索の結果を取得する。
package websearch

import "context"

// Page は、検索で見つかった 1 つのページ。
type Page struct {
	Title string
	URL   string
	Text  string
}

// Searcher は Web を検索する。
type Searcher interface {
	Search(ctx context.Context, query string, maxResults int32) ([]Page, error)
}
