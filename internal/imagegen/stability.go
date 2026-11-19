package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

const (
	outputFormatPNG = "png"
	contentType     = "application/json"
	// finish_reasons が内容による拒否のときの接頭辞。
	filterReasonPrefix = "Filter reason:"
	// 固定のネガティブプロンプトと利用者分の区切り。
	negativePromptSeparator = ", "
)

// invokeClient は Bedrock Runtime のクライアントのうち使うメソッドだけ。
type invokeClient interface {
	InvokeModel(ctx context.Context, params *bedrockruntime.InvokeModelInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error)
}

// StabilityGenerator は Bedrock の Stability の画像生成モデルを呼ぶ Generator。
type StabilityGenerator struct {
	client invokeClient
	cfg    Config
}

var _ Generator = (*StabilityGenerator)(nil)

func NewStabilityGenerator(client invokeClient, cfg Config) *StabilityGenerator {
	return &StabilityGenerator{client: client, cfg: cfg}
}

type coreRequest struct {
	Prompt         string `json:"prompt"`
	AspectRatio    string `json:"aspect_ratio"`
	NegativePrompt string `json:"negative_prompt"`
	OutputFormat   string `json:"output_format"`
}

type sketchRequest struct {
	Prompt         string `json:"prompt"`
	Image          string `json:"image"`
	NegativePrompt string `json:"negative_prompt"`
	OutputFormat   string `json:"output_format"`
}

type stabilityResponse struct {
	Images        []string  `json:"images"`
	FinishReasons []*string `json:"finish_reasons"`
}

// FromText は Stable Image Core に 1 回だけ生成を依頼する。
func (g *StabilityGenerator) FromText(ctx context.Context, prompt Prompt, aspectRatio string) ([]byte, error) {
	return g.invoke(ctx, g.cfg.CoreModelID, coreRequest{
		Prompt:         prompt.Positive + g.cfg.DiagramStyleSuffix,
		AspectRatio:    aspectRatio,
		NegativePrompt: g.negativePrompt(prompt),
		OutputFormat:   outputFormatPNG,
	})
}

// FromSketch は Control Sketch の推論プロファイルに 1 回だけ生成を依頼する。縦横比は送らない。
func (g *StabilityGenerator) FromSketch(ctx context.Context, prompt Prompt, source []byte) ([]byte, error) {
	return g.invoke(ctx, g.cfg.SketchModelID, sketchRequest{
		Prompt:         prompt.Positive + g.cfg.DiagramStyleSuffix,
		Image:          base64.StdEncoding.EncodeToString(source),
		NegativePrompt: g.negativePrompt(prompt),
		OutputFormat:   outputFormatPNG,
	})
}

// negativePrompt は設定の固定値に、利用者が避けたい要素を足す。
func (g *StabilityGenerator) negativePrompt(prompt Prompt) string {
	if prompt.Negative == "" {
		return g.cfg.NegativePrompt
	}
	return g.cfg.NegativePrompt + negativePromptSeparator + prompt.Negative
}

func (g *StabilityGenerator) invoke(ctx context.Context, modelID string, request any) ([]byte, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("stability: encode request: %w", err)
	}
	out, err := g.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(modelID),
		ContentType: aws.String(contentType),
		Accept:      aws.String(contentType),
		Body:        body,
	})
	if err != nil {
		return nil, fmt.Errorf("stability: invoke model: %w", err)
	}
	return decodeImage(out.Body)
}

// decodeImage は応答の finish_reasons[0] を見て、画像を返すか、内容拒否か通常の失敗にする。
func decodeImage(body []byte) ([]byte, error) {
	var resp stabilityResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("stability: decode response: %w", err)
	}
	if len(resp.FinishReasons) > 0 && resp.FinishReasons[0] != nil {
		reason := *resp.FinishReasons[0]
		if strings.HasPrefix(reason, filterReasonPrefix) {
			return nil, ErrContentFiltered
		}
		return nil, fmt.Errorf("stability: finish reason %q", reason)
	}
	if len(resp.Images) == 0 {
		return nil, errors.New("stability: response has no image")
	}
	image, err := base64.StdEncoding.DecodeString(resp.Images[0])
	if err != nil {
		return nil, fmt.Errorf("stability: decode image: %w", err)
	}
	return image, nil
}
