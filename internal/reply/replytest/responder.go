// Package replytest は reply.Responder の fake を提供する。
package replytest

import (
	"context"

	"discord-ai-assistant/internal/command"
)

// Call は Respond が受けた引数 1 回分。
type Call struct {
	Invocation command.Invocation
	Result     command.Result
}

// Responder は受けた呼び出しを順に記録し、Errs の n 番目を n 番目の呼び出しで返す。
// 並行には呼ばれない前提で排他しない。
type Responder struct {
	Errs  []error // n 番目の呼び出しで返すエラー。足りなければ nil
	Calls []Call
}

func (r *Responder) Respond(_ context.Context, inv command.Invocation, result command.Result) error {
	n := len(r.Calls)
	r.Calls = append(r.Calls, Call{Invocation: inv, Result: result})
	if n < len(r.Errs) {
		return r.Errs[n]
	}
	return nil
}
