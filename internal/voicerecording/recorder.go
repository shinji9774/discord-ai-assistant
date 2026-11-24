package voicerecording

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/voicesummary"
)

// VoiceConnection は、ボイスチャンネルへの 1 本の接続を表す。
type VoiceConnection interface {
	// Join は、ボイスチャンネルに参加し、音声を受信できる状態になったら戻る。
	Join(ctx context.Context, guildID, channelID string) error
	// Receive は、受信した音声を frames に渡し始める。frames は 1 つの goroutine から呼ぶ。
	Receive(frames FrameSink)
	// HumanCount は、bot ではない参加者の数を、参加の直後と、変わるたびに流す。
	HumanCount() <-chan int
	// Disconnected は、Leave を呼ぶ前に接続が切れたら閉じる。
	Disconnected() <-chan struct{}
	// Leave は、ボイスチャンネルから退出する。戻ったあとは frames を呼ばない。
	Leave(ctx context.Context)
}

// FrameSink は、1 人の話者の Opus フレームを 1 つ受け取る。
type FrameSink interface {
	WriteFrame(userID string, opus []byte, receivedAt time.Time)
}

var _ FrameSink = (*Tracks)(nil)

const (
	// endMarkerPollInterval は、録音中に終了マーカーを読む間隔。
	endMarkerPollInterval = 5 * time.Second
	// firstTrackNumber は、マニフェストと S3 の音声のトラックの最初の番号。
	firstTrackNumber = 1

	logKeyReason = "reason"
	logKeyTracks = "tracks"
	logKeyError  = "error"

	msgRecordingStarted     = "recording started"
	msgRecordingEnded       = "recording ended"
	msgNicknameChangeFailed = "bot nickname change failed"
	msgEndNoticeFailed      = "end notice post failed"
	msgLockReleaseFailed    = "recording lock release failed"
)

// ErrRequestMismatch は、ロックを持つ依頼が、録音タスクに渡された依頼と違うことを表す。
var ErrRequestMismatch = errors.New("recording lock belongs to another request")

// RecorderDependencies は、録音タスクが使う外部の操作。
type RecorderDependencies struct {
	Sessions *SessionStore
	Voice    VoiceConnection
	Guild    *DiscordGuild
	Poster   reply.ChannelPoster
	Audio    *voicesummary.S3Store
	Jobs     *voicesummary.TranscribeJobs
	Now      func() time.Time
	WorkDir  string // 録音中の FLAC を書くディレクトリ
}

// RecorderTiming は、録音タスクが使う時間の設定。
type RecorderTiming struct {
	MaxDuration time.Duration // 録音の上限
	StopPoll    time.Duration // 終了マーカーを読む間隔
	JoinTimeout time.Duration // 参加の待ち時間
}

// Recorder は、1 件の録音を、参加から文字起こしの開始まで行う。
type Recorder struct {
	deps   RecorderDependencies
	timing RecorderTiming
	logger *slog.Logger
}

// NewRecorder は Recorder を作る。logger には、呼び出し側が依頼 ID とサーバの ID を付けておく。
func NewRecorder(deps RecorderDependencies, timing RecorderTiming, logger *slog.Logger) *Recorder {
	return &Recorder{deps: deps, timing: timing, logger: logger}
}

// recording は、録音を終えた結果。tracks は除外した人を除いた、初めて発話した順のトラック。
type recording struct {
	reason    EndReason
	tracks    []Track
	startedAt time.Time
	endedAt   time.Time
}

