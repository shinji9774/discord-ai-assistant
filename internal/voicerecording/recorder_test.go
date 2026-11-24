package voicerecording_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"

	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/reply/replytest"
	"discord-ai-assistant/internal/voicerecording"
	"discord-ai-assistant/internal/voicerecording/voicerecordingtest"
	"discord-ai-assistant/internal/voicesummary"
	"discord-ai-assistant/test/discordfake"
	"discord-ai-assistant/test/s3fake"
	"discord-ai-assistant/test/transcribefake"
)

const (
	botDisplayName = "録音bot"
	speakerAName   = "たろう"
	speakerBName   = "はなこ"

	// テストの間隔。本番の値は TestConfigRecorderTiming で確かめる。
	testStopPoll    = 10 * time.Millisecond
	testMaxDuration = time.Hour
	testJoinTimeout = time.Second

	// recordingLength は、台本の録音の長さ（時計が返す開始と終了の差）。
	recordingLength = 2 * time.Second
	// otherRequestID は、ロックを持つ別の依頼の ID。
	otherRequestID = "req-other"
)

var testTiming = voicerecording.RecorderTiming{MaxDuration: testMaxDuration, StopPoll: testStopPoll, JoinTimeout: testJoinTimeout}

// twoSpeakerFrames は、A が 0 秒と 20 ms、B が 500 ms に話す台本のフレーム。A が先に話す。
var twoSpeakerFrames = []voicerecordingtest.Frame{
	{UserID: speakerA, Opus: opusSilenceFrame, ReceivedAt: at(0)},
	{UserID: speakerA, Opus: opusSilenceFrame, ReceivedAt: at(20 * time.Millisecond)},
	{UserID: speakerB, Opus: opusSilenceFrame, ReceivedAt: at(500 * time.Millisecond)},
}

// recorderFixture は、Recorder の依存を本物のクライアントと fake で組み立てたもの。
type recorderFixture struct {
	deps    voicerecording.RecorderDependencies
	store   *voicerecording.SessionStore
	audio   *voicesummary.S3Store
	s3      *s3fake.Server
	discord *discordfake.Server
	jobs    *transcribefake.Server
	poster  *replytest.ChannelPoster
	voice   *voicerecordingtest.Voice
}

func newRecorderFixture(t *testing.T, script voicerecordingtest.Script) recorderFixture {
	t.Helper()
	client, s3Fake := newS3Fake(t)
	guild, discordFake := newDiscordGuild(t)
	addMember(discordFake, testBotUserID, botDisplayName, nil, true)
	addMember(discordFake, speakerA, speakerAName, nil, false)
	addMember(discordFake, speakerB, speakerBName, nil, false)
	jobs, transcribeFake := newTranscribeJobs(t, s3Fake)
	f := recorderFixture{
		store:   voicerecording.NewSessionStore(client, testBucket),
		audio:   voicesummary.NewS3Store(client, testBucket),
		s3:      s3Fake,
		discord: discordFake,
		jobs:    transcribeFake,
		poster:  &replytest.ChannelPoster{},
		voice:   voicerecordingtest.NewVoice(script),
	}
	f.deps = voicerecording.RecorderDependencies{
		Sessions: f.store, Voice: f.voice, Guild: guild, Poster: f.poster, Audio: f.audio, Jobs: jobs,
		Now: steppingClock(at(0), at(recordingLength)), WorkDir: t.TempDir(),
	}
	return f
}

// newTranscribeJobs は、結果を s3fake に書く transcribefake へつないだ TranscribeJobs を返す。
func newTranscribeJobs(t *testing.T, results transcribefake.ObjectWriter) (*voicesummary.TranscribeJobs, *transcribefake.Server) {
	t.Helper()
	fake := transcribefake.New(results)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	client := transcribe.New(transcribe.Options{
		Region:           testRegion,
		BaseEndpoint:     aws.String(srv.URL),
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		RetryMaxAttempts: 1,
	})
	return voicesummary.NewTranscribeJobs(client, testBucket), fake
}

