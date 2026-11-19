// Package websearchtest は websearch.Searcher の fake を提供する。
package websearchtest

import (
	"context"

	"discord-ai-assistant/internal/websearch"
)

// Response は、1 回の検索に返す結果。
type Response struct {
	Pages []websearch.Page
	Err   error
}

// Call は、受けた 1 回の検索。
type Call struct {
	Query      string
	MaxResults int32
}

// Searcher は、呼ばれた順に Responses を返す。使い切ったら、結果なしを返す。
type Searcher struct {
	Responses []Response
	Calls     []Call
}

var _ websearch.Searcher = (*Searcher)(nil)

func (s *Searcher) Search(_ context.Context, query string, maxResults int32) ([]websearch.Page, error) {
	s.Calls = append(s.Calls, Call{Query: query, MaxResults: maxResults})
	index := len(s.Calls) - 1
	if index >= len(s.Responses) {
		return nil, nil
	}
	return s.Responses[index].Pages, s.Responses[index].Err
}
