package summarize

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// Message は会話履歴の 1 件の投稿。
type Message struct {
	ID         snowflake.ID
	SentAt     time.Time
	AuthorName string // 投稿者の表示名
	FromBot    bool   // bot または webhook の投稿
	System     bool   // 参加通知・ピン留めなどのシステムメッセージ
	InProgress bool   // 応答を生成中（「考え中」）の投稿
	Content    string // ユーザーのメンションを表示名に置き換えた本文
}

// PageSize は 1 回の読み取りで得る投稿の最大件数。Discord の API の上限に合わせる。
const PageSize = 100

// ErrHistoryForbidden は、bot がチャンネルの会話履歴を読めないこと（Discord が 403 を返した）を表す。
var ErrHistoryForbidden = errors.New("bot cannot read the channel history")

// HistoryReader はチャンネルの会話履歴を読む。
type HistoryReader interface {
	// ReadBefore は before より前の投稿を新しい順に最大 PageSize 件返す。
	ReadBefore(ctx context.Context, channelID, before snowflake.ID) ([]Message, error)
}

// Conversation は要約の対象になる投稿。
type Conversation struct {
	Messages []Message // 古い順
	Clipped  bool      // 上限を超えたため、古い投稿を除いた
}

// collect は期間中の会話の投稿を新しい方から集め、古い順にして返す。
// 期間の開始より古い投稿に達したとき、履歴が尽きたとき、上限を 1 件超えたときに読むのをやめる。
func collect(ctx context.Context, history HistoryReader, channelID snowflake.ID, period Period, includeBots bool, maxMessages int) (Conversation, error) {
	var newestFirst []Message
	before := snowflake.New(period.Until)
	for {
		page, err := history.ReadBefore(ctx, channelID, before)
		if err != nil {
			return Conversation{}, fmt.Errorf("read channel history: %w", err)
		}
		taken, reachedSince := takeWithinPeriod(page, period.Since, includeBots)
		newestFirst = append(newestFirst, taken...)
		if reachedSince || len(page) < PageSize || len(newestFirst) > maxMessages {
			return toConversation(newestFirst, maxMessages), nil
		}
		before = page[len(page)-1].ID
	}
}

// takeWithinPeriod は新しい順のページから、since 以後の会話の投稿を取り出す。
// since より古い投稿に達したら reachedSince を真にする。
func takeWithinPeriod(page []Message, since time.Time, includeBots bool) (taken []Message, reachedSince bool) {
	for _, m := range page {
		if m.SentAt.Before(since) {
			return taken, true
		}
		if isConversation(m, includeBots) {
			taken = append(taken, m)
		}
	}
	return taken, false
}

// isConversation は、投稿が要約の対象になる会話かを判定する。
func isConversation(m Message, includeBots bool) bool {
	if m.System || m.InProgress || strings.TrimSpace(m.Content) == "" {
		return false
	}
	return includeBots || !m.FromBot
}

// toConversation は新しい順の投稿を上限までに切り詰め、古い順にする。
func toConversation(newestFirst []Message, maxMessages int) Conversation {
	clipped := len(newestFirst) > maxMessages
	if clipped {
		newestFirst = newestFirst[:maxMessages]
	}
	oldestFirst := slices.Clone(newestFirst)
	slices.Reverse(oldestFirst)
	return Conversation{Messages: oldestFirst, Clipped: clipped}
}
