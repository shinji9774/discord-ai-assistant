package voicerecording

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

const (
	recordingsPrefix = "recordings/"
	activeName       = "/active.json"
	startedName      = "/started.json"
	endName          = "/end.json"
	optOutDir        = "/optout/"
	keySeparator     = "/"

	// ifNoneMatchAny は、キーが無いときだけ書く条件付き書き込みの値。
	ifNoneMatchAny = "*"
	// lockTakeoverGrace は、ロックの期限を過ぎてから、ほかの依頼が取り直せるようになるまでの猶予。
	lockTakeoverGrace = 5 * time.Minute

	codeNoSuchKey = "NoSuchKey"
)

// conditionalFailureCodes は、条件付きの操作で条件を満たさなかったことを表す S3 のエラーコード。
// 412 は条件が合わないとき、409 は同じキーへの書き込みが同時に進んでいるとき。
var conditionalFailureCodes = map[string]bool{
	"PreconditionFailed":         true,
	"ConditionalRequestConflict": true,
}

// SessionStore は、録音中の状態（ロック・開始・終了・除外）を S3 に置く。
type SessionStore struct {
	client *s3.Client
	bucket string
}

func NewSessionStore(client *s3.Client, bucket string) *SessionStore {
	return &SessionStore{client: client, bucket: bucket}
}

type endRecord struct {
	Reason EndReason `json:"reason"`
}

func activeKey(guildID string) string {
	return recordingsPrefix + guildID + activeName
}

func sessionKey(session Session, name string) string {
	return recordingsPrefix + session.GuildID + keySeparator + session.RequestID + name
}

func optOutKey(session Session, userID string) string {
	return sessionKey(session, optOutDir+userID)
}

// Acquire は、サーバの録音のロックを取る。録音中なら ErrRecordingActive。
// 期限と猶予を過ぎたロックは、session.AcceptedAt を現在時刻として取り直す。
func (s *SessionStore) Acquire(ctx context.Context, session Session) error {
	body, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	err = s.put(ctx, activeKey(session.GuildID), body, ifAbsent)
	if isConditionalFailure(err) {
		return s.takeOver(ctx, session, body)
	}
	if err != nil {
		return fmt.Errorf("put active session: %w", err)
	}
	return nil
}

func (s *SessionStore) takeOver(ctx context.Context, session Session, body []byte) error {
	current, etag, err := s.readActive(ctx, session.GuildID)
	if errors.Is(err, ErrNoRecording) {
		return ErrRecordingActive
	}
	if err != nil {
		return err
	}
	if !lockExpired(current, session.AcceptedAt) {
		return ErrRecordingActive
	}
	err = s.put(ctx, activeKey(session.GuildID), body, ifMatch(etag))
	if isConditionalFailure(err) || hasErrorCode(err, codeNoSuchKey) {
		return ErrRecordingActive
	}
	if err != nil {
		return fmt.Errorf("take over active session: %w", err)
	}
	return nil
}

// lockExpired は、期限と猶予を過ぎたロックかを返す。猶予の終わりちょうどは過ぎたものとする。
func lockExpired(current Session, now time.Time) bool {
	return !now.Before(current.Deadline.Add(lockTakeoverGrace))
}

// Active は、サーバで録音中の録音を返す。無ければ ErrNoRecording。
func (s *SessionStore) Active(ctx context.Context, guildID string) (Session, error) {
	session, _, err := s.readActive(ctx, guildID)
	return session, err
}

func (s *SessionStore) readActive(ctx context.Context, guildID string) (Session, string, error) {
	body, etag, err := s.get(ctx, activeKey(guildID))
	if hasErrorCode(err, codeNoSuchKey) {
		return Session{}, "", ErrNoRecording
	}
	if err != nil {
		return Session{}, "", fmt.Errorf("get active session: %w", err)
	}
	var session Session
	if err := json.Unmarshal(body, &session); err != nil {
		return Session{}, "", fmt.Errorf("decode active session: %w", err)
	}
	return session, etag, nil
}

