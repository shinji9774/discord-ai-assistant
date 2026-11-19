package ask

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/websearch"
)

const (
	toolNameWebSearch  = "web_search"
	toolParameterQuery = "query"
	queryDescription   = "検索する内容"
	msgSearchFailed    = "web search failed"
	logKeyError        = "error"
	sectionSeparator   = "\n\n"
	pageEntryFormat    = "[%d] %s\nURL: %s\n%s"
)

// searchSession は、1 回の /ask の間の、検索の回数と見つけたページを持つ。質問をまたいで使わない。
type searchSession struct {
	searcher websearch.Searcher
	cfg      WebSearchConfig
	logger   *slog.Logger
	searches int
	failed   bool
	pages    []websearch.Page
}

func newSearchSession(searcher websearch.Searcher, cfg WebSearchConfig, logger *slog.Logger) *searchSession {
	return &searchSession{searcher: searcher, cfg: cfg, logger: logger}
}

// run は、モデルが求めた検索を実行し、モデルへ返す文を作る。
func (s *searchSession) run(ctx context.Context, call llm.ToolCall) llm.ToolOutput {
	if s.searches >= s.cfg.MaxSearches {
		return llm.ToolOutput{Text: s.cfg.LimitReachedMessage}
	}
	s.searches++
	found, err := s.searcher.Search(ctx, call.Input[toolParameterQuery], s.cfg.ResultsPerSearch)
	if err != nil {
		s.failed = true
		s.logger.WarnContext(ctx, msgSearchFailed, logKeyError, err.Error())
		return llm.ToolOutput{Text: s.cfg.SearchFailedMessage, Failed: true}
	}
	if len(found) == 0 {
		return llm.ToolOutput{Text: s.cfg.NoResultsMessage}
	}
	return llm.ToolOutput{Text: s.describe(found)}
}

// describe は、見つけたページを、番号・タイトル・URL・抜粋の形で並べる。
func (s *searchSession) describe(found []websearch.Page) string {
	entries := make([]string, 0, len(found))
	for _, page := range found {
		entries = append(entries, fmt.Sprintf(pageEntryFormat, s.numberOf(page), sourceTitle(page.Title), page.URL, firstRunes(page.Text, s.cfg.MaxResultChars)))
	}
	return strings.Join(entries, sectionSeparator)
}

// numberOf は、ページの番号を返す。同じ URL のページには、同じ番号を返す。
func (s *searchSession) numberOf(page websearch.Page) int {
	for index, known := range s.pages {
		if known.URL == page.URL {
			return index + firstPageNumber
		}
	}
	s.pages = append(s.pages, page)
	return len(s.pages)
}

// answerBody は、回答の下に、検索の注記と出典を足す。
func (s *searchSession) answerBody(answer string) string {
	sections := []string{answer}
	notice := searchNotice(s.searches > 0, s.failed, len(s.pages))
	sources := formatSources(answer, s.pages, s.cfg.FallbackSourceCount)
	for _, section := range []string{notice, sources} {
		if section != "" {
			sections = append(sections, section)
		}
	}
	return strings.Join(sections, sectionSeparator)
}
