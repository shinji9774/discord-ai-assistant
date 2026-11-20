package summarize_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"discord-ai-assistant/internal/summarize"
	"discord-ai-assistant/test/discordfake"
)

const historyChannel = "1300000000000000001"

var (
	sato   = discordfake.HistoryUser{ID: "2001", Username: "sato", GlobalName: "佐藤"}
	suzuki = discordfake.HistoryUser{ID: "2002", Username: "suzuki", GlobalName: "鈴木"}
	tanaka = discordfake.HistoryUser{ID: "2003", Username: "tanaka"}
	helper = discordfake.HistoryUser{ID: "9001", Username: "helper-bot", Bot: true}
	hook   = discordfake.HistoryUser{ID: "9002", Username: "通知"}
)

// newHistoryReader は discordfake に向けた DiscordHistoryReader を作る。
func newHistoryReader(t *testing.T) (*summarize.DiscordHistoryReader, *discordfake.Server) {
	t.Helper()
	fake := discordfake.New()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return summarize.NewDiscordHistoryReader("bot-token", srv.URL), fake
}

// sentAt は 2026-11-18 10:00（日本時間）から minute 分後の UTC の時刻。
func sentAt(minute int) time.Time {
	return time.Date(2026, 11, 18, 1, minute, 0, 0, time.UTC)
}

// idAt は sentAt(minute) に作られた投稿の ID。
func idAt(minute int) snowflake.ID {
	return snowflake.New(sentAt(minute))
}

