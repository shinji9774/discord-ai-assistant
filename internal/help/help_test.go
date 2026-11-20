package help_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"discord-ai-assistant/internal/catalog"
	"discord-ai-assistant/internal/config/configtest"
	"discord-ai-assistant/internal/help"
)

const (
	discordMessageLengthLimit = 2000

	bytesPerKB = 1000
	bytesPerMB = 1_000_000

	maxLengthPhraseFormat = "最大 %d 文字"
	maxKBPhraseFormat     = "%d KB まで"
	maxMBPhraseFormat     = "%d MB まで"

	summarizeDefaultPeriodPhraseFormat = "直近 %d 時間"
	summarizeMaxMessagesPhraseFormat   = "新しい方から %d 件まで"

	summarizeVoiceMaxMinutesPhraseFormat = "%d 分まで"
)

// phrasesMissingFrom は、phrases のうち、text に含まれないものを返す。
func phrasesMissingFrom(text string, phrases []string) []string {
	var missing []string
	for _, phrase := range phrases {
		if !strings.Contains(text, phrase) {
			missing = append(missing, phrase)
		}
	}
	return missing
}

// 条件: 同梱の設定ファイルを読む。
// 内容: 使い方に書かれた上限（/ask・/translate・/image の文字数、/translate のファイルの大きさ、/image の元の画像の大きさ、/summarize の既定の期間と件数、/summarize-voice の録音の長さ）が、設定の値と一致する（7.5、6.6）。
func TestUsage_Limits_MatchConfig(t *testing.T) {
	cfg := configtest.Load(t)
	limits := []string{
		fmt.Sprintf(maxLengthPhraseFormat, cfg.Ask.MaxInputLength),
		fmt.Sprintf(maxLengthPhraseFormat, cfg.Translate.MaxTextLength),
		fmt.Sprintf(maxLengthPhraseFormat, cfg.Image.MaxPromptLength),
		fmt.Sprintf(maxKBPhraseFormat, cfg.Translate.MaxFileSizeBytes/bytesPerKB),
		fmt.Sprintf(maxMBPhraseFormat, cfg.Image.Source.MaxBytes/bytesPerMB),
		fmt.Sprintf(summarizeDefaultPeriodPhraseFormat, cfg.Summarize.DefaultPeriodHours),
		fmt.Sprintf(summarizeMaxMessagesPhraseFormat, cfg.Summarize.MaxMessages),
		fmt.Sprintf(summarizeVoiceMaxMinutesPhraseFormat, cfg.SummarizeVoice.MaxRecordingMinutes),
	}

	missing := phrasesMissingFrom(help.Usage, limits)

	if len(missing) != 0 {
		t.Errorf("usage does not state %q", missing)
	}
}

// 条件: 登録されている全コマンド（catalog.Definitions()）。
// 内容: すべてのコマンドについて、"/" + 名前 が Usage に含まれる。漏れたコマンド名の一覧は空になる。
func TestUsage_RegisteredCommands_AllDocumented(t *testing.T) {
	definitions := catalog.Definitions(configtest.Load(t))

	var undocumented []string
	for _, definition := range definitions {
		if name := "/" + definition.CommandName(); !strings.Contains(help.Usage, name) {
			undocumented = append(undocumented, name)
		}
	}

	if len(undocumented) != 0 {
		t.Errorf("commands missing from Usage = %q, want none", undocumented)
	}
}

// 条件: 埋め込まれた Usage。
// 内容: 文字数が Discord のメッセージ上限（2000）以下になる。
func TestUsage_Length_FitsDiscordMessageLimit(t *testing.T) {
	usage := help.Usage

	got := utf8.RuneCountInString(usage)

	if got > discordMessageLengthLimit {
		t.Errorf("Usage length = %d, want <= %d", got, discordMessageLengthLimit)
	}
}
