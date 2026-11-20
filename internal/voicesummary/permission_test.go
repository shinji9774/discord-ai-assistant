package voicesummary_test

import (
	"testing"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/voicesummary"
)

// チャンネルの種類ごとに、投稿に要る権限（design「missingPostPermissions」）。
const (
	textPostPermissions   = discord.PermissionViewChannel | discord.PermissionSendMessages | discord.PermissionAttachFiles
	threadPostPermissions = discord.PermissionViewChannel | discord.PermissionSendMessagesInThreads | discord.PermissionAttachFiles
	voicePostPermissions  = textPostPermissions | discord.PermissionConnect
)

// 条件: チャンネルの種類（テキスト、お知らせ、3 種のスレッド、ボイス、ステージ）ごとに、要る権限が揃う場合と 1 つ欠ける場合。
// 内容: 揃えば false、欠ければ true。スレッドでは SendMessages ではなく SendMessagesInThreads を、ボイスとステージでは Connect も求める。
func TestMissingPostPermissions_ByChannelType_ReportsMissing(t *testing.T) {
	tests := []struct {
		name        string
		channelType discord.ChannelType
		perms       discord.Permissions
		want        bool
	}{
		{"text with all", discord.ChannelTypeGuildText, textPostPermissions, false},
		{"text without view channel", discord.ChannelTypeGuildText, textPostPermissions.Remove(discord.PermissionViewChannel), true},
		{"text without attach files", discord.ChannelTypeGuildText, textPostPermissions.Remove(discord.PermissionAttachFiles), true},
		{"news with all", discord.ChannelTypeGuildNews, textPostPermissions, false},
		{"news without send messages", discord.ChannelTypeGuildNews, textPostPermissions.Remove(discord.PermissionSendMessages), true},
		{"public thread with all", discord.ChannelTypeGuildPublicThread, threadPostPermissions, false},
		{"public thread with send messages only", discord.ChannelTypeGuildPublicThread, textPostPermissions, true},
		{"private thread with all", discord.ChannelTypeGuildPrivateThread, threadPostPermissions, false},
		{"private thread without send in threads", discord.ChannelTypeGuildPrivateThread, threadPostPermissions.Remove(discord.PermissionSendMessagesInThreads), true},
		{"news thread with all", discord.ChannelTypeGuildNewsThread, threadPostPermissions, false},
		{"news thread without attach files", discord.ChannelTypeGuildNewsThread, threadPostPermissions.Remove(discord.PermissionAttachFiles), true},
		{"voice with all", discord.ChannelTypeGuildVoice, voicePostPermissions, false},
		{"voice without connect", discord.ChannelTypeGuildVoice, textPostPermissions, true},
		{"stage with all", discord.ChannelTypeGuildStageVoice, voicePostPermissions, false},
		{"stage without connect", discord.ChannelTypeGuildStageVoice, textPostPermissions, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := voicesummary.MissingPostPermissions(tt.channelType, tt.perms)

			if got != tt.want {
				t.Errorf("MissingPostPermissions(%d, %s) = %v, want %v", tt.channelType, tt.perms, got, tt.want)
			}
		})
	}
}
