package summarize_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"discord-ai-assistant/internal/summarize"
	"discord-ai-assistant/internal/summarize/summarizetest"
)

var (
	collectChannel = snowflake.ID(1300000000000000001)
	collectPeriod  = summarize.Period{
		Since: time.Date(2026, 11, 18, 14, 0, 0, 0, jst),
		Until: time.Date(2026, 11, 19, 14, 0, 0, 0, jst),
	}
)

const unlimited = 1000

// postsEveryMinute は from から 1 分ごとの count 件の投稿を古い順に作る。
func postsEveryMinute(from time.Time, count int) []summarize.Message {
	posts := make([]summarize.Message, 0, count)
	for i := range count {
		posts = append(posts, summarizetest.Post(from.Add(time.Duration(i)*time.Minute), "佐藤", fmt.Sprintf("発言 %d", i)))
	}
	return posts
}

// 条件: 期間の直前、開始ちょうど、期間中、終了ちょうどの投稿がある。
// 内容: 開始ちょうどと期間中の投稿だけが古い順に集まる（since 以上 until 未満）。
func TestCollect_PostsAroundPeriod_KeepsPostsWithinPeriodOldestFirst(t *testing.T) {
	atSince := summarizetest.Post(collectPeriod.Since, "佐藤", "始めます")
	during := summarizetest.Post(collectPeriod.Since.Add(time.Hour), "鈴木", "了解です")
	history := &summarizetest.History{Messages: []summarize.Message{
		summarizetest.Post(collectPeriod.Since.Add(-time.Millisecond), "佐藤", "期間の前"),
		atSince,
		during,
		summarizetest.Post(collectPeriod.Until, "鈴木", "期間の後"),
	}}

	got, err := summarize.Collect(context.Background(), history, collectChannel, collectPeriod, false, unlimited)

	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if want := (summarize.Conversation{Messages: []summarize.Message{atSince, during}}); !reflect.DeepEqual(got, want) {
		t.Errorf("Collect = %+v, want %+v", got, want)
	}
}

// 条件: システムメッセージ、空白だけの本文、生成中の投稿、bot の投稿が混ざる。include_bots は偽。
// 内容: 利用者の通常の投稿だけが集まる。
func TestCollect_NonConversationPosts_ExcludesThem(t *testing.T) {
	human := summarizetest.Post(collectPeriod.Since.Add(1*time.Minute), "佐藤", "会議は 10 時から")
	system := summarizetest.Post(collectPeriod.Since.Add(2*time.Minute), "佐藤", "参加しました")
	system.System = true
	blank := summarizetest.Post(collectPeriod.Since.Add(3*time.Minute), "鈴木", " \n ")
	inProgress := summarizetest.Post(collectPeriod.Since.Add(4*time.Minute), "assistant", "考え中")
	inProgress.InProgress = true
	bot := summarizetest.Post(collectPeriod.Since.Add(5*time.Minute), "helper-bot", "定期通知")
	bot.FromBot = true
	history := &summarizetest.History{Messages: []summarize.Message{human, system, blank, inProgress, bot}}

	got, err := summarize.Collect(context.Background(), history, collectChannel, collectPeriod, false, unlimited)

	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if want := (summarize.Conversation{Messages: []summarize.Message{human}}); !reflect.DeepEqual(got, want) {
		t.Errorf("Collect = %+v, want %+v", got, want)
	}
}

// 条件: 利用者の投稿と bot の投稿がある。include_bots は真。
// 内容: bot の投稿も集まる。
func TestCollect_IncludeBots_KeepsBotPosts(t *testing.T) {
	human := summarizetest.Post(collectPeriod.Since.Add(1*time.Minute), "佐藤", "会議は 10 時から")
	bot := summarizetest.Post(collectPeriod.Since.Add(2*time.Minute), "helper-bot", "定期通知")
	bot.FromBot = true
	history := &summarizetest.History{Messages: []summarize.Message{human, bot}}

	got, err := summarize.Collect(context.Background(), history, collectChannel, collectPeriod, true, unlimited)

	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if want := (summarize.Conversation{Messages: []summarize.Message{human, bot}}); !reflect.DeepEqual(got, want) {
		t.Errorf("Collect = %+v, want %+v", got, want)
	}
}

// 条件: 期間中に 250 件（3 ページ分）の投稿がある。
// 内容: ページをたどって 250 件すべてが古い順に集まり、読み取りは 3 回。
func TestCollect_PostsOverSeveralPages_FollowsPages(t *testing.T) {
	posts := postsEveryMinute(collectPeriod.Since, 250)
	history := &summarizetest.History{Messages: posts}

	got, err := summarize.Collect(context.Background(), history, collectChannel, collectPeriod, false, unlimited)

	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if want := (summarize.Conversation{Messages: posts}); !reflect.DeepEqual(got, want) || history.Reads != 3 {
		t.Errorf("Collect returned %d posts with %d reads, want %d posts with 3 reads", len(got.Messages), history.Reads, len(want.Messages))
	}
}

// 条件: 期間中に 150 件、期間の前に 300 件の投稿がある。
// 内容: 期間の開始より古い投稿に達したページで読むのをやめる（読み取りは 2 回）。
func TestCollect_ReachesPostBeforeSince_StopsReading(t *testing.T) {
	inPeriod := postsEveryMinute(collectPeriod.Since, 150)
	beforePeriod := postsEveryMinute(collectPeriod.Since.Add(-300*time.Minute), 300)
	history := &summarizetest.History{Messages: append(beforePeriod, inPeriod...)}

	got, err := summarize.Collect(context.Background(), history, collectChannel, collectPeriod, false, unlimited)

	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if want := (summarize.Conversation{Messages: inPeriod}); !reflect.DeepEqual(got, want) || history.Reads != 2 {
		t.Errorf("Collect returned %d posts with %d reads, want %d posts with 2 reads", len(got.Messages), history.Reads, len(want.Messages))
	}
}

// 条件: 上限 150 件に対し、期間中に 400 件の投稿がある。
// 内容: 新しい方から 150 件が古い順に残り、Clipped が真。上限 + 1 件に達したページで読むのをやめる（読み取りは 2 回）。
func TestCollect_MoreThanMaxMessages_KeepsNewestAndClips(t *testing.T) {
	const maxMessages = 150
	posts := postsEveryMinute(collectPeriod.Since, 400)
	history := &summarizetest.History{Messages: posts}

	got, err := summarize.Collect(context.Background(), history, collectChannel, collectPeriod, false, maxMessages)

	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := summarize.Conversation{Messages: posts[len(posts)-maxMessages:], Clipped: true}
	if !reflect.DeepEqual(got, want) || history.Reads != 2 {
		t.Errorf("Collect returned %d posts (clipped %v) with %d reads, want %d posts (clipped true) with 2 reads",
			len(got.Messages), got.Clipped, history.Reads, len(want.Messages))
	}
}

// 条件: 履歴の読み取りが ErrHistoryForbidden で失敗する。
// 内容: そのエラーを包んで返す（errors.Is で判定できる）。
func TestCollect_ReaderFails_ReturnsWrappedError(t *testing.T) {
	history := &summarizetest.History{Err: summarize.ErrHistoryForbidden}

	_, err := summarize.Collect(context.Background(), history, collectChannel, collectPeriod, false, unlimited)

	if !errors.Is(err, summarize.ErrHistoryForbidden) {
		t.Errorf("err = %v, want wrapping %v", err, summarize.ErrHistoryForbidden)
	}
}