// steppingClock は、呼ぶたびに times を順に返し、尽きたら最後の値を返し続ける時計。
func steppingClock(times ...time.Time) func() time.Time {
	var mu sync.Mutex
	next := 0
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		now := times[next]
		if next < len(times)-1 {
			next++
		}
		return now
	}
}

// acquire は、録音中のロックを取る。期限は Run の context の期限になるので、今から数える。
func (f recorderFixture) acquire(t *testing.T, requestID string) voicerecording.Session {
	t.Helper()
	session := testSession(requestID, time.Now())
	mustDo(t, f.store.Acquire(context.Background(), session))
	return session
}

// stopped は、ロックを取り、/record-stop を受けた状態にする。
func (f recorderFixture) stopped(t *testing.T) voicerecording.Session {
	t.Helper()
	session := f.acquire(t, testRequestID)
	_, err := f.store.End(context.Background(), session, voicerecording.EndReasonStopCommand)
	mustDo(t, err)
	return session
}

func (f recorderFixture) run(timing voicerecording.RecorderTiming) error {
	recorder := voicerecording.NewRecorder(f.deps, timing, slog.New(slog.DiscardHandler))
	return recorder.Run(context.Background(), testGuildID, testRequestID)
}

// released は、ロックが外れているかを返す。
func (f recorderFixture) released(t *testing.T) bool {
	t.Helper()
	_, err := f.store.Active(context.Background(), testGuildID)
	return errors.Is(err, voicerecording.ErrNoRecording)
}

// manifest は、置かれたマニフェストを返す。無ければ nil。
func (f recorderFixture) manifest(t *testing.T) *voicesummary.Manifest {
	t.Helper()
	m, err := f.audio.ReadManifest(context.Background(), testRequestID)
	if errors.Is(err, voicesummary.ErrRequestNotFound) {
		return nil
	}
	mustDo(t, err)
	return &m
}

// storedTrack は、S3 に置かれた音声 1 つ。
type storedTrack struct {
	Key     string
	Samples int64
}

// storedAudio は、依頼の音声をキーの順に読み、サンプル数を数える。
func (f recorderFixture) storedAudio(t *testing.T, s3Keys []string) []storedTrack {
	t.Helper()
	prefix := "requests/" + testRequestID + "/audio/"
	var got []storedTrack
	for _, key := range s3Keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		got = append(got, storedTrack{Key: key, Samples: flacSamples(t, f.download(t, key))})
	}
	return got
}

// download は、S3 のオブジェクトを一時ファイルに書き、そのパスを返す。
func (f recorderFixture) download(t *testing.T, key string) string {
	t.Helper()
	data, ok := f.s3.Object(testBucket, key)
	if !ok {
		t.Fatalf("object %s is not stored", key)
	}
	path := filepath.Join(t.TempDir(), filepath.Base(key))
	mustDo(t, os.WriteFile(path, data, 0o600))
	return path
}

// jobNames は、始まった文字起こしのジョブ名を始まった順に返す。
func (f recorderFixture) jobNames() []string {
	var names []string
	for _, job := range f.jobs.Jobs() {
		names = append(names, job.Name)
	}
	return names
}

// audioKey と jobName は、voicesummary の取り決め（requests/{依頼}/audio/{トラック}.flac、接頭辞-依頼-トラック-試行）。
func audioKey(track int) string {
	return "requests/" + testRequestID + "/audio/" + strconv.Itoa(track) + ".flac"
}

func jobName(track int) string {
	return voicesummary.JobNamePrefix + testRequestID + "-" + strconv.Itoa(track) + "-1"
}

