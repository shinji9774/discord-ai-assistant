package summarize_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/llm/llmtest"
	"discord-ai-assistant/internal/summarize"
	"discord-ai-assistant/internal/summarize/summarizetest"
)

// 条件: 依存を渡さずに作ったコマンドの定義。
// 内容: 名前 summarize、since・until（文字列）、include_bots（真偽）、visibility の順のオプションを持ち、サーバのチャンネルでだけ使える。
func TestDefinition_Always_DescribesSummarizeCommand(t *testing.T) {
	cmd := summarize.New(nil, nil, summarize.Config{}, nil)

	got := cmd.Definition()

	assertJSONEqual(t, got, `{
		"type": 1,
		"name": "summarize",
		"description": "このチャンネルの会話を要約する",
		"options": [
			{"type": 3, "name": "since", "description": "対象期間の開始（例: 2026-11-18 09:00。日本時間）"},
			{"type": 3, "name": "until", "description": "対象期間の終了（例: 2026-11-19。日本時間）"},
			{"type": 5, "name": "include_bots", "description": "bot の投稿も要約に含める"},
			{"type": 3, "name": "visibility", "description": "結果を見られる人（省略時は全員）",
			 "choices": [{"name": "全員", "value": "public"}, {"name": "実行者だけ", "value": "private"}]}
		],
		"contexts": [0]
	}`)
}

const (
	testChannel = "1300000000000000001"
	summaryText = "## 会議\n- 佐藤: 10 時から"

	// botCanRead は、チャンネルを見る権限とメッセージ履歴を読む権限の両方。
	botCanRead    = discord.PermissionViewChannel | discord.PermissionReadMessageHistory
	memberCanRead = discord.PermissionReadMessageHistory
)

var fixedNow = time.Date(2026, 11, 19, 14, 0, 0, 0, jst)

func testConfig() summarize.Config {
	return summarize.Config{DefaultPeriodHours: 24, MaxMessages: 1000, MaxOutputTokens: 4096, SystemPrompt: "会話を要約してください"}
}

// scenario は、fake の依存と固定の時刻で作った /summarize。
type scenario struct {
	gen     *llmtest.Generator
	history *summarizetest.History
	cmd     *summarize.Command
}

func newScenario(cfg summarize.Config, posts ...summarize.Message) *scenario {
	gen := &llmtest.Generator{Result: llm.Result{Text: summaryText}}
	history := &summarizetest.History{Messages: posts}
	return &scenario{gen: gen, history: history, cmd: summarize.New(gen, history, cfg, func() time.Time { return fixedNow })}
}

// outcome は実行の結果と、履歴を読んだ回数、モデルへの依頼。
type outcome struct {
	Result   command.Result
	Reads    int
	Requests []llm.Request
}

func (s *scenario) outcome(result command.Result) outcome {
	return outcome{Result: result, Reads: s.history.Reads, Requests: s.gen.Requests}
}

// invocation は testChannel での /summarize の実行。
func invocation(options map[string]string, appPermissions, memberPermissions discord.Permissions) command.Invocation {
	return command.Invocation{
		ApplicationID:     "app1",
		InteractionToken:  "tok1",
		ChannelID:         testChannel,
		CommandName:       "summarize",
		Options:           options,
		AppPermissions:    appPermissions,
		MemberPermissions: memberPermissions,
	}
}

