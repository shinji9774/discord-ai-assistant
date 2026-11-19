// Package llmtest は llm.TextGenerator と llm.ToolGenerator の fake を提供する。
package llmtest

import (
	"context"

	"discord-ai-assistant/internal/llm"
)

// Generator は決められた結果を返し、受けた依頼を受けた順に記録する。
// 並行には呼ばれない前提で排他しない。
type Generator struct {
	Result   llm.Result
	Err      error
	Block    bool // 真なら ctx が終わるまで待ち、ctx.Err() を返す
	Requests []llm.Request

	// ToolCalls は、GenerateWithTools が順に実行するツールの呼び出し。
	ToolCalls []llm.ToolCall
	// ToolRequests は、GenerateWithTools が受けた依頼（Run は nil にして記録する）。
	ToolRequests []llm.ToolRequest
	// ToolOutputs は、ToolCalls を実行して得た結果。
	ToolOutputs []llm.ToolOutput
}

func (g *Generator) Generate(ctx context.Context, req llm.Request) (llm.Result, error) {
	g.Requests = append(g.Requests, req)
	if g.Block {
		<-ctx.Done()
		return llm.Result{}, ctx.Err()
	}
	return g.Result, g.Err
}
