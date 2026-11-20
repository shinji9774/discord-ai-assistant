package voicesummary

import "github.com/disgoorg/disgo/discord"

// basePostPermissions は、チャンネルの種類によらず投稿に要る権限。文字起こしをファイルで添付するため AttachFiles も要る。
const basePostPermissions = discord.PermissionViewChannel | discord.PermissionAttachFiles

// missingPostPermissions は、投稿に足りない権限があれば true を返す。
func missingPostPermissions(channelType discord.ChannelType, perms discord.Permissions) bool {
	return !perms.Has(requiredPostPermissions(channelType))
}

func requiredPostPermissions(channelType discord.ChannelType) discord.Permissions {
	required := basePostPermissions | sendPermission(channelType)
	if isVoiceChannel(channelType) {
		required |= discord.PermissionConnect
	}
	return required
}

// sendPermission は、スレッドとそれ以外とで違う、メッセージを送る権限を返す。
func sendPermission(channelType discord.ChannelType) discord.Permissions {
	switch channelType {
	case discord.ChannelTypeGuildPublicThread, discord.ChannelTypeGuildPrivateThread, discord.ChannelTypeGuildNewsThread:
		return discord.PermissionSendMessagesInThreads
	default:
		return discord.PermissionSendMessages
	}
}

// isVoiceChannel は、ボイスチャンネルの中のテキスト欄（ボイスとステージ）かを返す。そこでは Connect も要る。
func isVoiceChannel(channelType discord.ChannelType) bool {
	return channelType == discord.ChannelTypeGuildVoice || channelType == discord.ChannelTypeGuildStageVoice
}
