package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/test/bedrockfake"
)

const (
	toolName          = "web_search"
	toolUseID         = "tool-1"
	searchQuery       = "Go 最新版"
	maxToolTurns      = 3
	stopReasonEnd     = "end_turn"
	stopReasonToolUse = "tool_use"

	secondToolUseID = "tool-2"
	secondQuery     = "Go リリースノート"
)

// toolRecorder は、呼ばれたツールを記録し、決めた結果を返す。
type toolRecorder struct {
	Output llm.ToolOutput
	Calls  []llm.ToolCall
}

func (r *toolRecorder) run(_ context.Context, call llm.ToolCall) llm.ToolOutput {
	r.Calls = append(r.Calls, call)
	return r.Output
}

func toolRequest(run llm.ToolRunner) llm.ToolRequest {
	return llm.ToolRequest{
		Request: llm.Request{System: "sys", UserText: "質問", MaxOutputTokens: 100},
		Tools: []llm.Tool{{
			Name:        toolName,
			Description: "Web を検索する",
			Parameters:  []llm.ToolParameter{{Name: "query", Description: "検索する内容"}},
		}},
		Run:      run,
		MaxTurns: maxToolTurns,
	}
}

func searchToolUse() bedrockfake.ToolUse {
	return bedrockfake.ToolUse{ID: toolUseID, Name: toolName, Input: map[string]string{"query": searchQuery}}
}

// outcome は、1 回の GenerateWithTools で観測できる結果。
type outcome struct {
	Result        llm.Result
	ConverseCount int
	Calls         []llm.ToolCall
}

// failure は、失敗した GenerateWithTools で観測できる結果。
type failure struct {
	IsExpectedError bool
	ConverseCount   int
}

// bodyField は、依頼の本文（JSON）から、1 つの項目を取り出す。
func bodyField(t *testing.T, body, field string) string {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return string(fields[field])
}

// assertSameJSON は、2 つの JSON が同じ値を表すことを確かめる（項目の順と空白は問わない）。
func assertSameJSON(t *testing.T, got, want string) {
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

// 条件: モデルがツールを求めずに回答する。
// 内容: 回答がそのまま返る。モデルの呼び出しは 1 回で、ツールは実行されない。
func TestGenerateWithTools_ModelAnswersWithoutTool_ReturnsTextWithOneConverse(t *testing.T) {
	gen, fake := newGenerator(t)
	fake.RespondWith("答え", stopReasonEnd)
	recorder := &toolRecorder{}

	result, err := gen.GenerateWithTools(context.Background(), toolRequest(recorder.run))

	got := outcome{Result: result, ConverseCount: len(fake.ConverseBodies()), Calls: recorder.Calls}
	want := outcome{Result: llm.Result{Text: "答え"}, ConverseCount: 1}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("GenerateWithTools() = %+v, %v, want %+v", got, err, want)
	}
}

// 条件: 文字列の引数 query を 1 つ持つツールを渡して生成する。
// 内容: モデルへの依頼に、ツールの名前・説明・引数の定義（必須の文字列）が入る。
func TestGenerateWithTools_Request_SendsToolSpecification(t *testing.T) {
	gen, fake := newGenerator(t)
	recorder := &toolRecorder{}
	want := `{"tools":[{"toolSpec":{"name":"web_search","description":"Web を検索する","inputSchema":{"json":{"type":"object","properties":{"query":{"type":"string","description":"検索する内容"}},"required":["query"]}}}}]}`

	_, err := gen.GenerateWithTools(context.Background(), toolRequest(recorder.run))

	if err != nil {
		t.Fatalf("GenerateWithTools() error = %v", err)
	}
	assertSameJSON(t, bodyField(t, fake.ConverseBodies()[0], "toolConfig"), want)
}

