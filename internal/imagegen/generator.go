// Package imagegen は /image コマンドと、画像を生成するモデルへの契約を提供する。
package imagegen

import (
	"context"
	"errors"
)

// ErrContentFiltered は、モデルが内容を理由に生成を拒んだことを表す。
var ErrContentFiltered = errors.New("imagegen: content filtered by the model")

// TextRequest は文章からの生成の依頼。
type TextRequest struct {
	Prompt      string // 英語の指示文
	AspectRatio string
}

// SketchRequest はラフからの生成の依頼。縦横比は元の画像に従うので持たない。
type SketchRequest struct {
	Prompt string // 英語の指示文
	Source []byte // PNG または JPEG
}

// Generator は PNG 画像を 1 枚返す。
type Generator interface {
	FromText(ctx context.Context, req TextRequest) ([]byte, error)
	FromSketch(ctx context.Context, req SketchRequest) ([]byte, error)
}
