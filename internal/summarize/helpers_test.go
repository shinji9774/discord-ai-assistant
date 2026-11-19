package summarize_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"discord-ai-assistant/internal/summarize"
)

// jst は日本時間。期待値の時刻を利用者の入力と同じ時間帯で書くために使う。
var jst = time.FixedZone("JST", 9*60*60)

// periodText は期間を UTC の文字列にする。時間帯の表し方の違い（*time.Location）を比較から除くため。
func periodText(p summarize.Period) string {
	return fmt.Sprintf("%s 〜 %s", p.Since.UTC().Format(time.RFC3339), p.Until.UTC().Format(time.RFC3339))
}

// assertJSONEqual は got を JSON にして、wantJSON と値として等しいかを比べる（キーの順と空白の違いは無視する）。
func assertJSONEqual(t *testing.T, got any, wantJSON string) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(gotJSON, &gotValue); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal([]byte(wantJSON), &wantValue); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("json =\n%s\nwant\n%s", gotJSON, wantJSON)
	}
}
