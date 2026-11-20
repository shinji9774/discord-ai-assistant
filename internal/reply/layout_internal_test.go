package reply

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"discord-ai-assistant/internal/command"
)

// testMention は、依頼者への通知として本文の先頭に置く文字列（design: "<@{利用者 ID}>\n"）。ASCII だけなので、バイト数が UTF-16 の長さと等しい。
const testMention = "<@1600000000000000001>\n"

// laidOutFile は、添付 1 つ分の名前と中身。
type laidOutFile struct {
	Name    string
	Content string
}

// laidOutMessage は、messageLayout を比較できる形にしたもの。
type laidOutMessage struct {
	Content string
	Files   []laidOutFile
}

// readLayout は添付の中身を読み、messageLayout 全体を 1 回で比較できる形にする。
func readLayout(t *testing.T, layout messageLayout) laidOutMessage {
	t.Helper()
	files := []laidOutFile{}
	for _, f := range layout.files {
		content, err := io.ReadAll(f.Reader)
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		files = append(files, laidOutFile{Name: f.Name, Content: string(content)})
	}
	return laidOutMessage{Content: layout.content, Files: files}
}

// 条件: メンションと本文を合わせて、ちょうど 2000 文字（UTF-16 で 2000）。
// 内容: メンションに続けて本文がそのまま content に入り、ファイルは付かない。
func TestLayoutMessage_MentionAndTextAtContentLimit_KeepsTextInContent(t *testing.T) {
	text := strings.Repeat("あ", DiscordContentLimit-len(testMention))

	got := readLayout(t, layoutMessage(testMention, command.Result{Text: text}))

	want := laidOutMessage{Content: testMention + text, Files: []laidOutFile{}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("layoutMessage() = %+v, want %+v", got, want)
	}
}

// 条件: メンションと本文を合わせて 2001 文字（UTF-16 で 2001）で、結果に transcript.txt が 1 つある。
// 内容: content はメンションと AttachmentNotice になり、本文の result.txt、transcript.txt の順に添付される。
func TestLayoutMessage_MentionAndTextOverContentLimit_MovesTextToFileBeforeResultFiles(t *testing.T) {
	text := strings.Repeat("あ", DiscordContentLimit-len(testMention)+1)
	result := command.Result{Text: text, Files: []command.File{{Name: "transcript.txt", Content: []byte("[00:00:12] たろう: こんにちは")}}}

	got := readLayout(t, layoutMessage(testMention, result))

	want := laidOutMessage{
		Content: testMention + "結果が長いため、ファイルに出力しました。",
		Files: []laidOutFile{
			{Name: "result.txt", Content: text},
			{Name: "transcript.txt", Content: "[00:00:12] たろう: こんにちは"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("layoutMessage() = %+v, want %+v", got, want)
	}
}

// 条件: メンションなしで、本文が 2001 文字の Markdown。
// 内容: content は AttachmentNotice だけになり、本文は result.md に添付される（応答の更新と同じ振り分け）。
func TestLayoutMessage_NoMentionMarkdownOverContentLimit_AttachesMarkdownFile(t *testing.T) {
	text := strings.Repeat("あ", DiscordContentLimit+1)

	got := readLayout(t, layoutMessage("", command.Result{Text: text, Format: command.TextFormatMarkdown}))

	want := laidOutMessage{
		Content: "結果が長いため、ファイルに出力しました。",
		Files:   []laidOutFile{{Name: "result.md", Content: text}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("layoutMessage() = %+v, want %+v", got, want)
	}
}
