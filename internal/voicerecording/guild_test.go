package voicerecording_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/voicerecording"
	"discord-ai-assistant/test/discordfake"
)

const (
	testBotToken = "test-bot-token"
	// Discord のエラーコード（Task 3 の実機確認で確かめた値）。
	codeUnknownVoiceState = 10065
	codeMissingAccess     = 50001
)

func newDiscordGuild(t *testing.T) (*voicerecording.DiscordGuild, *discordfake.Server) {
	t.Helper()
	fake := discordfake.New()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return voicerecording.NewDiscordGuild(testBotToken, srv.URL), fake
}

func permissionString(p discord.Permissions) string {
	return strconv.FormatInt(int64(p), 10)
}

// addMember は、GET /guilds/{g}/members/{u} にメンバーを返させる。
func addMember(fake *discordfake.Server, userID, globalName string, nick *string, bot bool) {
	fake.AddJSON("/guilds/"+testGuildID+"/members/"+userID, map[string]any{
		"user":  map[string]any{"id": userID, "username": "user-" + userID, "global_name": globalName, "bot": bot},
		"nick":  nick,
		"roles": []string{},
	})
}

// addVoiceState は、GET /guilds/{g}/voice-states/{u} に、channelID のボイスチャンネルにいる状態を返させる。
func addVoiceState(fake *discordfake.Server, userID, channelID string) {
	fake.AddJSON("/guilds/"+testGuildID+"/voice-states/"+userID, map[string]any{
		"guild_id": testGuildID, "channel_id": channelID, "user_id": userID, "session_id": "session",
		"deaf": false, "mute": false, "self_deaf": false, "self_mute": false, "suppress": false,
	})
}

// addPermissionFixtures は、@everyone のロールが everyonePerms を持つサーバと、
// @everyone の上書きで channelDeny を拒むチャンネル（channelID）と、ロールを持たない bot のメンバーを返させる。
func addPermissionFixtures(fake *discordfake.Server, everyonePerms, channelDeny discord.Permissions, channelType discord.ChannelType, channelID string) {
	fake.AddJSON("/guilds/"+testGuildID, map[string]any{
		"id": testGuildID, "name": "test", "owner_id": "1900000000000000009",
		"roles": []map[string]any{{"id": testGuildID, "name": "@everyone", "permissions": permissionString(everyonePerms)}},
	})
	fake.AddJSON("/channels/"+channelID, map[string]any{
		"id": channelID, "type": int(channelType), "guild_id": testGuildID, "name": "channel",
		"permission_overwrites": []map[string]any{{"id": testGuildID, "type": 0, "allow": "0", "deny": permissionString(channelDeny)}},
	})
	addMember(fake, testBotUserID, "議事録 bot", nil, true)
}

// 条件: 利用者がボイスチャンネルにいる
// 内容: そのボイスチャンネルの ID を返す
func TestDiscordGuildVoiceChannelOf_UserInVoice_ReturnsChannelID(t *testing.T) {
	guild, fake := newDiscordGuild(t)
	addVoiceState(fake, testStarterID, testVoiceChannelID)

	got, err := guild.VoiceChannelOf(context.Background(), testGuildID, testStarterID)

	if err != nil || got != testVoiceChannelID {
		t.Errorf("VoiceChannelOf = %q, %v; want %q, nil", got, err, testVoiceChannelID)
	}
}

// 条件: 利用者がボイスチャンネルにいない（Discord が Unknown Voice State で拒む）
// 内容: ErrNotInVoice を返す
func TestDiscordGuildVoiceChannelOf_UnknownVoiceState_ReturnsErrNotInVoice(t *testing.T) {
	guild, fake := newDiscordGuild(t)
	fake.RejectPath(http.MethodGet, "/guilds/"+testGuildID+"/voice-states/"+testStarterID, http.StatusNotFound, codeUnknownVoiceState)

	_, err := guild.VoiceChannelOf(context.Background(), testGuildID, testStarterID)

	if !errors.Is(err, voicerecording.ErrNotInVoice) {
		t.Errorf("VoiceChannelOf error = %v, want ErrNotInVoice", err)
	}
}