// Run は、guildID のロックを持つ依頼 requestID の録音を行う。
func (r *Recorder) Run(ctx context.Context, guildID, requestID string) error {
	session, err := r.ownSession(ctx, guildID, requestID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithDeadline(ctx, session.Deadline)
	defer cancel()
	logger := r.logger

	started, err := r.start(ctx, session, logger)
	if err != nil {
		return err
	}
	result, err := r.record(ctx, session)
	if err != nil {
		return err
	}
	logger.Info(msgRecordingEnded, logKeyReason, string(result.reason), logKeyTracks, len(result.tracks))
	return r.finish(ctx, session, started, result, logger)
}

// ownSession は、ロックを読み、依頼 requestID のものであることを確かめる。
func (r *Recorder) ownSession(ctx context.Context, guildID, requestID string) (Session, error) {
	session, err := r.deps.Sessions.Active(ctx, guildID)
	if err != nil {
		return Session{}, fmt.Errorf("read recording lock: %w", err)
	}
	if session.RequestID != requestID {
		return Session{}, fmt.Errorf("%w: lock %s, task %s", ErrRequestMismatch, session.RequestID, requestID)
	}
	return session, nil
}

// start は、ボイスチャンネルに参加し、開始を記録して周知する。
func (r *Recorder) start(ctx context.Context, session Session, logger *slog.Logger) (Started, error) {
	joinCtx, cancel := context.WithTimeout(ctx, r.timing.JoinTimeout)
	defer cancel()
	if err := r.deps.Voice.Join(joinCtx, session.GuildID, session.VoiceChannelID); err != nil {
		return Started{}, fmt.Errorf("join voice channel: %w", err)
	}
	nick, err := r.deps.Guild.BotNickname(ctx, session.GuildID, session.BotUserID)
	if err != nil {
		return Started{}, fmt.Errorf("read bot nickname: %w", err)
	}
	started := Started{OriginalNickname: originalNickname(nick)}
	if err := r.deps.Sessions.MarkStarted(ctx, session, started); err != nil {
		return Started{}, fmt.Errorf("mark recording started: %w", err)
	}
	r.markNickname(ctx, session, started.OriginalNickname, logger)
	if err := r.deps.Poster.Post(ctx, startNotice(session)); err != nil {
		return Started{}, fmt.Errorf("post start notice: %w", err)
	}
	logger.Info(msgRecordingStarted)
	return started, nil
}

// markNickname は、bot の表示名に録音中の印を付ける。失敗しても録音は続ける。
func (r *Recorder) markNickname(ctx context.Context, session Session, original string, logger *slog.Logger) {
	displayName := original
	if displayName == "" {
		name, err := r.deps.Guild.MemberName(ctx, session.GuildID, session.BotUserID)
		if err != nil {
			logger.Warn(msgNicknameChangeFailed, logKeyError, err.Error())
			return
		}
		displayName = name
	}
	if err := r.deps.Guild.SetBotNickname(ctx, session.GuildID, recordingNickname(displayName)); err != nil {
		logger.Warn(msgNicknameChangeFailed, logKeyError, err.Error())
	}
}

// record は、終わるまで受信し、退出してトラックを閉じる。
func (r *Recorder) record(ctx context.Context, session Session) (recording, error) {
	startedAt := r.deps.Now()
	tracks := NewTracks(r.deps.WorkDir, startedAt)
	r.deps.Voice.Receive(tracks)
	reason, err := r.waitForEnd(ctx, session)
	if err != nil {
		return recording{}, fmt.Errorf("wait for recording end: %w", err)
	}
	r.deps.Voice.Leave(ctx)
	endedAt := r.deps.Now()
	closed, err := tracks.Close(endedAt)
	if err != nil {
		return recording{}, fmt.Errorf("close tracks: %w", err)
	}
	kept, err := r.withoutOptedOut(ctx, session, closed)
	if err != nil {
		return recording{}, err
	}
	return recording{reason: reason, tracks: kept, startedAt: startedAt, endedAt: endedAt}, nil
}

// waitForEnd は、録音が終わるまで待ち、最終の終了の理由を返す。
// 自動の理由は End で書き、先に書かれていた理由があればそれを返す。
func (r *Recorder) waitForEnd(ctx context.Context, session Session) (EndReason, error) {
	limit := time.NewTimer(r.timing.MaxDuration)
	defer limit.Stop()
	poll := time.NewTicker(r.timing.StopPoll)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-limit.C:
			return r.deps.Sessions.End(ctx, session, EndReasonTimeLimit)
		case <-r.deps.Voice.Disconnected():
			return r.deps.Sessions.End(ctx, session, EndReasonDisconnected)
		case humans := <-r.deps.Voice.HumanCount():
			if humans == 0 {
				return r.deps.Sessions.End(ctx, session, EndReasonEveryoneLeft)
			}
		case <-poll.C:
			reason, err := r.deps.Sessions.Ended(ctx, session)
			if errors.Is(err, ErrNotEnded) {
				continue
			}
			return reason, err
		}
	}
}

// withoutOptedOut は、除外した人のトラックを外し、その一時ファイルを消す。
func (r *Recorder) withoutOptedOut(ctx context.Context, session Session, tracks []Track) ([]Track, error) {
	kept := make([]Track, 0, len(tracks))
	for _, track := range tracks {
		optedOut, err := r.deps.Sessions.OptedOut(ctx, session, track.UserID)
		if err != nil {
			return nil, fmt.Errorf("read opt-out: %w", err)
		}
		if !optedOut {
			kept = append(kept, track)
			continue
		}
		if err := os.Remove(track.Path); err != nil {
			return nil, fmt.Errorf("remove opted-out track: %w", err)
		}
	}
	return kept, nil
}

