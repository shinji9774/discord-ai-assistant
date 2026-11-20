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

var inv = command.Invocation{ApplicationID: "123456789012345678", InteractionToken: "tok1"}

func newResponder(t *testing.T) (*reply.DiscordResponder, *discordfake.Server) {
	t.Helper()
	fake := discordfake.New()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return reply.NewDiscordResponder(srv.URL), fake
}

func assertResponses(t *testing.T, fake *discordfake.Server, want []discordfake.Response) {
	t.Helper()
	if got := fake.Responses(); !reflect.DeepEqual(got, want) {
		t.Errorf("Responses() = %+v, want %+v", got, want)
	}
}

// 条件: 本文がちょうど 2000 文字（UTF-16 で 2000）。
// 内容: 本文がそのまま応答の Content に入り、添付は付かない。
func TestRespond_TextAtContentLimit_UpdatesResponseWithBody(t *testing.T) {
	responder, fake := newResponder(t)
	text := strings.Repeat("あ", 2000)

	err := responder.Respond(context.Background(), inv, command.Result{Text: text})

	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	assertResponses(t, fake, []discordfake.Response{{ApplicationID: "123456789012345678", Token: "tok1", Content: text, Files: map[string]string{}}})
}

// 条件: 本文が 2001 文字（UTF-16 で 2001）。
// 内容: Content は AttachmentNotice になり、全文が result.txt に添付される。
func TestRespond_TextOverContentLimit_AttachesResultFile(t *testing.T) {
	responder, fake := newResponder(t)
	text := strings.Repeat("あ", 2001)

	err := responder.Respond(context.Background(), inv, command.Result{Text: text})

	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	assertResponses(t, fake, []discordfake.Response{{ApplicationID: "123456789012345678", Token: "tok1", Content: "結果が長いため、ファイルに出力しました。", Files: map[string]string{"result.txt": text}}})
}

// 条件: 本文が 2001 文字で、記法が Markdown。
// 内容: Content は AttachmentNotice になり、全文が result.md に添付される。
func TestRespond_MarkdownTextOverContentLimit_AttachesMarkdownFile(t *testing.T) {
	responder, fake := newResponder(t)
	text := strings.Repeat("あ", 2001)

	err := responder.Respond(context.Background(), inv, command.Result{Text: text, Format: command.TextFormatMarkdown})

	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	assertResponses(t, fake, []discordfake.Response{{ApplicationID: "123456789012345678", Token: "tok1", Content: "結果が長いため、ファイルに出力しました。", Files: map[string]string{"result.md": text}}})
}

// 条件: 絵文字 1001 個（Unicode 上は 1001 文字、UTF-16 では 2002）。
// 内容: UTF-16 で数えて上限を超えるため、result.txt に添付される。
func TestRespond_EmojiOverLimitInUTF16_AttachesResultFile(t *testing.T) {
	responder, fake := newResponder(t)
	text := strings.Repeat("😀", 1001)

	err := responder.Respond(context.Background(), inv, command.Result{Text: text})

	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	assertResponses(t, fake, []discordfake.Response{{ApplicationID: "123456789012345678", Token: "tok1", Content: "結果が長いため、ファイルに出力しました。", Files: map[string]string{"result.txt": text}}})
}

// 条件: 短い本文と Result.Files が 1 つ。
// 内容: 本文は Content に入り、ファイルがそのまま添付される。
func TestRespond_ResultWithFiles_AttachesFiles(t *testing.T) {
	responder, fake := newResponder(t)
	result := command.Result{Text: "要約です", Files: []command.File{{Name: "summary.md", Content: []byte("# 要約")}}}

	err := responder.Respond(context.Background(), inv, result)

	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	assertResponses(t, fake, []discordfake.Response{{ApplicationID: "123456789012345678", Token: "tok1", Content: "要約です", Files: map[string]string{"summary.md": "# 要約"}}})
}

// 条件: Discord が 500 を返す。
// 内容: status・Discord のコードと文言だけのエラーが返り、interaction token と URL を含まない。
func TestRespond_DiscordFails_ReturnsStatusCodeAndMessageWithoutToken(t *testing.T) {
	responder, fake := newResponder(t)
	fake.FailWith(500)

	err := responder.Respond(context.Background(), inv, command.Result{Text: "x"})

	want := "discord rejected the response update: status 500, code 0: rejected by fake"
	if err == nil || err.Error() != want {
		t.Errorf("Respond() error = %v, want %q", err, want)
	}
}
