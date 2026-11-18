package translate_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	awstranslate "github.com/aws/aws-sdk-go-v2/service/translate"

	"discord-ai-assistant/internal/translate"
	"discord-ai-assistant/test/translatefake"
)

const testRegion = "ap-northeast-1"

var english = translate.Language{Name: "英語", Code: "en"}

// newAmazonTranslator は fake サーバに向けた Amazon Translate の Translator を作る。
func newAmazonTranslator(t *testing.T) (translate.Translator, *translatefake.Server) {
	t.Helper()
	fake := translatefake.New()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := awstranslate.New(awstranslate.Options{
		Region:           testRegion,
		BaseEndpoint:     &server.URL,
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		RetryMaxAttempts: 1,
	})
	return translate.NewAmazonTranslator(client), fake
}

// 条件: Amazon Translate が「Hello」を返す。
// 内容: 訳文がそのまま返る。
func TestTranslateText_NormalResponse_ReturnsTranslatedText(t *testing.T) {
	translator, fake := newAmazonTranslator(t)
	fake.RespondWith("Hello")

	got, err := translator.TranslateText(context.Background(), "こんにちは", english)

	if err != nil {
		t.Fatalf("TranslateText() error = %v", err)
	}
	if want := "Hello"; got != want {
		t.Errorf("TranslateText() = %q, want %q", got, want)
	}
}

// 条件: 英語を翻訳先にして翻訳する。
// 内容: 翻訳元は auto、翻訳先は en の依頼が 1 件送られる。
func TestTranslateText_TargetEnglish_SendsAutoSourceAndTargetCode(t *testing.T) {
	translator, fake := newAmazonTranslator(t)

	_, _ = translator.TranslateText(context.Background(), "こんにちは", english)

	want := []translatefake.Request{{Text: "こんにちは", SourceLanguageCode: "auto", TargetLanguageCode: "en"}}
	if got := fake.Requests(); !reflect.DeepEqual(got, want) {
		t.Errorf("Requests() = %+v, want %+v", got, want)
	}
}

// 条件: Amazon Translate が 500 で失敗する。
// 内容: エラーが返る。
func TestTranslateText_ServiceFails_ReturnsError(t *testing.T) {
	translator, fake := newAmazonTranslator(t)
	fake.FailWith(http.StatusInternalServerError)

	_, err := translator.TranslateText(context.Background(), "こんにちは", english)

	if err == nil {
		t.Error("TranslateText() error = nil, want error")
	}
}

// 条件: Amazon Translate が「Hello」を返す。
// 内容: 翻訳済みファイルの中身がそのまま返る。
func TestTranslateDocument_NormalResponse_ReturnsTranslatedContent(t *testing.T) {
	translator, fake := newAmazonTranslator(t)
	fake.RespondWithDocument("Hello")
	doc := translate.Document{Content: []byte("こんにちは"), ContentType: "text/plain"}

	got, err := translator.TranslateDocument(context.Background(), doc, english)

	if err != nil {
		t.Fatalf("TranslateDocument() error = %v", err)
	}
	if want := []byte("Hello"); !reflect.DeepEqual(got, want) {
		t.Errorf("TranslateDocument() = %q, want %q", got, want)
	}
}

// 条件: 英語を翻訳先にして text/plain のファイルを翻訳する。
// 内容: 中身・形式つきで、翻訳元は auto、翻訳先は en の依頼が 1 件送られる。
func TestTranslateDocument_TargetEnglish_SendsDocumentWithAutoSourceAndTargetCode(t *testing.T) {
	translator, fake := newAmazonTranslator(t)
	doc := translate.Document{Content: []byte("こんにちは"), ContentType: "text/plain"}

	_, _ = translator.TranslateDocument(context.Background(), doc, english)

	want := []translatefake.DocumentRequest{{Content: "こんにちは", ContentType: "text/plain", SourceLanguageCode: "auto", TargetLanguageCode: "en"}}
	if got := fake.DocumentRequests(); !reflect.DeepEqual(got, want) {
		t.Errorf("DocumentRequests() = %+v, want %+v", got, want)
	}
}

// 条件: Amazon Translate が 500 で失敗する。
// 内容: エラーが返る。
func TestTranslateDocument_ServiceFails_ReturnsError(t *testing.T) {
	translator, fake := newAmazonTranslator(t)
	fake.FailWith(http.StatusInternalServerError)
	doc := translate.Document{Content: []byte("こんにちは"), ContentType: "text/plain"}

	_, err := translator.TranslateDocument(context.Background(), doc, english)

	if err == nil {
		t.Error("TranslateDocument() error = nil, want error")
	}
}