// 条件: 本番の設定（録音の上限 180 分）
// 内容: 上限は設定どおり、終了マーカーを読む間隔は 5 秒、参加の待ち時間は 60 秒（設計書「設定設計」）
func TestConfigRecorderTiming_MaxRecordingMinutes_UsesSpecIntervals(t *testing.T) {
	cfg := voicerecording.Config{MaxRecordingMinutes: 180}

	got := cfg.RecorderTiming()

	want := voicerecording.RecorderTiming{MaxDuration: 180 * time.Minute, StopPoll: 5 * time.Second, JoinTimeout: 60 * time.Second}
	if got != want {
		t.Errorf("RecorderTiming = %+v, want %+v", got, want)
	}
}

// 条件: 2 人（A が先）が話し、/record-stop を受けた録音
// 内容: 開始と終了を投稿し、話した順に番号を付けたマニフェストと 2 秒分の音声を置き、
//
//	話者ごとに文字起こしを始め、表示名を印つき→元（未設定）に戻し、退出してロックを外す
func TestRecorderRun_StopCommand_StoresRecordingAndStartsTranscription(t *testing.T) {
	f := newRecorderFixture(t, voicerecordingtest.Script{Frames: twoSpeakerFrames})
	session := f.stopped(t)

	err := f.run(testTiming)

	type outcome struct {
		Err       error
		Posts     []reply.ChannelPost
		Manifest  *voicesummary.Manifest
		Audio     []storedTrack
		Jobs      []string
		Nicknames []discordfake.NicknameChange
		Voice     voicerecordingtest.State
		Released  bool
	}
	got := outcome{
		Err: err, Posts: f.poster.Posts, Manifest: f.manifest(t), Audio: f.storedAudio(t, f.s3Keys(t)),
		Jobs: f.jobNames(), Nicknames: f.discord.NicknameChanges(), Voice: f.voice.State(), Released: f.released(t),
	}
	want := outcome{
		Posts: []reply.ChannelPost{voicerecording.StartNotice(session), voicerecording.EndNotice(session, "停止コマンド", true)},
		Manifest: &voicesummary.Manifest{
			RequestID: testRequestID, ChannelID: testTextChannelID, RequesterID: testStarterID,
			StartedAt: at(0), Duration: recordingLength,
			Speakers: []voicesummary.Speaker{{Track: 1, Name: speakerAName}, {Track: 2, Name: speakerBName}},
		},
		// どちらのトラックも、録音の開始から終了まで（2 秒）の長さになる。
		Audio: []storedTrack{
			{Key: audioKey(1), Samples: int64(recordingLength/time.Second) * samplesPerSecond},
			{Key: audioKey(2), Samples: int64(recordingLength/time.Second) * samplesPerSecond},
		},
		Jobs: []string{jobName(1), jobName(2)},
		Nicknames: []discordfake.NicknameChange{
			{GuildID: testGuildID, Nick: "[録音中] " + botDisplayName},
			{GuildID: testGuildID, Nick: ""},
		},
		Voice:    voicerecordingtest.State{GuildID: testGuildID, ChannelID: testVoiceChannelID, Receiving: true, Left: true},
		Released: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v,\nwant %+v", got, want)
	}
}

