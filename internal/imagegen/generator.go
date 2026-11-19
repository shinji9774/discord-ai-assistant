// Package imagegen は /image コマンドと、画像を生成するモデルへの契約を提供する。
package imagegen

import (
	"context"
	"errors"
)

// ErrContentFiltered は、モデルが内容を理由に生成を拒んだことを表す。
var ErrContentFiltered = errors.New("imagegen: content filtered by the model")

// Prompt は画像を生成するモデルへ渡す、英語の指示文と避けたい要素。
type Prompt struct {
	Positive string
	Negative string // 空のときは、利用者が避けたい要素を指定していない
}

// Generator は PNG 画像を 1 枚返す。
type Generator interface {
	FromText(ctx context.Context, prompt Prompt, aspectRatio string) ([]byte, error)
	// FromSketch の縦横比は元の画像に従う。source は PNG または JPEG。
	FromSketch(ctx context.Context, prompt Prompt, source []byte) ([]byte, error)
}
