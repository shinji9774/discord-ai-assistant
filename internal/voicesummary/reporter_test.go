package voicesummary_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/llm/llmtest"
	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/reply/replytest"
	"discord-ai-assistant/internal/voicesummary"
	"discord-ai-assistant/internal/voicesummary/voicesummarytest"
)

const (
	// reportJobName は、はなこ（トラック 2）のジョブの名前。完了のイベントがこの名前で届く。
	reportJobName = "summarize-voice-1441234567890123456-2-1"
	// reportSummary は、モデルが返す要約。ログに出ないことを確かめるため 1 行にする。
	reportSummary = "予算は来週までに確定する"
)

// segmentJSON は、Transcribe の結果の results.audio_segments[] の 1 つ。時刻は秒の文字列（Transcribe の形式）。
type segmentJSON struct {
	StartTime  string `json:"start_time"`
	EndTime    string `json:"end_time"`
	Transcript string `json:"transcript"`
}

// transcribeResultJSON は、segments を持つ Transcribe の結果の JSON。segments が無ければ空の配列にする。
func transcribeResultJSON(t *testing.T, segments ...segmentJSON) []byte {
	t.Helper()
	result := map[string]any{"results": map[string]any{"audio_segments": append([]segmentJSON{}, segments...)}}
	content, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal transcribe result: %v", err)
	}
	return content
}

// reportUtterances は、scenario の 2 トラックの結果を開始時刻の順にまとめた発話。
// たろう（0.5 秒、10 秒）の間に、はなこ（3.25 秒）が入る。
func reportUtterances() []voicesummary.Utterance {
	return []voicesummary.Utterance{
		{Start: 500 * time.Millisecond, Speaker: "たろう", Text: "おはようございます"},
		{Start: 3250 * time.Millisecond, Speaker: "はなこ", Text: "よろしくお願いします"},
		{Start: 10 * time.Second, Speaker: "たろう", Text: "議題は予算です"},
	}
}

// reportBodies は、ログに出てはならない本文（文字起こしと要約）。
func reportBodies() []string {
	return []string{"おはようございます", "よろしくお願いします", "議題は予算です", reportSummary}
}

// reporterScenario は、acceptedManifest の依頼を受け付けて、2 トラックとも文字起こしが終わった状態の Reporter の依存。
type reporterScenario struct {
	store  *voicesummarytest.Store
	jobs   *voicesummarytest.Jobs
	gen    *llmtest.Generator
	poster *replytest.ChannelPoster
	logs   *bytes.Buffer
}

func newReporterScenario(t *testing.T) *reporterScenario {
	t.Helper()
	ctx := context.Background()
	store := &voicesummarytest.Store{}
	if err := store.CreateManifest(ctx, acceptedManifest()); err != nil {
		t.Fatalf("CreateManifest: %v", err)
	}
	for track, audio := range map[int]string{1: "たろうの音声", 2: "はなこの音声"} {
		if err := store.PutAudio(ctx, requestID, track, strings.NewReader(audio)); err != nil {
			t.Fatalf("PutAudio: %v", err)
		}
	}
	store.PutTranscript(requestID, 1, transcribeResultJSON(t,
		segmentJSON{StartTime: "0.5", EndTime: "2.0", Transcript: "おはようございます"},
		segmentJSON{StartTime: "10.0", EndTime: "12.5", Transcript: "議題は予算です"},
	))
	store.PutTranscript(requestID, 2, transcribeResultJSON(t,
		segmentJSON{StartTime: "3.25", EndTime: "5.0", Transcript: "よろしくお願いします"},
	))
	jobs := &voicesummarytest.Jobs{Statuses: map[voicesummary.JobRef]voicesummary.JobStatus{
		{RequestID: requestID, Track: 1, Attempt: 1}: voicesummary.JobCompleted,
		{RequestID: requestID, Track: 2, Attempt: 1}: voicesummary.JobCompleted,
	}}
	return &reporterScenario{
		store:  store,
		jobs:   jobs,
		gen:    &llmtest.Generator{Result: llm.Result{Text: reportSummary}},
		poster: &replytest.ChannelPoster{},
		logs:   &bytes.Buffer{},
	}
}

