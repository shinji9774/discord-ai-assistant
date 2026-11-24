package voicerecording

import (
	"fmt"
	"slices"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// allPermissions は、サーバの所有者と管理者が持つ、すべての権限。
const allPermissions = ^discord.Permissions(0)

// grant は、上書き 1 段分の許可と拒否。
type grant struct {
	allow discord.Permissions
	deny  discord.Permissions
}

func (g grant) applyTo(p discord.Permissions) discord.Permissions {
	return p&^g.deny | g.allow
}

// channelPermissions は、チャンネルでのメンバーの権限を Discord の規則で計算する。
// 所有者と管理者は全権限。それ以外は @everyone とロールの和に、@everyone の上書き、ロールの上書き、メンバーの上書きの順で重ねる。
func channelPermissions(guild discord.RestGuild, member discord.Member, overwrites discord.PermissionOverwrites) discord.Permissions {
	if member.User.ID == guild.OwnerID {
		return allPermissions
	}
	perms := rolePermissions(guild, member.RoleIDs)
	if perms.Has(discord.PermissionAdministrator) {
		return allPermissions
	}
	everyone, roles, own := overwriteGrants(guild.ID, member, overwrites)
	return own.applyTo(roles.applyTo(everyone.applyTo(perms)))
}

// rolePermissions は、@everyone（ID がサーバの ID のロール）とメンバーのロールの権限の和を返す。
func rolePermissions(guild discord.RestGuild, roleIDs []snowflake.ID) discord.Permissions {
	var perms discord.Permissions
	for _, role := range guild.Roles {
		if role.ID == guild.ID || slices.Contains(roleIDs, role.ID) {
			perms |= role.Permissions
		}
	}
	return perms
}

// overwriteGrants は、チャンネルの上書きを @everyone・メンバーのロール・メンバー自身の 3 段に分けて集める。
func overwriteGrants(guildID snowflake.ID, member discord.Member, overwrites discord.PermissionOverwrites) (everyone, roles, own grant) {
	for _, overwrite := range overwrites {
		switch o := overwrite.(type) {
		case discord.RolePermissionOverwrite:
			if o.RoleID == guildID {
				everyone = grant{allow: o.Allow, deny: o.Deny}
			} else if slices.Contains(member.RoleIDs, o.RoleID) {
				roles = grant{allow: roles.allow | o.Allow, deny: roles.deny | o.Deny}
			}
		case discord.MemberPermissionOverwrite:
			if o.UserID == member.User.ID {
				own = grant{allow: o.Allow, deny: o.Deny}
			}
		}
	}
	return everyone, roles, own
}

// voiceChannelPermissions は、bot がボイスチャンネルに参加して音声を受けるのに要る権限。
const voiceChannelPermissions = discord.PermissionViewChannel | discord.PermissionConnect

// 不足している権限の案内の組み立て。例: 「このチャンネル: ファイルを添付／ボイスチャンネル: 接続」
const (
	thisChannelLabel  = "このチャンネル"
	voiceChannelLabel = "ボイスチャンネル"
	placeFormat       = "%s: %s"
	placeSeparator    = "／"
	nameSeparator     = "、"
)

// permissionLabels は、案内に出す権限の名前。並びは案内に出す順。
var permissionLabels = []struct {
	permission discord.Permissions
	label      string
}{
	{discord.PermissionViewChannel, "チャンネルを見る"},
	{discord.PermissionSendMessages, "メッセージを送信"},
	{discord.PermissionSendMessagesInThreads, "スレッドでメッセージを送信"},
	{discord.PermissionAttachFiles, "ファイルを添付"},
	{discord.PermissionConnect, "接続"},
}

// permissionPlace は、権限を確かめる場所と、そこで要る権限と bot が持つ権限。
type permissionPlace struct {
	label    string
	required discord.Permissions
	granted  discord.Permissions
}

// describeMissing は、不足している権限を場所ごとに並べた文を返す。不足が無ければ空文字。
func describeMissing(places []permissionPlace) string {
	var described []string
	for _, place := range places {
		names := missingNames(place.required &^ place.granted)
		if len(names) == 0 {
			continue
		}
		described = append(described, fmt.Sprintf(placeFormat, place.label, strings.Join(names, nameSeparator)))
	}
	return strings.Join(described, placeSeparator)
}

func missingNames(missing discord.Permissions) []string {
	var names []string
	for _, p := range permissionLabels {
		if missing.Has(p.permission) {
			names = append(names, p.label)
		}
	}
	return names
}
