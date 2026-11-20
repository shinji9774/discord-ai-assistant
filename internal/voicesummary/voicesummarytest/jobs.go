package voicesummarytest

import (
	"context"

	"discord-ai-assistant/internal/voicesummary"
)

// Job は、1 トラックの文字起こしのジョブ。
type Job struct {
	RequestID string
	Track     int
}

// Jobs は voicesummary.JobStarter と voicesummary.JobInspector のインメモリ実装。
// 並行には呼ばれない前提で排他しない。
type Jobs struct {
	StartErrs []error                        // n 番目の Start の呼び出しで返すエラー。足りなければ nil
	Started   []Job                          // 開始できたジョブ（開始した順）
	Statuses  map[Job]voicesummary.JobStatus // Status が返す状態。無いジョブは JobPending（未開始）
	StatusErr error                          // nil でなければ Status はこれを返す

	starts int // Start が呼ばれた回数
}

var (
	_ voicesummary.JobStarter   = (*Jobs)(nil)
	_ voicesummary.JobInspector = (*Jobs)(nil)
)

// Start は、仕込んだエラーがなければジョブを開始したものとして記録する。
func (j *Jobs) Start(_ context.Context, requestID string, track int) error {
	n := j.starts
	j.starts++
	if n < len(j.StartErrs) && j.StartErrs[n] != nil {
		return j.StartErrs[n]
	}
	j.Started = append(j.Started, Job{RequestID: requestID, Track: track})
	return nil
}

func (j *Jobs) Status(_ context.Context, requestID string, track int) (voicesummary.JobStatus, error) {
	if j.StatusErr != nil {
		return voicesummary.JobPending, j.StatusErr
	}
	status, ok := j.Statuses[Job{RequestID: requestID, Track: track}]
	if !ok {
		return voicesummary.JobPending, nil
	}
	return status, nil
}
