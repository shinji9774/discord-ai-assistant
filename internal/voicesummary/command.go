package voicesummary

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/voicesummary/craig"
)

// コマンドとオプションの名前と説明。利用者向けの文言のため、設定ではなく定数で持つ。
const (
	CommandName        = "summarize-voice"
	commandDescription = "Craig の録音を文字起こしして要約する"
	optionLink         = "link"
	linkDescription    = "Craig の録音リンク（ほかの人には見えません）"

	// acceptedMessage は、受け付けたときに実行者だけへ返す文。
	acceptedMessage = "録音の要約を受け付けました。完了したら、このチャンネルに結果を投稿します。"

	// 案内文。失敗ではなく、利用者にリンク、録音、権限を見直してもらう返信。
	notRecordingLinkGuidance     = "Craig の録音リンクを指定してください。録音の開始時に Craig から届く、`/rec/` を含むリンクです。"
	botCannotPostGuidance        = "bot にこのチャンネルへメッセージとファイルを投稿する権限がないため、結果を投稿できません。サーバの管理者に相談してください。"
	recordingUnavailableGuidance = "録音を取得できませんでした。リンクが正しいか、録音の保存期限が切れていないかを確かめてください。"
	otherGuildGuidance           = "このサーバで録音された録音だけを要約できます。"
	recordingLiveGuidance        = "録音がまだ続いています。録音を停止してから、もう一度実行してください。"
	// recordingTooLongGuidanceFormat には、扱える長さ（分）を埋め込む。
	recordingTooLongGuidanceFormat = "録音が長すぎるため、要約できません。扱える長さは %d 分までです。"

	// 案内の理由。worker が診断のためにログへ出す。
	reasonNotRecordingLink     = "not_recording_link"
	reasonBotCannotPost        = "bot_cannot_post"
	reasonRecordingUnavailable = "recording_unavailable"
	reasonOtherGuild           = "other_guild"
	reasonRecordingLive        = "recording_live"
	reasonRecordingTooLong     = "recording_too_long"

	// Craig 側の録音を削除したあとに、受け付けの文へ続ける案内。
	craigDeletedNotice      = "Craig 側の録音は削除しました。同じ録音ではもう再実行できません。"
	craigDeleteFailedNotice = "Craig 側の録音を削除できませんでした。Craig の録音のページから削除してください。"
	noticeSeparator         = "\n"

	// downloadDirPattern は、録音を取り出す一時ディレクトリの名前の接頭辞。
	downloadDirPattern = "summarize-voice-"
	// cleanupTimeout は、失敗した依頼のデータを消すのに使える時間。呼び出し元の期限とは別に取る。
	// Reporter が claim を解放するとき（retryLater）にも使う。
	// worker の返信の予備（reply_reserve_seconds）より短くし、失敗通知の返信の時間を残す。
	cleanupTimeout = 5 * time.Second
)

// errTrackMissing は、対象の話者のトラックが、ダウンロードした録音に無かったことを表す。
var errTrackMissing = errors.New("recording has no track for a target speaker")

// RecordingSource は、録音の情報と音声を取得する。
type RecordingSource interface {
	// Describe は録音の情報を返す。取得できないときは craig.ErrRecordingUnavailable。
	Describe(ctx context.Context, link craig.Link) (craig.Recording, error)
	// Download は、全トラックを 1 つずつ dir に取り出して visit に渡す。
	// visit が返ると、そのファイルを消す。
	Download(ctx context.Context, link craig.Link, dir string, visit func(craig.TrackFile) error) error
	// Delete は、Craig 側の録音の音声と、ダウンロード用に変換したファイルを消す。link.DeleteKey が空でないときだけ呼ぶ。
	Delete(ctx context.Context, link craig.Link) error
}

// AudioStore は、受け付けの側が使う置き場。
type AudioStore interface {
	// CreateManifest は依頼を記録する。同じ依頼 ID が既にあれば ErrRequestExists。
	CreateManifest(ctx context.Context, m Manifest) error
	PutAudio(ctx context.Context, requestID string, track int, audio io.ReadSeeker) error
	// Discard は、依頼の音声、途中結果、マニフェストを消す。
	Discard(ctx context.Context, m Manifest) error
}

// JobStarter は、1 トラックの文字起こしを開始する。
type JobStarter interface {
	// Start は、同じジョブが既にあれば nil を返す。
	Start(ctx context.Context, job JobRef) error
}

// Dependencies は、/summarize-voice の受け付けが使う外部の依存。
type Dependencies struct {
	Recordings RecordingSource
	Store      AudioStore
	Jobs       JobStarter
	Poster     reply.ChannelPoster
}

