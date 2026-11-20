package command

import (
	"context"
	"log/slog"

	"github.com/disgoorg/disgo/discord"
)

const (
	TruncatedNotice = "（上限に達したため、途中までの結果です）"
	FailureMessage  = "処理に失敗しました。時間をおいてもう一度実行してください。"
)

// Invocation は ingress が worker へ渡す、コマンド 1 回分の実行依頼。
type Invocation struct {
	ApplicationID     string                `json:"application_id"`
	InteractionToken  string                `json:"interaction_token"`
	ChannelID         string                `json:"channel_id"` // コマンドが実行されたチャンネル
	CommandName       string                `json:"command_name"`
	Options           map[string]string     `json:"options"`
	Attachments       map[string]Attachment `json:"attachments,omitempty"`        // key は添付ファイル型のオプション名
	AppPermissions    discord.Permissions   `json:"app_permissions,omitempty"`    // 実行元チャンネルでの bot の権限
	MemberPermissions discord.Permissions   `json:"member_permissions,omitempty"` // 実行元チャンネルでの実行者の権限
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

	Confidential bool // 真なら、worker は失敗ログに Text を出さない

	Guidance *Guidance // 非 nil なら、処理を終えた理由が案内だった（失敗ではない）。worker が理由をログに出す
}

// Guidance は、コマンドが利用者への案内で処理を終えた理由。worker が診断のためにログへ出す。
// Attrs に本文・秘密情報（token、URL、会話、利用者の入力文）を入れてはならない。
type Guidance struct {
	Reason string      // 理由。snake_case の固定値で、各コマンドのパッケージで定数にする
	Attrs  []slog.Attr // 原因の切り分けに使う診断値
}

// Guided は、利用者への案内で処理を終えるときの Result を作る。reason と attrs は worker のログ用で、利用者には見えない。
func Guided(text, reason string, attrs ...slog.Attr) Result {
	return Result{Text: text, Guidance: &Guidance{Reason: reason, Attrs: attrs}}
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
