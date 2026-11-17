// ingress は Discord の interaction を受ける Lambda のエントリポイント。依存の組み立てだけを行う。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"

	"discord-ai-assistant/internal/ingress"
)

const (
	envPublicKey          = "DISCORD_PUBLIC_KEY"
	envWorkerFunctionName = "WORKER_FUNCTION_NAME"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ingress:", err)
		os.Exit(1)
	}
}

func run() error {
	publicKeyHex, err := requireEnv(envPublicKey)
	if err != nil {
		return err
	}
	workerFunctionName, err := requireEnv(envWorkerFunctionName)
	if err != nil {
		return err
	}
	publicKey, err := ingress.ParsePublicKey(publicKeyHex)
	if err != nil {
		return fmt.Errorf("parse %s: %w", envPublicKey, err)
	}
	awsConfig, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}

	invoker := ingress.NewLambdaInvoker(awslambda.NewFromConfig(awsConfig), workerFunctionName)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	lambda.Start(ingress.NewHandler(publicKey, invoker, logger).Handle)
	return nil
}

func requireEnv(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	return value, nil
}
