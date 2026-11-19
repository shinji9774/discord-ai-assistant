//go:build integration

// Package integration は Lambda RIE 上の ingress・worker を fake の外部サービスにつないで動かす結合テスト。
package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"discord-ai-assistant/test/bedrockfake"
	"discord-ai-assistant/test/discordfake"
	"discord-ai-assistant/test/lambdafake"
	"discord-ai-assistant/test/ssmfake"
	"discord-ai-assistant/test/translatefake"
	"discord-ai-assistant/test/websearchfake"
)

const (
	envImage     = "IT_IMAGE"
	defaultImage = "discord-ai-assistant:it"

	rieBinaryPath     = "/usr/local/bin/aws-lambda-rie"
	rieContainerPort  = "8080/tcp"
	rieInvocationPath = "/2015-03-31/functions/function/invocations"

	hostGatewayAlias = "host.docker.internal"
	hostGatewayEntry = hostGatewayAlias + ":host-gateway"

	botTokenParameterName = "/discord-ai-assistant/bot-token"
	botTokenValue         = "it-bot-token"
	workerFunctionName    = "worker"
)

// rieInvocationMu は worker の RIE への呼び出しを 1 件ずつにする。
var rieInvocationMu sync.Mutex

// 結合テスト全体で共有する、fake と RIE コンテナ。
var (
	discord    *discordfake.Server
	bedrock    *bedrockfake.Server
	translateF *translatefake.Server
	lambdaF    *lambdafake.Server
	ssm        *ssmfake.Server
	webSearch  *websearchfake.Server

	workerContainer  testcontainers.Container
	ingressContainer testcontainers.Container
	ingressURL       string
	workerURL        string
	ingressPublicKey ed25519.PublicKey
	ingressPrivate   ed25519.PrivateKey
	discordURL       string // discordfake のコンテナから見える URL
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	if err := setUp(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "integration setup:", err)
		return 1
	}
	defer tearDown(ctx)
	return m.Run()
}

func setUp(ctx context.Context) error {
	var err error
	ingressPublicKey, ingressPrivate, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	discord, bedrock, translateF, lambdaF, ssm = discordfake.New(), bedrockfake.New(), translatefake.New(), lambdafake.New(), ssmfake.New()
	webSearch = websearchfake.New()
	ssm.SetParameter(botTokenParameterName, botTokenValue)

	discordURL, err = serveFake(discord)
	if err != nil {
		return err
	}
	bedrockURL, err := serveFake(bedrock)
	if err != nil {
		return err
	}
	translateURL, err := serveFake(translateF)
	if err != nil {
		return err
	}
	lambdaURL, err := serveFake(lambdaF)
	if err != nil {
		return err
	}
	ssmURL, err := serveFake(ssm)
	if err != nil {
		return err
	}

	webSearchURL, err := serveFake(webSearch)
	if err != nil {
		return err
	}

	workerContainer, err = startRIE(ctx, "/worker", map[string]string{
		"BOT_TOKEN_PARAMETER_NAME":           botTokenParameterName,
		"AWS_ENDPOINT_URL_BEDROCK_RUNTIME":   bedrockURL,
		"AWS_ENDPOINT_URL_TRANSLATE":         translateURL,
		"AWS_ENDPOINT_URL_SSM":               ssmURL,
		"DISCORD_API_BASE_URL":               discordURL,
		"AWS_ENDPOINT_URL_BEDROCK_AGENTCORE": webSearchURL,
	})
	if err != nil {
		return err
	}
	workerURL, err = containerURL(ctx, workerContainer)
	if err != nil {
		return err
	}
	forwardToWorker()

	ingressContainer, err = startRIE(ctx, "/ingress", map[string]string{
		"DISCORD_PUBLIC_KEY":      hex.EncodeToString(ingressPublicKey),
		"WORKER_FUNCTION_NAME":    workerFunctionName,
		"AWS_ENDPOINT_URL_LAMBDA": lambdaURL,
	})
	if err != nil {
		return err
	}
	ingressURL, err = containerURL(ctx, ingressContainer)
	return err
}

func tearDown(ctx context.Context) {
	for _, c := range []testcontainers.Container{ingressContainer, workerContainer} {
		if c != nil {
			_ = testcontainers.TerminateContainer(c, testcontainers.StopContext(ctx))
		}
	}
}

// serveFake は fake を 0.0.0.0 の空きポートで待ち受けさせ、コンテナから見える URL を返す。
func serveFake(handler http.Handler) (string, error) {
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return "", err
	}
	go func() { _ = http.Serve(listener, handler) }()
	port := listener.Addr().(*net.TCPAddr).Port
	return fmt.Sprintf("http://%s:%d", hostGatewayAlias, port), nil
}

func startRIE(ctx context.Context, binary string, env map[string]string) (testcontainers.Container, error) {
	image := os.Getenv(envImage)
	if image == "" {
		image = defaultImage
	}
	env["AWS_REGION"] = "ap-northeast-1"
	env["AWS_ACCESS_KEY_ID"] = "test"
	env["AWS_SECRET_ACCESS_KEY"] = "test"
	return testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        image,
			Entrypoint:   []string{rieBinaryPath, binary},
			ExposedPorts: []string{rieContainerPort},
			Env:          env,
			HostConfigModifier: func(hostConfig *container.HostConfig) {
				hostConfig.ExtraHosts = append(hostConfig.ExtraHosts, hostGatewayEntry)
			},
			WaitingFor: wait.ForListeningPort(rieContainerPort),
		},
		Started: true,
	})
}

func containerURL(ctx context.Context, c testcontainers.Container) (string, error) {
	host, err := c.Host(ctx)
	if err != nil {
		return "", err
	}
	port, err := c.MappedPort(ctx, rieContainerPort)
	if err != nil {
		return "", err
	}
	return "http://" + net.JoinHostPort(host, port.Port()) + rieInvocationPath, nil
}

// forwardToWorker は lambdafake が受けた依頼を worker のコンテナへ転送するよう設定する。
func forwardToWorker() {
	lambdaF.OnInvoke(func(payload []byte) { postToRIE(workerURL, payload) })
}

// postToRIE は payload を RIE の invocations エンドポイントへ送り、応答を読み捨てる。
// RIE は同時に 1 件しか処理できず（重なるとプロセスが落ちる）、返信は呼び出しの完了より先に届くので、
// 前の呼び出しが終わるまで待ってから送る。
func postToRIE(url string, payload []byte) {
	rieInvocationMu.Lock()
	defer rieInvocationMu.Unlock()
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		fmt.Fprintln(os.Stderr, "forward to worker:", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
}
