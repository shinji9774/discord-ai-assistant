package voicesummary

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/reply"
)

const (
	// transcriptFileName は、文字起こしの全文を添付するファイルの名前。
	transcriptFileName = "transcript.txt"

	// reportFailedMessage は、完了後の処理が失敗したときのログのメッセージ。
	reportFailedMessage = "voice summary report failed"

	// ログの項目のキー。文字起こしと要約の本文は出さない。
	logKeyCommand   = "command"
	logKeyRequestID = "request_id"
	logKeyStage     = "stage"
	logKeyError     = "error"

	// 失敗した段階。ログの stage に出す。
	stageManifest   = "manifest"
	stageStatus     = "status"
	stageClaim      = "claim"
	stageTranscribe = "transcribe"
	stageRead       = "read"
	stageSummarize  = "summarize"
	stagePost       = "post"
	stageDiscard    = "discard"
)

// errTranscriptionFailed は、依頼の文字起こしのジョブが 1 つ以上失敗したことを表す。
var errTranscriptionFailed = errors.New("transcription job failed")

// ResultStore は、完了後の側が使う置き場。
type ResultStore interface {
	// ReadManifest は依頼の記録を返す。なければ ErrRequestNotFound。
	ReadManifest(ctx context.Context, requestID string) (Manifest, error)
	// ReadTranscript は、1 トラックの文字起こしの結果（Transcribe の JSON）を返す。
	ReadTranscript(ctx context.Context, requestID string, track int) ([]byte, error)
	// ClaimReport は、投稿する権利を取る。既に取られていれば ErrReportClaimed。
	ClaimReport(ctx context.Context, requestID string) error
	Discard(ctx context.Context, m Manifest) error
}

// JobStatus は、1 トラックの文字起こしのジョブの状態。
type JobStatus int

const (
	JobPending JobStatus = iota // 未開始、または処理中
	JobCompleted
	JobFailed
)

// JobInspector は、1 トラックの文字起こしの状態を返す。
type JobInspector interface {
	Status(ctx context.Context, requestID string, track int) (JobStatus, error)
}

// Reporter は、文字起こしの完了を受けて、統合・要約・投稿・削除を順に行う。
type Reporter struct {
	store  ResultStore
	jobs   JobInspector
	gen    llm.TextGenerator
	poster reply.ChannelPoster
	cfg    Config
	logger *slog.Logger
}

func NewReporter(store ResultStore, jobs JobInspector, gen llm.TextGenerator,
	poster reply.ChannelPoster, cfg Config, logger *slog.Logger) *Reporter {
	return &Reporter{store: store, jobs: jobs, gen: gen, poster: poster, cfg: cfg, logger: logger}
}

// Report は、jobName のジョブが属する依頼を進める。
// 全トラックのジョブが終わり、投稿する権利を取れた実行だけが、結果（作れなければ失敗）を 1 回投稿して依頼のデータを消す。
// この機能のジョブでない、依頼の記録が無い、終わっていないジョブがある、ほかの実行が担当している場合は、何もせず nil を返す。
func (r *Reporter) Report(ctx context.Context, jobName string) error {
	requestID, ok := requestIDOf(jobName)
	if !ok {
		return nil
	}
	m, err := r.store.ReadManifest(ctx, requestID)
	if errors.Is(err, ErrRequestNotFound) {
		return nil
	}
	if err != nil {
		return r.fail(requestID, stageManifest, fmt.Errorf("read manifest: %w", err))
	}
	statuses, err := r.statuses(ctx, m)
	if err != nil {
		return r.fail(requestID, stageStatus, err)
	}
	if slices.Contains(statuses, JobPending) {
		return nil
	}
	err = r.store.ClaimReport(ctx, requestID)
	if errors.Is(err, ErrReportClaimed) {
		return nil
	}
	if err != nil {
		return r.fail(requestID, stageClaim, fmt.Errorf("claim report: %w", err))
	}
	return r.deliver(ctx, m, slices.Contains(statuses, JobFailed))
}

