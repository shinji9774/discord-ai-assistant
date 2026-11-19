package command

import (
	"context"

	"github.com/disgoorg/disgo/discord"
)

const (
	TruncatedNotice = "（上限に達したため、途中までの結果です）"
	FailureMessage  = "処理に失敗しました。時間をおいてもう一度実行してください。"
)

// Invocation は ingress が worker へ渡す、コマンド 1 回分の実行依頼。
type Invocation struct {
	ApplicationID    string                `json:"application_id"`
	InteractionToken string                `json:"interaction_token"`
	ChannelID        string                `json:"channel_id"` // コマンドが実行されたチャンネル
	CommandName      string                `json:"command_name"`
	Options          map[string]string     `json:"options"`
	Attachments      map[string]Attachment `json:"attachments,omitempty"` // key は添付ファイル型のオプション名
}

// Attachment は利用者が添付したファイルの情報。中身は URL から取得する。
type Attachment struct {
	Filename    string `json:"filename"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
}

// Result はコマンドの実行結果。Discord に返す本文と添付ファイル。
type Result struct {
	Text   string
	Format TextFormat
	Files  []File
}

// TextFormat は Result.Text の記法。長文を添付に回すときのファイル形式を決める。
type TextFormat int

const (
	TextFormatPlain TextFormat = iota // ゼロ値。既存コマンドはこのまま
	TextFormatMarkdown
)

// File は Discord に添付するファイル。
type File struct {
	Name    string
	Content []byte
}

// Command は Discord のスラッシュコマンド 1 つ分の定義と実行。
type Command interface {
	Definition() discord.SlashCommandCreate
	Execute(ctx context.Context, inv Invocation) (Result, error)
}
