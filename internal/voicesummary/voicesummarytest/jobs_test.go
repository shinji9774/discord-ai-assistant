package voicesummarytest_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"discord-ai-assistant/internal/voicesummary"
	"discord-ai-assistant/internal/voicesummary/voicesummarytest"
)

// 条件: トラック 1 だけ状態を仕込んだジョブの状態を、トラック 1 と、開始されていないトラック 2 について読む。
// 内容: トラック 1 は仕込んだ状態、トラック 2 は JobPending（未開始は処理中と同じ扱い）を返す。
func TestJobsStatus_SetAndUnknownJobs_ReturnsSetStatusOrPending(t *testing.T) {
	jobs := &voicesummarytest.Jobs{Statuses: map[voicesummary.JobRef]voicesummary.JobStatus{
		{RequestID: "1441234567890123456", Track: 1, Attempt: 1}: voicesummary.JobCompleted,
	}}
	tests := []struct {
		name  string
		track int
		want  voicesummary.JobStatus
	}{
		{"set status", 1, voicesummary.JobCompleted},
		{"not started", 2, voicesummary.JobPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := jobs.Status(context.Background(), voicesummary.JobRef{RequestID: "1441234567890123456", Track: tt.track, Attempt: 1})

			if err != nil || got != tt.want {
				t.Errorf("Status = %v, %v; want %v, nil", got, err, tt.want)
			}
		})
	}
}

// 条件: 2 回目の Start が失敗するよう仕込み、2 トラックを順に開始する。
// 内容: 2 回目は仕込んだエラーを返し、開始できたジョブとしては 1 回目のものだけが残る。
func TestJobsStart_SecondStartFails_RecordsOnlyStartedJob(t *testing.T) {
	errStart := errors.New("start failed")
	jobs := &voicesummarytest.Jobs{StartErrs: []error{nil, errStart}}
	if err := jobs.Start(context.Background(), voicesummary.JobRef{RequestID: "1441234567890123456", Track: 1, Attempt: 1}); err != nil {
		t.Fatalf("first Start: %v", err)
	}

	err := jobs.Start(context.Background(), voicesummary.JobRef{RequestID: "1441234567890123456", Track: 2, Attempt: 1})

	if want := []voicesummary.JobRef{{RequestID: "1441234567890123456", Track: 1, Attempt: 1}}; !errors.Is(err, errStart) || !reflect.DeepEqual(jobs.Started, want) {
		t.Errorf("Start = %v (started %+v), want %v (started %+v)", err, jobs.Started, errStart, want)
	}
}
