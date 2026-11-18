//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"discord-ai-assistant/test/discordfake"
	"discord-ai-assistant/test/ssmfake"
	"discord-ai-assistant/test/translatefake"
)

const (
	// disgo の REST は snowflake を要求するため、数字だけの ID を使う。
	testApplicationID = "123456789012345678"
	testInteraction   = "it-interaction-token-9f3c7a1e5b2d4086"
	testTimestamp     = "1700000000"

	responseWaitTimeout = 30 * time.Second
	deferredResponse    = `{"type":5}`
)

// resetFakes は各テストの先頭で呼び、Discord・Bedrock・Translate・Lambda の fake を初期状態に戻す。
// ssmfake は worker の起動時の記録を検証に使うので初期化しない。
func resetFakes() {
	discord.Reset()
	bedrock.Reset()
	translateF.Reset()
	lambdaF.Reset()
	forwardToWorker()
}

// commandBody は署名前の interaction の本文（スラッシュコマンド 1 つ・文字列オプション 1 つ）を作る。
func commandBody(t *testing.T, commandName, optionName, optionValue string) string {
	t.Helper()
	body := map[string]any{
		"type":           2,
		"application_id": testApplicationID,
		"token":          testInteraction,
		"channel_id":     "ch1",
		"data": map[string]any{
			"name":    commandName,
			"options": []map[string]any{{"name": optionName, "type": 3, "value": optionValue}},
		},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// fileCommandBody は署名前の interaction の本文（/translate file:memo.txt to:en）を作る。
// 添付の実体は data.resolved.attachments にあり、options の value は添付の ID になる。
func fileCommandBody(t *testing.T, attachmentURL string) string {
	t.Helper()
	body := map[string]any{
		"type":           2,
		"application_id": testApplicationID,
		"token":          testInteraction,
		"channel_id":     "ch1",
		"data": map[string]any{
			"name": "translate",
			"options": []map[string]any{
				{"name": "file", "type": 11, "value": "att1"},
				{"name": "to", "type": 3, "value": "en"},
			},
			"resolved": map[string]any{
				"attachments": map[string]any{
					"att1": map[string]any{"id": "att1", "filename": "memo.txt", "size": 15, "url": attachmentURL, "content_type": "text/plain; charset=utf-8"},
				},
			},
		},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// invokeIngress は署名つきの Function URL イベントを ingress のコンテナへ POST し、応答を返す。
func invokeIngress(t *testing.T, body string, privateKey ed25519.PrivateKey) events.LambdaFunctionURLResponse {
	t.Helper()
	signature := ed25519.Sign(privateKey, []byte(testTimestamp+body))
	event := events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-Signature-Ed25519":   hex.EncodeToString(signature),
			"X-Signature-Timestamp": testTimestamp,
		},
		Body: body,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ingressURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var got events.LambdaFunctionURLResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("ingress の応答が解釈できない: %v: %s", err, raw)
	}
	return got
}

// waitForDiscordResponse は fake の Discord に届く次の返信を待ち、届かなければテストを止める。
func waitForDiscordResponse(t *testing.T) discordfake.Response {
	t.Helper()
	got, ok := discord.WaitForResponse(responseWaitTimeout)
	if !ok {
		t.Fatalf("Discord への返信が %s 以内に届かなかった", responseWaitTimeout)
	}
	return got
}

// assertDiscordResponse は fake の Discord に届いた次の返信が want と一致することを確かめる。
func assertDiscordResponse(t *testing.T, want discordfake.Response) {
	t.Helper()
	got := waitForDiscordResponse(t)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Discord への返信 = %+v, want %+v", got, want)
	}
}

func assertDeferred(t *testing.T, got events.LambdaFunctionURLResponse) {
	t.Helper()
	if got.StatusCode != http.StatusOK || got.Body != deferredResponse {
		t.Errorf("ingress の応答 = %d %s, want 200 %s", got.StatusCode, got.Body, deferredResponse)
	}
}

// newOtherKey は ingress が知らない署名鍵を作る。
func newOtherKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return publicKey, privateKey
}

// workerLogs は worker のコンテナがここまでに出力したログ全体を返す。
func workerLogs(t *testing.T) string {
	t.Helper()
	reader, err := workerContainer.Logs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	logs, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(logs)
}

func discordReply(content string, files map[string]string) discordfake.Response {
	return discordfake.Response{ApplicationID: testApplicationID, Token: testInteraction, Content: content, Files: files}
}

// 条件: Amazon Translate が「こんにちは」を返し、/translate text:Hello を受ける。
// 内容: ingress が 200 {"type":5} を返し、Discord へ原文つきの翻訳結果が返信される。
func TestTranslate_ValidCommand_RepliesTranslationWithOriginal(t *testing.T) {
	resetFakes()
	translateF.RespondWith("こんにちは")
	body := commandBody(t, "translate", "text", "Hello")

	got := invokeIngress(t, body, ingressPrivate)

	assertDeferred(t, got)
	assertDiscordResponse(t, discordReply("> Hello\n\nこんにちは", map[string]string{}))
}

