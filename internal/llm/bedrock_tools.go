package llm

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

const (
	schemaKeyType        = "type"
	schemaKeyProperties  = "properties"
	schemaKeyRequired    = "required"
	schemaKeyDescription = "description"
	schemaTypeObject     = "object"
	schemaTypeString     = "string"
)

// ErrInvalidToolUse は、モデルが求めたツールの呼び出しを読み取れなかったことを表す。
var ErrInvalidToolUse = errors.New("invalid tool use")

var _ ToolGenerator = (*BedrockGenerator)(nil)

// GenerateWithTools は、モデルがツールを求めるたびに実行して結果を返し、最後の回答を返す。
func (g *BedrockGenerator) GenerateWithTools(ctx context.Context, req ToolRequest) (Result, error) {
	messages := []types.Message{userTextMessage(req.UserText)}
	for range req.MaxTurns {
		out, err := g.converseWithTools(ctx, req, messages)
		if err != nil {
			return Result{}, err
		}
		if out.StopReason != types.StopReasonToolUse {
			return resultOf(out)
		}
		exchange, err := runTools(ctx, out, req.Run)
		if err != nil {
			return Result{}, err
		}
		messages = append(messages, exchange...)
	}
	return Result{}, ErrToolTurnsExceeded
}

func userTextMessage(text string) types.Message {
	return types.Message{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: text}}}
}

func (g *BedrockGenerator) converseWithTools(ctx context.Context, req ToolRequest, messages []types.Message) (*bedrockruntime.ConverseOutput, error) {
	out, err := g.client.Converse(ctx, &bedrockruntime.ConverseInput{
		ModelId:         aws.String(g.modelID),
		System:          []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: req.System}},
		Messages:        messages,
		InferenceConfig: &types.InferenceConfiguration{MaxTokens: aws.Int32(req.MaxOutputTokens)},
		ToolConfig:      &types.ToolConfiguration{Tools: toolSpecs(req.Tools)},
	})
	if err != nil {
		return nil, fmt.Errorf("bedrock converse: %w", err)
	}
	return out, nil
}

func toolSpecs(tools []Tool) []types.Tool {
	specs := make([]types.Tool, 0, len(tools))
	for _, tool := range tools {
		specs = append(specs, &types.ToolMemberToolSpec{Value: types.ToolSpecification{
			Name:        aws.String(tool.Name),
			Description: aws.String(tool.Description),
			InputSchema: &types.ToolInputSchemaMemberJson{Value: document.NewLazyDocument(inputSchema(tool.Parameters))},
		}})
	}
	return specs
}

func inputSchema(parameters []ToolParameter) map[string]any {
	properties := map[string]any{}
	required := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		properties[parameter.Name] = map[string]any{
			schemaKeyType:        schemaTypeString,
			schemaKeyDescription: parameter.Description,
		}
		required = append(required, parameter.Name)
	}
	return map[string]any{
		schemaKeyType:       schemaTypeObject,
		schemaKeyProperties: properties,
		schemaKeyRequired:   required,
	}
}

// runTools は、応答の中のツールの呼び出しを順に実行し、次に送る 2 つのメッセージ（応答そのものと、結果）を返す。
func runTools(ctx context.Context, out *bedrockruntime.ConverseOutput, run ToolRunner) ([]types.Message, error) {
	message, ok := out.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return nil, ErrInvalidToolUse
	}
	var results []types.ContentBlock
	for _, block := range message.Value.Content {
		use, ok := block.(*types.ContentBlockMemberToolUse)
		if !ok {
			continue
		}
		call, err := toolCallOf(use.Value)
		if err != nil {
			return nil, err
		}
		results = append(results, toolResultBlock(use.Value.ToolUseId, run(ctx, call)))
	}
	if len(results) == 0 {
		return nil, ErrInvalidToolUse
	}
	return []types.Message{message.Value, {Role: types.ConversationRoleUser, Content: results}}, nil
}

func toolCallOf(use types.ToolUseBlock) (ToolCall, error) {
	if use.Input == nil {
		return ToolCall{}, ErrInvalidToolUse
	}
	var input map[string]string
	if err := use.Input.UnmarshalSmithyDocument(&input); err != nil {
		return ToolCall{}, fmt.Errorf("%w: %v", ErrInvalidToolUse, err)
	}
	return ToolCall{Name: aws.ToString(use.Name), Input: input}, nil
}

func toolResultBlock(toolUseID *string, output ToolOutput) types.ContentBlock {
	status := types.ToolResultStatusSuccess
	if output.Failed {
		status = types.ToolResultStatusError
	}
	return &types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{
		ToolUseId: toolUseID,
		Content:   []types.ToolResultContentBlock{&types.ToolResultContentBlockMemberText{Value: output.Text}},
		Status:    status,
	}}
}