// 条件: /record-stop を受けずに、参加者が 0 人になる・上限が経過する・切断される
// 内容: それぞれの理由で録音を終え、その理由を終了の投稿に入れてロックを外す
func TestRecorderRun_AutomaticEnd_PostsReasonAndReleases(t *testing.T) {
	cases := []struct {
		name      string
		script    voicerecordingtest.Script
		timing    voicerecording.RecorderTiming
		wantLabel string
	}{
		{
			"参加者が 0 人になる",
			voicerecordingtest.Script{Frames: twoSpeakerFrames, HumanCounts: []int{2, 0}},
			testTiming,
			"参加者全員の退出",
		},
		{
			// 上限をテストのために 50 ms にしているので、分に直した表記は 0 分になる。
			"上限が経過する",
			voicerecordingtest.Script{Frames: twoSpeakerFrames},
			voicerecording.RecorderTiming{MaxDuration: 50 * time.Millisecond, StopPoll: testStopPoll, JoinTimeout: testJoinTimeout},
			"録音の上限（0 分）への到達",
		},
		{
			"切断される",
			voicerecordingtest.Script{Frames: twoSpeakerFrames, DisconnectOnReceive: true},
			testTiming,
			"ボイスチャンネルからの切断",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecorderFixture(t, tc.script)
			session := f.acquire(t, testRequestID)

			err := f.run(tc.timing)

			type outcome struct {
				Err      error
				Posts    []reply.ChannelPost
				Released bool
			}
			got := outcome{Err: err, Posts: f.poster.Posts, Released: f.released(t)}
			want := outcome{
				Posts:    []reply.ChannelPost{voicerecording.StartNotice(session), voicerecording.EndNotice(session, tc.wantLabel, true)},
				Released: true,
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v,\nwant %+v", got, want)
			}
		})
	}
}

// 条件: B が /record-optout を実行した録音
// 内容: マニフェスト・音声・文字起こしは A だけになり、B の一時ファイルは消える
func TestRecorderRun_OptedOutSpeaker_ExcludesTrackAndRemovesLocalFile(t *testing.T) {
	f := newRecorderFixture(t, voicerecordingtest.Script{Frames: twoSpeakerFrames})
	session := f.stopped(t)
	mustDo(t, f.store.OptOut(context.Background(), session, speakerB))

	err := f.run(testTiming)

	type outcome struct {
		Err        error
		Speakers   []voicesummary.Speaker
		AudioKeys  []string
		Jobs       []string
		LocalFiles int
	}
	got := outcome{Err: err, Speakers: f.manifest(t).Speakers, AudioKeys: f.audioKeys(t), Jobs: f.jobNames(), LocalFiles: f.localFiles(t)}
	want := outcome{
		Speakers:   []voicesummary.Speaker{{Track: 1, Name: speakerAName}},
		AudioKeys:  []string{audioKey(1)},
		Jobs:       []string{jobName(1)},
		LocalFiles: 1, // A のトラックだけが残る
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v,\nwant %+v", got, want)
	}
}

// 条件: 誰も話さずに /record-stop を受けた録音
// 内容: 対象の音声なしの終了を投稿し、マニフェストも文字起こしも作らずにロックを外す
func TestRecorderRun_NoSpeech_PostsWithoutAudioAndSkipsTranscription(t *testing.T) {
	f := newRecorderFixture(t, voicerecordingtest.Script{})
	session := f.stopped(t)

	err := f.run(testTiming)

	type outcome struct {
		Err      error
		Posts    []reply.ChannelPost
		Manifest *voicesummary.Manifest
		Jobs     []string
		Released bool
	}
	got := outcome{Err: err, Posts: f.poster.Posts, Manifest: f.manifest(t), Jobs: f.jobNames(), Released: f.released(t)}
	want := outcome{
		Posts:    []reply.ChannelPost{voicerecording.StartNotice(session), voicerecording.EndNotice(session, "停止コマンド", false)},
		Released: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v,\nwant %+v", got, want)
	}
}

// 条件: bot の表示名の変更を Discord が権限で拒む
// 内容: 録音は止めずに最後まで行い、ロックを外す
func TestRecorderRun_NicknameChangeRejected_CompletesRecording(t *testing.T) {
	f := newRecorderFixture(t, voicerecordingtest.Script{Frames: twoSpeakerFrames})
	f.discord.RejectPath(http.MethodPatch, "/guilds/"+testGuildID+"/members/@me", http.StatusForbidden, codeMissingAccess)
	session := f.stopped(t)

	err := f.run(testTiming)

	type outcome struct {
		Err      error
		Posts    []reply.ChannelPost
		Jobs     []string
		Released bool
	}
	got := outcome{Err: err, Posts: f.poster.Posts, Jobs: f.jobNames(), Released: f.released(t)}
	want := outcome{
		Posts:    []reply.ChannelPost{voicerecording.StartNotice(session), voicerecording.EndNotice(session, "停止コマンド", true)},
		Jobs:     []string{jobName(1), jobName(2)},
		Released: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v,\nwant %+v", got, want)
	}
}

