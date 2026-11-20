package reply_test

import (
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/test/discordfake"
)

const (
	testBotToken  = "bot-token1"
	postChannelID = "1300000000000000001"
	requesterID   = "1600000000000000001"

	// requesterMention は、依頼者への通知として本文の先頭に置く文字列（design: "<@{利用者 ID}>\n" + 結果）。
	// ASCII だけなので、バイト数が UTF-16 の長さと等しい。
	requesterMention = "<@" + requesterID + ">\n"
)

func newChannelPoster(t *testing.T) (*reply.DiscordChannelPoster, *discordfake.Server) {
	t.Helper()
	fake := discordfake.New()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return reply.NewDiscordChannelPoster(testBotToken, srv.URL), fake
}

// postTo は、テストのチャンネルへ依頼者に通知して投稿する ChannelPost を作る。
func postTo(result command.Result) reply.ChannelPost {
	return reply.ChannelPost{ChannelID: postChannelID, NotifyUserID: requesterID, Result: result}
}

// postedMessage は、bot トークンで投稿され、依頼者だけが通知先の ChannelMessage を作る。
func postedMessage(content string, files map[string]string) discordfake.ChannelMessage {
	return discordfake.ChannelMessage{
		ChannelID:      postChannelID,
		Authorization:  "Bot " + testBotToken,
		Content:        content,
		AllowedUserIDs: []string{requesterID},
		Files:          files,
	}
}

func assertChannelMessages(t *testing.T, fake *discordfake.Server, want []discordfake.ChannelMessage) {
	t.Helper()
	if got := fake.ChannelMessages(); !reflect.DeepEqual(got, want) {
		t.Errorf("ChannelMessages() = %+v, want %+v", got, want)
	}
}

// 条件: 短い結果を投稿する。
// 内容: bot トークンで、依頼者へのメンションに続けて結果を投稿し、通知先は依頼者だけになる（3.8）。
func TestPost_ShortResult_PostsWithMentionNotifyingOnlyRequester(t *testing.T) {
	poster, fake := newChannelPoster(t)

	err := poster.Post(context.Background(), postTo(command.Result{Text: "要約です"}))

	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	assertChannelMessages(t, fake, []discordfake.ChannelMessage{postedMessage(requesterMention+"要約です", map[string]string{})})
}

// 条件: メンションと結果を合わせて、ちょうど 2000 文字（UTF-16 で 2000）。
// 内容: 結果がそのまま本文に入り、添付は付かない。
func TestPost_MentionAndTextAtContentLimit_PostsTextInContent(t *testing.T) {
	poster, fake := newChannelPoster(t)
	text := strings.Repeat("あ", reply.DiscordContentLimit-len(requesterMention))

	err := poster.Post(context.Background(), postTo(command.Result{Text: text}))

	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	assertChannelMessages(t, fake, []discordfake.ChannelMessage{postedMessage(requesterMention+text, map[string]string{})})
}

// 条件: メンションと結果を合わせて 2001 文字（UTF-16 で 2001）。
// 内容: 本文はメンションと AttachmentNotice になり、結果の全文が result.txt に添付される。
func TestPost_MentionAndTextOverContentLimit_AttachesResultFile(t *testing.T) {
	poster, fake := newChannelPoster(t)
	text := strings.Repeat("あ", reply.DiscordContentLimit-len(requesterMention)+1)

	err := poster.Post(context.Background(), postTo(command.Result{Text: text}))

	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	assertChannelMessages(t, fake, []discordfake.ChannelMessage{
		postedMessage(requesterMention+"結果が長いため、ファイルに出力しました。", map[string]string{"result.txt": text}),
	})
}

// 条件: 短い要約と、文字起こしの transcript.txt を 1 つ持つ結果を投稿する。
// 内容: 要約は本文に入り、transcript.txt がそのまま添付される（3.7）。
func TestPost_ResultWithTranscriptFile_AttachesTranscript(t *testing.T) {
	poster, fake := newChannelPoster(t)
	result := command.Result{
		Text:   "要約です",
		Format: command.TextFormatMarkdown,
		Files:  []command.File{{Name: "transcript.txt", Content: []byte("[00:00:12] たろう: こんにちは")}},
	}

	err := poster.Post(context.Background(), postTo(result))

	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	assertChannelMessages(t, fake, []discordfake.ChannelMessage{
		postedMessage(requesterMention+"要約です", map[string]string{"transcript.txt": "[00:00:12] たろう: こんにちは"}),
	})
}

// 条件: Discord が 500 を返す。
// 内容: status・Discord のコードと文言だけのエラーが返り、投稿しようとした本文を含まない。
func TestPost_DiscordFails_ReturnsStatusAndCodeWithoutBody(t *testing.T) {
	poster, fake := newChannelPoster(t)
	fake.FailWith(500)

	err := poster.Post(context.Background(), postTo(command.Result{Text: "会議の要約の本文"}))

	want := "discord rejected the channel message: status 500, code 0: rejected by fake"
	if err == nil || err.Error() != want {
		t.Errorf("Post() error = %v, want %q", err, want)
	}
}