// statuses は、依頼の全話者のジョブの状態を、マニフェストの話者の順に返す。
func (r *Reporter) statuses(ctx context.Context, m Manifest) ([]JobStatus, error) {
	statuses := make([]JobStatus, 0, len(m.Speakers))
	for _, s := range m.Speakers {
		status, err := r.jobs.Status(ctx, m.RequestID, s.Track)
		if err != nil {
			return nil, fmt.Errorf("status of track %d: %w", s.Track, err)
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

// deliver は、結果を作って 1 回だけ投稿し、依頼のデータを消す。結果を作れなければ失敗を投稿する。
// どの段階の失敗もログに出し、まとめて返す。
func (r *Reporter) deliver(ctx context.Context, m Manifest, anyJobFailed bool) error {
	result, stage, buildErr := r.buildResult(ctx, m, anyJobFailed)
	if buildErr != nil {
		r.logFailure(m.RequestID, stage, buildErr)
		result = command.Result{Text: command.FailureMessage}
	}
	var postErr, discardErr error
	if err := r.poster.Post(ctx, requesterPost(m, result)); err != nil {
		postErr = r.fail(m.RequestID, stagePost, fmt.Errorf("post report: %w", err))
	}
	if err := r.store.Discard(ctx, m); err != nil {
		discardErr = r.fail(m.RequestID, stageDiscard, fmt.Errorf("discard request: %w", err))
	}
	return errors.Join(buildErr, postErr, discardErr)
}

// buildResult は、投稿する結果を作る。作れなければ、失敗した段階とエラーを返す。
// 失敗したジョブがあれば、残りの結果を使わない。発話が 1 つも無ければ、要約せずに「発話なし」にする。
func (r *Reporter) buildResult(ctx context.Context, m Manifest, anyJobFailed bool) (command.Result, string, error) {
	if anyJobFailed {
		return command.Result{}, stageTranscribe, errTranscriptionFailed
	}
	utterances, err := r.readUtterances(ctx, m)
	if err != nil {
		return command.Result{}, stageRead, err
	}
	if len(utterances) == 0 {
		return noSpeechResult(m), "", nil
	}
	transcript := renderTranscript(utterances)
	summary, err := r.gen.Generate(ctx, llm.Request{
		System:          r.cfg.SystemPrompt,
		UserText:        transcript,
		MaxOutputTokens: r.cfg.MaxOutputTokens,
	})
	if err != nil {
		return command.Result{}, stageSummarize, fmt.Errorf("generate summary: %w", err)
	}
	return command.Result{
		Text:   formatReport(m, summary),
		Format: command.TextFormatMarkdown,
		Files:  []command.File{{Name: transcriptFileName, Content: []byte(transcript)}},
	}, "", nil
}

// readUtterances は、全話者の文字起こしの結果を読み、開始時刻の順の発話にまとめる。
func (r *Reporter) readUtterances(ctx context.Context, m Manifest) ([]Utterance, error) {
	bySpeaker := make(map[Speaker][]Segment, len(m.Speakers))
	for _, speaker := range m.Speakers {
		transcribeJSON, err := r.store.ReadTranscript(ctx, m.RequestID, speaker.Track)
		if err != nil {
			return nil, fmt.Errorf("read transcript of track %d: %w", speaker.Track, err)
		}
		segments, err := parseSegments(transcribeJSON)
		if err != nil {
			return nil, fmt.Errorf("parse transcript of track %d: %w", speaker.Track, err)
		}
		bySpeaker[speaker] = segments
	}
	return mergeUtterances(bySpeaker), nil
}

// fail は、失敗をログに出して、そのエラーを返す。
func (r *Reporter) fail(requestID, stage string, err error) error {
	r.logFailure(requestID, stage, err)
	return err
}

// logFailure は、コマンド名、依頼 ID、失敗した段階、原因をログに出す。
func (r *Reporter) logFailure(requestID, stage string, err error) {
	r.logger.Error(reportFailedMessage,
		slog.String(logKeyCommand, CommandName),
		slog.String(logKeyRequestID, requestID),
		slog.String(logKeyStage, stage),
		slog.Any(logKeyError, err),
	)
}