// report は、いまの依存で作った Reporter に、jobName のジョブの終了を 1 回伝える。
func (s *reporterScenario) report(jobName string) error {
	logger := slog.New(slog.NewJSONHandler(s.logs, nil))
	reporter := voicesummary.NewReporter(s.store, s.jobs, s.gen, s.poster, validConfig(), logger)
	return reporter.Report(context.Background(), jobName)
}

// reportLog は、Reporter が出したログ 1 行のうち、比べる項目。
type reportLog struct {
	Level     string
	Msg       string
	Command   string
	RequestID string
	Stage     string
	HasError  bool // 原因（error）が出ているか
}

// failureLog は、stage の段階で失敗したときに出るログ。
func failureLog(stage string) reportLog {
	return reportLog{Level: "ERROR", Msg: "voice summary report failed", Command: "summarize-voice", RequestID: requestID, Stage: stage, HasError: true}
}

func (s *reporterScenario) parsedLogs(t *testing.T) []reportLog {
	t.Helper()
	return parseReportLogs(t, s.logs)
}

// parseReportLogs は、logs に出た JSON のログを 1 行ずつ reportLog にする。
func parseReportLogs(t *testing.T, logs *bytes.Buffer) []reportLog {
	t.Helper()
	var parsed []reportLog
	for line := range strings.Lines(logs.String()) {
		var entry struct {
			Level     string `json:"level"`
			Msg       string `json:"msg"`
			Command   string `json:"command"`
			RequestID string `json:"request_id"`
			Stage     string `json:"stage"`
			Error     string `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("unmarshal log %q: %v", line, err)
		}
		parsed = append(parsed, reportLog{
			Level: entry.Level, Msg: entry.Msg, Command: entry.Command,
			RequestID: entry.RequestID, Stage: entry.Stage, HasError: entry.Error != "",
		})
	}
	return parsed
}

// reporterOutcome は、Report の終わり方と、置き場・モデル・投稿・ログに残ったもの。
type reporterOutcome struct {
	Failed   bool // Report がエラーを返したか
	Keys     []string
	Started  []voicesummary.JobRef
	Requests []llm.Request
	Posts    []reply.ChannelPost
	Logs     []reportLog
}

// verify は、残ったものが want と一致し、ログに文字起こしと要約の本文が出ていないことを確かめる。
func (s *reporterScenario) verify(t *testing.T, err error, want reporterOutcome) {
	t.Helper()
	got := reporterOutcome{
		Failed:   err != nil,
		Keys:     s.store.Keys(),
		Started:  s.jobs.Started,
		Requests: s.gen.Requests,
		Posts:    s.poster.Posts,
		Logs:     s.parsedLogs(t),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("outcome =\n%+v\nwant\n%+v\n(err = %v)", got, want, err)
	}
	for _, body := range reportBodies() {
		if strings.Contains(s.logs.String(), body) {
			t.Errorf("logs contain body %q:\n%s", body, s.logs.String())
		}
	}
}

// acceptedRequestKeys は、受け付けて文字起こしが終わった依頼が置き場に持つもの。
func acceptedRequestKeys() []string {
	return []string{
		requestID + "/audio/1.flac", requestID + "/audio/2.flac", requestID + "/manifest.json",
		requestID + "/transcripts/1.json", requestID + "/transcripts/2.json",
	}
}

// claimedRequestKeys は、acceptedRequestKeys に、投稿の権利（claim）が加わったもの。
func claimedRequestKeys() []string {
	return []string{
		requestID + "/audio/1.flac", requestID + "/audio/2.flac", requestID + "/manifest.json",
		requestID + "/report.claim", requestID + "/transcripts/1.json", requestID + "/transcripts/2.json",
	}
}

// reportedRequestKeys は、報告を終えた依頼が置き場に持つもの。claim だけが残る（再配送されても二度投稿しないため）。
func reportedRequestKeys() []string {
	return []string{requestID + "/report.claim"}
}

// summaryRequest は、時刻順の文字起こし全体を、設定の指示文と上限で 1 回だけ要約する依頼。
func summaryRequest() llm.Request {
	cfg := validConfig()
	return llm.Request{System: cfg.SystemPrompt, UserText: voicesummary.RenderTranscript(reportUtterances()), MaxOutputTokens: cfg.MaxOutputTokens}
}

// summaryPost は、見出しと要約を本文に、文字起こしを transcript.txt に入れて、依頼者に通知して投稿する内容。
func summaryPost() reply.ChannelPost {
	return reply.ChannelPost{
		ChannelID:    voiceChannelID,
		NotifyUserID: voiceRequesterID,
		Result: command.Result{
			Text:   voicesummary.FormatReport(acceptedManifest(), llm.Result{Text: reportSummary}),
			Format: command.TextFormatMarkdown,
			Files:  []command.File{{Name: "transcript.txt", Content: []byte(voicesummary.RenderTranscript(reportUtterances()))}},
		},
	}
}

// failurePost は、失敗を依頼者に通知して投稿する内容。
func failurePost() reply.ChannelPost {
	return reply.ChannelPost{ChannelID: voiceChannelID, NotifyUserID: voiceRequesterID, Result: command.Result{Text: command.FailureMessage}}
}

// 条件: 依頼の 2 トラックの文字起こしがどちらも終わり、最後に終わったジョブの終了が届く。
// 内容: 発話を時刻順にまとめて 1 回だけ要約し、見出しと要約と transcript.txt を依頼者に通知して投稿してから、依頼のデータを消す（claim は残す）。
func TestReport_AllJobsCompleted_PostsSummaryAndDiscards(t *testing.T) {
	s := newReporterScenario(t)

	err := s.report(reportJobName)

	s.verify(t, err, reporterOutcome{Keys: reportedRequestKeys(), Requests: []llm.Request{summaryRequest()}, Posts: []reply.ChannelPost{summaryPost()}})
}

// 条件: 投稿と削除を終えた依頼のジョブの終了が、もう一度届く（イベントの重複）。
// 内容: 2 回目は（マニフェストが無いので）何もせず、要約も投稿も 1 回のまま。置き場には claim だけが残る。
func TestReport_CalledAgainAfterReporting_PostsOnlyOnce(t *testing.T) {
	s := newReporterScenario(t)
	if err := s.report(reportJobName); err != nil {
		t.Fatalf("first Report: %v", err)
	}

	err := s.report(reportJobName)

	s.verify(t, err, reporterOutcome{Keys: reportedRequestKeys(), Requests: []llm.Request{summaryRequest()}, Posts: []reply.ChannelPost{summaryPost()}})
}

// 条件: ほかのジョブが処理中または未開始、ほかの実行が投稿の権利を取り済み、依頼の記録が無い、この機能のジョブでない。
// 内容: どれも何もせずに nil を返す。要約も投稿もログもせず、置き場はそのまま。
func TestReport_NotReadyOrNotMine_DoesNothing(t *testing.T) {
	tests := []struct {
		name     string
		arrange  func(t *testing.T, s *reporterScenario)
		jobName  string
		wantKeys []string
	}{
		{"other job in progress", func(_ *testing.T, s *reporterScenario) {
			s.jobs.Statuses[voicesummary.JobRef{RequestID: requestID, Track: 1, Attempt: 1}] = voicesummary.JobPending
		}, reportJobName, acceptedRequestKeys()},
		{"other job not started", func(_ *testing.T, s *reporterScenario) {
			delete(s.jobs.Statuses, voicesummary.JobRef{RequestID: requestID, Track: 1, Attempt: 1})
		}, reportJobName, acceptedRequestKeys()},
		{"already claimed by another run", func(t *testing.T, s *reporterScenario) {
			if err := s.store.ClaimReport(context.Background(), requestID); err != nil {
				t.Fatalf("ClaimReport: %v", err)
			}
		}, reportJobName, claimedRequestKeys()},
		{"request not found", func(*testing.T, *reporterScenario) {}, "summarize-voice-1449999999999999999-1-1", acceptedRequestKeys()},
		{"job of another feature", func(*testing.T, *reporterScenario) {}, "other-feature-job-1", acceptedRequestKeys()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newReporterScenario(t)
			tt.arrange(t, s)

			err := s.report(tt.jobName)

			s.verify(t, err, reporterOutcome{Keys: tt.wantKeys})
		})
	}
}

// 条件: 2 トラックの結果に、本文のある発話が 1 つも無い（空白だけの発話と、発話の無いトラック）。
// 内容: 要約せずに、話者を見出しに並べた「発話なし」を依頼者に通知して投稿し、依頼のデータを消す（claim は残す）。
func TestReport_NoUtterances_PostsNoSpeechReportAndDiscards(t *testing.T) {
	s := newReporterScenario(t)
	s.store.PutTranscript(requestID, 1, transcribeResultJSON(t, segmentJSON{StartTime: "1.0", EndTime: "2.0", Transcript: "  "}))
	s.store.PutTranscript(requestID, 2, transcribeResultJSON(t))

	err := s.report(reportJobName)

	s.verify(t, err, reporterOutcome{Keys: reportedRequestKeys(), Posts: []reply.ChannelPost{noSpeechPost(acceptedManifest())}})
}

// 条件: トラック 1 の試行 1 が FAILED、トラック 2 は COMPLETED。
// 内容: トラック 1 の試行 2 を開始し、投稿せず、claim を作らずに nil を返す。
func TestReport_FirstAttemptFailed_StartsRetryAndWaits(t *testing.T) {
	s := newReporterScenario(t)
	s.jobs.Statuses[voicesummary.JobRef{RequestID: requestID, Track: 1, Attempt: voicesummary.FirstAttempt}] = voicesummary.JobFailed

	err := s.report(reportJobName)

	s.verify(t, err, reporterOutcome{
		Keys:    acceptedRequestKeys(),
		Started: []voicesummary.JobRef{{RequestID: requestID, Track: 1, Attempt: voicesummary.RetryAttempt}},
	})
}

// 条件: トラック 1 の試行 1 が FAILED で試行 2 が COMPLETED、トラック 2 は COMPLETED。
// 内容: 全トラックが完了したときと同じ要約を 1 回投稿し、データを消す。
func TestReport_RetryCompleted_PostsSummaryAndDiscards(t *testing.T) {
	s := newReporterScenario(t)
	s.jobs.Statuses[voicesummary.JobRef{RequestID: requestID, Track: 1, Attempt: voicesummary.FirstAttempt}] = voicesummary.JobFailed
	s.jobs.Statuses[voicesummary.JobRef{RequestID: requestID, Track: 1, Attempt: voicesummary.RetryAttempt}] = voicesummary.JobCompleted

	err := s.report(reportJobName)

	s.verify(t, err, reporterOutcome{Keys: reportedRequestKeys(), Requests: []llm.Request{summaryRequest()}, Posts: []reply.ChannelPost{summaryPost()}})
}

// 条件: トラック 1 の試行 1 と試行 2 がどちらも FAILED。
// 内容: 結果を読まずに、依頼者への通知つきで失敗を投稿し、データを消す。
func TestReport_RetryFailed_PostsFailureAndDiscards(t *testing.T) {
	s := newReporterScenario(t)
	s.jobs.Statuses[voicesummary.JobRef{RequestID: requestID, Track: 1, Attempt: voicesummary.FirstAttempt}] = voicesummary.JobFailed
	s.jobs.Statuses[voicesummary.JobRef{RequestID: requestID, Track: 1, Attempt: voicesummary.RetryAttempt}] = voicesummary.JobFailed

	err := s.report(reportJobName)

	s.verify(t, err, reporterOutcome{Keys: reportedRequestKeys(), Posts: []reply.ChannelPost{failurePost()}})
}

// 条件: 全トラックが完了し、要約に失敗する。
// 内容: エラーを返し、投稿せず、claim を消してデータを残す。
func TestReport_SummaryFails_ReleasesClaimAndKeepsData(t *testing.T) {
	s := newReporterScenario(t)
	s.gen.Err = errors.New("injected failure")

	err := s.report(reportJobName)

	s.verify(t, err, reporterOutcome{
		Failed:   true,
		Keys:     acceptedRequestKeys(),
		Requests: []llm.Request{summaryRequest()},
		Logs:     []reportLog{failureLog("summarize")},
	})
}

// 条件: 全トラックが完了し、結果の読み取りの失敗、または結果の形の誤りが起きる。
// 内容: エラーを返し、投稿せず、claim を消してデータを残す。失敗した段階をログに出す。
func TestReport_ResultCannotBeRead_ReleasesClaimAndKeepsData(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(s *reporterScenario)
	}{
		{"reading transcript fails", func(s *reporterScenario) { s.store.ReadTranscriptErr = errors.New("injected failure") }},
		{"transcript is not json", func(s *reporterScenario) { s.store.PutTranscript(requestID, 2, []byte("{")) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newReporterScenario(t)
			tt.arrange(s)

			err := s.report(reportJobName)

			s.verify(t, err, reporterOutcome{Failed: true, Keys: acceptedRequestKeys(), Logs: []reportLog{failureLog("read")}})
		})
	}
}

// 条件: 1 回目の呼び出しで要約に失敗し、Lambda の再試行で要約が成功する。
// 内容: 2 回目で要約を 1 回だけ投稿し、データを消す。
func TestReport_RetriedAfterSummaryFailure_PostsSummaryOnce(t *testing.T) {
	s := newReporterScenario(t)
	s.gen.Err = errors.New("injected failure")
	if err := s.report(reportJobName); err == nil {
		t.Fatal("first Report = nil; want error")
	}
	s.gen.Err = nil

	err := s.report(reportJobName)

	s.verify(t, err, reporterOutcome{
		Keys:     reportedRequestKeys(),
		Requests: []llm.Request{summaryRequest(), summaryRequest()},
		Posts:    []reply.ChannelPost{summaryPost()},
		Logs:     []reportLog{failureLog("summarize")},
	})
}

// 条件: 全トラックが完了し、Discord が投稿を断る（ErrPostRejected）。そのあと Lambda の再試行で呼ばれる。
// 内容: 1 回目はエラーを返して claim を消しデータを残し、2 回目で 1 回だけ投稿する。
func TestReport_DiscordRejectsThenRetried_PostsOnce(t *testing.T) {
	s := newReporterScenario(t)
	s.poster.Errs = []error{fmt.Errorf("post rejected: %w", reply.ErrPostRejected)}
	if err := s.report(reportJobName); err == nil {
		t.Fatal("first Report = nil; want error")
	}

	err := s.report(reportJobName)

	s.verify(t, err, reporterOutcome{
		Keys:     reportedRequestKeys(),
		Requests: []llm.Request{summaryRequest(), summaryRequest()},
		Posts:    []reply.ChannelPost{summaryPost(), summaryPost()},
		Logs:     []reportLog{failureLog("post")},
	})
}

// 条件: 全トラックが完了し、投稿が通信の失敗で終わる（投稿されたか分からない）。もう一度呼ばれる。
// 内容: どちらも nil を返し、ログを出し、データを消す。2 回目は何もしない。
func TestReport_PostConnectionFailsThenCalledAgain_DoesNotRetryPost(t *testing.T) {
	s := newReporterScenario(t)
	s.poster.Errs = []error{errors.New("connection reset")}
	if err := s.report(reportJobName); err != nil {
		t.Fatalf("first Report = %v; want nil", err)
	}

	err := s.report(reportJobName)

	s.verify(t, err, reporterOutcome{
		Keys:     reportedRequestKeys(),
		Requests: []llm.Request{summaryRequest()},
		Posts:    []reply.ChannelPost{summaryPost()},
		Logs:     []reportLog{failureLog("post")},
	})
}

// 条件: 結果は作れて投稿も終わったが、依頼のデータの削除が失敗する。
// 内容: ログを出して nil を返す。削除に失敗した依頼は claim ごと残る。
func TestReport_DiscardFails_LogsStageAndReturnsNil(t *testing.T) {
	s := newReporterScenario(t)
	s.store.DiscardErr = errors.New("injected failure")

	err := s.report(reportJobName)

	s.verify(t, err, reporterOutcome{
		Keys:     claimedRequestKeys(),
		Requests: []llm.Request{summaryRequest()},
		Posts:    []reply.ChannelPost{summaryPost()},
		Logs:     []reportLog{failureLog("discard")},
	})
}

// 条件: 投稿の権利を取る前に、依頼の記録の読み取り、またはジョブの状態の取得が失敗する。
// 内容: 投稿も削除もせず（Lambda の再試行に任せる）、エラーを返して失敗した段階をログに出す。
func TestReport_FailureBeforeClaim_ReturnsErrorWithoutPosting(t *testing.T) {
	errInjected := errors.New("injected failure")
	tests := []struct {
		name      string
		arrange   func(s *reporterScenario)
		wantStage string
	}{
		{"reading manifest fails", func(s *reporterScenario) { s.store.ReadManifestErr = errInjected }, "manifest"},
		{"job status fails", func(s *reporterScenario) { s.jobs.StatusErr = errInjected }, "status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newReporterScenario(t)
			tt.arrange(s)

			err := s.report(reportJobName)

			s.verify(t, err, reporterOutcome{Failed: true, Keys: acceptedRequestKeys(), Logs: []reportLog{failureLog(tt.wantStage)}})
		})
	}
}
