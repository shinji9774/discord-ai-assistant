// Package llm は文章生成モデルへの依頼を抽象化する。
package llm

import "context"

// Request は文章生成の依頼。会話履歴は持たない。
type Request struct {
	System          string
	UserText        string
	MaxOutputTokens int32
}

// Result は文章生成の結果。Truncated は出力上限で打ち切られたことを示す。
type Result struct {
	Text      string
	Truncated bool
}

// TextGenerator は 1 回の依頼から文章を生成する。
type TextGenerator interface {
	Generate(ctx context.Context, req Request) (Result, error)
}
