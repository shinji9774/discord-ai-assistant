// Package llmtest は llm.TextGenerator の fake を提供する。
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
}

func (g *Generator) Generate(ctx context.Context, req llm.Request) (llm.Result, error) {
	g.Requests = append(g.Requests, req)
	if g.Block {
		<-ctx.Done()
		return llm.Result{}, ctx.Err()
	}
	return g.Result, g.Err
}
