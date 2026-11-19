// Package summarizetest は summarize のテスト用の fake を提供する。
package summarizetest

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"discord-ai-assistant/internal/summarize"
)

// History は summarize.HistoryReader のインメモリ実装。
type History struct {
	Messages []summarize.Message // チャンネルの投稿（順不同）
	Err      error               // nil でなければ ReadBefore はこれを返す
	Reads    int                 // ReadBefore が呼ばれた回数
}

var _ summarize.HistoryReader = (*History)(nil)

// ReadBefore は before より前の投稿を新しい順に最大 summarize.PageSize 件返す。
func (h *History) ReadBefore(_ context.Context, _, before snowflake.ID) ([]summarize.Message, error) {
	h.Reads++
	if h.Err != nil {
		return nil, h.Err
	}
	var older []summarize.Message
	for _, m := range h.Messages {
		if m.ID < before {
			older = append(older, m)
		}
	}
	slices.SortFunc(older, func(a, b summarize.Message) int { return cmp.Compare(b.ID, a.ID) })
	return older[:min(len(older), summarize.PageSize)], nil
}

// Post は sentAt に author が投稿した、利用者による通常の投稿を作る。ID は sentAt から作る。
func Post(sentAt time.Time, author, content string) summarize.Message {
	return summarize.Message{ID: snowflake.New(sentAt), SentAt: sentAt, AuthorName: author, Content: content}
}
