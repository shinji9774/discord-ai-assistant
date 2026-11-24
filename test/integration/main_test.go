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
	"discord-ai-assistant/test/craigfake"
	"discord-ai-assistant/test/discordfake"
	"discord-ai-assistant/test/ecsfake"
	"discord-ai-assistant/test/lambdafake"
	"discord-ai-assistant/test/s3fake"
	"discord-ai-assistant/test/ssmfake"
	"discord-ai-assistant/test/transcribefake"
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
	voiceBucketName       = "it-voice-bucket"

	// 録音タスクの起動先。worker の RunTask は ecsfake が受ける。
	recorderClusterARN        = "arn:aws:ecs:ap-northeast-1:000000000000:cluster/it-recorder"
	recorderTaskDefinitionARN = "arn:aws:ecs:ap-northeast-1:000000000000:task-definition/it-recorder:1"
	recorderSubnetIDs         = "subnet-0it000000000000a1,subnet-0it000000000000b2"
	recorderSecurityGroupID   = "sg-0it0000000000000c3"
)

// rieInvocationMu は RIE への呼び出し（worker・voicereporter・voicefailure・recordingfailure）を 1 件ずつにする。
// worker の処理が終わってから voicereporter・voicefailure・recordingfailure を呼ぶ順序も、これで保つ。
var rieInvocationMu sync.Mutex

// 結合テスト全体で共有する、fake と RIE コンテナ。
var (
	discord     *discordfake.Server
	bedrock     *bedrockfake.Server
	translateF  *translatefake.Server
	lambdaF     *lambdafake.Server
	ssm         *ssmfake.Server
	webSearch   *websearchfake.Server
	craigF      *craigfake.Server
	s3F         *s3fake.Server
	transcribeF *transcribefake.Server
	reporterSSM *ssmfake.Server // voicereporter・voicefailure・recordingfailure 用。worker の ssm の記録（ちょうど 1 件）を崩さないために分ける
	ecsF        *ecsfake.Server

	workerContainer           testcontainers.Container
	ingressContainer          testcontainers.Container
	voiceReporterContainer    testcontainers.Container
	voiceFailureContainer     testcontainers.Container
	recordingFailureContainer testcontainers.Container
	ingressURL                string
	workerURL                 string
	voiceReporterURL          string
	voiceFailureURL           string
	recordingFailureURL       string
	ingressPublicKey          ed25519.PublicKey
	ingressPrivate            ed25519.PrivateKey
	discordURL                string // discordfake のコンテナから見える URL
	craigURL                  string // craigfake のコンテナから見える URL。録音リンクの組み立てにも使う
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
	craigF, s3F = craigfake.New(), s3fake.New()
	transcribeF = transcribefake.New(s3F)
	ssm.SetParameter(botTokenParameterName, botTokenValue)
	reporterSSM = ssmfake.New()
	reporterSSM.SetParameter(botTokenParameterName, botTokenValue)

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
	craigURL, err = serveFake(craigF)
	if err != nil {
		return err
	}
	s3URL, err := serveFake(s3F)
	if err != nil {
		return err
	}
	transcribeURL, err := serveFake(transcribeF)
	if err != nil {
		return err
	}
	reporterSSMURL, err := serveFake(reporterSSM)
	if err != nil {
		return err
	}
	ecsF = ecsfake.New()
	ecsURL, err := serveFake(ecsF)
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
		"VOICE_BUCKET_NAME":                  voiceBucketName,
		"AWS_ENDPOINT_URL_S3":                s3URL,
		"AWS_ENDPOINT_URL_TRANSCRIBE":        transcribeURL,
		"CRAIG_BASE_URL":                     craigURL,
		"RECORDER_CLUSTER_ARN":               recorderClusterARN,
		"RECORDER_TASK_DEFINITION_ARN":       recorderTaskDefinitionARN,
		"RECORDER_SUBNET_IDS":                recorderSubnetIDs,
		"RECORDER_SECURITY_GROUP_ID":         recorderSecurityGroupID,
		"AWS_ENDPOINT_URL_ECS":               ecsURL,
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
	if err != nil {
		return err
	}

	voiceReporterContainer, err = startRIE(ctx, "/voicereporter", map[string]string{
		"BOT_TOKEN_PARAMETER_NAME":         botTokenParameterName,
		"VOICE_BUCKET_NAME":                voiceBucketName,
		"AWS_ENDPOINT_URL_S3":              s3URL,
		"AWS_ENDPOINT_URL_TRANSCRIBE":      transcribeURL,
		"AWS_ENDPOINT_URL_BEDROCK_RUNTIME": bedrockURL,
		"AWS_ENDPOINT_URL_SSM":             reporterSSMURL,
		"DISCORD_API_BASE_URL":             discordURL,
	})
	if err != nil {
		return err
	}
	voiceReporterURL, err = containerURL(ctx, voiceReporterContainer)
	if err != nil {
		return err
	}

	voiceFailureContainer, err = startRIE(ctx, "/voicefailure", map[string]string{
		"BOT_TOKEN_PARAMETER_NAME": botTokenParameterName,
		"VOICE_BUCKET_NAME":        voiceBucketName,
		"AWS_ENDPOINT_URL_S3":      s3URL,
		"AWS_ENDPOINT_URL_SSM":     reporterSSMURL,
		"DISCORD_API_BASE_URL":     discordURL,
	})
	if err != nil {
		return err
	}
	voiceFailureURL, err = containerURL(ctx, voiceFailureContainer)
	if err != nil {
		return err
	}

	recordingFailureContainer, err = startRIE(ctx, "/recordingfailure", map[string]string{
		"BOT_TOKEN_PARAMETER_NAME": botTokenParameterName,
		"VOICE_BUCKET_NAME":        voiceBucketName,
		"AWS_ENDPOINT_URL_S3":      s3URL,
		"AWS_ENDPOINT_URL_SSM":     reporterSSMURL,
		"DISCORD_API_BASE_URL":     discordURL,
	})
	if err != nil {
		return err
	}
	recordingFailureURL, err = containerURL(ctx, recordingFailureContainer)
	return err
}

func tearDown(ctx context.Context) {
	for _, c := range []testcontainers.Container{ingressContainer, workerContainer, voiceReporterContainer, voiceFailureContainer, recordingFailureContainer} {
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

// waitForRIEIdle は、進行中の RIE への呼び出しが終わるまで待つ。
// worker は Discord への返信のあとにもログを出すので、ログを調べる前に呼ぶ。
func waitForRIEIdle() {
	rieInvocationMu.Lock()
	defer rieInvocationMu.Unlock()
}
