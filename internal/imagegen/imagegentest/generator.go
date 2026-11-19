// Package imagegentest は imagegen.Generator の fake を提供する。
package imagegentest

import (
	"context"

	"discord-ai-assistant/internal/imagegen"
)

var _ imagegen.Generator = (*Generator)(nil)

// TextCall は FromText が受けた引数。
type TextCall struct {
	Prompt      imagegen.Prompt
	AspectRatio string
}

// SketchCall は FromSketch が受けた引数。
type SketchCall struct {
	Prompt imagegen.Prompt
	Source []byte
}

// Generator は決められた画像または失敗を返し、受けた呼び出しを操作ごとに受けた順に記録する。
// 並行には呼ばれない前提で排他しない。
type Generator struct {
	Image          []byte
	Err            error
	TextRequests   []TextCall
	SketchRequests []SketchCall
}

func (g *Generator) FromText(_ context.Context, prompt imagegen.Prompt, aspectRatio string) ([]byte, error) {
	g.TextRequests = append(g.TextRequests, TextCall{Prompt: prompt, AspectRatio: aspectRatio})
	return g.Image, g.Err
}

func (g *Generator) FromSketch(_ context.Context, prompt imagegen.Prompt, source []byte) ([]byte, error) {
	g.SketchRequests = append(g.SketchRequests, SketchCall{Prompt: prompt, Source: source})
	return g.Image, g.Err
}
