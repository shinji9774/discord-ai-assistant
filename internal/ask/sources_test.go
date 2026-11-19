package ask

import (
	"reflect"
	"strings"
	"testing"

	"discord-ai-assistant/internal/websearch"
)

// pagesNamed は、名前をタイトルにし、名前から決まる URL を持つページを作る。
func pagesNamed(names ...string) []websearch.Page {
	pages := make([]websearch.Page, 0, len(names))
	for _, name := range names {
		pages = append(pages, websearch.Page{Title: name, URL: "https://" + name + ".example/"})
	}
	return pages
}

// 条件: 回答が、3 つのページのうち 3 番と 1 番を、この順で示している。
// 内容: 示されたページだけが、番号の小さい順に並ぶ。URL は山かっこで囲まれる（2.1、2.5）。
func TestFormatSources_AnswerCitesPages_ListsCitedPagesInNumberOrder(t *testing.T) {
	pages := pagesNamed("a", "b", "c")

	got := formatSources("B です[3]。A です[1]。", pages, 2)

	want := "出典:\n[1] a <https://a.example/>\n[3] c <https://c.example/>"
	if got != want {
		t.Errorf("formatSources() = %q, want %q", got, want)
	}
}

// 条件: 回答が、ページを 1 つも示していない。予備の件数は 2。
// 内容: 先頭から 2 件のページが並ぶ（2.3）。
func TestFormatSources_AnswerWithoutCitation_ListsFirstFallbackCountPages(t *testing.T) {
	pages := pagesNamed("a", "b", "c")

	got := formatSources("答えです。", pages, 2)

	want := "出典:\n[1] a <https://a.example/>\n[2] b <https://b.example/>"
	if got != want {
		t.Errorf("formatSources() = %q, want %q", got, want)
	}
}

// 条件: 回答が示した番号（0 と 4）が、どれもページの番号（1〜3）の外にある。予備の件数は 1。
// 内容: 示したページが無いものとして扱い、先頭から 1 件のページが並ぶ。
func TestFormatSources_CitationOutOfRange_FallsBackToFirstPages(t *testing.T) {
	pages := pagesNamed("a", "b", "c")

	got := formatSources("[0] と [4]", pages, 1)

	want := "出典:\n[1] a <https://a.example/>"
	if got != want {
		t.Errorf("formatSources() = %q, want %q", got, want)
	}
}

// 条件: 回答が、同じ番号を 2 回示している。
// 内容: そのページは 1 回だけ並ぶ（2.4）。
func TestFormatSources_SameNumberCitedTwice_ListsPageOnce(t *testing.T) {
	pages := pagesNamed("a", "b", "c")

	got := formatSources("[2] と [2]", pages, 2)

	want := "出典:\n[2] b <https://b.example/>"
	if got != want {
		t.Errorf("formatSources() = %q, want %q", got, want)
	}
}

// 条件: ページが 1 つも無い。
// 内容: 出典は空になる（2.2）。
func TestFormatSources_NoPages_ReturnsEmpty(t *testing.T) {
	got := formatSources("答え[1]", nil, 5)

	if got != "" {
		t.Errorf("formatSources() = %q, want empty", got)
	}
}

// 条件: タイトルに改行があり、1 行にすると 84 文字（上限の 80 文字より長い）。
// 内容: 改行が空白になり、先頭の 80 文字に省略の記号が付く。
func TestFormatSources_LongTitleWithLineBreak_ListsSingleLineTruncatedTitle(t *testing.T) {
	pages := []websearch.Page{{Title: "見出し\n" + strings.Repeat("あ", 80), URL: "https://long.example/"}}

	got := formatSources("答え", pages, 1)

	want := "出典:\n[1] 見出し " + strings.Repeat("あ", 76) + "… <https://long.example/>"
	if got != want {
		t.Errorf("formatSources() = %q, want %q", got, want)
	}
}

// searchOutcome は、検索の 1 回分の結果。
type searchOutcome struct {
	searched  bool
	failed    bool
	pageCount int
}

// noticesFor は、結果ごとの注記を同じ順に集める。
func noticesFor(outcomes []searchOutcome) []string {
	var notices []string
	for _, outcome := range outcomes {
		notices = append(notices, searchNotice(outcome.searched, outcome.failed, outcome.pageCount))
	}
	return notices
}

// 条件: 検索していない／失敗した回がありページもある／失敗した回がありページが無い／成功したがページが無い／成功してページがある、の 5 通り。
// 内容: 失敗した回があれば失敗の注記、失敗が無くページが無ければ見つからなかった注記、それ以外は注記なし（4.1、4.2）。
func TestSearchNotice_EachSearchOutcome_ReturnsMatchingNotice(t *testing.T) {
	outcomes := []searchOutcome{
		{searched: false, failed: false, pageCount: 0},
		{searched: true, failed: true, pageCount: 2},
		{searched: true, failed: true, pageCount: 0},
		{searched: true, failed: false, pageCount: 0},
		{searched: true, failed: false, pageCount: 2},
	}

	got := noticesFor(outcomes)

	want := []string{
		"",
		"※ Web 検索に失敗した回があるため、情報を確認しきれていない可能性があります。",
		"※ Web 検索に失敗した回があるため、情報を確認しきれていない可能性があります。",
		"※ Web で該当する情報が見つかりませんでした。",
		"",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("searchNotice() = %q, want %q", got, want)
	}
}