// Command は /summarize-voice。
type Command struct {
	deps Dependencies
	cfg  Config
}

var _ command.SecretInput = (*Command)(nil)

// New は /summarize-voice を作る。依存は Execute のときにだけ使う。
func New(deps Dependencies, cfg Config) *Command {
	return &Command{deps: deps, cfg: cfg}
}

// Definition は Discord に登録するコマンドの定義を返す。録音したサーバのチャンネルへ投稿するため、サーバでだけ使える。
func (c *Command) Definition() discord.SlashCommandCreate {
	return discord.SlashCommandCreate{
		Name:        CommandName,
		Description: commandDescription,
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: optionLink, Description: linkDescription, Required: true},
		},
		Contexts: []discord.InteractionContextType{discord.InteractionContextTypeGuild},
	}
}

// HasSecretInput は、入力の録音リンクが鍵を含むことを示す。応答は実行者だけに見え、入力はログに出ない。
func (c *Command) HasSecretInput() {}

// Execute は、リンク、権限、録音を順に確かめてから、話者ごとの文字起こしを始めて受け付けの文を返す。
// 利用者が直せる状況は案内を返し、録音の取得、置き場、文字起こしの開始の失敗はエラーを返す。
// エラー文に録音リンクと鍵を入れない。
func (c *Command) Execute(ctx context.Context, inv command.Invocation) (command.Result, error) {
	link, err := craig.ParseLink(inv.Options[optionLink], c.cfg.CraigBaseURL)
	if err != nil {
		return command.Guided(notRecordingLinkGuidance, reasonNotRecordingLink), nil
	}
	if missingPostPermissions(inv.ChannelType, inv.AppPermissions) {
		return command.Guided(botCannotPostGuidance, reasonBotCannotPost), nil
	}
	rec, err := c.deps.Recordings.Describe(ctx, link)
	if errors.Is(err, craig.ErrRecordingUnavailable) {
		return command.Guided(recordingUnavailableGuidance, reasonRecordingUnavailable), nil
	}
	if err != nil {
		return command.Result{}, fmt.Errorf("describe recording: %w", err)
	}
	if guidance, refused := c.recordingGuidance(inv, rec); refused {
		return guidance, nil
	}
	return c.accept(ctx, inv, link, rec)
}

// recordingGuidance は、要約できない録音への案内を返す。
// サーバを先に確かめるのは、ほかのサーバの録音の状態を伝えないため。
func (c *Command) recordingGuidance(inv command.Invocation, rec craig.Recording) (command.Result, bool) {
	switch {
	case rec.GuildID != inv.GuildID:
		return command.Guided(otherGuildGuidance, reasonOtherGuild), true
	case rec.Live:
		return command.Guided(recordingLiveGuidance, reasonRecordingLive), true
	case rec.Duration > c.maxRecordingLength():
		return command.Guided(fmt.Sprintf(recordingTooLongGuidanceFormat, c.cfg.MaxRecordingMinutes), reasonRecordingTooLong), true
	}
	return command.Result{}, false
}

func (c *Command) maxRecordingLength() time.Duration {
	return time.Duration(c.cfg.MaxRecordingMinutes) * time.Minute
}

// accept は、依頼を記録してから、対象の話者の文字起こしを始める。話者がいなければ「発話なし」を投稿する。
// 同じ依頼が既に記録されていれば（worker の重複起動）、先に動いている実行に任せて何もしない。
// 記録したあとで失敗したら、依頼のデータを消してからエラーを返す。
func (c *Command) accept(ctx context.Context, inv command.Invocation, link craig.Link, rec craig.Recording) (command.Result, error) {
	m := newManifest(inv, rec)
	err := c.deps.Store.CreateManifest(ctx, m)
	if errors.Is(err, ErrRequestExists) {
		return acceptedResult(), nil
	}
	if err != nil {
		return command.Result{}, fmt.Errorf("create manifest: %w", err)
	}
	if len(m.Speakers) == 0 {
		result, err := c.reportNoSpeech(ctx, m)
		if err != nil {
			return result, err
		}
		return c.deleteFromCraig(ctx, link, result), nil
	}
	if err := c.transcribeTracks(ctx, link, m); err != nil {
		return command.Result{}, errors.Join(err, c.discard(ctx, m))
	}
	return c.deleteFromCraig(ctx, link, acceptedResult()), nil
}

// deleteFromCraig は、削除用のリンクのときだけ Craig 側の録音を消し、その結果を応答の文に続ける。
// 受け付けは済んでいるので、削除の失敗はエラーにしない。
func (c *Command) deleteFromCraig(ctx context.Context, link craig.Link, result command.Result) command.Result {
	if link.DeleteKey == "" {
		return result
	}
	notice := craigDeletedNotice
	if err := c.deps.Recordings.Delete(ctx, link); err != nil {
		notice = craigDeleteFailedNotice
	}
	result.Text += noticeSeparator + notice
	return result
}

