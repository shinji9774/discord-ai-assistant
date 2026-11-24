package voicerecording_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/reply/replytest"
	"discord-ai-assistant/internal/voicerecording"
	"discord-ai-assistant/internal/voicesummary"
	"discord-ai-assistant/test/discordfake"
	"discord-ai-assistant/test/s3fake"
)

// originalBotNickname は、録音の前に bot に付いていた表示名。
const originalBotNickname = "議事録 bot"

// manifestKey は、voicesummary の取り決め（requests/{依頼}/manifest.json）のマニフェストのキー。
const manifestKey = "requests/" + testRequestID + "/manifest.json"

// failureFixture は、FailureHandler の依存を本物のクライアントと fake で組み立てたもの。
type failureFixture struct {
	deps    voicerecording.FailureDependencies
	store   *voicerecording.SessionStore
	audio   *voicesummary.S3Store
	s3      *s3fake.Server
	discord *discordfake.Server
	poster  *replytest.ChannelPoster
}

func newFailureFixture(t *testing.T) failureFixture {
	t.Helper()
	client, s3Fake := newS3Fake(t)
	guild, discordFake := newDiscordGuild(t)
	f := failureFixture{
		store:   voicerecording.NewSessionStore(client, testBucket),
		audio:   voicesummary.NewS3Store(client, testBucket),
		s3:      s3Fake,
		discord: discordFake,
		poster:  &replytest.ChannelPoster{},
	}
	f.deps = voicerecording.FailureDependencies{Sessions: f.store, Audio: f.audio, Guild: guild, Poster: f.poster}
	return f
}

// acquire は、依頼 requestID の録音のロックを取る。
func (f failureFixture) acquire(t *testing.T, requestID string) voicerecording.Session {
	t.Helper()
	session := testSession(requestID, testAcceptedAt)
	mustDo(t, f.store.Acquire(context.Background(), session))
	return session
}

// started は、ロックを取り、開始の投稿まで済んだ状態にする。
func (f failureFixture) started(t *testing.T) voicerecording.Session {
	t.Helper()
	session := f.acquire(t, testRequestID)
	mustDo(t, f.store.MarkStarted(context.Background(), session, voicerecording.Started{OriginalNickname: originalBotNickname}))
	return session
}

// storeTrack は、話者 1 人のマニフェストと音声を置く（録音タスクが音声を置いた後に止まった状態）。
func (f failureFixture) storeTrack(t *testing.T, session voicerecording.Session) {
	t.Helper()
	manifest := voicesummary.Manifest{
		RequestID: session.RequestID, ChannelID: session.TextChannelID, RequesterID: session.StarterID,
		StartedAt: testAcceptedAt, Duration: time.Minute,
		Speakers: []voicesummary.Speaker{{Track: 1, Name: "たろう"}},
	}
	mustDo(t, f.audio.CreateManifest(context.Background(), manifest))
	mustDo(t, f.audio.PutAudio(context.Background(), session.RequestID, 1, bytes.NewReader([]byte("flac"))))
}

func (f failureFixture) handle() error {
	return f.handleStopped(voicerecording.StopUnfinished)
}

func (f failureFixture) handleStopped(outcome voicerecording.StopOutcome) error {
	handler := voicerecording.NewFailureHandler(f.deps, slog.New(slog.DiscardHandler))
	return handler.Handle(context.Background(), voicerecording.StoppedTask{GuildID: testGuildID, RequestID: testRequestID, Outcome: outcome})
}

// activeRequest は、ロックを持つ依頼の ID を返す。ロックが無ければ空。
func (f failureFixture) activeRequest(t *testing.T) string {
	t.Helper()
	session, err := f.store.Active(context.Background(), testGuildID)
	if errors.Is(err, voicerecording.ErrNoRecording) {
		return ""
	}
	mustDo(t, err)
	return session.RequestID
}

// requestKeys は、依頼の音声・文字起こし・マニフェストのキー（voicesummary の requests/{依頼}/ の下）を返す。
func (f failureFixture) requestKeys() []string {
	var keys []string
	for _, key := range f.s3.Keys(testBucket) {
		if strings.HasPrefix(key, "requests/"+testRequestID+"/") {
			keys = append(keys, key)
		}
	}
	return keys
}

// failureOutcome は、後始末の結果のうち利用者と後続の依頼から見えるもの。
type failureOutcome struct {
	Err           error
	Posts         []reply.ChannelPost
	RequestKeys   []string
	Nicknames     []discordfake.NicknameChange
	ActiveRequest string
}

