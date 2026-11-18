package translate

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awstranslate "github.com/aws/aws-sdk-go-v2/service/translate"
	"github.com/aws/aws-sdk-go-v2/service/translate/types"
)

// sourceLanguageAuto は翻訳元の言語を Amazon Translate に判定させる指定。
const sourceLanguageAuto = "auto"

// Document は翻訳するファイルの中身と形式（MIME タイプ）。
type Document struct {
	Content     []byte
	ContentType string
}

// Translator は文章とファイルを指定した言語へ翻訳する。
type Translator interface {
	TranslateText(ctx context.Context, text string, target Language) (string, error)
	// TranslateDocument は入力と同じ形式の翻訳済みファイルの中身を返す。
	TranslateDocument(ctx context.Context, doc Document, target Language) ([]byte, error)
}

// translateClient は Amazon Translate のクライアントのうち使うメソッドだけ。
type translateClient interface {
	TranslateText(ctx context.Context, params *awstranslate.TranslateTextInput, optFns ...func(*awstranslate.Options)) (*awstranslate.TranslateTextOutput, error)
	TranslateDocument(ctx context.Context, params *awstranslate.TranslateDocumentInput, optFns ...func(*awstranslate.Options)) (*awstranslate.TranslateDocumentOutput, error)
}

type amazonTranslator struct {
	client translateClient
}

var _ Translator = (*amazonTranslator)(nil)

func NewAmazonTranslator(client translateClient) Translator {
	return &amazonTranslator{client: client}
}

// TranslateText は翻訳元を自動判定して TranslateText を 1 回だけ呼ぶ。
func (t *amazonTranslator) TranslateText(ctx context.Context, text string, target Language) (string, error) {
	out, err := t.client.TranslateText(ctx, &awstranslate.TranslateTextInput{
		Text:               aws.String(text),
		SourceLanguageCode: aws.String(sourceLanguageAuto),
		TargetLanguageCode: aws.String(target.Code),
	})
	if err != nil {
		return "", fmt.Errorf("amazon translate: %w", err)
	}
	return aws.ToString(out.TranslatedText), nil
}

// TranslateDocument は翻訳元を自動判定して TranslateDocument を 1 回だけ呼ぶ。
func (t *amazonTranslator) TranslateDocument(ctx context.Context, doc Document, target Language) ([]byte, error) {
	out, err := t.client.TranslateDocument(ctx, &awstranslate.TranslateDocumentInput{
		Document:           &types.Document{Content: doc.Content, ContentType: aws.String(doc.ContentType)},
		SourceLanguageCode: aws.String(sourceLanguageAuto),
		TargetLanguageCode: aws.String(target.Code),
	})
	if err != nil {
		return nil, fmt.Errorf("amazon translate document: %w", err)
	}
	if out.TranslatedDocument == nil {
		return nil, fmt.Errorf("amazon translate document: empty response")
	}
	return out.TranslatedDocument.Content, nil
}