// reportNoSpeech は、「発話なし」を依頼者に通知して投稿し、依頼のデータを消す。
func (c *Command) reportNoSpeech(ctx context.Context, m Manifest) (command.Result, error) {
	if err := c.deps.Poster.Post(ctx, requesterPost(m, noSpeechResult(m))); err != nil {
		return command.Result{}, errors.Join(fmt.Errorf("post no speech report: %w", err), c.discard(ctx, m))
	}
	if err := c.discard(ctx, m); err != nil {
		return command.Result{}, err
	}
	return acceptedResult(), nil
}

// transcribeTracks は、録音を一時ディレクトリへ取り出し、対象の話者のトラックごとに音声を置いて文字起こしを始める。
// 対象でないトラック（bot など）は飛ばす。対象の話者のトラックが 1 つでも無ければエラーにする。
func (c *Command) transcribeTracks(ctx context.Context, link craig.Link, m Manifest) error {
	dir, err := os.MkdirTemp("", downloadDirPattern)
	if err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}
	defer os.RemoveAll(dir)

	targets := make(map[int]bool, len(m.Speakers))
	for _, s := range m.Speakers {
		targets[s.Track] = true
	}
	started := 0
	visit := func(f craig.TrackFile) error {
		if !targets[f.Track] {
			return nil
		}
		if err := c.transcribeTrack(ctx, m.RequestID, f); err != nil {
			return err
		}
		started++
		return nil
	}
	if err := c.deps.Recordings.Download(ctx, link, dir, visit); err != nil {
		return fmt.Errorf("download recording: %w", err)
	}
	if started != len(targets) {
		return errTrackMissing
	}
	return nil
}

// transcribeTrack は、1 トラックの音声を置いて、その文字起こしを始める。
func (c *Command) transcribeTrack(ctx context.Context, requestID string, f craig.TrackFile) error {
	audio, err := os.Open(f.Path)
	if err != nil {
		// os.Open のエラーは *fs.PathError で、パスに録音のファイル名が入りうるため、原因だけを残す。
		return fmt.Errorf("open audio of track %d: %w", f.Track, errors.Unwrap(err))
	}
	defer audio.Close()
	if err := c.deps.Store.PutAudio(ctx, requestID, f.Track, audio); err != nil {
		return fmt.Errorf("put audio of track %d: %w", f.Track, err)
	}
	if err := c.deps.Jobs.Start(ctx, JobRef{RequestID: requestID, Track: f.Track, Attempt: firstAttempt}); err != nil {
		return fmt.Errorf("start transcription of track %d: %w", f.Track, err)
	}
	return nil
}

// discard は依頼のデータを消す。呼び出し元の期限が切れていても消せるよう、期限を取り直す。
func (c *Command) discard(ctx context.Context, m Manifest) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if err := c.deps.Store.Discard(cleanupCtx, m); err != nil {
		return fmt.Errorf("discard request: %w", err)
	}
	return nil
}

// newManifest は、受け付けた依頼の記録を作る。依頼 ID は interaction ID。bot の話者は含めない。
func newManifest(inv command.Invocation, rec craig.Recording) Manifest {
	return Manifest{
		RequestID:   inv.InteractionID,
		ChannelID:   inv.ChannelID,
		RequesterID: inv.UserID,
		StartedAt:   rec.StartedAt,
		Duration:    rec.Duration,
		Speakers:    targetSpeakers(rec.Speakers),
	}
}

// targetSpeakers は、bot を除いた話者を録音の順に返す。1 人もいなければ nil。
func targetSpeakers(speakers []craig.Speaker) []Speaker {
	var targets []Speaker
	for _, s := range speakers {
		if s.Bot {
			continue
		}
		targets = append(targets, Speaker{Track: s.Track, Name: s.Name})
	}
	return targets
}

func acceptedResult() command.Result {
	return command.Result{Text: acceptedMessage}
}

// noSpeechResult は、「発話なし」の投稿の結果。見出しが Markdown のため、長文の添付は result.md になる。
func noSpeechResult(m Manifest) command.Result {
	return command.Result{Text: formatNoSpeechReport(m), Format: command.TextFormatMarkdown}
}

// requesterPost は、依頼のチャンネルへ、依頼者にだけ通知して投稿する内容。
func requesterPost(m Manifest, result command.Result) reply.ChannelPost {
	return reply.ChannelPost{ChannelID: m.ChannelID, NotifyUserID: m.RequesterID, Result: result}
}