// 条件: 期間の指定の誤り、または bot・実行者の権限の不足。
// 内容: それぞれの案内文を返し、会話履歴を読まず、モデルも呼ばない。
func TestExecute_PreconditionNotMet_RepliesGuidanceWithoutReading(t *testing.T) {
	const (
		invalidFormat    = "since と until は `YYYY-MM-DD` または `YYYY-MM-DD HH:MM`（日本時間）で指定してください。"
		inverted         = "期間の指定が正しくありません。since は until より前の日時にしてください。"
		noConversation   = "指定した期間に、要約する会話がありませんでした。"
		botCannotRead    = "bot にこのチャンネルの会話履歴を読む権限がないため、要約できません。サーバの管理者に相談してください。"
		memberCannotRead = "このチャンネルの会話履歴を読む権限がないため、要約できません。"
	)
	tests := []struct {
		name string
		inv  command.Invocation
		want string
	}{
		{"invalid since format", invocation(map[string]string{"since": "2026/11/18"}, botCanRead, memberCanRead), invalidFormat},
		{"since after until", invocation(map[string]string{"since": "2026-11-18 12:00", "until": "2026-11-18 09:00"}, botCanRead, memberCanRead), inverted},
		{"since in future", invocation(map[string]string{"since": "2026-11-20"}, botCanRead, memberCanRead), noConversation},
		{"bot cannot read history", invocation(map[string]string{}, discord.PermissionViewChannel, memberCanRead), botCannotRead},
		{"bot cannot view channel", invocation(map[string]string{}, discord.PermissionReadMessageHistory, memberCanRead), botCannotRead},
		{"member cannot read history", invocation(map[string]string{}, botCanRead, 0), memberCannotRead},
		{"invalid format checked before permissions", invocation(map[string]string{"until": "昨日"}, 0, 0), invalidFormat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newScenario(testConfig())

			result, err := s.cmd.Execute(context.Background(), tt.inv)

			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got, want := s.outcome(result), (outcome{Result: command.Result{Text: tt.want}}); !reflect.DeepEqual(got, want) {
				t.Errorf("outcome = %+v, want %+v", got, want)
			}
		})
	}
}

// defaultPeriodHeading は fixedNow までの既定の 24 時間に 2 件のときの見出し。
const defaultPeriodHeading = "**対象期間**: 2026-11-18 14:00 〜 2026-11-19 14:00（日本時間） / **メッセージ**: 2 件"

// 条件: since と until を省略し、直近 24 時間に利用者の投稿が 2 件ある。
// 内容: 会話の記録をモデルに 1 回だけ渡し、見出しつきの要約を Markdown・機密の印つきで返す。
func TestExecute_PostsInDefaultPeriod_RepliesSummaryWithHeading(t *testing.T) {
	s := newScenario(testConfig(),
		summarizetest.Post(fixedNow.Add(-2*time.Hour), "佐藤", "会議は 10 時から"),
		summarizetest.Post(fixedNow.Add(-1*time.Hour), "鈴木", "了解です"),
	)

	result, err := s.cmd.Execute(context.Background(), invocation(map[string]string{}, botCanRead, memberCanRead))

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := outcome{
		Result: command.Result{
			Text:         defaultPeriodHeading + "\n\n" + summaryText,
			Format:       command.TextFormatMarkdown,
			Confidential: true,
		},
		Reads: 1,
		Requests: []llm.Request{{
			System:          testConfig().SystemPrompt,
			UserText:        "[2026-11-19 12:00] 佐藤: 会議は 10 時から\n[2026-11-19 13:00] 鈴木: 了解です",
			MaxOutputTokens: testConfig().MaxOutputTokens,
		}},
	}
	if got := s.outcome(result); !reflect.DeepEqual(got, want) {
		t.Errorf("outcome = %+v, want %+v", got, want)
	}
}

// 条件: include_bots に true を指定し、利用者の投稿と bot の投稿がある。
// 内容: bot の投稿もモデルへの会話の記録に入る。
func TestExecute_IncludeBotsTrue_SendsBotPostsToModel(t *testing.T) {
	bot := summarizetest.Post(fixedNow.Add(-1*time.Hour), "helper-bot", "定期通知")
	bot.FromBot = true
	s := newScenario(testConfig(), summarizetest.Post(fixedNow.Add(-2*time.Hour), "佐藤", "会議は 10 時から"), bot)

	_, err := s.cmd.Execute(context.Background(), invocation(map[string]string{"include_bots": "true"}, botCanRead, memberCanRead))

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []llm.Request{{
		System:          testConfig().SystemPrompt,
		UserText:        "[2026-11-19 12:00] 佐藤: 会議は 10 時から\n[2026-11-19 13:00] helper-bot: 定期通知",
		MaxOutputTokens: testConfig().MaxOutputTokens,
	}}
	if !reflect.DeepEqual(s.gen.Requests, want) {
		t.Errorf("requests = %+v, want %+v", s.gen.Requests, want)
	}
}

