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

// 条件: Discord が履歴の取得を 403（Missing Access）で拒む。
// 内容: ErrHistoryForbidden を返す。
func TestDiscordHistoryReaderReadBefore_Forbidden_ReturnsErrHistoryForbidden(t *testing.T) {
	reader, fake := newHistoryReader(t)
	fake.RejectHistory(historyChannel, http.StatusForbidden, 50001)

	_, err := reader.ReadBefore(context.Background(), snowflake.MustParse(historyChannel), idAt(60))

	if !errors.Is(err, summarize.ErrHistoryForbidden) {
		t.Errorf("err = %v, want %v", err, summarize.ErrHistoryForbidden)
	}
}

// 条件: Discord が履歴の取得を 404（Unknown Channel、コード 10003）で拒む。
// 内容: status と Discord のコードだけを含むエラーを返す（レスポンス本文と URL を含めない）。
func TestDiscordHistoryReaderReadBefore_OtherHTTPError_ReturnsStatusAndCodeOnly(t *testing.T) {
	reader, fake := newHistoryReader(t)
	fake.RejectHistory(historyChannel, http.StatusNotFound, 10003)

	_, err := reader.ReadBefore(context.Background(), snowflake.MustParse(historyChannel), idAt(60))

	if want := "get channel messages: status 404, code 10003"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// 条件: Discord の API に接続できない（サーバーが閉じている）。
// 内容: 元のエラー（URL を含みうる）の文言を含めず、固定の文言のエラーを返す。
func TestDiscordHistoryReaderReadBefore_ConnectionFailed_ReturnsFixedMessageWithoutURL(t *testing.T) {
	srv := httptest.NewServer(discordfake.New())
	reader := summarize.NewDiscordHistoryReader("bot-token", srv.URL)
	srv.Close()

	_, err := reader.ReadBefore(context.Background(), snowflake.MustParse(historyChannel), idAt(60))

	if want := "get channel messages: request failed"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}