// 条件: @everyone は閲覧と接続を持つが、ボイスチャンネルの上書きで接続を拒まれている
// 内容: 閲覧だけを返す
func TestDiscordGuildBotPermissionsIn_ConnectDeniedByOverwrite_ReturnsViewOnly(t *testing.T) {
	guild, fake := newDiscordGuild(t)
	addPermissionFixtures(fake, joinable, discord.PermissionConnect, discord.ChannelTypeGuildVoice, testVoiceChannelID)

	got, err := guild.BotPermissionsIn(context.Background(), testGuildID, testVoiceChannelID, testBotUserID)

	if err != nil || got&joinable != discord.PermissionViewChannel {
		t.Errorf("BotPermissionsIn = %d, %v; want view only", got, err)
	}
}

// 条件: bot がチャンネルを見られない（Discord が Missing Access で拒む）
// 内容: 権限なし（0）として返し、エラーにしない
func TestDiscordGuildBotPermissionsIn_ChannelHidden_ReturnsNoPermissions(t *testing.T) {
	guild, fake := newDiscordGuild(t)
	fake.RejectPath(http.MethodGet, "/channels/"+testVoiceChannelID, http.StatusForbidden, codeMissingAccess)

	got, err := guild.BotPermissionsIn(context.Background(), testGuildID, testVoiceChannelID, testBotUserID)

	if err != nil || got != 0 {
		t.Errorf("BotPermissionsIn = %d, %v; want 0, nil", got, err)
	}
}

// 条件: bot に表示名が付いているサーバと、付いていないサーバ
// 内容: 付いていれば表示名、付いていなければ空文字を返す
func TestDiscordGuildBotNickname_WithAndWithoutNick_ReturnsNickOrEmpty(t *testing.T) {
	nick := "議事録 bot"
	cases := []struct {
		name string
		nick *string
		want string
	}{
		{name: "表示名あり", nick: &nick, want: nick},
		{name: "表示名なし", nick: nil, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			guild, fake := newDiscordGuild(t)
			addMember(fake, testBotUserID, "bot", tc.nick, true)

			got, err := guild.BotNickname(context.Background(), testGuildID, testBotUserID)

			if err != nil || got != tc.want {
				t.Errorf("BotNickname = %q, %v; want %q, nil", got, err, tc.want)
			}
		})
	}
}

// 条件: bot の表示名を設定し、次に空文字で消す
// 内容: Discord に 2 回の変更が順に届く
func TestDiscordGuildSetBotNickname_SetThenClear_SendsBothChanges(t *testing.T) {
	guild, fake := newDiscordGuild(t)
	mustDo(t, guild.SetBotNickname(context.Background(), testGuildID, "[録音中] 議事録 bot"))

	err := guild.SetBotNickname(context.Background(), testGuildID, "")

	want := []discordfake.NicknameChange{{GuildID: testGuildID, Nick: "[録音中] 議事録 bot"}, {GuildID: testGuildID, Nick: ""}}
	if got := fake.NicknameChanges(); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("changes = %+v, %v; want %+v, nil", got, err, want)
	}
}

// 条件: 話者に global_name がある
// 内容: サーバの表示名ではなく global_name を返す（voicesummary の話者名と同じ規則）
func TestDiscordGuildMemberName_GlobalNameSet_ReturnsGlobalName(t *testing.T) {
	guild, fake := newDiscordGuild(t)
	nick := "サーバでの名前"
	addMember(fake, testStarterID, "佐藤", &nick, false)

	got, err := guild.MemberName(context.Background(), testGuildID, testStarterID)

	if err != nil || got != "佐藤" {
		t.Errorf("MemberName = %q, %v; want %q, nil", got, err, "佐藤")
	}
}
