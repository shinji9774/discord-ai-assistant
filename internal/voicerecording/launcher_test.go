package voicerecording_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"discord-ai-assistant/test/ecsfake"
)

// 条件: ECS が起動を受け付ける
// 内容: Fargate のタスクを 1 つ、パブリック IP つきで起動し、録音のサーバと依頼 ID を環境変数で渡す
func TestECSLauncherLaunch_Accepted_RunsOneFargateTaskWithSessionIDs(t *testing.T) {
	launcher, fake := newECSLauncher(t)

	err := launcher.Launch(context.Background(), testSession(testRequestID, testAcceptedAt))

	want := []ecsfake.RunTaskRequest{{
		Cluster: testClusterARN, TaskDefinition: testTaskDefinitionARN, LaunchType: "FARGATE", Count: 1,
		Subnets: testSubnetIDs, SecurityGroups: []string{testSecurityGroupID}, AssignPublicIP: "ENABLED",
		Overrides: []ecsfake.ContainerOverride{{
			Name:        "recorder",
			Environment: map[string]string{"GUILD_ID": testGuildID, "REQUEST_ID": testRequestID},
		}},
	}}
	if err != nil || !reflect.DeepEqual(fake.Requests(), want) {
		t.Errorf("Launch = %v, requests %+v; want nil, %+v", err, fake.Requests(), want)
	}
}

// 条件: ECS が配置の失敗を返す（HTTP は成功）
// 内容: エラーを返す（StartCommand がロックを外せるように）
func TestECSLauncherLaunch_PlacementRejected_ReturnsError(t *testing.T) {
	launcher, fake := newECSLauncher(t)
	fake.RejectPlacement()

	err := launcher.Launch(context.Background(), testSession(testRequestID, testAcceptedAt))

	if err == nil {
		t.Error("Launch = nil, want error")
	}
}

// 条件: ECS の API がエラーを返す
// 内容: エラーを返す
func TestECSLauncherLaunch_ServiceError_ReturnsError(t *testing.T) {
	launcher, fake := newECSLauncher(t)
	fake.FailWith(http.StatusInternalServerError)

	err := launcher.Launch(context.Background(), testSession(testRequestID, testAcceptedAt))

	if err == nil {
		t.Error("Launch = nil, want error")
	}
}
