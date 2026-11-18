// Package translate は /translate コマンドを提供する。
package translate

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
)

const (
	commandName = "translate"
	optionText  = "text"
	optionFile  = "file"
	optionTo    = "to"

	// MaxTextLength は text の最大文字数。
	// Amazon Translate の TranslateText は入力を 10,000 バイト（UTF-8）までしか受け付けず、
	// 日本語は 1 文字 3 バイトなので、3000 文字なら約 9,000 バイトで収まる。
	MaxTextLength = 3000

	// MaxFileSize は file の最大バイト数。Amazon Translate の TranslateDocument が受け付ける上限。
	MaxFileSize = 100000

	contentTypeText = "text/plain"
	contentTypeHTML = "text/html"
	contentTypeDocx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

	messageNoInput         = "翻訳する文章（text）かファイル（file）を指定してください。"
	messageBothInputs      = "text と file はどちらか一方だけ指定してください。"
	messageUnsupportedFile = "対応しているファイル形式は .txt / .html / .docx です。"
	messageFileTooLarge    = "ファイルは 100 KB 以下にしてください。"
	messageFileTranslated  = "%s を%sに翻訳しました。"
)

// contentTypesByExtension は拡張子（小文字）から TranslateDocument に渡す形式を引く。
// Discord の content_type は charset 付きなどで揺れるため、拡張子で判定する。
var contentTypesByExtension = map[string]string{
	".txt":  contentTypeText,
	".html": contentTypeHTML,
	".htm":  contentTypeHTML,
	".docx": contentTypeDocx,
}

// Command は文章またはファイルを指定した言語へ翻訳する。
type Command struct {
	translator Translator
	fetcher    AttachmentFetcher
}

func New(translator Translator, fetcher AttachmentFetcher) *Command {
	return &Command{translator: translator, fetcher: fetcher}
}

func (c *Command) Definition() discord.SlashCommandCreate {
	maxLength := MaxTextLength
	return discord.SlashCommandCreate{
		Name:        commandName,
		Description: "文章またはファイルを指定した言語に翻訳します",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{
				Name:        optionText,
				Description: "翻訳する文章",
				MaxLength:   &maxLength,
			},
			discord.ApplicationCommandOptionAttachment{
				Name:        optionFile,
				Description: "翻訳するファイル（.txt / .html / .docx、100 KB まで）",
			},
			discord.ApplicationCommandOptionString{
				Name:        optionTo,
				Description: "翻訳先の言語（省略時は英語）",
				Choices:     languageChoices(),
			},
		},
		Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild},
	}
}

func (c *Command) Execute(ctx context.Context, inv command.Invocation) (command.Result, error) {
	text := inv.Options[optionText]
	file, hasFile := inv.Attachments[optionFile]
	if text == "" && !hasFile {
		return command.Result{Text: messageNoInput}, nil
	}
	if text != "" && hasFile {
		return command.Result{Text: messageBothInputs}, nil
	}
	target, err := targetLanguage(inv.Options[optionTo])
	if err != nil {
		return command.Result{}, err
	}
	if hasFile {
		return c.translateFile(ctx, file, target)
	}
	return c.translateText(ctx, text, target)
}

func (c *Command) translateText(ctx context.Context, text string, target Language) (command.Result, error) {
	translated, err := c.translator.TranslateText(ctx, text, target)
	if err != nil {
		return command.Result{}, err
	}
	return command.Result{Text: command.FormatAnswer(text, translated, false)}, nil
}

func (c *Command) translateFile(ctx context.Context, file command.Attachment, target Language) (command.Result, error) {
	extension := path.Ext(file.Filename)
	contentType, ok := contentTypesByExtension[strings.ToLower(extension)]
	if !ok {
		return command.Result{Text: messageUnsupportedFile}, nil
	}
	if file.Size > MaxFileSize {
		return command.Result{Text: messageFileTooLarge}, nil
	}
	content, err := c.fetcher.Fetch(ctx, file.URL)
	if err != nil {
		return command.Result{}, err
	}
	translated, err := c.translator.TranslateDocument(ctx, Document{Content: content, ContentType: contentType}, target)
	if err != nil {
		return command.Result{}, err
	}
	name := strings.TrimSuffix(file.Filename, extension) + "_" + target.Code + extension
	return command.Result{
		Text:  fmt.Sprintf(messageFileTranslated, file.Filename, target.Name),
		Files: []command.File{{Name: name, Content: translated}},
	}, nil
}

// targetLanguage は to オプションの言語コードを言語にする。省略時は英語。
func targetLanguage(code string) (Language, error) {
	if code == "" {
		return English, nil
	}
	language, ok := languageByCode(code)
	if !ok {
		return Language{}, fmt.Errorf("unsupported target language: %q", code)
	}
	return language, nil
}

func languageChoices() []discord.ApplicationCommandOptionChoiceString {
	choices := make([]discord.ApplicationCommandOptionChoiceString, 0, len(languages))
	for _, language := range languages {
		choices = append(choices, discord.ApplicationCommandOptionChoiceString{Name: language.Name, Value: language.Code})
	}
	return choices
}
