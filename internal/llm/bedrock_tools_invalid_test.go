package llm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"discord-ai-assistant/internal/llm"
)

// toolUseOutput は、指定のツール呼び出しブロックだけを含むモデルの応答。
func toolUseOutput(blocks ...types.ContentBlock) *bedrockruntime.ConverseOutput {
	return &bedrockruntime.ConverseOutput{Output: &types.ConverseOutputMemberMessage{
		Value: types.Message{Role: types.ConversationRoleAssistant, Content: blocks},
	}}
}

// 条件: モデルの応答が、メッセージでない／ツール呼び出しを含まない／入力が無い／入力が文字列の対応表でない。
// 内容: いずれも ErrInvalidToolUse が真で、どの検査で外れたかがエラー文で区別できる。
func TestRunTools_InvalidToolUse_WrapsErrInvalidToolUseWithReason(t *testing.T) {
	notStringMap := document.NewLazyDocument(map[string]any{"query": 1})
	tests := []struct {
		name string
		out  *bedrockruntime.ConverseOutput
		want string
	}{
		{"output is not a message", &bedrockruntime.ConverseOutput{}, "invalid tool use: response output is not a message"},
		{"no tool use block", toolUseOutput(&types.ContentBlockMemberText{Value: "text"}), "invalid tool use: stop reason is tool use but no tool use block"},
		{"tool use without input", toolUseOutput(&types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{}}), "invalid tool use: tool use has no input"},
		{"input is not a string map", toolUseOutput(&types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{Input: notStringMap}}), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := llm.RunTools(context.Background(), tt.out, nil)

			got := invalidToolUse{IsExpectedError: errors.Is(err, llm.ErrInvalidToolUse), HasReason: err != nil && strings.HasPrefix(err.Error(), tt.want)}
			if want := (invalidToolUse{IsExpectedError: true, HasReason: true}); got != want {
				t.Errorf("RunTools() = %+v (err %v), want %+v", got, err, want)
			}
		})
	}
}

// invalidToolUse は、不正なツール呼び出しのエラーで観測できる結果。
type invalidToolUse struct {
	IsExpectedError bool
	HasReason       bool
}
