package ask_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"discord-ai-assistant/internal/ask"
	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/llm/llmtest"
	"discord-ai-assistant/internal/websearch"
	"discord-ai-assistant/internal/websearch/websearchtest"
)

const (
	question        = "Go とは？"
	failureNotice   = "※ Web 検索に失敗した回があるため、情報を確認しきれていない可能性があります。"
	noResultsNotice = "※ Web で該当する情報が見つかりませんでした。"
)

// fixedNow は UTC の 2026-11-18 16:30。日本時間では 2026-11-19 になる。
var fixedNow = time.Date(2026, 11, 18, 16, 30, 0, 0, time.UTC)

var errBoom = errors.New("boom")

// testConfig は、検索 2 回まで・1 回 3 件・抜粋 10 文字・予備の出典 2 件の設定を返す。
func testConfig() ask.Config {
	return ask.Config{
		MaxInputLength:  6000,
		MaxOutputTokens: 16000,
		SystemPrompt:    "質問に答える。今日は {today}。",
		WebSearch: ask.WebSearchConfig{
			MaxSearches:         2,
			ResultsPerSearch:    3,
			MaxResultChars:      10,
			FallbackSourceCount: 2,
			ToolDescription:     "Web を検索する",
			LimitReachedMessage: "上限",
			SearchFailedMessage: "失敗",
			NoResultsMessage:    "0 件",
		},
	}
}

// scenario は、fake のモデルと fake の検索をつないだ /ask。
type scenario struct {
	gen      *llmtest.Generator
	searcher *websearchtest.Searcher
	logs     *bytes.Buffer
	cmd      *ask.Command
}

func newScenario(cfg ask.Config) *scenario {
	gen := &llmtest.Generator{Result: llm.Result{Text: "答え"}}
	searcher := &websearchtest.Searcher{}
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	cmd := ask.New(gen, searcher, cfg, func() time.Time { return fixedNow }, logger)
	return &scenario{gen: gen, searcher: searcher, logs: logs, cmd: cmd}
}

// searchCall は、モデルが求める Web 検索の呼び出しを作る。
func searchCall(query string) llm.ToolCall {
	return llm.ToolCall{Name: "web_search", Input: map[string]string{"query": query}}
}

// page は、名前をタイトルにし、名前から決まる URL と本文を持つページを作る。
func page(name string) websearch.Page {
	return websearch.Page{Title: name, URL: "https://" + name + ".example/", Text: name + " の本文"}
}

// found は、名前のページが見つかった検索結果を作る。
func found(names ...string) websearchtest.Response {
	pages := make([]websearch.Page, 0, len(names))
	for _, name := range names {
		pages = append(pages, page(name))
	}
	return websearchtest.Response{Pages: pages}
}

func askInvocation() command.Invocation {
	return command.Invocation{CommandName: "ask", Options: map[string]string{"question": question}}
}

func assertAnswer(t *testing.T, got command.Result, err error, wantText string) {
	t.Helper()
	want := command.Result{Text: wantText, Format: command.TextFormatMarkdown}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Execute() = %+v, %v, want %+v", got, err, want)
	}
}

func assertJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("unmarshal got %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("unmarshal want %q: %v", want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("got %s, want %s", got, want)
	}
}

// 条件: モデルが Web を検索せずに回答する。
// 内容: 質問の引用と回答だけが返る。出典も注記も付かない（1.2、2.2）。
func TestExecute_AnswerWithoutSearch_ReturnsAnswerWithoutSourcesOrNotice(t *testing.T) {
	s := newScenario(testConfig())
	s.gen.Result = llm.Result{Text: "プログラミング言語です"}

	got, err := s.cmd.Execute(context.Background(), askInvocation())

	assertAnswer(t, got, err, "> Go とは？\n\nプログラミング言語です")
}

// 条件: UTC の 2026-11-18 16:30（日本時間の 2026-11-19）に質問する。検索の上限は 2 回。
// 内容: モデルへの依頼に、日本時間の今日の日付を入れた指示文、質問、Web 検索のツール、呼び出しの上限（検索の上限 + 2）が入る（1.4）。
func TestExecute_Question_RequestsAnswerWithSearchToolAndTodayInJST(t *testing.T) {
	s := newScenario(testConfig())
	want := []llm.ToolRequest{{
		Request: llm.Request{System: "質問に答える。今日は 2026-11-19。", UserText: question, MaxOutputTokens: 16000},
		Tools: []llm.Tool{{
			Name:        "web_search",
			Description: "Web を検索する",
			Parameters:  []llm.ToolParameter{{Name: "query", Description: "検索する内容"}},
		}},
		MaxTurns: 4,
	}}

	_, err := s.cmd.Execute(context.Background(), askInvocation())

	if err != nil || !reflect.DeepEqual(s.gen.ToolRequests, want) {
		t.Errorf("tool requests = %+v, %v, want %+v", s.gen.ToolRequests, err, want)
	}
}

