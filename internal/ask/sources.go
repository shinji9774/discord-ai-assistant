package ask

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"discord-ai-assistant/internal/websearch"
)

const (
	sourcesHeading      = "出典:"
	sourceLineFormat    = "[%d] %s <%s>"
	maxTitleRunes       = 80
	titleEllipsis       = "…"
	firstPageNumber     = 1
	allMatches          = -1
	citationNumberGroup = 1
	noticeSearchFailed  = "※ Web 検索に失敗した回があるため、情報を確認しきれていない可能性があります。"
	noticeNoResults     = "※ Web で該当する情報が見つかりませんでした。"
)

var (
	citationPattern = regexp.MustCompile(`\[(\d+)\]`)
	lineBreaks      = strings.NewReplacer("\r", " ", "\n", " ")
)

// formatSources は出典の一覧を作る。pages[i] の番号は i+1。
// 回答が番号で示したページを番号順に並べる。1 つも示していなければ、先頭から fallbackCount 件を並べる。
func formatSources(answer string, pages []websearch.Page, fallbackCount int) string {
	if len(pages) == 0 {
		return ""
	}
	numbers := citedNumbers(answer, len(pages))
	if len(numbers) == 0 {
		numbers = firstNumbers(min(fallbackCount, len(pages)))
	}
	lines := []string{sourcesHeading}
	for _, number := range numbers {
		page := pages[number-firstPageNumber]
		lines = append(lines, fmt.Sprintf(sourceLineFormat, number, sourceTitle(page.Title), page.URL))
	}
	return strings.Join(lines, "\n")
}

// citedNumbers は、回答の中の [番号] のうち、ページがある番号を、重複なしの昇順で返す。
func citedNumbers(answer string, pageCount int) []int {
	var numbers []int
	for _, match := range citationPattern.FindAllStringSubmatch(answer, allMatches) {
		number, err := strconv.Atoi(match[citationNumberGroup])
		if err != nil || number < firstPageNumber || number > pageCount || slices.Contains(numbers, number) {
			continue
		}
		numbers = append(numbers, number)
	}
	slices.Sort(numbers)
	return numbers
}

func firstNumbers(count int) []int {
	numbers := make([]int, 0, count)
	for number := firstPageNumber; number <= count; number++ {
		numbers = append(numbers, number)
	}
	return numbers
}

// sourceTitle はタイトルを 1 行にし、長ければ切り詰める。
func sourceTitle(title string) string {
	singleLine := lineBreaks.Replace(title)
	if len([]rune(singleLine)) <= maxTitleRunes {
		return singleLine
	}
	return firstRunes(singleLine, maxTitleRunes) + titleEllipsis
}

func firstRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

// searchNotice は、検索の結果に応じて回答に添える注記を返す。添えるものが無ければ空文字。
func searchNotice(searched, failed bool, pageCount int) string {
	if failed {
		return noticeSearchFailed
	}
	if searched && pageCount == 0 {
		return noticeNoResults
	}
	return ""
}
