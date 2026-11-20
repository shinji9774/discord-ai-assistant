package voicesummary

import (
	"fmt"
	"strings"
	"time"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
)

// 結果の見出しと文。利用者向けの文言のため、設定ではなく定数で持つ。
const (
	headingFormat         = "**録音開始**: %s（日本時間） / **長さ**: %s / **話者**: %s"
	startedAtLayout       = "2006-01-02 15:04"
	hoursAndMinutesFormat = "%d 時間 %d 分"
	minutesFormat         = "%d 分"
	speakerNameSeparator  = "、"
	// noSpeakersLabel は、対象の話者（bot を除く）が 1 人もいないときに、見出しの話者の欄に出す文字列。
	noSpeakersLabel  = "なし"
	noSpeechMessage  = "この録音には、要約する発話がありませんでした。"
	sectionSeparator = "\n\n"
)

// japanTime は見出しの日時の時間帯。依存の規則のため、summarize のものを共有しない。
var japanTime = time.FixedZone("JST", 9*60*60)

// formatReport は、見出し、要約、注記からなる結果の文面を作る。
func formatReport(m Manifest, summary llm.Result) string {
	sections := []string{heading(m), summary.Text}
	if summary.Truncated {
		sections = append(sections, command.TruncatedNotice)
	}
	return strings.Join(sections, sectionSeparator)
}

// formatNoSpeechReport は、見出しと「発話なし」の文を作る。
func formatNoSpeechReport(m Manifest) string {
	return strings.Join([]string{heading(m), noSpeechMessage}, sectionSeparator)
}

// heading は、録音開始の日時（日本時間）、長さ、話者の一覧の見出しを作る。
func heading(m Manifest) string {
	return fmt.Sprintf(headingFormat,
		m.StartedAt.In(japanTime).Format(startedAtLayout),
		formatLength(m.Duration),
		speakerNames(m.Speakers))
}

// formatLength は録音の長さを「H 時間 M 分」で表す。1 時間未満は「M 分」。秒は切り捨てる。
func formatLength(d time.Duration) string {
	hours := int(d / time.Hour)
	minutes := int(d % time.Hour / time.Minute)
	if hours == 0 {
		return fmt.Sprintf(minutesFormat, minutes)
	}
	return fmt.Sprintf(hoursAndMinutesFormat, hours, minutes)
}

func speakerNames(speakers []Speaker) string {
	if len(speakers) == 0 {
		return noSpeakersLabel
	}
	names := make([]string, 0, len(speakers))
	for _, s := range speakers {
		names = append(names, s.Name)
	}
	return strings.Join(names, speakerNameSeparator)
}