// 条件: モデルが「Go 最新版」で Web 検索を求める。1 回の検索の件数の設定は 3。
// 内容: モデルが求めた検索文と、設定の件数で検索する（1.1）。
func TestExecute_ModelSearches_SearchesWithQueryAndConfiguredCount(t *testing.T) {
	s := newScenario(testConfig())
	s.gen.ToolCalls = []llm.ToolCall{searchCall("Go 最新版")}

	_, err := s.cmd.Execute(context.Background(), askInvocation())

	want := []websearchtest.Call{{Query: "Go 最新版", MaxResults: 3}}
	if err != nil || !reflect.DeepEqual(s.searcher.Calls, want) {
		t.Errorf("search calls = %+v, %v, want %+v", s.searcher.Calls, err, want)
	}
}

// 条件: 検索で 3 つのページが見つかり、回答が 2 番と 1 番を、この順で示す。
// 内容: 回答の下に、示されたページだけが番号順に、タイトルと山かっこつきの URL で並ぶ（1.1、2.1、2.5）。
func TestExecute_AnswerCitesPages_ListsOnlyCitedPagesInNumberOrder(t *testing.T) {
	s := newScenario(testConfig())
	s.searcher.Responses = []websearchtest.Response{found("a", "b", "c")}
	s.gen.ToolCalls = []llm.ToolCall{searchCall("q")}
	s.gen.Result = llm.Result{Text: "B です[2]。A です[1]。"}

	got, err := s.cmd.Execute(context.Background(), askInvocation())

	assertAnswer(t, got, err, "> Go とは？\n\nB です[2]。A です[1]。\n\n出典:\n[1] a <https://a.example/>\n[2] b <https://b.example/>")
}

// 条件: 2 回の検索で、1 回目に a・b、2 回目に b・c が見つかる。回答はページを示さない。予備の出典は 5 件まで。
// 内容: 2 回目も検索される（1.3）。同じページ b は、出典に 1 回だけ並ぶ（2.4）。
func TestExecute_SamePageFoundTwice_ListsPageOnce(t *testing.T) {
	cfg := testConfig()
	cfg.WebSearch.FallbackSourceCount = 5
	s := newScenario(cfg)
	s.searcher.Responses = []websearchtest.Response{found("a", "b"), found("b", "c")}
	s.gen.ToolCalls = []llm.ToolCall{searchCall("q1"), searchCall("q2")}

	got, err := s.cmd.Execute(context.Background(), askInvocation())

	assertAnswer(t, got, err, "> Go とは？\n\n答え\n\n出典:\n[1] a <https://a.example/>\n[2] b <https://b.example/>\n[3] c <https://c.example/>")
}

// 条件: 検索で 3 つのページが見つかり、回答はページを 1 つも示さない。予備の出典は 2 件まで。
// 内容: 先頭から 2 件のページが、出典に並ぶ（2.3）。
func TestExecute_AnswerWithoutCitation_ListsFirstFallbackCountPages(t *testing.T) {
	s := newScenario(testConfig())
	s.searcher.Responses = []websearchtest.Response{found("a", "b", "c")}
	s.gen.ToolCalls = []llm.ToolCall{searchCall("q")}

	got, err := s.cmd.Execute(context.Background(), askInvocation())

	assertAnswer(t, got, err, "> Go とは？\n\n答え\n\n出典:\n[1] a <https://a.example/>\n[2] b <https://b.example/>")
}

// 条件: 検索の上限が 2 回のとき、モデルが 3 回の検索を求める。
// 内容: 検索は 2 回で止まる。3 回目の求めには、上限に達した旨の文がモデルへ返る（3.1、3.2）。
func TestExecute_SearchesBeyondLimit_StopsSearchingAndTellsModel(t *testing.T) {
	type observed struct {
		SearchCount int
		ToolOutputs []llm.ToolOutput
	}
	s := newScenario(testConfig())
	s.searcher.Responses = []websearchtest.Response{found("a"), found("b")}
	s.gen.ToolCalls = []llm.ToolCall{searchCall("q1"), searchCall("q2"), searchCall("q3")}

	_, err := s.cmd.Execute(context.Background(), askInvocation())

	got := observed{SearchCount: len(s.searcher.Calls), ToolOutputs: s.gen.ToolOutputs}
	want := observed{
		SearchCount: 2,
		ToolOutputs: []llm.ToolOutput{
			{Text: "[1] a\nURL: https://a.example/\na の本文"},
			{Text: "[2] b\nURL: https://b.example/\nb の本文"},
			{Text: "上限"},
		},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("observed = %+v, %v, want %+v", got, err, want)
	}
}