// 条件: モデルが 1 回目にツールを求め、2 回目に回答する。
// 内容: 求められた名前と引数でツールが 1 回実行され、2 回目の回答が返る。
func TestGenerateWithTools_ModelUsesTool_RunsToolAndReturnsFinalText(t *testing.T) {
	gen, fake := newGenerator(t)
	fake.RespondWithToolUse(searchToolUse())
	fake.RespondWith("最終回答", stopReasonEnd)
	recorder := &toolRecorder{Output: llm.ToolOutput{Text: "検索結果"}}

	result, err := gen.GenerateWithTools(context.Background(), toolRequest(recorder.run))

	got := outcome{Result: result, ConverseCount: len(fake.ConverseBodies()), Calls: recorder.Calls}
	want := outcome{
		Result:        llm.Result{Text: "最終回答"},
		ConverseCount: 2,
		Calls:         []llm.ToolCall{{Name: toolName, Input: map[string]string{"query": searchQuery}}},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("GenerateWithTools() = %+v, %v, want %+v", got, err, want)
	}
}

// 条件: モデルがツールを求め、ツールが成功の結果を返す。
// 内容: 2 回目の依頼に、質問・モデルが求めた呼び出し・ツールの結果（成功）が、この順で入る。
func TestGenerateWithTools_AfterToolRun_SendsToolUseAndResultBack(t *testing.T) {
	gen, fake := newGenerator(t)
	fake.RespondWithToolUse(searchToolUse())
	recorder := &toolRecorder{Output: llm.ToolOutput{Text: "検索結果"}}
	want := `[{"role":"user","content":[{"text":"質問"}]},{"role":"assistant","content":[{"toolUse":{"toolUseId":"tool-1","name":"web_search","input":{"query":"Go 最新版"}}}]},{"role":"user","content":[{"toolResult":{"toolUseId":"tool-1","content":[{"text":"検索結果"}],"status":"success"}}]}]`

	_, err := gen.GenerateWithTools(context.Background(), toolRequest(recorder.run))

	if err != nil {
		t.Fatalf("GenerateWithTools() error = %v", err)
	}
	assertSameJSON(t, bodyField(t, fake.ConverseBodies()[1], "messages"), want)
}

// 条件: モデルが 1 回の応答で、異なる引数のツールを 2 つ求める。
// 内容: 2 つのツールがどちらも求められた順に実行され、2 つの結果が 1 つの user メッセージに、求められた順で入る。
func TestGenerateWithTools_ModelRequestsTwoToolsAtOnce_RunsBothAndSendsBothResults(t *testing.T) {
	gen, fake := newGenerator(t)
	secondToolUse := bedrockfake.ToolUse{ID: secondToolUseID, Name: toolName, Input: map[string]string{"query": secondQuery}}
	fake.RespondWithToolUse(searchToolUse(), secondToolUse)
	recorder := &toolRecorder{Output: llm.ToolOutput{Text: "検索結果"}}
	wantCalls := []llm.ToolCall{
		{Name: toolName, Input: map[string]string{"query": searchQuery}},
		{Name: toolName, Input: map[string]string{"query": secondQuery}},
	}
	wantMessages := `[{"role":"user","content":[{"text":"質問"}]},{"role":"assistant","content":[{"toolUse":{"toolUseId":"tool-1","name":"web_search","input":{"query":"Go 最新版"}}},{"toolUse":{"toolUseId":"tool-2","name":"web_search","input":{"query":"Go リリースノート"}}}]},{"role":"user","content":[{"toolResult":{"toolUseId":"tool-1","content":[{"text":"検索結果"}],"status":"success"}},{"toolResult":{"toolUseId":"tool-2","content":[{"text":"検索結果"}],"status":"success"}}]}]`

	_, err := gen.GenerateWithTools(context.Background(), toolRequest(recorder.run))

	if err != nil {
		t.Fatalf("GenerateWithTools() error = %v", err)
	}
	if !reflect.DeepEqual(recorder.Calls, wantCalls) {
		t.Errorf("tool calls = %+v, want %+v", recorder.Calls, wantCalls)
	}
	assertSameJSON(t, bodyField(t, fake.ConverseBodies()[1], "messages"), wantMessages)
}