// 条件: ロックを別の依頼が持っている（録音タスクの起動が遅れ、別の録音が始まった）
// 内容: ErrRequestMismatch を返し、参加せず、別の依頼のロックに触らない
func TestRecorderRun_LockOfOtherRequest_ReturnsErrRequestMismatch(t *testing.T) {
	f := newRecorderFixture(t, voicerecordingtest.Script{Frames: twoSpeakerFrames})
	f.acquire(t, otherRequestID)

	err := f.run(testTiming)

	active, activeErr := f.store.Active(context.Background(), testGuildID)
	mustDo(t, activeErr)
	type outcome struct {
		Mismatch    bool
		Voice       voicerecordingtest.State
		LockRequest string
	}
	got := outcome{Mismatch: errors.Is(err, voicerecording.ErrRequestMismatch), Voice: f.voice.State(), LockRequest: active.RequestID}
	want := outcome{Mismatch: true, LockRequest: otherRequestID}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// 条件: ロックの期限が過ぎている（起動が期限に間に合わなかった）
// 内容: 期限切れのエラーを返し、参加も開始の記録も投稿もしない
func TestRecorderRun_PastDeadline_FailsWithoutStarting(t *testing.T) {
	f := newRecorderFixture(t, voicerecordingtest.Script{Frames: twoSpeakerFrames})
	// 受付から testSessionLength（4 時間）より前に受け付けたことにして、期限を過去にする。
	session := testSession(testRequestID, time.Now().Add(-5*time.Hour))
	mustDo(t, f.store.Acquire(context.Background(), session))

	err := f.run(testTiming)

	_, startedErr := f.store.Started(context.Background(), session)
	type outcome struct {
		DeadlineExceeded bool
		Voice            voicerecordingtest.State
		Posts            []reply.ChannelPost
		NotStarted       bool
	}
	got := outcome{
		DeadlineExceeded: errors.Is(err, context.DeadlineExceeded), Voice: f.voice.State(),
		Posts: f.poster.Posts, NotStarted: errors.Is(startedErr, voicerecording.ErrNotStarted),
	}
	want := outcome{DeadlineExceeded: true, NotStarted: true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// handedOver は、引き渡し済みの録音の状態（マニフェストと音声が残り、文字起こしが始まっている）。
type handedOver struct {
	Err       error
	Manifest  bool
	AudioKeys []string
	Jobs      []string
}

func (f recorderFixture) handedOver(t *testing.T, err error) handedOver {
	t.Helper()
	return handedOver{Err: err, Manifest: f.manifest(t) != nil, AudioKeys: f.audioKeys(t), Jobs: f.jobNames()}
}

// 条件: 文字起こしを始めた後で、ロックの解除が失敗する
// 内容: Run は成功し、マニフェストと音声は残り、話者ごとの文字起こしは始まっている
func TestRecorderRun_ReleaseFailsAfterHandover_SucceedsAndKeepsRecording(t *testing.T) {
	f := newRecorderFixture(t, voicerecordingtest.Script{Frames: twoSpeakerFrames})
	f.stopped(t)
	f.s3.FailDelete(testBucket, voicerecording.ActiveKey(testGuildID))

	err := f.run(testTiming)

	want := handedOver{Manifest: true, AudioKeys: []string{audioKey(1), audioKey(2)}, Jobs: []string{jobName(1), jobName(2)}}
	if got := f.handedOver(t, err); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// 条件: 終了の投稿が Discord に拒否される
// 内容: Run は成功し、文字起こしは始まり、ロックは外れる
func TestRecorderRun_EndNoticeFails_StillStartsTranscription(t *testing.T) {
	f := newRecorderFixture(t, voicerecordingtest.Script{Frames: twoSpeakerFrames})
	f.poster.Errs = []error{nil, errors.New("post rejected")} // 1 回目は開始の投稿、2 回目が終了の投稿
	f.stopped(t)

	err := f.run(testTiming)

	type outcome struct {
		handedOver
		Released bool
	}
	got := outcome{handedOver: f.handedOver(t, err), Released: f.released(t)}
	want := outcome{
		handedOver: handedOver{Manifest: true, AudioKeys: []string{audioKey(1), audioKey(2)}, Jobs: []string{jobName(1), jobName(2)}},
		Released:   true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// 条件: 誰も話さず、終了の投稿の後でロックの解除が失敗する
// 内容: 引き渡すものが無いので、Run は成功する
func TestRecorderRun_NoSpeechAndReleaseFails_Succeeds(t *testing.T) {
	f := newRecorderFixture(t, voicerecordingtest.Script{})
	f.stopped(t)
	f.s3.FailDelete(testBucket, voicerecording.ActiveKey(testGuildID))

	err := f.run(testTiming)

	if err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// 条件: 2 人目のトラックの文字起こしの開始が失敗する
// 内容: Run はエラーを返し、置いた音声とマニフェストは消えている
func TestRecorderRun_StartTranscriptionFails_DiscardsRecordingAndReturnsError(t *testing.T) {
	f := newRecorderFixture(t, voicerecordingtest.Script{Frames: twoSpeakerFrames})
	f.jobs.FailStart(jobName(2))
	f.stopped(t)

	err := f.run(testTiming)

	type outcome struct {
		Failed    bool
		Manifest  bool
		AudioKeys []string
	}
	state := f.handedOver(t, err)
	got := outcome{Failed: state.Err != nil, Manifest: state.Manifest, AudioKeys: state.AudioKeys}
	want := outcome{Failed: true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// 条件: 2 人目のトラックの文字起こしの開始が失敗し、置いた音声を消すのも失敗する
// 内容: Run は、開始の失敗と消す処理の失敗の両方をまとめたエラーを返し、消せなかったマニフェストは残る
func TestRecorderRun_StartTranscriptionAndDiscardFail_ReturnsJoinedError(t *testing.T) {
	f := newRecorderFixture(t, voicerecordingtest.Script{Frames: twoSpeakerFrames})
	f.jobs.FailStart(jobName(2))
	f.s3.FailDelete(testBucket, audioKey(1))
	f.stopped(t)

	err := f.run(testTiming)

	type outcome struct {
		StartFailed   bool
		DiscardFailed bool
		Manifest      bool
	}
	got := outcome{
		StartFailed:   err != nil && strings.Contains(err.Error(), "start transcription"),
		DiscardFailed: err != nil && strings.Contains(err.Error(), "discard recording"),
		Manifest:      f.manifest(t) != nil,
	}
	want := outcome{StartFailed: true, DiscardFailed: true, Manifest: true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v (err = %v)", got, want, err)
	}
}

// s3Keys は、バケットのキーを昇順で返す。
func (f recorderFixture) s3Keys(t *testing.T) []string {
	t.Helper()
	return f.s3.Keys(testBucket)
}

// audioKeys は、依頼の音声のキーだけを返す。
func (f recorderFixture) audioKeys(t *testing.T) []string {
	t.Helper()
	var keys []string
	for _, track := range f.storedAudio(t, f.s3Keys(t)) {
		keys = append(keys, track.Key)
	}
	return keys
}

// localFiles は、録音の作業ディレクトリに残ったファイルの数を返す。
func (f recorderFixture) localFiles(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(f.deps.WorkDir)
	mustDo(t, err)
	return len(entries)
}
