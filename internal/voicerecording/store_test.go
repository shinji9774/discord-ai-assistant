package voicerecording_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"discord-ai-assistant/internal/voicerecording"
)

// lockGrace は、仕様（設計書）の「期限を過ぎてからロックを取り直すまでの猶予」。
const lockGrace = 5 * time.Minute

type activeResult struct {
	Session voicerecording.Session
	Err     error
}

func activeOf(store *voicerecording.SessionStore) activeResult {
	session, err := store.Active(context.Background(), testGuildID)
	return activeResult{Session: session, Err: err}
}

// 条件: 録音のロックを取る
// 内容: Active が同じ録音を返す
func TestSessionStoreAcquire_NoActiveRecording_BecomesActive(t *testing.T) {
	store, _ := newSessionStore(t)
	session := testSession(testRequestID, testAcceptedAt)

	err := store.Acquire(context.Background(), session)

	if got, want := activeOf(store), (activeResult{Session: session}); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Acquire = %v, Active = %+v, want nil, %+v", err, got, want)
	}
}

// 条件: ロックの期限（＋猶予）の前後で、別の依頼がロックを取ろうとする
// 内容: 期限＋猶予ちょうどからは取り直せ、それより前は ErrRecordingActive で先のロックが残る
func TestSessionStoreAcquire_SecondRequest_TakesOverOnlyAfterDeadlineAndGrace(t *testing.T) {
	expiry := testAcceptedAt.Add(testSessionLength + lockGrace)
	cases := []struct {
		name       string
		acceptedAt time.Time
		wantErr    error
		wantActive string
	}{
		{name: "期限内は取れない", acceptedAt: testAcceptedAt.Add(time.Minute), wantErr: voicerecording.ErrRecordingActive, wantActive: testRequestID},
		{name: "猶予の終わる直前も取れない", acceptedAt: expiry.Add(-time.Nanosecond), wantErr: voicerecording.ErrRecordingActive, wantActive: testRequestID},
		{name: "猶予の終わりちょうどで取り直す", acceptedAt: expiry, wantErr: nil, wantActive: "req-2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newSessionStore(t)
			mustDo(t, store.Acquire(context.Background(), testSession(testRequestID, testAcceptedAt)))

			err := store.Acquire(context.Background(), testSession("req-2", tc.acceptedAt))

			got := activeOf(store)
			if !errors.Is(err, tc.wantErr) || got.Session.RequestID != tc.wantActive {
				t.Errorf("Acquire = %v, active = %s; want %v, %s", err, got.Session.RequestID, tc.wantErr, tc.wantActive)
			}
		})
	}
}

// 条件: 自分のロックを外す
// 内容: Active が ErrNoRecording を返す
func TestSessionStoreRelease_OwnLock_RemovesLock(t *testing.T) {
	store, _ := newSessionStore(t)
	session := testSession(testRequestID, testAcceptedAt)
	mustDo(t, store.Acquire(context.Background(), session))

	err := store.Release(context.Background(), session)

	if got := activeOf(store); err != nil || !errors.Is(got.Err, voicerecording.ErrNoRecording) {
		t.Errorf("Release = %v, Active = %+v; want nil, ErrNoRecording", err, got)
	}
}

// 条件: 期限切れのロックを別の依頼が取り直したあとで、古い録音がロックを外す
// 内容: 新しい依頼のロックは残る
func TestSessionStoreRelease_AfterTakeover_KeepsNewLock(t *testing.T) {
	store, _ := newSessionStore(t)
	old := testSession(testRequestID, testAcceptedAt)
	takeover := testSession("req-2", old.Deadline.Add(lockGrace))
	mustDo(t, store.Acquire(context.Background(), old))
	mustDo(t, store.Acquire(context.Background(), takeover))

	err := store.Release(context.Background(), old)

	if got, want := activeOf(store), (activeResult{Session: takeover}); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Release = %v, Active = %+v; want nil, %+v", err, got, want)
	}
}

// 条件: ロックが無いサーバでロックを外す
// 内容: エラーにしない（既に外れているものとして扱う）
func TestSessionStoreRelease_NoLock_ReturnsNil(t *testing.T) {
	store, _ := newSessionStore(t)

	err := store.Release(context.Background(), testSession(testRequestID, testAcceptedAt))

	if err != nil {
		t.Errorf("Release = %v, want nil", err)
	}
}

// 条件: 終了の理由を 2 回書く（停止コマンドのあとに参加者全員の退出）
// 内容: 2 回とも先に書いた理由を返し、Ended も先の理由を返す
func TestSessionStoreEnd_SecondReason_KeepsFirstReason(t *testing.T) {
	store, _ := newSessionStore(t)
	session := testSession(testRequestID, testAcceptedAt)
	first, err := store.End(context.Background(), session, voicerecording.EndReasonStopCommand)
	mustDo(t, err)
	second, err := store.End(context.Background(), session, voicerecording.EndReasonEveryoneLeft)
	mustDo(t, err)

	ended, err := store.Ended(context.Background(), session)

	got := []voicerecording.EndReason{first, second, ended}
	want := []voicerecording.EndReason{voicerecording.EndReasonStopCommand, voicerecording.EndReasonStopCommand, voicerecording.EndReasonStopCommand}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("reasons = %v, err = %v; want %v, nil", got, err, want)
	}
}

// 条件: 状態をまだ書いていない録音の、開始・終了・除外を読む
// 内容: それぞれ ErrNotStarted・ErrNotEnded・false を返す
func TestSessionStore_NothingWritten_ReportsNotStartedNotEndedNotOptedOut(t *testing.T) {
	store, _ := newSessionStore(t)
	session := testSession(testRequestID, testAcceptedAt)
	_, startedErr := store.Started(context.Background(), session)
	_, endedErr := store.Ended(context.Background(), session)

	optedOut, err := store.OptedOut(context.Background(), session, testStarterID)

	if !errors.Is(startedErr, voicerecording.ErrNotStarted) || !errors.Is(endedErr, voicerecording.ErrNotEnded) || optedOut || err != nil {
		t.Errorf("Started = %v, Ended = %v, OptedOut = %v, %v", startedErr, endedErr, optedOut, err)
	}
}

// 条件: 開始の記録（元の表示名）を書く
// 内容: Started が同じ内容を返す
func TestSessionStoreMarkStarted_ThenStarted_ReturnsSameRecord(t *testing.T) {
	store, _ := newSessionStore(t)
	session := testSession(testRequestID, testAcceptedAt)
	want := voicerecording.Started{OriginalNickname: "議事録 bot"}
	mustDo(t, store.MarkStarted(context.Background(), session, want))

	got, err := store.Started(context.Background(), session)

	if err != nil || got != want {
		t.Errorf("Started = %+v, %v; want %+v, nil", got, err, want)
	}
}

// 条件: 1 人が録音から除外を依頼する
// 内容: その人だけが除外され、ほかの人は除外されない
func TestSessionStoreOptOut_OneUser_OptsOutOnlyThatUser(t *testing.T) {
	store, _ := newSessionStore(t)
	session := testSession(testRequestID, testAcceptedAt)
	const other = "1700000000000000007"
	mustDo(t, store.OptOut(context.Background(), session, testStarterID))
	starter, err := store.OptedOut(context.Background(), session, testStarterID)
	mustDo(t, err)

	otherOptedOut, err := store.OptedOut(context.Background(), session, other)

	if got := []bool{starter, otherOptedOut}; err != nil || !reflect.DeepEqual(got, []bool{true, false}) {
		t.Errorf("OptedOut = %v, %v; want [true false], nil", got, err)
	}
}
