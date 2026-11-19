package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
)

const (
	webSearchID   = "web-search.v1" // AgentCore が提供する Web 検索ツールの ID
	maxQueryRunes = 200             // 検索サービスが受け付ける検索文の最大文字数
	schemeHTTP    = "http"
	schemeHTTPS   = "https"
)

// ErrEmptyQuery は、検索文が空であることを表す。
var ErrEmptyQuery = errors.New("search query is empty")

// InvokeWebSearchAPI は、AgentCore のクライアントのうち、Web 検索に使う部分。
type InvokeWebSearchAPI interface {
	InvokeWebSearch(ctx context.Context, params *bedrockagentcore.InvokeWebSearchInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeWebSearchOutput, error)
}

// AgentCoreSearcher は、AgentCore Web Search で検索する。
type AgentCoreSearcher struct {
	client InvokeWebSearchAPI
}

var _ Searcher = (*AgentCoreSearcher)(nil)

func NewAgentCoreSearcher(client InvokeWebSearchAPI) *AgentCoreSearcher {
	return &AgentCoreSearcher{client: client}
}

// Search は query で検索し、http か https の URL を持つページだけを返す。
func (s *AgentCoreSearcher) Search(ctx context.Context, query string, maxResults int32) ([]Page, error) {
	searchQuery := firstRunes(strings.TrimSpace(query), maxQueryRunes)
	if searchQuery == "" {
		return nil, ErrEmptyQuery
	}
	out, err := s.client.InvokeWebSearch(ctx, &bedrockagentcore.InvokeWebSearchInput{
		SearchQuery: aws.String(searchQuery),
		WebSearchId: aws.String(webSearchID),
		MaxResults:  aws.Int32(maxResults),
	})
	if err != nil {
		return nil, fmt.Errorf("invoke web search: %w", err)
	}
	return pagesOf(out.Results), nil
}

func pagesOf(results []types.WebSearchResult) []Page {
	pages := make([]Page, 0, len(results))
	for _, result := range results {
		pageURL := aws.ToString(result.Url)
		if !isWebURL(pageURL) {
			continue
		}
		pages = append(pages, Page{Title: titleOrURL(aws.ToString(result.Title), pageURL), URL: pageURL, Text: aws.ToString(result.Text)})
	}
	return pages
}

func isWebURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return (parsed.Scheme == schemeHTTP || parsed.Scheme == schemeHTTPS) && parsed.Host != ""
}

func titleOrURL(title, pageURL string) string {
	if strings.TrimSpace(title) == "" {
		return pageURL
	}
	return title
}

func firstRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