// 条件: Web 検索が失敗する。
// 内容: 回答は返る。回答の下に、Web の情報を確認しきれていない旨の注記が付く。出典は付かない（4.1）。
func TestExecute_SearchFails_ReturnsAnswerWithFailureNotice(t *testing.T) {
	s := newScenario(testConfig())
	s.searcher.Responses = []websearchtest.Response{{Err: errBoom}}
	s.gen.ToolCalls = []llm.ToolCall{searchCall("q")}

	got, err := s.cmd.Execute(context.Background(), askInvocation())

	assertAnswer(t, got, err, "> Go とは？\n\n答え\n\n"+failureNotice)
}

// 条件: Web 検索が失敗する。
// 内容: モデルへ、検索に失敗した旨の文が、失敗の結果として返る。
func TestExecute_SearchFails_TellsModelSearchFailed(t *testing.T) {
	s := newScenario(testConfig())
	s.searcher.Responses = []websearchtest.Response{{Err: errBoom}}
	s.gen.ToolCalls = []llm.ToolCall{searchCall("q")}

	_, err := s.cmd.Execute(context.Background(), askInvocation())

	want := []llm.ToolOutput{{Text: "失敗", Failed: true}}
	if err != nil || !reflect.DeepEqual(s.gen.ToolOutputs, want) {
		t.Errorf("tool outputs = %+v, %v, want %+v", s.gen.ToolOutputs, err, want)
	}
}

// 条件: Web 検索の結果が 1 件も無い。
// 内容: 回答は返る。回答の下に、Web で見つからなかった旨の注記が付く。出典は付かない（4.2）。
func TestExecute_SearchFindsNothing_ReturnsAnswerWithNoResultsNotice(t *testing.T) {
	s := newScenario(testConfig())
	s.gen.ToolCalls = []llm.ToolCall{searchCall("q")}

	got, err := s.cmd.Execute(context.Background(), askInvocation())

	assertAnswer(t, got, err, "> Go とは？\n\n答え\n\n"+noResultsNotice)
}

// 条件: Web 検索の結果が 1 件も無い。
// 内容: モデルへ、結果が無かった旨の文が返る。
func TestExecute_SearchFindsNothing_TellsModelNoResults(t *testing.T) {
	s := newScenario(testConfig())
	s.gen.ToolCalls = []llm.ToolCall{searchCall("q")}

	_, err := s.cmd.Execute(context.Background(), askInvocation())

	want := []llm.ToolOutput{{Text: "0 件"}}
	if err != nil || !reflect.DeepEqual(s.gen.ToolOutputs, want) {
		t.Errorf("tool outputs = %+v, %v, want %+v", s.gen.ToolOutputs, err, want)
	}
}

// 条件: 1 回目の検索が失敗し、2 回目の検索でページ a が見つかる。回答は 1 番を示す。
// 内容: 「回答 → 失敗の注記 → 出典」の順で返る。
func TestExecute_SearchFailsThenSucceeds_ReturnsNoticeAndSources(t *testing.T) {
	s := newScenario(testConfig())
	s.searcher.Responses = []websearchtest.Response{{Err: errBoom}, found("a")}
	s.gen.ToolCalls = []llm.ToolCall{searchCall("q1"), searchCall("q2")}
	s.gen.Result = llm.Result{Text: "A です[1]。"}

	got, err := s.cmd.Execute(context.Background(), askInvocation())

	assertAnswer(t, got, err, "> Go とは？\n\nA です[1]。\n\n"+failureNotice+"\n\n出典:\n[1] a <https://a.example/>")
}

// 条件: 検索の後で、回答の生成が失敗する。
// 内容: 生成のエラーがそのまま返り、結果は空になる（呼び出し元が、従来の失敗の通知を出す。4.3）。
func TestExecute_GeneratorFails_ReturnsError(t *testing.T) {
	s := newScenario(testConfig())
	s.searcher.Responses = []websearchtest.Response{found("a")}
	s.gen.ToolCalls = []llm.ToolCall{searchCall("q")}
	s.gen.Err = errBoom

	got, err := s.cmd.Execute(context.Background(), askInvocation())

	if !errors.Is(err, errBoom) || !reflect.DeepEqual(got, command.Result{}) {
		t.Errorf("Execute() = %+v, %v, want empty result and %v", got, err, errBoom)
	}
}

