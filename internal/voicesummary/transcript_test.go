package voicesummary_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"discord-ai-assistant/internal/voicesummary"
)

// readTestdata は testdata の中のファイルを読む。
func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return data
}

// 条件: 3 つの発話を持つ Transcribe の結果（2 つ目は本文が空白だけ）。
// 内容: 空白だけの発話を捨て、start_time（秒の文字列）を経過時間にした 2 つの発話を返す。
func TestParseSegments_ResultWithBlankSegment_DropsBlankSegment(t *testing.T) {
	data := readTestdata(t, "transcribe_speech.json")

	got, err := voicesummary.ParseSegments(data)

	if err != nil {
		t.Fatalf("ParseSegments: %v", err)
	}
	want := []voicesummary.Segment{
		{Start: 1*time.Second + 230*time.Millisecond, Text: "おはようございます。"},
		{Start: 12*time.Minute + 34*time.Second + 60*time.Millisecond, Text: "今日は予定を決めましょう。"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("segments =\n%+v\nwant\n%+v", got, want)
	}
}

// 条件: 発話のない Transcribe の結果（無音だけのトラック）。
// 内容: 発話なし（nil）を返し、エラーにしない。
func TestParseSegments_SilentTrack_ReturnsNoSegments(t *testing.T) {
	data := readTestdata(t, "transcribe_silent.json")

	got, err := voicesummary.ParseSegments(data)

	if err != nil || got != nil {
		t.Errorf("ParseSegments = (%+v, %v), want (nil, nil)", got, err)
	}
}

// 条件: JSON として読めない結果と、start_time が数でない結果。
// 内容: どちらもエラーを返す。
func TestParseSegments_BrokenResult_ReturnsError(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"not json", "{"},
		{"start time not a number", `{"results":{"audio_segments":[{"transcript":"はい","start_time":"abc"}]}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := voicesummary.ParseSegments([]byte(tt.data))

			if err == nil {
				t.Error("ParseSegments error = nil, want an error")
			}
		})
	}
}

// 条件: 2 人の話者の発話。0:10 に 2 人が同時に話し始める。トラック 1 は「はなこ」、トラック 2 は「たろう」。
// 内容: 開始時刻の早い順に並び、同時刻はトラック番号の小さい順（名前の順ではない）になる。
func TestMergeUtterances_TwoSpeakers_OrdersByStartThenTrack(t *testing.T) {
	bySpeaker := map[voicesummary.Speaker][]voicesummary.Segment{
		{Track: 2, Name: "たろう"}: {
			{Start: 1 * time.Second, Text: "始めましょう。"},
			{Start: 10 * time.Second, Text: "金曜はどうですか。"},
		},
		{Track: 1, Name: "はなこ"}: {
			{Start: 5 * time.Second, Text: "はい。"},
			{Start: 10 * time.Second, Text: "木曜がいいです。"},
		},
	}

	got := voicesummary.MergeUtterances(bySpeaker)

	want := []voicesummary.Utterance{
		{Start: 1 * time.Second, Speaker: "たろう", Text: "始めましょう。"},
		{Start: 5 * time.Second, Speaker: "はなこ", Text: "はい。"},
		{Start: 10 * time.Second, Speaker: "はなこ", Text: "木曜がいいです。"},
		{Start: 10 * time.Second, Speaker: "たろう", Text: "金曜はどうですか。"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("utterances =\n%+v\nwant\n%+v", got, want)
	}
}

// 条件: どの話者にも発話がない。
// 内容: 空の発話の並びを返す（Reporter は「発話なし」として扱う）。
func TestMergeUtterances_NoSegments_ReturnsEmpty(t *testing.T) {
	bySpeaker := map[voicesummary.Speaker][]voicesummary.Segment{
		{Track: 1, Name: "はなこ"}: nil,
		{Track: 2, Name: "たろう"}: nil,
	}

	got := voicesummary.MergeUtterances(bySpeaker)

	if want := []voicesummary.Utterance{}; !reflect.DeepEqual(got, want) {
		t.Errorf("utterances = %#v, want %#v", got, want)
	}
}

// 条件: 1 秒台、12 分台、1 時間台に始まる 3 つの発話。
// 内容: 「[HH:MM:SS] 話者名: 本文」を改行でつなぐ。秒未満は切り捨てる。
func TestRenderTranscript_ThreeUtterances_RendersElapsedTimeLines(t *testing.T) {
	utterances := []voicesummary.Utterance{
		{Start: 1*time.Second + 230*time.Millisecond, Speaker: "はなこ", Text: "おはようございます。"},
		{Start: 12*time.Minute + 34*time.Second + 60*time.Millisecond, Speaker: "たろう", Text: "予定を決めましょう。"},
		{Start: 1*time.Hour + 2*time.Minute + 3*time.Second, Speaker: "はなこ", Text: "賛成です。"},
	}

	got := voicesummary.RenderTranscript(utterances)

	want := "[00:00:01] はなこ: おはようございます。\n" +
		"[00:12:34] たろう: 予定を決めましょう。\n" +
		"[01:02:03] はなこ: 賛成です。"
	if got != want {
		t.Errorf("transcript =\n%s\nwant\n%s", got, want)
	}
}
