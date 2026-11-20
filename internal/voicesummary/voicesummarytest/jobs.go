package voicesummarytest

import (
	"context"

	"discord-ai-assistant/internal/voicesummary"
)

// Jobs は voicesummary.JobController のインメモリ実装。
// 並行には呼ばれない前提で排他しない。
type Jobs struct {
	StartErrs []error                                        // n 番目の Start の呼び出しで返すエラー。足りなければ nil
	Started   []voicesummary.JobRef                          // 開始できたジョブ（開始した順）
	Statuses  map[voicesummary.JobRef]voicesummary.JobStatus // Status が返す状態。無いジョブは JobPending（未開始）
	StatusErr error                                          // nil でなければ Status はこれを返す

	starts int // Start が呼ばれた回数
}

var (
	_ voicesummary.JobController = (*Jobs)(nil)
)

// Start は、仕込んだエラーがなければジョブを開始したものとして記録する。
func (j *Jobs) Start(_ context.Context, job voicesummary.JobRef) error {
	n := j.starts
	j.starts++
	if n < len(j.StartErrs) && j.StartErrs[n] != nil {
		return j.StartErrs[n]
	}
	j.Started = append(j.Started, job)
	return nil
}

func (j *Jobs) Status(_ context.Context, job voicesummary.JobRef) (voicesummary.JobStatus, error) {
	if j.StatusErr != nil {
		return voicesummary.JobPending, j.StatusErr
	}
	status, ok := j.Statuses[job]
	if !ok {
		return voicesummary.JobPending, nil
	}
	return status, nil
}
