package summarize

import (
	"errors"
	"time"
)

// japanTime は利用者が日時を指定し、結果に日時を表示する時間帯。
var japanTime = time.FixedZone("JST", 9*60*60)

// 利用者が since と until に書ける形式。
const (
	dateTimeLayout = "2006-01-02 15:04"
	dateLayout     = "2006-01-02"
)

// Period は要約の対象期間。Since 以上 Until 未満。
type Period struct {
	Since time.Time
	Until time.Time
}

var (
	// ErrInvalidDateTime は since または until が日時の形式（YYYY-MM-DD HH:MM、YYYY-MM-DD）でないときに返す。
	ErrInvalidDateTime = errors.New("invalid date-time format")
	// ErrInvertedPeriod は since と until の両方を指定し、since が until 以後のときに返す。
	ErrInvertedPeriod = errors.New("since is not before until")
	// ErrSinceInFuture は until を省略し、since が現在以後のときに返す。
	ErrSinceInFuture = errors.New("since is not before now")
)

// resolvePeriod は since と until の指定から対象期間を決める。
// 省略した側は now と defaultLength で補う。両方省略なら now までの defaultLength。
func resolvePeriod(since, until string, now time.Time, defaultLength time.Duration) (Period, error) {
	sinceTime, hasSince, err := parseDateTime(since)
	if err != nil {
		return Period{}, err
	}
	untilTime, hasUntil, err := parseDateTime(until)
	if err != nil {
		return Period{}, err
	}
	switch {
	case hasSince && hasUntil:
		return orderedPeriod(sinceTime, untilTime, ErrInvertedPeriod)
	case hasSince:
		return orderedPeriod(sinceTime, now, ErrSinceInFuture)
	case hasUntil:
		return Period{Since: untilTime.Add(-defaultLength), Until: untilTime}, nil
	default:
		return Period{Since: now.Add(-defaultLength), Until: now}, nil
	}
}

// orderedPeriod は since が until より前なら期間を返し、そうでなければ errNotBefore を返す。
func orderedPeriod(since, until time.Time, errNotBefore error) (Period, error) {
	if !since.Before(until) {
		return Period{}, errNotBefore
	}
	return Period{Since: since, Until: until}, nil
}

// parseDateTime は日本時間の日時（または日付）を読む。空なら指定なしとして hasValue を偽にする。
func parseDateTime(value string) (t time.Time, hasValue bool, err error) {
	if value == "" {
		return time.Time{}, false, nil
	}
	for _, layout := range []string{dateTimeLayout, dateLayout} {
		if parsed, err := time.ParseInLocation(layout, value, japanTime); err == nil {
			return parsed, true, nil
		}
	}
	return time.Time{}, false, ErrInvalidDateTime
}
