package translate

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// AttachmentFetcher は添付ファイルの URL から中身を取得する。
type AttachmentFetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

type httpAttachmentFetcher struct {
	client *http.Client
}

var _ AttachmentFetcher = (*httpAttachmentFetcher)(nil)

func NewHTTPAttachmentFetcher(client *http.Client) AttachmentFetcher {
	return &httpAttachmentFetcher{client: client}
}

func (f *httpAttachmentFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build attachment request: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch attachment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch attachment: unexpected status %d", resp.StatusCode)
	}
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	return content, nil
}
