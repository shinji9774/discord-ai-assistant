package ingress

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"discord-ai-assistant/internal/command"
)

// WorkerInvoker は worker を非同期に起動する。
type WorkerInvoker interface {
	InvokeAsync(ctx context.Context, inv command.Invocation) error
}

// LambdaInvoker は worker の Lambda 関数を Event 型で起動する。
type LambdaInvoker struct {
	client       *lambda.Client
	functionName string
}

func NewLambdaInvoker(client *lambda.Client, functionName string) *LambdaInvoker {
	return &LambdaInvoker{client: client, functionName: functionName}
}

// InvokeAsync は inv を JSON にして、応答を待たずに worker を起動する。
func (i *LambdaInvoker) InvokeAsync(ctx context.Context, inv command.Invocation) error {
	payload, err := json.Marshal(inv)
	if err != nil {
		return fmt.Errorf("marshal invocation: %w", err)
	}
	_, err = i.client.Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   aws.String(i.functionName),
		InvocationType: lambdatypes.InvocationTypeEvent,
		Payload:        payload,
	})
	if err != nil {
		return fmt.Errorf("invoke worker: %w", err)
	}
	return nil
}
