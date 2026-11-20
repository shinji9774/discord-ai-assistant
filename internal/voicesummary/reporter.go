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
	stageManifest  = "manifest"
	stageStatus    = "status"
	stageClaim     = "claim"
	stageRead      = "read"
	stageSummarize = "summarize"
	stagePost      = "post"
	stageDiscard   = "discard"

	// 再試行と失敗の通知の段階（stageRetry、stageReleaseClaim は Reporter が使う）。
	stageRetry         = "retry"
	stageReleaseClaim  = "release claim"
	stageNotifyFailure = "notify failure"
)

// ResultStore は、完了後の側が使う置き場。
type ResultStore interface {
	ClaimStore
	// ReadTranscript は、1 トラックの文字起こしの結果（Transcribe の JSON）を返す。
	ReadTranscript(ctx context.Context, requestID string, track int) ([]byte, error)
	// ReleaseClaim は、投稿する権利を手放す。データは消さない。
	ReleaseClaim(ctx context.Context, requestID string) error
}

// JobStatus は、1 トラックの文字起こしのジョブの状態。
type JobStatus int

const (
	JobPending JobStatus = iota // 未開始、または処理中
	JobCompleted
	JobFailed
)

// JobController は、1 トラックの文字起こしの開始と状態の取得を行う。
type JobController interface {
	JobStarter
	Status(ctx context.Context, job JobRef) (JobStatus, error)
}

// transcriptionOutcome は、依頼の文字起こしの全体の状態。
type transcriptionOutcome int

const (
	transcriptionPending transcriptionOutcome = iota
	transcriptionCompleted
	transcriptionFailed
)

// Reporter は、文字起こしの完了を受けて、統合・要約・投稿・削除を順に行う。
type Reporter struct {
	store    ResultStore
	jobs     JobController
	gen      llm.TextGenerator
	poster   reply.ChannelPoster
	failures *FailureNotifier
	cfg      Config
	logger   *slog.Logger
}

func NewReporter(store ResultStore, jobs JobController, gen llm.TextGenerator,
	poster reply.ChannelPoster, cfg Config, logger *slog.Logger) *Reporter {
	return &Reporter{
		store: store, jobs: jobs, gen: gen, poster: poster,
		failures: NewFailureNotifier(store, poster, logger), cfg: cfg, logger: logger,
	}
}

// Report は、jobName のジョブが属する依頼を進める。
// 失敗したトラックは 1 回だけ開き直し、全トラックが終わって投稿する権利を取れた実行だけが、結果（やり直しも失敗なら失敗）を 1 回投稿して依頼のデータを消す。
// 結果を作れない、または Discord が投稿を断ったときは、権利を手放してエラーを返し、Lambda の再試行に任せる。
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
	outcome, err := r.transcriptionOutcome(ctx, m)
	if err != nil || outcome == transcriptionPending {
		return err
	}
	err = r.store.ClaimReport(ctx, m.RequestID)
	if errors.Is(err, ErrReportClaimed) {
		return nil
	}
	if err != nil {
		return r.fail(m.RequestID, stageClaim, fmt.Errorf("claim report: %w", err))
	}
	if outcome == transcriptionFailed {
		r.failures.notifyFailure(ctx, m)
		return nil
	}
	return r.deliver(ctx, m)
}

// transcriptionOutcome は、全トラックの状態をまとめる。待つトラックがあれば Pending、
// 失敗（最終）のトラックがあれば Failed、それ以外は Completed。
func (r *Reporter) transcriptionOutcome(ctx context.Context, m Manifest) (transcriptionOutcome, error) {
	outcomes := make([]transcriptionOutcome, 0, len(m.Speakers))
	for _, speaker := range m.Speakers {
		outcome, err := r.trackOutcome(ctx, m.RequestID, speaker.Track)
		if err != nil {
			return transcriptionPending, err
		}
		outcomes = append(outcomes, outcome)
	}
	if slices.Contains(outcomes, transcriptionPending) {
		return transcriptionPending, nil
	}
	if slices.Contains(outcomes, transcriptionFailed) {
		return transcriptionFailed, nil
	}
	return transcriptionCompleted, nil
}

