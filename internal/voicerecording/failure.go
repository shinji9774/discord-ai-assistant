package voicerecording

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/voicesummary"
)

const (
	logKeyGuildID   = "guild_id"
	logKeyRequestID = "request_id"
	logKeyStarted   = "started"

	msgRecordingFailureHandled = "recording failure handled"

	// recorderExitCodeHandedOver は、録音タスクが引き渡しまで終えて正常に終了したときの exit code。
	recorderExitCodeHandedOver = 0
)

// StopOutcome は、止まった録音タスクが、録音を引き渡すところまで終えていたかを表す。
type StopOutcome int

const (
	// StopUnfinished は、引き渡しの前に止まった（異常終了、起動の失敗など）。exit code が分からない場合も含む。
	StopUnfinished StopOutcome = iota
	// StopHandedOver は、録音タスクが引き渡しまで終えて正常に終了した。
	StopHandedOver
)

// StoppedTask は、止まった録音タスクのサーバと依頼の ID と、その終わり方。
type StoppedTask struct {
	GuildID   string
	RequestID string
	Outcome   StopOutcome
}

// ErrNotRecordingTask は、停止したタスクの上書きに、録音のサーバと依頼の ID が無いことを表す。
var ErrNotRecordingTask = errors.New("stopped task has no recording request")

// FailureDependencies は、録音タスクの異常終了の後始末が使う外部の操作。
type FailureDependencies struct {
	Sessions *SessionStore
	Audio    *voicesummary.S3Store
	Guild    *DiscordGuild
	Poster   reply.ChannelPoster
}

// FailureHandler は、止まった録音タスクの後始末（音声の破棄・周知・表示名・ロック）をする。
type FailureHandler struct {
	deps   FailureDependencies
	logger *slog.Logger
}

func NewFailureHandler(deps FailureDependencies, logger *slog.Logger) *FailureHandler {
	return &FailureHandler{deps: deps, logger: logger}
}

// stoppedTask は、ECS Task State Change の detail のうち、録音タスクに渡した環境変数の部分。
type stoppedTask struct {
	Containers []struct {
		Name     string `json:"name"`
		ExitCode *int   `json:"exitCode"`
	} `json:"containers"`
	Overrides struct {
		ContainerOverrides []struct {
			Name        string `json:"name"`
			Environment []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"environment"`
		} `json:"containerOverrides"`
	} `json:"overrides"`
}

// StoppedRecording は、ECS Task State Change の detail から、録音タスクに渡したサーバと依頼の ID と、終わり方を読む。
func StoppedRecording(detail json.RawMessage) (StoppedTask, error) {
	var task stoppedTask
	if err := json.Unmarshal(detail, &task); err != nil {
		return StoppedTask{}, fmt.Errorf("decode task state change: %w", err)
	}
	env := make(map[string]string)
	for _, override := range task.Overrides.ContainerOverrides {
		if override.Name != recorderContainerName {
			continue
		}
		for _, pair := range override.Environment {
			env[pair.Name] = pair.Value
		}
	}
	guildID, requestID := env[EnvGuildID], env[EnvRequestID]
	if guildID == "" || requestID == "" {
		return StoppedTask{}, ErrNotRecordingTask
	}
	return StoppedTask{GuildID: guildID, RequestID: requestID, Outcome: task.outcome()}, nil
}

// outcome は、録音タスクのコンテナの exit code から終わり方を決める。exit code が無ければ引き渡しの前に止まったとみなす。
func (t stoppedTask) outcome() StopOutcome {
	for _, container := range t.Containers {
		if container.Name == recorderContainerName && container.ExitCode != nil && *container.ExitCode == recorderExitCodeHandedOver {
			return StopHandedOver
		}
	}
	return StopUnfinished
}

// Handle は、止まった録音タスク task の後始末をする。
// ロックがその依頼のものでなければ、正常に終わった後か別の録音なので何もしない。
// 引き渡しまで終えていたタスクは、音声を残して投稿もせず、残ったロックだけを外す。
func (h *FailureHandler) Handle(ctx context.Context, task StoppedTask) error {
	guildID, requestID := task.GuildID, task.RequestID
	session, err := h.deps.Sessions.Active(ctx, guildID)
	if errors.Is(err, ErrNoRecording) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read recording lock: %w", err)
	}
	if session.RequestID != requestID {
		return nil
	}
	logger := h.logger.With(logKeyGuildID, guildID, logKeyRequestID, requestID)
	if task.Outcome == StopHandedOver {
		return h.releaseHandedOver(ctx, session)
	}

	if err := h.discardAudio(ctx, requestID); err != nil {
		return err
	}
	started, wasStarted, err := h.startedRecord(ctx, session)
	if err != nil {
		return err
	}
	postErr := h.deps.Poster.Post(ctx, failureNotice(session, wasStarted))
	if postErr != nil {
		postErr = fmt.Errorf("post failure notice: %w", postErr)
	}
	if wasStarted {
		restoreNickname(ctx, h.deps.Guild, session, started, logger)
	}
	// ロックはセッションの期限で切れるので、解除の失敗で再試行（投稿の重複）を起こさない。
	if err := h.deps.Sessions.Release(ctx, session); err != nil {
		logger.Warn(msgLockReleaseFailed, logKeyError, err.Error())
	}
	logger.Info(msgRecordingFailureHandled, logKeyStarted, wasStarted)
	return postErr
}

// releaseHandedOver は、引き渡し済みの録音の、残ったロックを外す。失敗は再試行に任せる（投稿しないので重複しない）。
func (h *FailureHandler) releaseHandedOver(ctx context.Context, session Session) error {
	if err := h.deps.Sessions.Release(ctx, session); err != nil {
		return fmt.Errorf("release recording lock: %w", err)
	}
	return nil
}

// discardAudio は、録音タスクが置いた依頼のマニフェストと音声を消す。マニフェストが無ければ何もしない。
func (h *FailureHandler) discardAudio(ctx context.Context, requestID string) error {
	manifest, err := h.deps.Audio.ReadManifest(ctx, requestID)
	if errors.Is(err, voicesummary.ErrRequestNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	if err := h.deps.Audio.Discard(ctx, manifest); err != nil {
		return fmt.Errorf("discard recording: %w", err)
	}
	return nil
}

// startedRecord は、開始の記録と、開始の投稿まで済んでいたかを返す。
func (h *FailureHandler) startedRecord(ctx context.Context, session Session) (Started, bool, error) {
	started, err := h.deps.Sessions.Started(ctx, session)
	if errors.Is(err, ErrNotStarted) {
		return Started{}, false, nil
	}
	if err != nil {
		return Started{}, false, fmt.Errorf("read recording start: %w", err)
	}
	return started, true, nil
}
