package voicerecording_test

import (
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"discord-ai-assistant/internal/voicerecording"
)

const (
	permGuildID snowflake.ID = 1100000000000000001
	permBotID   snowflake.ID = 1600000000000000006
	permOwnerID snowflake.ID = 1900000000000000009
	permRoleID  snowflake.ID = 1800000000000000008
	// joinable は、録音に参加するのに要る権限（設計書: VIEW_CHANNEL と CONNECT）。
	joinable = discord.PermissionViewChannel | discord.PermissionConnect
)

type permissionCase struct {
	name       string
	ownerID    snowflake.ID
	everyone   discord.Permissions
	role       discord.Permissions
	overwrites discord.PermissionOverwrites
	want       discord.Permissions
}

func everyoneOverwrite(allow, deny discord.Permissions) discord.PermissionOverwrite {
	return discord.RolePermissionOverwrite{RoleID: permGuildID, Allow: allow, Deny: deny}
}

// 条件: サーバの所有者・ロール・チャンネルの上書きの組み合わせ
// 内容: Discord の規則（所有者と管理者は全権限、@everyone → ロール → @everyone の上書き → ロールの上書き → メンバーの上書き）どおりに、参加に要る権限の有無が決まる
func TestChannelPermissions_RolesAndOverwrites_FollowsDiscordPrecedence(t *testing.T) {
	cases := []permissionCase{
		{name: "所有者は上書きで拒まれても全権限", ownerID: permBotID, overwrites: discord.PermissionOverwrites{everyoneOverwrite(0, joinable)}, want: joinable},
		{name: "管理者のロールは上書きで拒まれても全権限", role: discord.PermissionAdministrator, overwrites: discord.PermissionOverwrites{everyoneOverwrite(0, joinable)}, want: joinable},
		{name: "@everyone の上書きの拒否で参加できない", everyone: joinable, overwrites: discord.PermissionOverwrites{everyoneOverwrite(0, discord.PermissionConnect)}, want: discord.PermissionViewChannel},
		{name: "ロールの上書きの許可は @everyone の上書きの拒否に勝つ", everyone: joinable, overwrites: discord.PermissionOverwrites{
			everyoneOverwrite(0, discord.PermissionConnect),
			discord.RolePermissionOverwrite{RoleID: permRoleID, Allow: discord.PermissionConnect},
		}, want: joinable},
		{name: "メンバーの上書きの拒否はロールの上書きの許可に勝つ", everyone: joinable, overwrites: discord.PermissionOverwrites{
			discord.RolePermissionOverwrite{RoleID: permRoleID, Allow: discord.PermissionConnect},
			discord.MemberPermissionOverwrite{UserID: permBotID, Deny: discord.PermissionConnect},
		}, want: discord.PermissionViewChannel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			guild := discord.RestGuild{
				Guild: discord.Guild{ID: permGuildID, OwnerID: cmpOr(tc.ownerID, permOwnerID)},
				Roles: []discord.Role{{ID: permGuildID, Permissions: tc.everyone}, {ID: permRoleID, Permissions: tc.role}},
			}
			member := discord.Member{User: discord.User{ID: permBotID}, RoleIDs: []snowflake.ID{permRoleID}}

			got := voicerecording.ChannelPermissions(guild, member, tc.overwrites)

			if got&joinable != tc.want {
				t.Errorf("permissions & joinable = %d, want %d", got&joinable, tc.want)
			}
		})
	}
}

func cmpOr(id, fallback snowflake.ID) snowflake.ID {
	if id != 0 {
		return id
	}
	return fallback
}
