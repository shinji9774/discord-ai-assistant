package command_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
)

// stubCommand は名前だけを持つ Command の実装。
type stubCommand struct {
	name string
}

func (s stubCommand) Definition() discord.SlashCommandCreate {
	return discord.SlashCommandCreate{Name: s.name}
}

func (s stubCommand) Execute(ctx context.Context, inv command.Invocation) (command.Result, error) {
	return command.Result{}, nil
}

// findResult は Find の戻り値の組を 1 回で比較するための入れ物。
type findResult struct {
	Command command.Command
	Found   bool
}

func capturePanic(f func()) (recovered any) {
	defer func() { recovered = recover() }()
	f()
	return nil
}

func definitionNames(definitions []discord.ApplicationCommandCreate) []string {
	names := make([]string, 0, len(definitions))
	for _, d := range definitions {
		names = append(names, d.CommandName())
	}
	return names
}

// 条件: translate と ask を登録し、ask を検索する。
// 内容: ask の stub と true が返る。
func TestFind_RegisteredName_ReturnsCommand(t *testing.T) {
	translate, ask := stubCommand{name: "translate"}, stubCommand{name: "ask"}
	registry := command.NewRegistry(translate, ask)

	got, found := registry.Find("ask")

	if want := (findResult{Command: ask, Found: true}); !reflect.DeepEqual(findResult{got, found}, want) {
		t.Errorf("Find() = %+v, want %+v", findResult{got, found}, want)
	}
}

// 条件: 未登録の名前を検索する。
// 内容: nil と false が返る。
func TestFind_UnknownName_ReturnsFalse(t *testing.T) {
	registry := command.NewRegistry(stubCommand{name: "translate"})

	got, found := registry.Find("unknown")

	if want := (findResult{}); !reflect.DeepEqual(findResult{got, found}, want) {
		t.Errorf("Find() = %+v, want %+v", findResult{got, found}, want)
	}
}

// 条件: translate、ask の順で登録する。
// 内容: 定義の名前の列が登録順 ["translate", "ask"] になる。
func TestDefinitions_TwoCommands_ReturnsDefinitionsInRegistrationOrder(t *testing.T) {
	registry := command.NewRegistry(stubCommand{name: "translate"}, stubCommand{name: "ask"})

	got := definitionNames(registry.Definitions())

	if want := []string{"translate", "ask"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Definitions() names = %v, want %v", got, want)
	}
}

// 条件: 同じ名前のコマンドを 2 つ登録する。
// 内容: NewRegistry が panic する。
func TestNewRegistry_DuplicateName_Panics(t *testing.T) {
	a, b := stubCommand{name: "ask"}, stubCommand{name: "ask"}

	recovered := capturePanic(func() { command.NewRegistry(a, b) })

	if recovered == nil {
		t.Error("NewRegistry() did not panic on duplicate name")
	}
}

// secretStubCommand は、入力に秘密を含む目印（SecretInput）を持つ stubCommand。
type secretStubCommand struct {
	stubCommand
}

func (secretStubCommand) HasSecretInput() {}

var _ command.SecretInput = secretStubCommand{}

// newSecretAwareRegistry は、目印のない translate と、目印のある summarize-voice を登録する。
func newSecretAwareRegistry() *command.Registry {
	return command.NewRegistry(
		stubCommand{name: "translate"},
		secretStubCommand{stubCommand: stubCommand{name: "summarize-voice"}},
	)
}

// 条件: SecretInput を実装したコマンド（summarize-voice）の名前で問い合わせる。
// 内容: true が返る。
func TestHasSecretInput_SecretInputCommand_ReturnsTrue(t *testing.T) {
	registry := newSecretAwareRegistry()

	got := registry.HasSecretInput("summarize-voice")

	if !got {
		t.Error(`HasSecretInput("summarize-voice") = false, want true`)
	}
}

// 条件: SecretInput を実装していないコマンド（translate）の名前で問い合わせる。
// 内容: false が返る。
func TestHasSecretInput_PlainCommand_ReturnsFalse(t *testing.T) {
	registry := newSecretAwareRegistry()

	got := registry.HasSecretInput("translate")

	if got {
		t.Error(`HasSecretInput("translate") = true, want false`)
	}
}

// 条件: 登録されていない名前で問い合わせる。
// 内容: false が返る（panic しない）。
func TestHasSecretInput_UnknownName_ReturnsFalse(t *testing.T) {
	registry := newSecretAwareRegistry()

	got := registry.HasSecretInput("unknown")

	if got {
		t.Error(`HasSecretInput("unknown") = true, want false`)
	}
}

// privateStubCommand は、返信を常に非公開にする目印（PrivateReply）を持つ stubCommand。
type privateStubCommand struct {
	stubCommand
}

func (privateStubCommand) RepliesPrivately() {}

// repliesPrivatelyByName は、名前ごとの RepliesPrivately の答えを集める。
func repliesPrivatelyByName(registry *command.Registry, names []string) map[string]bool {
	got := make(map[string]bool, len(names))
	for _, name := range names {
		got[name] = registry.RepliesPrivately(name)
	}
	return got
}

// 条件: 返信を非公開にするコマンド、秘密入力のコマンド、どちらでもないコマンドを登録し、未登録の名前も問い合わせる
// 内容: 返信を非公開にするコマンドと秘密入力のコマンドだけが真になる
func TestRepliesPrivately_EachKindOfCommand_TrueOnlyForPrivateOrSecretInput(t *testing.T) {
	registry := command.NewRegistry(
		privateStubCommand{stubCommand{name: "record-start"}},
		secretStubCommand{stubCommand{name: "summarize-voice"}},
		stubCommand{name: "ask"},
	)
	names := []string{"record-start", "summarize-voice", "ask", "unknown"}

	got := repliesPrivatelyByName(registry, names)

	want := map[string]bool{"record-start": true, "summarize-voice": true, "ask": false, "unknown": false}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RepliesPrivately = %v, want %v", got, want)
	}
}
