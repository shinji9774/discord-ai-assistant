package voicerecording_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/voicerecording"
	"discord-ai-assistant/internal/voicesummary"
	"discord-ai-assistant/test/discordfake"
	"discord-ai-assistant/test/ecsfake"
	"discord-ai-assistant/test/s3fake"
)

const (
	testBucket         = "voice-bucket"
	testRegion         = "ap-northeast-1"
	testGuildID        = "1100000000000000001"
	testVoiceChannelID = "1200000000000000002"
	testTextChannelID  = "1300000000000000003"
	testStarterID      = "1400000000000000004"
	testBotUserID      = "1600000000000000006"
	testRequestID      = "req-1"
	// testSessionLength は、フィクスチャの受付から期限までの長さ。
	testSessionLength = 4 * time.Hour
)

var testAcceptedAt = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

// newS3Client は srv に向けた S3 クライアントを、本番と同じ ConfigureS3Client で作る。
func newS3Client(srv *httptest.Server) *s3.Client {
	cfg := aws.Config{
		Region:           testRegion,
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		BaseEndpoint:     aws.String(srv.URL),
		RetryMaxAttempts: 1,
	}
	return s3.NewFromConfig(cfg, voicesummary.ConfigureS3Client)
}

func newS3Fake(t *testing.T) (*s3.Client, *s3fake.Server) {
	t.Helper()
	fake := s3fake.New()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return newS3Client(srv), fake
}

func newSessionStore(t *testing.T) (*voicerecording.SessionStore, *s3fake.Server) {
	t.Helper()
	client, fake := newS3Fake(t)
	return voicerecording.NewSessionStore(client, testBucket), fake
}

// testSession は、testAcceptedAt からの相対で受付時刻と期限を決めた録音。
func testSession(requestID string, acceptedAt time.Time) voicerecording.Session {
	return voicerecording.Session{
		RequestID: requestID, GuildID: testGuildID, VoiceChannelID: testVoiceChannelID, TextChannelID: testTextChannelID,
		StarterID: testStarterID, BotUserID: testBotUserID, AcceptedAt: acceptedAt, Deadline: acceptedAt.Add(testSessionLength),
	}
}

// mustDo は、準備の段階の失敗でテストを止める。
func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("arrange: %v", err)
	}
}

const (
	testClusterARN        = "arn:aws:ecs:ap-northeast-1:123456789012:cluster/recorder"
	testTaskDefinitionARN = "arn:aws:ecs:ap-northeast-1:123456789012:task-definition/recorder:1"
	testSecurityGroupID   = "sg-0123456789abcdef0"
)

var testSubnetIDs = []string{"subnet-0aaaaaaaaaaaaaaa1", "subnet-0bbbbbbbbbbbbbbb2"}

// newECSLauncher は、ecsfake に向けた ECSLauncher を作る。
func newECSLauncher(t *testing.T) (*voicerecording.ECSLauncher, *ecsfake.Server) {
	t.Helper()
	fake := ecsfake.New()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	client := ecs.New(ecs.Options{
		Region:           testRegion,
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		BaseEndpoint:     aws.String(srv.URL),
		RetryMaxAttempts: 1,
	})
	target := voicerecording.LaunchTarget{
		ClusterARN: testClusterARN, TaskDefinitionARN: testTaskDefinitionARN,
		SubnetIDs: testSubnetIDs, SecurityGroupID: testSecurityGroupID,
	}
	return voicerecording.NewECSLauncher(client, target), fake
}

// textPostPermissions は、テキストチャンネルへ投稿するのに要る権限（voicesummary の投稿の条件）。
const textPostPermissions = discord.PermissionViewChannel | discord.PermissionSendMessages | discord.PermissionAttachFiles

// recordingFixture は、3 つのコマンドの依存を fake につないだもの。
type recordingFixture struct {
	deps    voicerecording.Dependencies
	store   *voicerecording.SessionStore
	discord *discordfake.Server
	ecs     *ecsfake.Server
}

func newRecordingFixture(t *testing.T) recordingFixture {
	t.Helper()
	store, _ := newSessionStore(t)
	guild, discordFake := newDiscordGuild(t)
	launcher, ecsFake := newECSLauncher(t)
	deps := voicerecording.Dependencies{
		Sessions: store, Guild: guild, Launcher: launcher,
		Now: func() time.Time { return testAcceptedAt },
	}
	return recordingFixture{deps: deps, store: store, discord: discordFake, ecs: ecsFake}
}

// commandInvocation は、テキストチャンネルで userID が name のコマンドを実行した呼び出し。
// bot のユーザー ID は、アプリケーション ID と同じ値になる（Discord の bot の決まり）。
func commandInvocation(name, userID string) command.Invocation {
	return command.Invocation{
		ApplicationID: testBotUserID, InteractionToken: "token", ChannelID: testTextChannelID, CommandName: name,
		AppPermissions: textPostPermissions, InteractionID: testRequestID, GuildID: testGuildID, UserID: userID,
		ChannelType: discord.ChannelTypeGuildText,
	}
}

// allowRecording は、開始者がボイスチャンネルにいて、bot がそのチャンネルを見て接続できる状態にする。
func allowRecording(fake *discordfake.Server) {
	addVoiceState(fake, testStarterID, testVoiceChannelID)
	addPermissionFixtures(fake, discord.PermissionViewChannel|discord.PermissionConnect, 0, discord.ChannelTypeGuildVoice, testVoiceChannelID)
}

// activeLock は、サーバのロックを返す。無ければ nil。
func (f recordingFixture) activeLock(t *testing.T) *voicerecording.Session {
	t.Helper()
	session, err := f.store.Active(context.Background(), testGuildID)
	if errors.Is(err, voicerecording.ErrNoRecording) {
		return nil
	}
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	return &session
}

// launchedEnvironments は、起動した録音タスクごとの環境変数の上書き。
func (f recordingFixture) launchedEnvironments() []map[string]string {
	var envs []map[string]string
	for _, request := range f.ecs.Requests() {
		for _, override := range request.Overrides {
			envs = append(envs, override.Environment)
		}
	}
	return envs
}

// definitionShape は、コマンドの定義のうち、仕様で決まっている部分。説明の文は比べない。
type definitionShape struct {
	Name     string
	Options  int
	Contexts []discord.InteractionContextType
}

func shapeOf(d discord.SlashCommandCreate) definitionShape {
	return definitionShape{Name: d.Name, Options: len(d.Options), Contexts: d.Contexts}
}
