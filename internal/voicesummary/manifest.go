package voicesummary

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// JobNamePrefix は、この機能の Transcribe のジョブ名の接頭辞。cfn/app.yaml のイベントルールと一致させる。
const JobNamePrefix = "summarize-voice-"

// 文字起こしの試行番号。初回のジョブが FAILED のとき、1 回だけ開き直す。
const (
	firstAttempt = 1
	retryAttempt = 2
)

const (
	jobNameFormat    = JobNamePrefix + "%s-%d-%d"
	jobNameSeparator = "-"
	decimalBase      = 10
)

// 依頼の置き場（AudioStore / ResultStore）が返すエラー。
var (
	ErrRequestExists   = errors.New("voice summary request already exists")
	ErrRequestNotFound = errors.New("voice summary request not found")
	ErrReportClaimed   = errors.New("voice summary report already claimed")
)

// Manifest は、受け付けた依頼の記録。録音リンクと鍵は持たない。
type Manifest struct {
	RequestID   string        `json:"request_id"`
	ChannelID   string        `json:"channel_id"`
	RequesterID string        `json:"requester_id"`
	StartedAt   time.Time     `json:"started_at"`
	Duration    time.Duration `json:"duration"`
	Speakers    []Speaker     `json:"speakers"`
}

// Speaker は、録音の 1 トラックの話者。Name は Craig の globalName、なければ username。
type Speaker struct {
	Track int    `json:"track"`
	Name  string `json:"name"`
}

// JobRef は、1 トラックの文字起こしのジョブを指す。
type JobRef struct {
	RequestID string
	Track     int
	Attempt   int
}

// jobName は "summarize-voice-{依頼 ID}-{トラック番号}-{試行番号}" を返す。
func jobName(job JobRef) string {
	return fmt.Sprintf(jobNameFormat, job.RequestID, job.Track, job.Attempt)
}

// requestIDOf は、ジョブ名から依頼 ID を取り出す。この機能のジョブ名でなければ ok は false。
func requestIDOf(jobName string) (string, bool) {
	rest, ok := strings.CutPrefix(jobName, JobNamePrefix)
	if !ok {
		return "", false
	}
	withoutAttempt, ok := cutDecimalSuffix(rest)
	if !ok {
		return "", false
	}
	requestID, ok := cutDecimalSuffix(withoutAttempt)
	if !ok || requestID == "" {
		return "", false
	}
	return requestID, true
}

// cutDecimalSuffix は、末尾の "-{数字}" を取り除く。
func cutDecimalSuffix(s string) (string, bool) {
	i := strings.LastIndex(s, jobNameSeparator)
	if i < 0 || !isDecimal(s[i+len(jobNameSeparator):]) {
		return "", false
	}
	return s[:i], true
}

// isDecimal は s が符号のない 10 進の数字だけからなるかを返す。
func isDecimal(s string) bool {
	_, err := strconv.ParseUint(s, decimalBase, 0)
	return err == nil
}
