package help_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/catalog"
	"discord-ai-assistant/internal/config/configtest"
	"discord-ai-assistant/internal/help"
)

const (
	// docsDir は、利用者向けの詳細ページ（GitHub Pages の公開元）。テストの作業ディレクトリ internal/help からの相対パス。
	docsDir = "../../docs"

	indexPage     = "index.md"
	recordingPage = "recording.md"

	// noDetailPage は、詳細ページを持たないコマンドに対応させる値。
	noDetailPage = ""

	bytesPerKB = 1000
	bytesPerMB = 1_000_000

	maxLengthPhraseFormat = "最大 %d 文字"
	maxKBPhraseFormat     = "%d KB まで"
	maxMBPhraseFormat     = "%d MB まで"

	summarizeDefaultPeriodPhraseFormat = "直近 %d 時間"
	summarizeMaxMessagesPhraseFormat   = "新しい方から %d 件まで"

	summarizeVoiceMaxMinutesPhraseFormat = "%d 分まで"

	recordingMaxMinutesPhraseFormat = "全員の退出か %d 分で終わり"
)

// commandPages は、登録コマンドと詳細ページの対応。コマンドを足したら、ここと docs/ のページを足す。
var commandPages = map[string]string{
	"translate":       "translate.md",
	"ask":             "ask.md",
	"image":           "image.md",
	"summarize":       "summarize.md",
	"summarize-voice": "summarize-voice.md",
	"record-start":    recordingPage,
	"record-stop":     recordingPage,
	"record-optout":   recordingPage,
	help.CommandName:  noDetailPage,
}

// markdownLinkTarget は、Markdown のリンク [文言](target) の target を取り出す。
var markdownLinkTarget = regexp.MustCompile(`\]\(([^)\s]+)\)`)

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

func readDoc(t *testing.T, page string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(docsDir, page))
	if err != nil {
		t.Fatalf("read %s: %v", page, err)
	}
	return string(content)
}

// commandsNotLinkedFrom は、詳細ページの対応が無いコマンドと、対応するページへのリンクが index に無いコマンドを返す。
func commandsNotLinkedFrom(index string, definitions []discord.ApplicationCommandCreate) []string {
	var problems []string
	for _, definition := range definitions {
		name := definition.CommandName()
		page, ok := commandPages[name]
		if !ok {
			problems = append(problems, name+": no entry in commandPages")
			continue
		}
		if page == noDetailPage {
			continue
		}
		if !strings.Contains(index, "]("+page+")") {
			problems = append(problems, name+": index.md does not link "+page)
		}
	}
	return problems
}

// limitsMissingFromDocs は、ページごとに、そのページに含まれない上限値の句を返す。
func limitsMissingFromDocs(t *testing.T, limitsByPage map[string][]string) map[string][]string {
	t.Helper()
	missing := map[string][]string{}
	for page, limits := range limitsByPage {
		if phrases := phrasesMissingFrom(readDoc(t, page), limits); len(phrases) != 0 {
			missing[page] = phrases
		}
	}
	return missing
}

// brokenRelativeLinks は、pages の相対リンクのうち、docsDir に存在しないファイルを指すものを「ページ -> リンク先」の形で返す。
func brokenRelativeLinks(t *testing.T, pages []string) []string {
	t.Helper()
	var broken []string
	for _, page := range pages {
		name := filepath.Base(page)
		for _, match := range markdownLinkTarget.FindAllStringSubmatch(readDoc(t, name), -1) {
			target := match[1]
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
				continue
			}
			path, _, _ := strings.Cut(target, "#")
			if path == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(docsDir, path)); err != nil {
				broken = append(broken, name+" -> "+target)
			}
		}
	}
	return broken
}

// 条件: 登録されている全コマンド（catalog.Definitions）と docs/index.md。
// 内容: /help 以外のすべてのコマンドに詳細ページの対応があり、目次からそのページへリンクしている（4.2）。
func TestDocs_RegisteredCommands_LinkedFromIndex(t *testing.T) {
	definitions := catalog.Definitions(configtest.Load(t))
	index := readDoc(t, indexPage)

	problems := commandsNotLinkedFrom(index, definitions)

	if len(problems) != 0 {
		t.Errorf("problems = %q, want none", problems)
	}
}

// 条件: 同梱の設定ファイルと docs/ の各コマンドのページ。
// 内容: 各ページに、そのコマンドの上限（文字数、ファイルと画像の大きさ、/summarize の既定の期間と件数、録音の長さ）が設定の値どおりに書かれている（4.3）。
func TestDocs_Limits_MatchConfig(t *testing.T) {
	cfg := configtest.Load(t)
	limitsByPage := map[string][]string{
		"translate.md": {
			fmt.Sprintf(maxLengthPhraseFormat, cfg.Translate.MaxTextLength),
			fmt.Sprintf(maxKBPhraseFormat, cfg.Translate.MaxFileSizeBytes/bytesPerKB),
		},
		"ask.md": {
			fmt.Sprintf(maxLengthPhraseFormat, cfg.Ask.MaxInputLength),
		},
		"image.md": {
			fmt.Sprintf(maxLengthPhraseFormat, cfg.Image.MaxPromptLength),
			fmt.Sprintf(maxMBPhraseFormat, cfg.Image.Source.MaxBytes/bytesPerMB),
		},
		"summarize.md": {
			fmt.Sprintf(summarizeDefaultPeriodPhraseFormat, cfg.Summarize.DefaultPeriodHours),
			fmt.Sprintf(summarizeMaxMessagesPhraseFormat, cfg.Summarize.MaxMessages),
		},
		"summarize-voice.md": {
			fmt.Sprintf(summarizeVoiceMaxMinutesPhraseFormat, cfg.SummarizeVoice.MaxRecordingMinutes),
		},
		recordingPage: {
			fmt.Sprintf(recordingMaxMinutesPhraseFormat, cfg.Recording.MaxRecordingMinutes),
		},
	}

	missing := limitsMissingFromDocs(t, limitsByPage)

	if len(missing) != 0 {
		t.Errorf("limits missing from docs = %q, want none", missing)
	}
}

// 条件: docs/ 直下のすべての Markdown。
// 内容: 外部 URL とページ内アンカーを除く相対リンクが、すべて docs/ 内の存在するファイルを指す（4.5）。
func TestDocs_RelativeLinks_PointToExistingFiles(t *testing.T) {
	pages, _ := filepath.Glob(filepath.Join(docsDir, "*.md"))
	if len(pages) == 0 {
		t.Fatalf("no pages in %s", docsDir)
	}

	broken := brokenRelativeLinks(t, pages)

	if len(broken) != 0 {
		t.Errorf("broken links = %q, want none", broken)
	}
}
