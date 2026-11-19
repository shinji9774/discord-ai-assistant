package llm

import (
	"context"
	"errors"
)

// ErrToolTurnsExceeded は、決めた回数のうちに、モデルが最後の回答を返さなかったことを表す。
var ErrToolTurnsExceeded = errors.New("tool turns exceeded")

// ToolParameter は、ツールが受け取る文字列の引数。
type ToolParameter struct {
	Name        string
	Description string
}

// Tool は、モデルが呼び出せるツールの定義。引数はどれも必須の文字列。
type Tool struct {
	Name        string
	Description string
	Parameters  []ToolParameter
}

// ToolCall は、モデルが求めたツールの呼び出し。
type ToolCall struct {
	Name  string
	Input map[string]string
}

// ToolOutput は、ツールの実行結果としてモデルへ返す文。
type ToolOutput struct {
	Text   string
	Failed bool
}

// ToolRunner はツールを実行する。
type ToolRunner func(ctx context.Context, call ToolCall) ToolOutput

// ToolRequest は、ツールを使える生成の依頼。
type ToolRequest struct {
	Request
	Tools    []Tool
	Run      ToolRunner
	MaxTurns int // モデルを呼ぶ回数の上限
}

// ToolGenerator は、ツールを使いながら文章を生成する。
type ToolGenerator interface {
	GenerateWithTools(ctx context.Context, req ToolRequest) (Result, error)
}
