// Package bottoken は SSM の SecureString から bot トークンを読む。
package bottoken

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// Load は GetParameter（WithDecryption: true）を 1 回呼んでトークンを返す。
// エラー文にトークンの値は含めない。
func Load(ctx context.Context, client *ssm.Client, parameterName string) (string, error) {
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(parameterName),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("ssm get parameter: %w", err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", errors.New("ssm parameter has no value")
	}
	return *out.Parameter.Value, nil
}
