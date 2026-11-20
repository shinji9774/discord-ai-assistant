package imagegen

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
)

const (
	CommandName          = "image"
	OptionPrompt         = "prompt"
	OptionNegativePrompt = "negative_prompt"
	OptionAspectRatio    = "aspect_ratio"
	OptionSource         = "source"

	DefaultAspectRatio = "1:1"
	OutputFileName     = "image.png"

	messageContentFiltered     = "内容が原因で、画像を生成できませんでした。文章または元にする画像を変えて、もう一度実行してください。"
	messageSourceNotAcceptable = "元にする画像を受け付けられませんでした。受け付けられるのは、PNG または JPEG で、10 MB 以下、各辺 64 ピクセル以上、総画素数 25,000,000 以下（例: 5712×4284）、縦横比 1:2.5 から 2.5:1 の画像です。"
)

// 案内の理由と診断値のキー。worker が診断のためにログへ出す。
const (
	reasonSourceNotAcceptable = "source_not_acceptable"
	reasonContentFiltered     = "content_filtered"
	attrKeyRejection          = "rejection"
)

// aspectRatios は aspect_ratio の選択肢。定義の choices と入力の検証が同じ表を使う。
var aspectRatios = []string{"1:1", "16:9", "3:2", "2:3", "9:16"}

// SourceFetcher は元にする画像を取得する。利用側（このパッケージ）が定義する。
type SourceFetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// Command は文章（と任意の元画像）から図を生成する。
type Command struct {
	text    llm.TextGenerator
	images  Generator
	fetcher SourceFetcher
	cfg     Config
}

func New(text llm.TextGenerator, images Generator, fetcher SourceFetcher, cfg Config) *Command {
	return &Command{text: text, images: images, fetcher: fetcher, cfg: cfg}
}

func (c *Command) Definition() discord.SlashCommandCreate {
	maxLength := c.cfg.MaxPromptLength
	return discord.SlashCommandCreate{
		Name:        CommandName,
		Description: "文章から図を生成します",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{
				Name:        OptionPrompt,
				Description: "図にしたい内容の文章",
				Required:    true,
				MaxLength:   &maxLength,
			},
			discord.ApplicationCommandOptionString{
				Name:        OptionNegativePrompt,
				Description: "生成したくない要素（任意）",
				MaxLength:   &maxLength,
			},
			discord.ApplicationCommandOptionString{
				Name:        OptionAspectRatio,
				Description: "図の縦横比（省略時は " + DefaultAspectRatio + "）",
				Choices:     aspectRatioChoices(),
			},
			discord.ApplicationCommandOptionAttachment{
				Name:        OptionSource,
				Description: "元にする画像（PNG または JPEG）",
			},
			command.VisibilityOption(),
		},
		Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild},
	}
}

func aspectRatioChoices() []discord.ApplicationCommandOptionChoiceString {
	choices := make([]discord.ApplicationCommandOptionChoiceString, 0, len(aspectRatios))
	for _, ratio := range aspectRatios {
		choices = append(choices, discord.ApplicationCommandOptionChoiceString{Name: ratio, Value: ratio})
	}
	return choices
}

func (c *Command) Execute(ctx context.Context, inv command.Invocation) (command.Result, error) {
	attachment, hasSource := inv.Attachments[OptionSource]
	// 元画像があるとき、縦横比は読まない（元画像の縦横比で決まる）。
	aspectRatio := ""
	if !hasSource {
		var err error
		aspectRatio, err = aspectRatioOf(inv.Options[OptionAspectRatio])
		if err != nil {
			return command.Result{}, err
		}
	}
	var source []byte
	if hasSource {
		var err error
		source, err = c.prepareSource(ctx, attachment)
		var rejection sourceRejection
		if errors.As(err, &rejection) {
			return command.Guided(messageSourceNotAcceptable, reasonSourceNotAcceptable, slog.String(attrKeyRejection, string(rejection))), nil
		}
		if err != nil {
			return command.Result{}, err
		}
	}
	prompt := inv.Options[OptionPrompt]
	written, err := writePrompt(ctx, c.text, c.cfg.PromptWriter, prompt, inv.Options[OptionNegativePrompt])
	if err != nil {
		return command.Result{}, err
	}
	var image []byte
	if hasSource {
		image, err = c.images.FromSketch(ctx, written, source)
	} else {
		image, err = c.images.FromText(ctx, written, aspectRatio)
	}
	if errors.Is(err, ErrContentFiltered) {
		return command.Guided(messageContentFiltered, reasonContentFiltered), nil
	}
	if err != nil {
		return command.Result{}, err
	}
	return command.Result{Text: prompt, Files: []command.File{{Name: OutputFileName, Content: image}}}, nil
}

// prepareSource は元画像を、大きさの判定 → 取得 → 形式と寸法の判定 → 必要なら縮小の順に用意する。
// 受け付けられないときは、満たさなかった条件（sourceRejection）を返す。
func (c *Command) prepareSource(ctx context.Context, attachment command.Attachment) ([]byte, error) {
	if rejection := checkSourceSize(c.cfg.Source, attachment); rejection != sourceAccepted {
		return nil, rejection
	}
	content, err := c.fetcher.Fetch(ctx, attachment.URL)
	if err != nil {
		return nil, fmt.Errorf("fetch source image: %w", err)
	}
	if rejection := checkSourceImage(c.cfg.Source, content); rejection != sourceAccepted {
		return nil, rejection
	}
	return fitSource(c.cfg.Source, content)
}

// aspectRatioOf は aspect_ratio オプションの値を検証する。省略時は既定の縦横比。
func aspectRatioOf(value string) (string, error) {
	if value == "" {
		return DefaultAspectRatio, nil
	}
	if !slices.Contains(aspectRatios, value) {
		return "", fmt.Errorf("unsupported aspect ratio: %q", value)
	}
	return value, nil
}
