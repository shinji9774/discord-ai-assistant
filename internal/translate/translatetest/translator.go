// Package translatetest は translate.Translator と translate.AttachmentFetcher の fake を提供する。
package translatetest

import (
	"context"

	"discord-ai-assistant/internal/translate"
)

// Call は Translator が受けた文章の翻訳依頼。
type Call struct {
	Text   string
	Target translate.Language
}

// DocumentCall は Translator が受けたファイルの翻訳依頼。
type DocumentCall struct {
	Document translate.Document
	Target   translate.Language
}

// Translator は決められた結果を返し、受けた依頼を受けた順に記録する。
// 並行には呼ばれない前提で排他しない。
type Translator struct {
	Result         string
	Err            error
	Calls          []Call
	DocumentResult []byte
	DocumentErr    error
	DocumentCalls  []DocumentCall
}

func (f *Translator) TranslateText(_ context.Context, text string, target translate.Language) (string, error) {
	f.Calls = append(f.Calls, Call{Text: text, Target: target})
	return f.Result, f.Err
}

func (f *Translator) TranslateDocument(_ context.Context, doc translate.Document, target translate.Language) ([]byte, error) {
	f.DocumentCalls = append(f.DocumentCalls, DocumentCall{Document: doc, Target: target})
	return f.DocumentResult, f.DocumentErr
}

// AttachmentFetcher は決められた中身を返し、受けた URL を受けた順に記録する。
// 並行には呼ばれない前提で排他しない。
type AttachmentFetcher struct {
	Content []byte
	Err     error
	URLs    []string
}

func (f *AttachmentFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	f.URLs = append(f.URLs, url)
	return f.Content, f.Err
}