// 条件: discordfake に memo.txt を置き、/translate file:memo.txt to:en を resolved.attachments つきで受ける。Amazon Translate が「Hello」を返す。
// 内容: Discord へ memo_en.txt に訳文を入れた添付つきの返信がされ、Translate へは添付の中身が渡る。
func TestTranslate_FileAttached_RepliesTranslatedFile(t *testing.T) {
	resetFakes()
	discord.AddAttachment("/attachments/1/memo.txt", "こんにちは")
	translateF.RespondWithDocument("Hello")
	body := fileCommandBody(t, discordURL+"/attachments/1/memo.txt")

	got := invokeIngress(t, body, ingressPrivate)

	assertDeferred(t, got)
	assertDiscordResponse(t, discordReply("memo.txt を英語に翻訳しました。", map[string]string{"memo_en.txt": "Hello"}))
	wantRequests := []translatefake.DocumentRequest{{Content: "こんにちは", ContentType: "text/plain", SourceLanguageCode: "auto", TargetLanguageCode: "en"}}
	if requests := translateF.DocumentRequests(); !reflect.DeepEqual(requests, wantRequests) {
		t.Errorf("DocumentRequests() = %+v, want %+v", requests, wantRequests)
	}
}

// 条件: Bedrock が「プログラミング言語です」を end_turn で返し、/ask question:Go とは？ を受ける。
// 内容: Discord へ質問つきの回答が返信される。
func TestAsk_ValidCommand_RepliesAnswerWithQuestion(t *testing.T) {
	resetFakes()
	bedrock.RespondWith("プログラミング言語です", "end_turn")
	body := commandBody(t, "ask", "question", "Go とは？")

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply("> Go とは？\n\nプログラミング言語です", map[string]string{}))
}

// 条件: Amazon Translate が 2001 文字の結果を返す。
// 内容: 本文は添付の案内になり、result.txt に全文が添付される。
func TestTranslate_ResultOverContentLimit_AttachesResultFile(t *testing.T) {
	resetFakes()
	translateF.RespondWith(strings.Repeat("あ", 2001))
	body := commandBody(t, "translate", "text", "Hello")

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply(
		"結果が長いため、ファイルに出力しました。",
		map[string]string{"result.txt": "> Hello\n\n" + strings.Repeat("あ", 2001)},
	))
}

// 条件: Bedrock が max_tokens で打ち切られた「こん」を返し、/ask question:Go とは？ を受ける。
// 内容: 本文の末尾に途中までの結果である旨が付く。
func TestAsk_AnswerStoppedByMaxTokens_AppendsTruncatedNotice(t *testing.T) {
	resetFakes()
	bedrock.RespondWith("こん", "max_tokens")
	body := commandBody(t, "ask", "question", "Go とは？")

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply(
		"> Go とは？\n\nこん\n\n（上限に達したため、途中までの結果です）",
		map[string]string{},
	))
}

// 条件: Amazon Translate が 400 で失敗する。
// 内容: Discord へ失敗を知らせる定型文が返信される。
func TestTranslate_TranslateFails_RepliesFailureMessage(t *testing.T) {
	resetFakes()
	translateF.FailWith(http.StatusBadRequest)
	body := commandBody(t, "translate", "text", "Hello")

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply("処理に失敗しました。時間をおいてもう一度実行してください。", map[string]string{}))
}

// 条件: ingress の公開鍵と異なる鍵で署名したコマンドを受ける。
// 内容: 401 を返し、worker は呼び出されない。
func TestIngress_InvalidSignature_Returns401WithoutInvokingWorker(t *testing.T) {
	resetFakes()
	_, otherKey := newOtherKey(t)
	body := commandBody(t, "translate", "text", "Hello")

	got := invokeIngress(t, body, otherKey)

	if got.StatusCode != http.StatusUnauthorized || len(lambdaF.Invocations()) != 0 {
		t.Errorf("status = %d, invocations = %+v, want 401 and none", got.StatusCode, lambdaF.Invocations())
	}
}

// 条件: /translate text:Hello を実行して返信を待つ。
// 内容: worker は SSM から bot トークンを復号つきで読んでいる。
func TestWorker_AfterCommand_HasReadBotTokenWithDecryption(t *testing.T) {
	resetFakes()
	translateF.RespondWith("こんにちは")
	body := commandBody(t, "translate", "text", "Hello")
	invokeIngress(t, body, ingressPrivate)
	waitForDiscordResponse(t)

	got := ssm.Requests()

	want := []ssmfake.Request{{Name: botTokenParameterName, WithDecryption: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ssm.Requests() = %+v, want %+v", got, want)
	}
}

// 条件: /translate text:Hello を実行して返信を待つ。
// 内容: worker のコンテナのログに bot トークンが含まれない。
func TestWorker_AfterCommand_DoesNotLogBotToken(t *testing.T) {
	resetFakes()
	translateF.RespondWith("こんにちは")
	body := commandBody(t, "translate", "text", "Hello")
	invokeIngress(t, body, ingressPrivate)
	waitForDiscordResponse(t)

	got := workerLogs(t)

	if strings.Contains(got, botTokenValue) {
		t.Errorf("worker のログに bot トークンが含まれている")
	}
}

// 条件: /translate text:Hello を実行して返信を待つ。
// 内容: worker のコンテナのログに interaction token が含まれない。
func TestWorker_AfterCommand_DoesNotLogInteractionToken(t *testing.T) {
	resetFakes()
	translateF.RespondWith("こんにちは")
	body := commandBody(t, "translate", "text", "Hello")
	invokeIngress(t, body, ingressPrivate)
	waitForDiscordResponse(t)

	got := workerLogs(t)

	if strings.Contains(got, testInteraction) {
		t.Errorf("worker のログに interaction token が含まれている")
	}
}
