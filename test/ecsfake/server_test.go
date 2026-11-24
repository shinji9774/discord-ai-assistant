package ecsfake_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"discord-ai-assistant/test/ecsfake"
)

const (
	cluster        = "arn:aws:ecs:ap-northeast-1:123456789012:cluster/recorder"
	taskDefinition = "arn:aws:ecs:ap-northeast-1:123456789012:task-definition/recorder:1"
	subnet         = "subnet-0aaaaaaaaaaaaaaa1"
	securityGroup  = "sg-0123456789abcdef0"
	containerName  = "recorder"
)

// runResult は、RunTask の応答のうち、テストが確かめる部分。
type runResult struct {
	Tasks    int
	Failures int
	Failed   bool
}

func newClient(t *testing.T) (*ecs.Client, *ecsfake.Server) {
	t.Helper()
	fake := ecsfake.New()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	client := ecs.New(ecs.Options{
		Region:           "ap-northeast-1",
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		BaseEndpoint:     aws.String(srv.URL),
		RetryMaxAttempts: 1,
	})
	return client, fake
}

// runTask は、録音タスクと同じ形の RunTask を送る。
func runTask(client *ecs.Client) runResult {
	out, err := client.RunTask(context.Background(), &ecs.RunTaskInput{
		Cluster:        aws.String(cluster),
		TaskDefinition: aws.String(taskDefinition),
		LaunchType:     types.LaunchTypeFargate,
		Count:          aws.Int32(1),
		NetworkConfiguration: &types.NetworkConfiguration{AwsvpcConfiguration: &types.AwsVpcConfiguration{
			Subnets: []string{subnet}, SecurityGroups: []string{securityGroup}, AssignPublicIp: types.AssignPublicIpEnabled,
		}},
		Overrides: &types.TaskOverride{ContainerOverrides: []types.ContainerOverride{{
			Name:        aws.String(containerName),
			Environment: []types.KeyValuePair{{Name: aws.String("GUILD_ID"), Value: aws.String("g1")}},
		}}},
	})
	if err != nil {
		return runResult{Failed: true}
	}
	return runResult{Tasks: len(out.Tasks), Failures: len(out.Failures)}
}

// 条件: 何も設定していない fake に RunTask を送る
// 内容: タスクを 1 つ返し、受けた依頼を SDK の入力どおりに記録する
func TestServerRunTask_Default_StartsOneTaskAndRecordsRequest(t *testing.T) {
	client, fake := newClient(t)

	got := runTask(client)

	want := runResult{Tasks: 1, Failures: 0}
	wantRequests := []ecsfake.RunTaskRequest{{
		Cluster: cluster, TaskDefinition: taskDefinition, LaunchType: "FARGATE", Count: 1,
		Subnets: []string{subnet}, SecurityGroups: []string{securityGroup}, AssignPublicIP: "ENABLED",
		Overrides: []ecsfake.ContainerOverride{{Name: containerName, Environment: map[string]string{"GUILD_ID": "g1"}}},
	}}
	if got != want || !reflect.DeepEqual(fake.Requests(), wantRequests) {
		t.Errorf("RunTask = %+v, requests %+v; want %+v, %+v", got, fake.Requests(), want, wantRequests)
	}
}

// 条件: RejectPlacement のあとに RunTask を送る
// 内容: タスクを返さず、配置の失敗を 1 つ返す（ECS が容量不足などで起動を断った応答）
func TestServerRunTask_RejectPlacement_ReturnsFailureWithoutTask(t *testing.T) {
	client, fake := newClient(t)
	fake.RejectPlacement()

	got := runTask(client)

	if want := (runResult{Tasks: 0, Failures: 1}); got != want {
		t.Errorf("RunTask = %+v, want %+v", got, want)
	}
}

// 条件: FailWith(500) のあとに RunTask を送る
// 内容: SDK がエラーを返す
func TestServerRunTask_FailWith_ReturnsError(t *testing.T) {
	client, fake := newClient(t)
	fake.FailWith(http.StatusInternalServerError)

	got := runTask(client)

	if want := (runResult{Failed: true}); got != want {
		t.Errorf("RunTask = %+v, want %+v", got, want)
	}
}

// 条件: 失敗の設定と依頼の記録があるときに Reset する
// 内容: 記録が消え、次の RunTask は成功する
func TestServerReset_AfterFailure_ForgetsRequestsAndFailure(t *testing.T) {
	client, fake := newClient(t)
	fake.FailWith(http.StatusInternalServerError)
	runTask(client)

	fake.Reset()

	got := runTask(client)
	if want := (runResult{Tasks: 1, Failures: 0}); got != want || len(fake.Requests()) != 1 {
		t.Errorf("after Reset: RunTask = %+v, requests %d; want %+v, 1", got, len(fake.Requests()), want)
	}
}
