package llmtest

import (
	"context"

	"discord-ai-assistant/internal/llm"
)

var _ llm.ToolGenerator = (*Generator)(nil)

// GenerateWithTools は依頼を記録し、ToolCalls の順にツールを実行してから、Result と Err を返す。
func (g *Generator) GenerateWithTools(ctx context.Context, req llm.ToolRequest) (llm.Result, error) {
	run := req.Run
	req.Run = nil
	g.ToolRequests = append(g.ToolRequests, req)
	if g.Block {
		<-ctx.Done()
		return llm.Result{}, ctx.Err()
	}
	for _, call := range g.ToolCalls {
		g.ToolOutputs = append(g.ToolOutputs, run(ctx, call))
	}
	return g.Result, g.Err
}
