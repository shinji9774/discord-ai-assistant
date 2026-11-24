package voicerecording

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// 録音タスクに RunTask の上書きで渡す環境変数。cmd/recorder が読む。
const (
	EnvGuildID   = "GUILD_ID"
	EnvRequestID = "REQUEST_ID"
)

const (
	// recorderContainerName は、録音タスクの定義（cfn）にあるコンテナの名前。
	recorderContainerName = "recorder"
	// recorderTaskCount は、1 回の録音で起動するタスクの数。
	recorderTaskCount = 1
)

// LaunchTarget は、録音タスクを起動する先（クラスタ・タスク定義・ネットワーク）。
type LaunchTarget struct {
	ClusterARN        string
	TaskDefinitionARN string
	SubnetIDs         []string
	SecurityGroupID   string
}

// ECSLauncher は、録音タスクを ECS Fargate で起動する。
type ECSLauncher struct {
	client *ecs.Client
	target LaunchTarget
}

func NewECSLauncher(client *ecs.Client, target LaunchTarget) *ECSLauncher {
	return &ECSLauncher{client: client, target: target}
}

// Launch は、録音タスクを 1 つ起動する。ECS が起動を受け付けなければエラーを返す。
// NAT を置かないため、パブリックサブネットでパブリック IP を割り当てる。
func (l *ECSLauncher) Launch(ctx context.Context, session Session) error {
	out, err := l.client.RunTask(ctx, l.runTaskInput(session))
	if err != nil {
		return fmt.Errorf("run recorder task: %w", err)
	}
	if len(out.Failures) > 0 {
		return fmt.Errorf("run recorder task: placement failed: %s", aws.ToString(out.Failures[0].Reason))
	}
	if len(out.Tasks) == 0 {
		return errors.New("run recorder task: no task started")
	}
	return nil
}

func (l *ECSLauncher) runTaskInput(session Session) *ecs.RunTaskInput {
	return &ecs.RunTaskInput{
		Cluster:        aws.String(l.target.ClusterARN),
		TaskDefinition: aws.String(l.target.TaskDefinitionARN),
		LaunchType:     types.LaunchTypeFargate,
		Count:          aws.Int32(recorderTaskCount),
		NetworkConfiguration: &types.NetworkConfiguration{AwsvpcConfiguration: &types.AwsVpcConfiguration{
			Subnets:        l.target.SubnetIDs,
			SecurityGroups: []string{l.target.SecurityGroupID},
			AssignPublicIp: types.AssignPublicIpEnabled,
		}},
		Overrides: &types.TaskOverride{ContainerOverrides: []types.ContainerOverride{{
			Name: aws.String(recorderContainerName),
			Environment: []types.KeyValuePair{
				{Name: aws.String(EnvGuildID), Value: aws.String(session.GuildID)},
				{Name: aws.String(EnvRequestID), Value: aws.String(session.RequestID)},
			},
		}}},
	}
}
