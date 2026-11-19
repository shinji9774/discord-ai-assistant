// Package reply は interaction の応答を更新して結果を Discord へ返す。
package reply

import (
	"context"

	"discord-ai-assistant/internal/command"
)

const (
	DiscordContentLimit        = 2000 // Discord が本文に許す文字数（UTF-16 のコード単位）
	AttachmentFileName         = "result.txt"
	MarkdownAttachmentFileName = "result.md"
	AttachmentNotice           = "結果が長いため、ファイルに出力しました。"
)

// Responder はコマンドの実行結果を、実行依頼元の interaction へ返す。
type Responder interface {
	Respond(ctx context.Context, inv command.Invocation, result command.Result) error
}
