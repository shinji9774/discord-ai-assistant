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

const (
	jobNameFormat  = JobNamePrefix + "%s-%d"
	trackSeparator = "-"
	decimalBase    = 10
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

// jobName は "summarize-voice-{依頼 ID}-{トラック番号}" を返す。
func jobName(requestID string, track int) string {
	return fmt.Sprintf(jobNameFormat, requestID, track)
}

// requestIDOf は、ジョブ名から依頼 ID を取り出す。この機能のジョブ名でなければ ok は false。
func requestIDOf(name string) (requestID string, ok bool) {
	rest, ok := strings.CutPrefix(name, JobNamePrefix)
	if !ok {
		return "", false
	}
	i := strings.LastIndex(rest, trackSeparator)
	if i < 0 {
		return "", false
	}
	requestID, track := rest[:i], rest[i+len(trackSeparator):]
	if requestID == "" || !isDecimal(track) {
		return "", false
	}
	return requestID, true
}

// isDecimal は s が符号のない 10 進の数字だけからなるかを返す。
func isDecimal(s string) bool {
	_, err := strconv.ParseUint(s, decimalBase, 0)
	return err == nil
}