// 条件: モデルがツールを求め、ツールが失敗の結果を返す。
// 内容: 2 回目の依頼のツールの結果に、失敗の文と、失敗の状態が入る。
func TestGenerateWithTools_ToolFailed_SendsErrorStatus(t *testing.T) {
	gen, fake := newGenerator(t)
	fake.RespondWithToolUse(searchToolUse())
	recorder := &toolRecorder{Output: llm.ToolOutput{Text: "失敗", Failed: true}}
	want := `[{"role":"user","content":[{"text":"質問"}]},{"role":"assistant","content":[{"toolUse":{"toolUseId":"tool-1","name":"web_search","input":{"query":"Go 最新版"}}}]},{"role":"user","content":[{"toolResult":{"toolUseId":"tool-1","content":[{"text":"失敗"}],"status":"error"}}]}]`

	_, err := gen.GenerateWithTools(context.Background(), toolRequest(recorder.run))

	if err != nil {
		t.Fatalf("GenerateWithTools() error = %v", err)
	}
	assertSameJSON(t, bodyField(t, fake.ConverseBodies()[1], "messages"), want)
}

// 条件: モデルが、呼び出しの上限（3 回）のすべてでツールを求め続ける。
// 内容: ErrToolTurnsExceeded が返る。モデルの呼び出しは、上限の 3 回で止まる。
func TestGenerateWithTools_ModelKeepsUsingTool_ReturnsErrToolTurnsExceeded(t *testing.T) {
	gen, fake := newGenerator(t)
	for range maxToolTurns {
		fake.RespondWithToolUse(searchToolUse())
	}
	recorder := &toolRecorder{Output: llm.ToolOutput{Text: "検索結果"}}

	_, err := gen.GenerateWithTools(context.Background(), toolRequest(recorder.run))

	got := failure{IsExpectedError: errors.Is(err, llm.ErrToolTurnsExceeded), ConverseCount: len(fake.ConverseBodies())}
	want := failure{IsExpectedError: true, ConverseCount: maxToolTurns}
	if got != want {
		t.Errorf("GenerateWithTools() = %+v (err %v), want %+v", got, err, want)
	}
}

// 条件: モデルが「ツールを求める」と答えたのに、応答にツールの呼び出しが入っていない。
// 内容: ErrInvalidToolUse が返る。モデルの呼び出しは 1 回で止まる。
func TestGenerateWithTools_ToolUseStopWithoutToolUseBlock_ReturnsErrInvalidToolUse(t *testing.T) {
	gen, fake := newGenerator(t)
	fake.RespondWith("text", stopReasonToolUse)
	recorder := &toolRecorder{}

	_, err := gen.GenerateWithTools(context.Background(), toolRequest(recorder.run))

	got := failure{IsExpectedError: errors.Is(err, llm.ErrInvalidToolUse), ConverseCount: len(fake.ConverseBodies())}
	want := failure{IsExpectedError: true, ConverseCount: 1}
	if got != want {
		t.Errorf("GenerateWithTools() = %+v (err %v), want %+v", got, err, want)
	}
}

// 条件: モデルの呼び出しが失敗する。
// 内容: エラーが返り、ツールは実行されない。
func TestGenerateWithTools_ConverseFails_ReturnsError(t *testing.T) {
	gen, fake := newGenerator(t)
	fake.FailWith(http.StatusBadRequest)
	recorder := &toolRecorder{}

	_, err := gen.GenerateWithTools(context.Background(), toolRequest(recorder.run))

	if err == nil || len(recorder.Calls) != 0 {
		t.Errorf("GenerateWithTools() error = %v, tool calls = %+v, want error and no tool calls", err, recorder.Calls)
	}
}