// 条件: メンションつきの投稿、bot の投稿、webhook の投稿、システムメッセージ、生成中の投稿がある。
// 内容: 新しい順に、表示名・bot か・システムか・生成中か・メンションを表示名にした本文へ変換する。
//
//	ユーザーのメンション（<@ID> と <@!ID>）は @表示名 に、ロールのメンション（<@&ID>）はそのまま。
func TestDiscordHistoryReaderReadBefore_VariousPosts_ConvertsToMessages(t *testing.T) {
	reader, fake := newHistoryReader(t)
	fake.AddHistory(historyChannel,
		discordfake.HistoryMessage{ID: idAt(0).String(), Timestamp: sentAt(0), Author: sato,
			Content: "<@2002> と <@!2003> に確認します。<@&3001> も見てください", Mentions: []discordfake.HistoryUser{suzuki, tanaka}},
		discordfake.HistoryMessage{ID: idAt(1).String(), Timestamp: sentAt(1), Author: helper, Content: "定期通知"},
		discordfake.HistoryMessage{ID: idAt(2).String(), Timestamp: sentAt(2), Author: hook, WebhookID: hook.ID, Content: "外部通知"},
		discordfake.HistoryMessage{ID: idAt(3).String(), Timestamp: sentAt(3), Author: sato, Type: 7},                // 7 はサーバへの参加通知
		discordfake.HistoryMessage{ID: idAt(4).String(), Timestamp: sentAt(4), Author: helper, Type: 20, Flags: 128}, // 128 は LOADING
	)

	got, err := reader.ReadBefore(context.Background(), snowflake.MustParse(historyChannel), idAt(60))

	if err != nil {
		t.Fatalf("ReadBefore: %v", err)
	}
	want := []summarize.Message{
		{ID: idAt(4), SentAt: sentAt(4), AuthorName: "helper-bot", FromBot: true, InProgress: true},
		{ID: idAt(3), SentAt: sentAt(3), AuthorName: "佐藤", System: true},
		{ID: idAt(2), SentAt: sentAt(2), AuthorName: "通知", FromBot: true, Content: "外部通知"},
		{ID: idAt(1), SentAt: sentAt(1), AuthorName: "helper-bot", FromBot: true, Content: "定期通知"},
		{ID: idAt(0), SentAt: sentAt(0), AuthorName: "佐藤", Content: "@鈴木 と @tanaka に確認します。<@&3001> も見てください"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadBefore =\n%+v\nwant\n%+v", got, want)
	}
}

// 条件: PageSize より 1 件多い投稿がある。
// 内容: 1 回の読み取りで PageSize 件を得る（Discord の既定の 50 件ではない）。
func TestDiscordHistoryReaderReadBefore_MoreThanPageSize_ReadsFullPage(t *testing.T) {
	reader, fake := newHistoryReader(t)
	for minute := range summarize.PageSize + 1 {
		fake.AddHistory(historyChannel, discordfake.HistoryMessage{
			ID: idAt(minute).String(), Timestamp: sentAt(minute), Author: sato, Content: fmt.Sprintf("発言 %d", minute),
		})
	}

	got, err := reader.ReadBefore(context.Background(), snowflake.MustParse(historyChannel), idAt(summarize.PageSize+1))

	if err != nil {
		t.Fatalf("ReadBefore: %v", err)
	}
	if len(got) != summarize.PageSize {
		t.Errorf("ReadBefore returned %d posts, want %d", len(got), summarize.PageSize)
	}
}

// 条件: Discord が履歴の取得を 403（コード 50001）で拒む。
// 内容: Discord のコードと文言を保持した HistoryForbiddenError を返し、errors.Is(err, ErrHistoryForbidden) も真になる。
func TestDiscordHistoryReaderReadBefore_Forbidden_ReturnsForbiddenErrorWithCodeAndMessage(t *testing.T) {
	reader, fake := newHistoryReader(t)
	fake.RejectHistory(historyChannel, http.StatusForbidden, 50001)

	_, err := reader.ReadBefore(context.Background(), snowflake.MustParse(historyChannel), idAt(60))

	var forbidden *summarize.HistoryForbiddenError
	got := historyForbiddenOutcome{IsErrHistoryForbidden: errors.Is(err, summarize.ErrHistoryForbidden)}
	if errors.As(err, &forbidden) {
		got.Error = *forbidden
	}
	want := historyForbiddenOutcome{IsErrHistoryForbidden: true, Error: summarize.HistoryForbiddenError{Code: 50001, Message: "rejected by fake"}}
	if got != want {
		t.Errorf("outcome = %+v, want %+v", got, want)
	}
}

// historyForbiddenOutcome は、403 のエラーを一括で比べるための値。
type historyForbiddenOutcome struct {
	IsErrHistoryForbidden bool
	Error                 summarize.HistoryForbiddenError
}

// 条件: Discord が履歴の取得を 404（Unknown Channel、コード 10003）で拒む。
// 内容: status・Discord のコード・Discord の文言だけを含むエラーを返す（レスポンス本文と URL を含めない）。
func TestDiscordHistoryReaderReadBefore_OtherHTTPError_ReturnsStatusAndCodeOnly(t *testing.T) {
	reader, fake := newHistoryReader(t)
	fake.RejectHistory(historyChannel, http.StatusNotFound, 10003)

	_, err := reader.ReadBefore(context.Background(), snowflake.MustParse(historyChannel), idAt(60))

	if want := "get channel messages: status 404, code 10003: rejected by fake"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// 条件: Discord の API への依頼が、ctx の取り消しで失敗する（*url.Error になる）。
// 内容: 内側のエラー（context.Canceled）を errors.Is でたどれ、エラー文に URL を含めない。
func TestDiscordHistoryReaderReadBefore_RequestFailed_WrapsInnerErrorWithoutURL(t *testing.T) {
	reader, _ := newHistoryReader(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := reader.ReadBefore(ctx, snowflake.MustParse(historyChannel), idAt(60))

	got := requestFailedOutcome{IsCanceled: errors.Is(err, context.Canceled), Message: fmt.Sprint(err)}
	want := requestFailedOutcome{IsCanceled: true, Message: "get channel messages: request failed: context canceled"}
	if got != want {
		t.Errorf("outcome = %+v, want %+v", got, want)
	}
}

// requestFailedOutcome は、接続の失敗のエラーを一括で比べるための値。
type requestFailedOutcome struct {
	IsCanceled bool
	Message    string
}
