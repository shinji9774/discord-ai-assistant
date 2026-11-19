package register_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"discord-ai-assistant/internal/catalog"
	"discord-ai-assistant/internal/config/configtest"
	"discord-ai-assistant/internal/register"
	"discord-ai-assistant/test/discordfake"
)

// disgo は snowflake を要求するため、数字だけの application id を使う。
const testApplicationID = "123456789012345678"

func newConfig(t *testing.T) (register.Config, *discordfake.Server) {
	t.Helper()
	fake := discordfake.New()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return register.Config{ApplicationID: testApplicationID, BotToken: "token1", APIBaseURL: server.URL}, fake
}

// 条件: ApplicationID と BotToken が揃っている。
// 内容: 全コマンドの定義が、bot トークン付きで 1 回の一括登録として送られる。
func TestRun_Success_ReplacesGlobalCommandsWithAllDefinitions(t *testing.T) {
	cfg, fake := newConfig(t)

	err := register.Run(context.Background(), cfg, catalog.Definitions(configtest.Load(t)), &bytes.Buffer{})

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := []discordfake.Registration{{ApplicationID: testApplicationID, Authorization: "Bot token1", CommandNames: []string{"translate", "ask", "image", "help"}}}
	if got := fake.Registrations(); !reflect.DeepEqual(got, want) {
		t.Errorf("Registrations() = %+v, want %+v", got, want)
	}
}

// 条件: ApplicationID と BotToken が揃っている。
// 内容: 登録したコマンド名が 1 行ずつ stdout に書かれる。
func TestRun_Success_PrintsRegisteredCommandNames(t *testing.T) {
	cfg, _ := newConfig(t)
	var stdout bytes.Buffer

	err := register.Run(context.Background(), cfg, catalog.Definitions(configtest.Load(t)), &stdout)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := stdout.String(); got != "translate\nask\nimage\nhelp\n" {
		t.Errorf("stdout = %q, want %q", got, "translate\nask\nimage\nhelp\n")
	}
}

// 条件: Discord が 401 を返す。
// 内容: エラーが返り、エラー文に bot トークンが含まれない。
func TestRun_DiscordFails_ReturnsErrorWithoutToken(t *testing.T) {
	cfg, fake := newConfig(t)
	fake.FailWith(401)

	err := register.Run(context.Background(), cfg, catalog.Definitions(configtest.Load(t)), &bytes.Buffer{})

	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
	if strings.Contains(err.Error(), "token1") {
		t.Errorf("error %q contains the bot token", err.Error())
	}
}

// 条件: BotToken が空。
// 内容: 環境変数名を含むエラーが返る。
func TestRun_MissingBotToken_ReturnsError(t *testing.T) {
	cfg, _ := newConfig(t)
	cfg.BotToken = ""

	err := register.Run(context.Background(), cfg, catalog.Definitions(configtest.Load(t)), &bytes.Buffer{})

	if err == nil || err.Error() != "DISCORD_BOT_TOKEN is not set" {
		t.Errorf("Run() error = %v, want %q", err, "DISCORD_BOT_TOKEN is not set")
	}
}

// 条件: ApplicationID が空。
// 内容: 環境変数名を含むエラーが返る。
func TestRun_MissingApplicationID_ReturnsError(t *testing.T) {
	cfg, _ := newConfig(t)
	cfg.ApplicationID = ""

	err := register.Run(context.Background(), cfg, catalog.Definitions(configtest.Load(t)), &bytes.Buffer{})

	if err == nil || err.Error() != "DISCORD_APPLICATION_ID is not set" {
		t.Errorf("Run() error = %v, want %q", err, "DISCORD_APPLICATION_ID is not set")
	}
}