// trackOutcome は、1 トラックの状態を返す。試行 1 が失敗していれば、試行 2 を見る。
func (r *Reporter) trackOutcome(ctx context.Context, requestID string, track int) (transcriptionOutcome, error) {
	status, err := r.jobs.Status(ctx, JobRef{RequestID: requestID, Track: track, Attempt: firstAttempt})
	if err != nil {
		return transcriptionPending, r.fail(requestID, stageStatus, fmt.Errorf("status of track %d: %w", track, err))
	}
	switch status {
	case JobCompleted:
		return transcriptionCompleted, nil
	case JobPending:
		return transcriptionPending, nil
	}
	return r.retryOutcome(ctx, JobRef{RequestID: requestID, Track: track, Attempt: retryAttempt})
}

// retryOutcome は、開き直したジョブの状態を返す。終わっていなければ開始する（既にあれば何もしない）。
func (r *Reporter) retryOutcome(ctx context.Context, retry JobRef) (transcriptionOutcome, error) {
	status, err := r.jobs.Status(ctx, retry)
	if err != nil {
		return transcriptionPending, r.fail(retry.RequestID, stageStatus, fmt.Errorf("status of track %d retry: %w", retry.Track, err))
	}
	switch status {
	case JobCompleted:
		return transcriptionCompleted, nil
	case JobFailed:
		return transcriptionFailed, nil
	}
	if err := r.jobs.Start(ctx, retry); err != nil {
		return transcriptionPending, r.fail(retry.RequestID, stageRetry, fmt.Errorf("start retry of track %d: %w", retry.Track, err))
	}
	return transcriptionPending, nil
}

// deliver は、結果を投稿してデータを消す。やり直せる失敗では claim を手放してエラーを返す。
func (r *Reporter) deliver(ctx context.Context, m Manifest) error {
	result, stage, err := r.buildResult(ctx, m)
	if err != nil {
		return r.retryLater(ctx, m, stage, err)
	}
	err = r.poster.Post(ctx, requesterPost(m, result))
	if errors.Is(err, reply.ErrPostRejected) {
		return r.retryLater(ctx, m, stagePost, fmt.Errorf("post report: %w", err))
	}
	if err != nil {
		// 投稿されたか分からない。重複を避けるため、やり直さない。
		r.logFailure(m.RequestID, stagePost, fmt.Errorf("post report: %w", err))
	}
	r.failures.discard(ctx, m)
	return nil
}

// retryLater は、claim を手放してエラーを返し、Lambda の再試行に任せる。データは残す。
// 呼び出し元の期限が切れていても手放せるよう、期限を取り直す。
func (r *Reporter) retryLater(ctx context.Context, m Manifest, stage string, cause error) error {
	err := r.fail(m.RequestID, stage, cause)
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if releaseErr := r.store.ReleaseClaim(releaseCtx, m.RequestID); releaseErr != nil {
		return errors.Join(err, r.fail(m.RequestID, stageReleaseClaim, fmt.Errorf("release claim: %w", releaseErr)))
	}
	return err
}

// buildResult は、投稿する結果を作る。作れなければ、失敗した段階とエラーを返す。
// 発話が 1 つも無ければ、要約せずに「発話なし」にする。
func (r *Reporter) buildResult(ctx context.Context, m Manifest) (command.Result, string, error) {
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

// logFailure は、Reporter の失敗を logFailure（package 関数）でログに出す。
func (r *Reporter) logFailure(requestID, stage string, err error) {
	logFailure(r.logger, requestID, stage, err)
}

// logFailure は、コマンド名、依頼 ID、失敗した段階、原因をログに出す。
func logFailure(logger *slog.Logger, requestID, stage string, err error) {
	logger.Error(reportFailedMessage,
		slog.String(logKeyCommand, CommandName),
		slog.String(logKeyRequestID, requestID),
		slog.String(logKeyStage, stage),
		slog.Any(logKeyError, err),
	)
}
