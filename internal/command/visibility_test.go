package command_test

import (
	"reflect"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
)

// 条件: オプションが未指定、「全員」、「実行者だけ」の 3 通り。
// 内容: 「実行者だけ」のときだけ true になる。
func TestIsPrivate_VisibilityValue_TrueOnlyForPrivate(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]string
		want    bool
	}{
		{"未指定", map[string]string{}, false},
		{"全員", map[string]string{command.VisibilityOptionName: command.VisibilityPublic}, false},
		{"実行者だけ", map[string]string{command.VisibilityOptionName: command.VisibilityPrivate}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := command.IsPrivate(tt.options)

			if got != tt.want {
				t.Errorf("IsPrivate(%v) = %v, want %v", tt.options, got, tt.want)
			}
		})
	}
}

// 条件: オプション定義を取得する。
// 内容: 名前が visibility で必須でなく、選択肢は「全員」「実行者だけ」の 2 つだけである。
func TestVisibilityOption_Definition_OptionalWithPublicAndPrivateChoices(t *testing.T) {
	want := []discord.ApplicationCommandOptionChoiceString{
		{Name: "全員", Value: command.VisibilityPublic},
		{Name: "実行者だけ", Value: command.VisibilityPrivate},
	}

	got := command.VisibilityOption()

	if got.Name != command.VisibilityOptionName || got.Required {
		t.Errorf("VisibilityOption() name=%q required=%v, want name=%q required=false", got.Name, got.Required, command.VisibilityOptionName)
	}
	if !reflect.DeepEqual(got.Choices, want) {
		t.Errorf("VisibilityOption().Choices = %v, want %v", got.Choices, want)
	}
}
