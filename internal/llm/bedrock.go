package llm

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// BedrockGenerator は Bedrock Converse API で文章を生成する。
type BedrockGenerator struct {
	client  *bedrockruntime.Client
	modelID string
}

func NewBedrockGenerator(client *bedrockruntime.Client, modelID string) *BedrockGenerator {
	return &BedrockGenerator{client: client, modelID: modelID}
}

// Generate は Converse を 1 回だけ呼び、最初のテキストブロックを返す。
func (g *BedrockGenerator) Generate(ctx context.Context, req Request) (Result, error) {
	out, err := g.client.Converse(ctx, &bedrockruntime.ConverseInput{
		ModelId: aws.String(g.modelID),
		System:  []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: req.System}},
		Messages: []types.Message{{
			Role:    types.ConversationRoleUser,
			Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: req.UserText}},
		}},
		InferenceConfig: &types.InferenceConfiguration{MaxTokens: aws.Int32(req.MaxOutputTokens)},
	})
	if err != nil {
		return Result{}, fmt.Errorf("bedrock converse: %w", err)
	}
	text, err := firstText(out)
	if err != nil {
		return Result{}, err
	}
	return Result{Text: text, Truncated: out.StopReason == types.StopReasonMaxTokens}, nil
}

func firstText(out *bedrockruntime.ConverseOutput) (string, error) {
	message, ok := out.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return "", errors.New("bedrock response has no message")
	}
	for _, block := range message.Value.Content {
		if text, ok := block.(*types.ContentBlockMemberText); ok {
			return text.Value, nil
		}
	}
	return "", errors.New("bedrock response has no text block")
}