// 条件: 期間に会話の投稿が無い（期間の外の投稿だけがある）。
// 内容: 「会話なし」の案内を返し、モデルを呼ばない。
func TestExecute_NoPostsInPeriod_RepliesNoConversationGuidance(t *testing.T) {
	s := newScenario(testConfig(), summarizetest.Post(fixedNow.Add(-48*time.Hour), "佐藤", "先週の話"))

	result, err := s.cmd.Execute(context.Background(), invocation(map[string]string{}, botCanRead, memberCanRead))

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := outcome{Result: command.Result{Text: "指定した期間に、要約する会話がありませんでした。"}, Reads: 1}
	if got := s.outcome(result); !reflect.DeepEqual(got, want) {
		t.Errorf("outcome = %+v, want %+v", got, want)
	}
}

// 条件: interaction の権限は足りているが、Discord が履歴の取得を拒む（ErrHistoryForbidden）。
// 内容: bot の権限不足の案内を返し、モデルを呼ばない。
func TestExecute_HistoryForbidden_RepliesBotPermissionGuidance(t *testing.T) {
	s := newScenario(testConfig())
	s.history.Err = summarize.ErrHistoryForbidden

	result, err := s.cmd.Execute(context.Background(), invocation(map[string]string{}, botCanRead, memberCanRead))

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := outcome{
		Result: command.Result{Text: "bot にこのチャンネルの会話履歴を読む権限がないため、要約できません。サーバの管理者に相談してください。"},
		Reads:  1,
	}
	if got := s.outcome(result); !reflect.DeepEqual(got, want) {
		t.Errorf("outcome = %+v, want %+v", got, want)
	}
}

// 条件: 履歴の取得、またはモデルの生成が失敗する。
// 内容: 空の結果と、元のエラーを包んだエラーを返す（worker が失敗の定型文を返信する）。
func TestExecute_DependencyFails_ReturnsWrappedError(t *testing.T) {
	errBroken := errors.New("broken")
	tests := []struct {
		name  string
		setup func(*scenario)
	}{
		{"history fails", func(s *scenario) { s.history.Err = errBroken }},
		{"generation fails", func(s *scenario) { s.gen.Err = errBroken }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newScenario(testConfig(), summarizetest.Post(fixedNow.Add(-time.Hour), "佐藤", "会議は 10 時から"))
			tt.setup(s)

			result, err := s.cmd.Execute(context.Background(), invocation(map[string]string{}, botCanRead, memberCanRead))

			if !errors.Is(err, errBroken) || !reflect.DeepEqual(result, command.Result{}) {
				t.Errorf("Execute = (%+v, %v), want (empty, wrapping %v)", result, err, errBroken)
			}
		})
	}
}

// 条件: 上限 1 件に対して 2 件の投稿があり、モデルの出力も上限で途中終了する。
// 内容: 新しい 1 件だけを要約し、結果の末尾に打ち切りと途中終了の注記が付く。
func TestExecute_ClippedAndTruncated_AppendsNotices(t *testing.T) {
	cfg := testConfig()
	cfg.MaxMessages = 1
	s := newScenario(cfg,
		summarizetest.Post(fixedNow.Add(-2*time.Hour), "佐藤", "会議は 10 時から"),
		summarizetest.Post(fixedNow.Add(-1*time.Hour), "鈴木", "了解です"),
	)
	s.gen.Result = llm.Result{Text: summaryText, Truncated: true}

	result, err := s.cmd.Execute(context.Background(), invocation(map[string]string{}, botCanRead, memberCanRead))

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := command.Result{
		Text: "**対象期間**: 2026-11-18 14:00 〜 2026-11-19 14:00（日本時間） / **メッセージ**: 1 件\n\n" +
			summaryText + "\n\n" +
			"（上限の 1 件を超えたため、古い会話は要約に含まれていません）\n\n" +
			"（上限に達したため、途中までの結果です）",
		Format:       command.TextFormatMarkdown,
		Confidential: true,
	}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("result = %+v, want %+v", result, want)
	}
}