func (f failureFixture) outcome(t *testing.T, err error) failureOutcome {
	t.Helper()
	return failureOutcome{
		Err: err, Posts: f.poster.Posts, RequestKeys: f.requestKeys(),
		Nicknames: f.discord.NicknameChanges(), ActiveRequest: f.activeRequest(t),
	}
}

// 条件: 開始の投稿の後で、音声を置いてから録音タスクが止まった
// 内容: 音声とマニフェストを消し、7.3 の投稿をして、表示名を元に戻し、ロックを外す
func TestFailureHandlerHandle_StartedWithStoredAudio_DiscardsNotifiesRestoresAndReleases(t *testing.T) {
	f := newFailureFixture(t)
	session := f.started(t)
	f.storeTrack(t, session)

	err := f.handle()

	want := failureOutcome{
		Posts:     []reply.ChannelPost{voicerecording.FailureNotice(session, true)},
		Nicknames: []discordfake.NicknameChange{{GuildID: testGuildID, Nick: originalBotNickname}},
	}
	if got := f.outcome(t, err); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// 条件: 開始の投稿の前に録音タスクが止まった（参加の失敗など）
// 内容: 7.1 の投稿をしてロックを外す。表示名は変えない
func TestFailureHandlerHandle_NotStarted_NotifiesStartFailureAndReleases(t *testing.T) {
	f := newFailureFixture(t)
	session := f.acquire(t, testRequestID)

	err := f.handle()

	want := failureOutcome{Posts: []reply.ChannelPost{voicerecording.FailureNotice(session, false)}}
	if got := f.outcome(t, err); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// 条件: ロックが無い（録音タスクが正常に終わってロックを外した後の停止）
// 内容: 何もせず、エラーも返さない
func TestFailureHandlerHandle_NoLock_DoesNothing(t *testing.T) {
	f := newFailureFixture(t)

	err := f.handle()

	if got := f.outcome(t, err); !reflect.DeepEqual(got, failureOutcome{}) {
		t.Errorf("got %+v, want nothing to happen", got)
	}
}

// 条件: ロックを、止まったタスクとは別の依頼が持っている
// 内容: 何もせず、別の依頼のロックを残す
func TestFailureHandlerHandle_LockOfOtherRequest_KeepsLock(t *testing.T) {
	f := newFailureFixture(t)
	f.acquire(t, otherRequestID)

	err := f.handle()

	want := failureOutcome{ActiveRequest: otherRequestID}
	if got := f.outcome(t, err); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// 条件: 失敗の投稿が Discord に拒否される
// 内容: 投稿のエラーを返すが、ロックは外す（次の録音を妨げない）
func TestFailureHandlerHandle_PostFails_ReleasesAndReturnsError(t *testing.T) {
	errPost := errors.New("post rejected")
	f := newFailureFixture(t)
	f.poster.Errs = []error{errPost}
	f.acquire(t, testRequestID)

	err := f.handle()

	if !errors.Is(err, errPost) || f.activeRequest(t) != "" {
		t.Errorf("Handle = %v (lock %q), want %v with lock released", err, f.activeRequest(t), errPost)
	}
}

// 条件: 失敗の投稿は成功し、ロックの解除が失敗する
// 内容: Handle は nil を返し、失敗の投稿は 1 回だけ（Lambda の再試行で重複しない）
func TestFailureHandlerHandle_ReleaseFails_ReturnsNilAfterSinglePost(t *testing.T) {
	f := newFailureFixture(t)
	session := f.acquire(t, testRequestID)
	f.s3.FailDelete(testBucket, voicerecording.ActiveKey(testGuildID))

	err := f.handle()

	want := failureOutcome{
		Posts:         []reply.ChannelPost{voicerecording.FailureNotice(session, false)},
		ActiveRequest: testRequestID, // 解除に失敗したロックは残る（期限で切れる）
	}
	if got := f.outcome(t, err); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// stoppedTaskDetail は、ECS Task State Change の detail のうち、上書きの部分を組み立てる。
// exitCode が nil なら、コンテナの終了の記録（containers）を付けない。
func stoppedTaskDetail(t *testing.T, containerName string, env map[string]string, exitCode *int) json.RawMessage {
	t.Helper()
	type pair struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	var pairs []pair
	for name, value := range env {
		pairs = append(pairs, pair{Name: name, Value: value})
	}
	var containers []map[string]any
	if exitCode != nil {
		containers = append(containers, map[string]any{"name": containerName, "exitCode": *exitCode})
	}
	detail, err := json.Marshal(map[string]any{
		"lastStatus": "STOPPED",
		"containers": containers,
		"overrides": map[string]any{"containerOverrides": []map[string]any{
			{"name": containerName, "environment": pairs},
		}},
	})
	mustDo(t, err)
	return detail
}

// 条件: 録音タスクのコンテナの上書きに、サーバと依頼の ID がある / どちらかが無い / 別のコンテナの上書きだけ
// 内容: ID を読む。読めなければ ErrNotRecordingTask
func TestStoppedRecording_ContainerOverrides_ReadsRecordingIDs(t *testing.T) {
	type ids struct {
		Task voicerecording.StoppedTask
		Err  error
	}
	recordingEnv := map[string]string{voicerecording.EnvGuildID: testGuildID, voicerecording.EnvRequestID: testRequestID}
	cases := []struct {
		name   string
		detail json.RawMessage
		want   ids
	}{
		{
			"録音タスクの上書き",
			stoppedTaskDetail(t, "recorder", recordingEnv, nil),
			ids{Task: voicerecording.StoppedTask{GuildID: testGuildID, RequestID: testRequestID}},
		},
		{
			"依頼の ID が無い",
			stoppedTaskDetail(t, "recorder", map[string]string{voicerecording.EnvGuildID: testGuildID}, nil),
			ids{Err: voicerecording.ErrNotRecordingTask},
		},
		{
			"別のコンテナの上書き",
			stoppedTaskDetail(t, "sidecar", recordingEnv, nil),
			ids{Err: voicerecording.ErrNotRecordingTask},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task, err := voicerecording.StoppedRecording(tc.detail)

			got := ids{Task: task, Err: err}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// 条件: 録音タスクのコンテナの exit code が 0 / 無い / 1、別のコンテナだけが 0 で終わっている
// 内容: 0 だけを引き渡し済み（StopHandedOver）とし、それ以外は引き渡しの前に止まった（StopUnfinished）とする
func TestStoppedRecording_ExitCode_DistinguishesHandedOverFromUnfinished(t *testing.T) {
	recordingEnv := map[string]string{voicerecording.EnvGuildID: testGuildID, voicerecording.EnvRequestID: testRequestID}
	exitSuccess, exitFailure := 0, 1
	cases := []struct {
		name   string
		detail json.RawMessage
		want   voicerecording.StopOutcome
	}{
		{"exit code 0", stoppedTaskDetail(t, "recorder", recordingEnv, &exitSuccess), voicerecording.StopHandedOver},
		{"exit code が無い（コンテナが動かなかった）", stoppedTaskDetail(t, "recorder", recordingEnv, nil), voicerecording.StopUnfinished},
		{"exit code 1", stoppedTaskDetail(t, "recorder", recordingEnv, &exitFailure), voicerecording.StopUnfinished},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task, err := voicerecording.StoppedRecording(tc.detail)

			if got := (outcomeResult{Outcome: task.Outcome, Err: err}); got != (outcomeResult{Outcome: tc.want}) {
				t.Errorf("got %+v, want outcome %v", got, tc.want)
			}
		})
	}
}

// outcomeResult は、StoppedRecording の戻り値のうち終わり方とエラー。
type outcomeResult struct {
	Outcome voicerecording.StopOutcome
	Err     error
}

// 条件: 録音タスクが引き渡しまで終えて exit 0 で止まり、ロックが残っている（ロックの解除に失敗した後）
// 内容: ロックを外すだけで、音声とマニフェストは残し、投稿も表示名の変更もしない
func TestFailureHandlerHandle_HandedOver_ReleasesLockWithoutDiscardOrPost(t *testing.T) {
	f := newFailureFixture(t)
	session := f.started(t)
	f.storeTrack(t, session)

	err := f.handleStopped(voicerecording.StopHandedOver)

	want := failureOutcome{RequestKeys: []string{audioKey(1), manifestKey}}
	if got := f.outcome(t, err); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// 条件: detail が JSON として壊れている
// 内容: ErrNotRecordingTask ではないエラーを返す（イベントの形の異常として調べられるように）
func TestStoppedRecording_BrokenJSON_ReturnsDecodeError(t *testing.T) {
	_, err := voicerecording.StoppedRecording(json.RawMessage(`{not json`))

	if err == nil || errors.Is(err, voicerecording.ErrNotRecordingTask) {
		t.Errorf("StoppedRecording = %v, want decode error", err)
	}
}