// 条件: 回答が、出力の上限で打ち切られる。
// 内容: 返信の末尾に、途中までの結果である旨の注記が付く。
func TestExecute_AnswerTruncated_AppendsTruncatedNotice(t *testing.T) {
	s := newScenario(testConfig())
	s.gen.Result = llm.Result{Text: "途中", Truncated: true}

	got, err := s.cmd.Execute(context.Background(), askInvocation())

	if err != nil || !strings.HasSuffix(got.Text, command.TruncatedNotice) {
		t.Errorf("Execute() = %q, %v, want it to end with %q", got.Text, err, command.TruncatedNotice)
	}
}

// 条件: 検索の上限が 1 回のとき、同じコマンドで 2 回続けて質問する。1 回目はページ a、2 回目はページ b が見つかる。
// 内容: 2 回目も検索され、2 回目の出典はページ b だけになる。検索の回数もページも、前の質問から引き継がない（5.2）。
func TestExecute_TwoQuestions_DoesNotCarryPagesOrSearchCount(t *testing.T) {
	cfg := testConfig()
	cfg.WebSearch.MaxSearches = 1
	s := newScenario(cfg)
	s.searcher.Responses = []websearchtest.Response{found("a"), found("b")}
	s.gen.ToolCalls = []llm.ToolCall{searchCall("q")}
	if _, err := s.cmd.Execute(context.Background(), askInvocation()); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}

	got, err := s.cmd.Execute(context.Background(), askInvocation())

	assertAnswer(t, got, err, "> Go とは？\n\n答え\n\n出典:\n[1] b <https://b.example/>")
}

// 条件: 検索で、本文が 16 文字のページが見つかる。抜粋の上限は 10 文字。
// 内容: モデルへ、番号・タイトル・URL・先頭 10 文字の抜粋が返る。
func TestExecute_SearchResult_GivesModelNumberedTitleURLAndTruncatedText(t *testing.T) {
	s := newScenario(testConfig())
	s.searcher.Responses = []websearchtest.Response{{Pages: []websearch.Page{{Title: "T", URL: "https://t.example/", Text: "0123456789ABCDEF"}}}}
	s.gen.ToolCalls = []llm.ToolCall{searchCall("q")}

	_, err := s.cmd.Execute(context.Background(), askInvocation())

	want := []llm.ToolOutput{{Text: "[1] T\nURL: https://t.example/\n0123456789"}}
	if err != nil || !reflect.DeepEqual(s.gen.ToolOutputs, want) {
		t.Errorf("tool outputs = %+v, %v, want %+v", s.gen.ToolOutputs, err, want)
	}
}

// 条件: Web 検索が失敗する。
// 内容: 失敗したことが警告としてログに残る。検索文はログに残らない。
func TestExecute_SearchFails_LogsWarningWithoutQuery(t *testing.T) {
	const secretQuery = "秘密の検索文"
	s := newScenario(testConfig())
	s.searcher.Responses = []websearchtest.Response{{Err: errBoom}}
	s.gen.ToolCalls = []llm.ToolCall{searchCall(secretQuery)}

	_, err := s.cmd.Execute(context.Background(), askInvocation())

	logged := s.logs.String()
	if err != nil || !strings.Contains(logged, "web search failed") || strings.Contains(logged, secretQuery) {
		t.Errorf("logs = %q, %v, want a warning without the query", logged, err)
	}
}

// 条件: Web 検索が成功し、回答が返る。
// 内容: ログには何も残らない（質問、検索の内容、検索結果、回答を残さない）。
func TestExecute_SearchSucceeds_LogsNothing(t *testing.T) {
	s := newScenario(testConfig())
	s.searcher.Responses = []websearchtest.Response{found("a")}
	s.gen.ToolCalls = []llm.ToolCall{searchCall("q")}

	_, err := s.cmd.Execute(context.Background(), askInvocation())

	if logged := s.logs.String(); err != nil || logged != "" {
		t.Errorf("logs = %q, %v, want empty", logged, err)
	}
}

// 条件: コマンドの定義を取得する。入力の上限の設定は 6000 文字。
// 内容: サーバ専用で、必須の文字列オプション question（最大 6000 文字）を持つ。移行前の定義と同じ（5.1）。
func TestDefinition_Always_IsGuildOnlyWithInputLimit(t *testing.T) {
	cmd := ask.New(nil, nil, testConfig(), nil, nil)
	want := `{"type":1,"name":"ask","description":"質問に回答します","options":[{"type":3,"name":"question","description":"質問の内容","required":true,"max_length":6000}],"contexts":[0]}`

	got, err := json.Marshal(cmd.Definition())

	if err != nil {
		t.Fatalf("marshal definition: %v", err)
	}
	assertJSONEqual(t, string(got), want)
}
