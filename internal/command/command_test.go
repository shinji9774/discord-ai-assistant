package command_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
)

// 条件: bot と実行者の権限を持つ Invocation を JSON にして読み戻す。
// 内容: 権限を含め、元と同じ Invocation になる（ingress から worker への受け渡し）。
func TestInvocation_JSONRoundTrip_KeepsPermissions(t *testing.T) {
	want := command.Invocation{
		ApplicationID:     "app1",
		InteractionToken:  "tok1",
		ChannelID:         "ch1",
		CommandName:       "summarize",
		Options:           map[string]string{},
		AppPermissions:    discord.PermissionViewChannel | discord.PermissionReadMessageHistory,
		MemberPermissions: discord.PermissionReadMessageHistory,
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got command.Invocation

	err = json.Unmarshal(encoded, &got)

	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

// 条件: 依頼 ID、サーバ、実行者、チャンネルの種類（ボイスチャンネル）を持つ Invocation を JSON にして読み戻す。
// 内容: 4 項目を含め、元と同じ Invocation になる（ingress から worker への受け渡し）。
func TestInvocation_JSONRoundTrip_KeepsRequestContext(t *testing.T) {
	want := command.Invocation{
		ApplicationID:    "app1",
		InteractionToken: "tok1",
		ChannelID:        "ch1",
		CommandName:      "summarize-voice",
		Options:          map[string]string{},
		InteractionID:    "1400000000000000001",
		GuildID:          "1500000000000000001",
		UserID:           "1600000000000000001",
		ChannelType:      discord.ChannelTypeGuildVoice,
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got command.Invocation

	err = json.Unmarshal(encoded, &got)

	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}