// Release は、ロックがこの録音のものなら外す。ほかの依頼のロックと、既に外れたロックには何もしない。
func (s *SessionStore) Release(ctx context.Context, session Session) error {
	current, etag, err := s.readActive(ctx, session.GuildID)
	if errors.Is(err, ErrNoRecording) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.RequestID != session.RequestID {
		return nil
	}
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:  aws.String(s.bucket),
		Key:     aws.String(activeKey(session.GuildID)),
		IfMatch: aws.String(etag),
	})
	if isConditionalFailure(err) || hasErrorCode(err, codeNoSuchKey) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete active session: %w", err)
	}
	return nil
}

// MarkStarted は、録音を始めたことと元の表示名を記録する。
func (s *SessionStore) MarkStarted(ctx context.Context, session Session, started Started) error {
	body, err := json.Marshal(started)
	if err != nil {
		return fmt.Errorf("encode started: %w", err)
	}
	if err := s.put(ctx, sessionKey(session, startedName), body, unconditionally); err != nil {
		return fmt.Errorf("put started: %w", err)
	}
	return nil
}

// Started は、録音を始めた記録を返す。無ければ ErrNotStarted。
func (s *SessionStore) Started(ctx context.Context, session Session) (Started, error) {
	var started Started
	err := s.getJSON(ctx, sessionKey(session, startedName), &started)
	if hasErrorCode(err, codeNoSuchKey) {
		return Started{}, ErrNotStarted
	}
	if err != nil {
		return Started{}, fmt.Errorf("get started: %w", err)
	}
	return started, nil
}

// End は、終了の理由を書く。先に書かれた理由があれば、それを返す（先に書いたものが勝つ）。
func (s *SessionStore) End(ctx context.Context, session Session, reason EndReason) (EndReason, error) {
	body, err := json.Marshal(endRecord{Reason: reason})
	if err != nil {
		return "", fmt.Errorf("encode end: %w", err)
	}
	err = s.put(ctx, sessionKey(session, endName), body, ifAbsent)
	if isConditionalFailure(err) {
		return s.Ended(ctx, session)
	}
	if err != nil {
		return "", fmt.Errorf("put end: %w", err)
	}
	return reason, nil
}

// Ended は、書かれた終了の理由を返す。無ければ ErrNotEnded。
func (s *SessionStore) Ended(ctx context.Context, session Session) (EndReason, error) {
	var record endRecord
	err := s.getJSON(ctx, sessionKey(session, endName), &record)
	if hasErrorCode(err, codeNoSuchKey) {
		return "", ErrNotEnded
	}
	if err != nil {
		return "", fmt.Errorf("get end: %w", err)
	}
	return record.Reason, nil
}

// OptOut は、利用者を録音から除外する。
func (s *SessionStore) OptOut(ctx context.Context, session Session, userID string) error {
	if err := s.put(ctx, optOutKey(session, userID), nil, unconditionally); err != nil {
		return fmt.Errorf("put optout: %w", err)
	}
	return nil
}

// OptedOut は、利用者が録音から除外されているかを返す。
func (s *SessionStore) OptedOut(ctx context.Context, session Session, userID string) (bool, error) {
	_, _, err := s.get(ctx, optOutKey(session, userID))
	if hasErrorCode(err, codeNoSuchKey) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get optout: %w", err)
	}
	return true, nil
}

// putCondition は、PutObject に条件を付ける。
type putCondition func(*s3.PutObjectInput)

func ifAbsent(in *s3.PutObjectInput) {
	in.IfNoneMatch = aws.String(ifNoneMatchAny)
}

func ifMatch(etag string) putCondition {
	return func(in *s3.PutObjectInput) { in.IfMatch = aws.String(etag) }
}

func unconditionally(*s3.PutObjectInput) {}

func (s *SessionStore) put(ctx context.Context, key string, body []byte, condition putCondition) error {
	in := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: bytes.NewReader(body)}
	condition(in)
	_, err := s.client.PutObject(ctx, in)
	return err
}

func (s *SessionStore) get(ctx context.Context, key string) ([]byte, string, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, "", err
	}
	defer out.Body.Close()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", err
	}
	return body, aws.ToString(out.ETag), nil
}

func (s *SessionStore) getJSON(ctx context.Context, key string, v any) error {
	body, _, err := s.get(ctx, key)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

func isConditionalFailure(err error) bool {
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && conditionalFailureCodes[apiErr.ErrorCode()]
}

func hasErrorCode(err error, code string) bool {
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == code
}
