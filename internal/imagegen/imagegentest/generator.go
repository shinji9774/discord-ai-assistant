// Package imagegentest は imagegen.Generator の fake を提供する。
package imagegentest

import (
	"context"

	"discord-ai-assistant/internal/imagegen"
)

var _ imagegen.Generator = (*Generator)(nil)

// Generator は決められた画像または失敗を返し、受けた依頼を操作ごとに受けた順に記録する。
// 並行には呼ばれない前提で排他しない。
type Generator struct {
	Image          []byte
	Err            error
	TextRequests   []imagegen.TextRequest
	SketchRequests []imagegen.SketchRequest
}

func (g *Generator) FromText(_ context.Context, req imagegen.TextRequest) ([]byte, error) {
	g.TextRequests = append(g.TextRequests, req)
	return g.Image, g.Err
}

func (g *Generator) FromSketch(_ context.Context, req imagegen.SketchRequest) ([]byte, error) {
	g.SketchRequests = append(g.SketchRequests, req)
	return g.Image, g.Err
}
