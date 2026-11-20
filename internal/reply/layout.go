package reply

import (
	"bytes"
	"unicode/utf16"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
)

// messageLayout は、Discord へ送る 1 件分の本文と添付ファイル。
type messageLayout struct {
	content string
	files   []*discord.File
}

// layoutMessage は、mention を先頭に置いた本文が Discord の上限に収まるかで、結果を本文に載せるかファイルにするかを決める。
// 収まらなければ、本文は mention と AttachmentNotice にし、結果の全文を記法に合うファイルにする。
// result.Files は、そのファイルの後ろにそのまま添付する。
func layoutMessage(mention string, result command.Result) messageLayout {
	layout := messageLayout{content: mention + result.Text}
	if utf16Length(layout.content) > DiscordContentLimit {
		layout.content = mention + AttachmentNotice
		layout.files = append(layout.files, discord.NewFile(attachmentFileName(result.Format), "", bytes.NewReader([]byte(result.Text))))
	}
	for _, f := range result.Files {
		layout.files = append(layout.files, discord.NewFile(f.Name, "", bytes.NewReader(f.Content)))
	}
	return layout
}

// attachmentFileName は、本文の記法に合うファイル名を返す。markdown は拡張子を .md にして、閲覧時に整形されるようにする。
func attachmentFileName(format command.TextFormat) string {
	if format == command.TextFormatMarkdown {
		return MarkdownAttachmentFileName
	}
	return AttachmentFileName
}

func utf16Length(s string) int {
	return len(utf16.Encode([]rune(s)))
}
