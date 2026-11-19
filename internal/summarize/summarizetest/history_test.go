package summarizetest_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"discord-ai-assistant/internal/summarize"
	"discord-ai-assistant/internal/summarize/summarizetest"
)

var channelID = snowflake.ID(1300000000000000001)

// 条件: 3 件の投稿を持つ履歴で、2 件目の投稿の ID を before にして読む。
// 内容: before より前の 1 件だけが返り、読んだ回数が 1 になる。
func TestHistoryReadBefore_BeforeSecondPost_ReturnsOlderPostsOnly(t *testing.T) {
	base := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	first := summarizetest.Post(base, "佐藤", "おはよう")
	second := summarizetest.Post(base.Add(time.Minute), "鈴木", "おはようございます")
	third := summarizetest.Post(base.Add(2*time.Minute), "佐藤", "会議は 10 時から")
	history := &summarizetest.History{Messages: []summarize.Message{first, second, third}}

	got, err := history.ReadBefore(context.Background(), channelID, second.ID)

	if err != nil {
		t.Fatalf("ReadBefore: %v", err)
	}
	if want := []summarize.Message{first}; !reflect.DeepEqual(got, want) || history.Reads != 1 {
		t.Errorf("ReadBefore = %+v (reads %d), want %+v (reads 1)", got, history.Reads, want)
	}
}

// 条件: PageSize より 1 件多い投稿を持つ履歴を、最も新しい投稿より後の ID で読む。
// 内容: 新しい順に PageSize 件が返る（最も古い 1 件は次のページ）。
func TestHistoryReadBefore_MoreThanPageSize_ReturnsNewestPageInDescendingOrder(t *testing.T) {
	base := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	var posts []summarize.Message
	for i := range summarize.PageSize + 1 {
		posts = append(posts, summarizetest.Post(base.Add(time.Duration(i)*time.Minute), "佐藤", "発言"))
	}
	history := &summarizetest.History{Messages: posts}
	want := make([]summarize.Message, 0, summarize.PageSize)
	for i := len(posts) - 1; i >= 1; i-- {
		want = append(want, posts[i])
	}

	got, err := history.ReadBefore(context.Background(), channelID, snowflake.New(base.Add(24*time.Hour)))

	if err != nil {
		t.Fatalf("ReadBefore: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadBefore returned %d posts, want the newest %d in descending order", len(got), len(want))
	}
}

// 条件: Err を仕込んだ履歴を読む。
// 内容: 仕込んだエラーが返る。
func TestHistoryReadBefore_ErrSet_ReturnsErr(t *testing.T) {
	history := &summarizetest.History{Err: summarize.ErrHistoryForbidden}

	_, err := history.ReadBefore(context.Background(), channelID, snowflake.New(time.Now()))

	if !errors.Is(err, summarize.ErrHistoryForbidden) {
		t.Errorf("err = %v, want %v", err, summarize.ErrHistoryForbidden)
	}
}