// finish は、録音を保存して文字起こしを始め、終了を周知して、表示名とロックを戻す。
// 文字起こしを始めた時点で録音は引き渡し済みで、それ以降の失敗は録音を失わせないよう Warn にとどめる。
func (r *Recorder) finish(ctx context.Context, session Session, started Started, result recording, logger *slog.Logger) error {
	hasAudio := len(result.tracks) > 0
	var manifest voicesummary.Manifest
	if hasAudio {
		var err error
		if manifest, err = r.storeRecording(ctx, session, result); err != nil {
			return err
		}
	}
	label := endReasonLabel(result.reason, r.timing.MaxDuration)
	if err := r.deps.Poster.Post(ctx, endNotice(session, label, hasAudio)); err != nil {
		logger.Warn(msgEndNoticeFailed, logKeyError, err.Error())
	}
	restoreNickname(ctx, r.deps.Guild, session, started, logger)
	if hasAudio {
		if err := r.handOver(ctx, session, manifest); err != nil {
			return err
		}
	}
	r.releaseAfterHandover(ctx, session, logger)
	return nil
}

// handOver は、文字起こしを始めて、録音を voicereporter に引き渡す。
// 始められなければ、置いた音声とマニフェストを消してからエラーを返す。
func (r *Recorder) handOver(ctx context.Context, session Session, manifest voicesummary.Manifest) error {
	startErr := r.startTranscriptions(ctx, session, len(manifest.Speakers))
	if startErr == nil {
		return nil
	}
	var discardErr error
	if err := r.deps.Audio.Discard(ctx, manifest); err != nil {
		discardErr = fmt.Errorf("discard recording: %w", err)
	}
	return errors.Join(startErr, discardErr)
}

// releaseAfterHandover は、引き渡しの後でロックを外す。失敗してもロックは期限で切れるので、録音は失わせない。
func (r *Recorder) releaseAfterHandover(ctx context.Context, session Session, logger *slog.Logger) {
	if err := r.deps.Sessions.Release(ctx, session); err != nil {
		logger.Warn(msgLockReleaseFailed, logKeyError, err.Error())
	}
}

// storeRecording は、voicesummary の取り決めでマニフェストと音声を S3 に置く。
func (r *Recorder) storeRecording(ctx context.Context, session Session, result recording) (voicesummary.Manifest, error) {
	speakers := make([]voicesummary.Speaker, 0, len(result.tracks))
	for i, track := range result.tracks {
		name, err := r.deps.Guild.MemberName(ctx, session.GuildID, track.UserID)
		if err != nil {
			return voicesummary.Manifest{}, fmt.Errorf("read speaker name: %w", err)
		}
		speakers = append(speakers, voicesummary.Speaker{Track: firstTrackNumber + i, Name: name})
	}
	manifest := voicesummary.Manifest{
		RequestID:   session.RequestID,
		ChannelID:   session.TextChannelID,
		RequesterID: session.StarterID,
		StartedAt:   result.startedAt,
		Duration:    result.endedAt.Sub(result.startedAt),
		Speakers:    speakers,
	}
	if err := r.deps.Audio.CreateManifest(ctx, manifest); err != nil {
		return voicesummary.Manifest{}, fmt.Errorf("create manifest: %w", err)
	}
	for i, track := range result.tracks {
		if err := r.putAudio(ctx, session.RequestID, firstTrackNumber+i, track.Path); err != nil {
			return voicesummary.Manifest{}, err
		}
	}
	return manifest, nil
}

func (r *Recorder) putAudio(ctx context.Context, requestID string, trackNumber int, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open track: %w", err)
	}
	defer file.Close()
	if err := r.deps.Audio.PutAudio(ctx, requestID, trackNumber, file); err != nil {
		return fmt.Errorf("put audio: %w", err)
	}
	return nil
}

// startTranscriptions は、トラックごとに文字起こしのジョブを始める。
func (r *Recorder) startTranscriptions(ctx context.Context, session Session, trackCount int) error {
	for i := range trackCount {
		job := voicesummary.JobRef{RequestID: session.RequestID, Track: firstTrackNumber + i, Attempt: voicesummary.FirstAttempt}
		if err := r.deps.Jobs.Start(ctx, job); err != nil {
			return fmt.Errorf("start transcription: %w", err)
		}
	}
	return nil
}

// restoreNickname は、bot の表示名を録音の前に戻す。失敗しても後続の処理は続ける。
func restoreNickname(ctx context.Context, guild *DiscordGuild, session Session, started Started, logger *slog.Logger) {
	if err := guild.SetBotNickname(ctx, session.GuildID, started.OriginalNickname); err != nil {
		logger.Warn(msgNicknameChangeFailed, logKeyError, err.Error())
	}
}
