package summarize_test

import (
	"errors"
	"testing"
	"time"

	"discord-ai-assistant/internal/summarize"
)

const defaultLength = 24 * time.Hour

var periodNow = time.Date(2026, 11, 19, 14, 0, 0, 0, jst)

// 条件: since と until の指定の有無と形式（日付のみ / 日時）を変える。
// 内容: 省略した側は「今」と既定の長さで補い、日付だけの指定はその日の 0 時（日本時間）になる。
func TestResolvePeriod_ValidInput_ReturnsPeriod(t *testing.T) {
	tests := []struct {
		name         string
		since, until string
		want         summarize.Period
	}{
		{"both omitted", "", "", summarize.Period{Since: periodNow.Add(-defaultLength), Until: periodNow}},
		{"since only", "2026-11-18 09:00", "", summarize.Period{Since: time.Date(2026, 11, 18, 9, 0, 0, 0, jst), Until: periodNow}},
		{"until only (date)", "", "2026-11-19", summarize.Period{Since: time.Date(2026, 11, 19, 0, 0, 0, 0, jst).Add(-defaultLength), Until: time.Date(2026, 11, 19, 0, 0, 0, 0, jst)}},
		{"both", "2026-11-17", "2026-11-18 12:30", summarize.Period{Since: time.Date(2026, 11, 17, 0, 0, 0, 0, jst), Until: time.Date(2026, 11, 18, 12, 30, 0, 0, jst)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := summarize.ResolvePeriod(tt.since, tt.until, periodNow, defaultLength)

			if err != nil {
				t.Fatalf("ResolvePeriod: %v", err)
			}
			if periodText(got) != periodText(tt.want) {
				t.Errorf("period = %s, want %s", periodText(got), periodText(tt.want))
			}
		})
	}
}

// 条件: 形式の誤り、since が until 以後、until を省略して since が今以後。
// 内容: それぞれ ErrInvalidDateTime、ErrInvertedPeriod、ErrSinceInFuture を返す。
func TestResolvePeriod_InvalidInput_ReturnsError(t *testing.T) {
	tests := []struct {
		name         string
		since, until string
		want         error
	}{
		{"slash separated since", "2026/11/18", "", summarize.ErrInvalidDateTime},
		{"words in until", "", "昨日", summarize.ErrInvalidDateTime},
		{"seconds given", "2026-11-18 09:00:00", "", summarize.ErrInvalidDateTime},
		{"since equals until", "2026-11-18", "2026-11-18 00:00", summarize.ErrInvertedPeriod},
		{"since after until", "2026-11-18 12:00", "2026-11-18 09:00", summarize.ErrInvertedPeriod},
		{"since equals now", "2026-11-19 14:00", "", summarize.ErrSinceInFuture},
		{"since after now", "2026-11-20", "", summarize.ErrSinceInFuture},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := summarize.ResolvePeriod(tt.since, tt.until, periodNow, defaultLength)

			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}
