package command

import "github.com/disgoorg/disgo/discord"

const (
	VisibilityOptionName = "visibility"
	VisibilityPublic     = "public"
	VisibilityPrivate    = "private"
)

// VisibilityOption は「全員」「実行者だけ」の 2 つから選ぶ任意のオプションを返す。
func VisibilityOption() discord.ApplicationCommandOptionString {
	return discord.ApplicationCommandOptionString{
		Name:        VisibilityOptionName,
		Description: "結果を見られる人（省略時は全員）",
		Choices: []discord.ApplicationCommandOptionChoiceString{
			{Name: "全員", Value: VisibilityPublic},
			{Name: "実行者だけ", Value: VisibilityPrivate},
		},
	}
}

// IsPrivate は、オプションが「実行者だけ」を選んでいるかを返す。未指定は false。
func IsPrivate(options map[string]string) bool {
	return options[VisibilityOptionName] == VisibilityPrivate
}
