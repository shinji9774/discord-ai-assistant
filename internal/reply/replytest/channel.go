package replytest

import (
	"context"

	"discord-ai-assistant/internal/reply"
)

var _ reply.ChannelPoster = (*ChannelPoster)(nil)

// ChannelPoster は受けた投稿を順に記録し、Errs の n 番目を n 番目の呼び出しで返す。
// 並行には呼ばれない前提で排他しない。
type ChannelPoster struct {
	Errs  []error // n 番目の呼び出しで返すエラー。足りなければ nil
	Posts []reply.ChannelPost
}

func (p *ChannelPoster) Post(_ context.Context, post reply.ChannelPost) error {
	n := len(p.Posts)
	p.Posts = append(p.Posts, post)
	if n < len(p.Errs) {
		return p.Errs[n]
	}
	return nil
}
