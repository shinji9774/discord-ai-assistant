//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"discord-ai-assistant/test/bedrockfake"
	"discord-ai-assistant/test/discordfake"
	"discord-ai-assistant/test/ssmfake"
	"discord-ai-assistant/test/translatefake"
	"discord-ai-assistant/test/websearchfake"
)

const (
	// disgo の REST は snowflake を要求するため、数字だけの ID を使う。
	testApplicationID = "123456789012345678"
	testInteraction   = "it-interaction-token-9f3c7a1e5b2d4086"
	testTimestamp     = "1700000000"

	responseWaitTimeout = 30 * time.Second
	deferredResponse    = `{"type":5}`
	// 公開範囲で「実行者だけ」を選んだときの処理中の表示（flags 64 は ephemeral）。
	privateDeferredResponse = `{"type":5,"data":{"flags":64}}`

	textModelID = "jp.amazon.nova-2-lite-v1:0" // config.yaml の text_model_id

	imageCoreModelID        = "stability.stable-image-core-v1:1"
	imageRegion             = "us-west-2"
	imageDefaultAspectRatio = "1:1"

	imagePrompt         = "ユーザー認証の流れ"
	writtenPrompt       = `{"prompt":"user authentication flow","negative_prompt":""}`
	imageFilteredNotice = "内容が原因で、画像を生成できませんでした。文章または元にする画像を変えて、もう一度実行してください。"
	imageFilterReason   = "Filter reason: prompt"
	imageFileName       = "image.png"
	generatedImage      = "generated png"

	imageSketchModelID    = "us.stability.stable-image-control-sketch-v1:0"
	imageSourcePath       = "/attachments/1/sketch.png"
	imageSourceSide       = 64
	imageSourceOptionID   = "att1"
	imageSourceNotAnImage = "not an image"
	imageSourceNotice     = "元にする画像を受け付けられませんでした。受け付けられるのは、PNG または JPEG で、10 MB 以下、各辺 64 ピクセル以上、総画素数 25,000,000 以下（例: 5712×4284）、縦横比 1:2.5 から 2.5:1 の画像です。"
)

// resetFakes は各テストの先頭で呼び、Discord・Bedrock・Translate・Lambda・Web 検索の fake を初期状態に戻す。
// ssmfake は worker の起動時の記録を検証に使うので初期化しない。
func resetFakes() {
	discord.Reset()
	bedrock.Reset()
	translateF.Reset()
	lambdaF.Reset()
	webSearch.Reset()
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

// interactionBody は署名前の interaction の本文を、スラッシュコマンドの data から作る。
func interactionBody(t *testing.T, data map[string]any) string {
	t.Helper()
	body := map[string]any{
		"type":           2,
		"application_id": testApplicationID,
		"token":          testInteraction,
		"channel_id":     "ch1",
		"data":           data,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// imageCommandBody は署名前の interaction の本文（/image prompt:... visibility:...）を作る。visibility が空ならオプションを付けない。
func imageCommandBody(t *testing.T, prompt, visibility string) string {
	t.Helper()
	options := []map[string]any{{"name": "prompt", "type": 3, "value": prompt}}
	if visibility != "" {
		options = append(options, map[string]any{"name": "visibility", "type": 3, "value": visibility})
	}
	return interactionBody(t, map[string]any{"name": "image", "options": options})
}

// imageSourceCommandBody は署名前の interaction の本文（/image prompt:... source:sketch.png）を作る。
// 添付の実体は data.resolved.attachments にあり、options の value は添付の ID になる。size は Discord が伝える大きさ。
func imageSourceCommandBody(t *testing.T, prompt, attachmentURL string, size int) string {
	t.Helper()
	return interactionBody(t, map[string]any{
		"name": "image",
		"options": []map[string]any{
			{"name": "prompt", "type": 3, "value": prompt},
			{"name": "source", "type": 11, "value": imageSourceOptionID},
		},
		"resolved": map[string]any{
			"attachments": map[string]any{
				imageSourceOptionID: map[string]any{"id": imageSourceOptionID, "filename": "sketch.png", "size": size, "url": attachmentURL, "content_type": "image/png"},
			},
		},
	})
}

// solidPNG は一辺 imageSourceSide ピクセルの単色の PNG を作る。縮小の対象にならない大きさである。
func solidPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, imageSourceSide, imageSourceSide))
	for y := 0; y < imageSourceSide; y++ {
		for x := 0; x < imageSourceSide; x++ {
			img.Set(x, y, color.White)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// imageInvocation は画像生成の依頼のうち、モデル ID・リージョン・本文の縦横比と元画像（base64）を取り出したもの。
type imageInvocation struct {
	ModelID     string
	Region      string
	AspectRatio string
	Image       string
}

// imageInvocations は bedrock が受けた InvokeModel 依頼を imageInvocation にして返す。
func imageInvocations(t *testing.T) []imageInvocation {
	t.Helper()
	got := []imageInvocation{}
	for _, req := range bedrock.InvokeRequests() {
		var body struct {
			AspectRatio string `json:"aspect_ratio"`
			Image       string `json:"image"`
		}
		if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
			t.Fatalf("InvokeModel の本文が解釈できない: %v: %s", err, req.Body)
		}
		got = append(got, imageInvocation{ModelID: req.ModelID, Region: req.Region, AspectRatio: body.AspectRatio, Image: body.Image})
	}
	return got
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
// 内容: ingress が 200 {"type":5} を返し、Discord へ原文つきの翻訳結果が返信される。Web は検索しない。
func TestTranslate_ValidCommand_RepliesTranslationWithOriginal(t *testing.T) {
	resetFakes()
	translateF.RespondWith("こんにちは")
	body := commandBody(t, "translate", "text", "Hello")

	got := invokeIngress(t, body, ingressPrivate)

	assertDeferred(t, got)
	assertDiscordResponse(t, discordReply("> Hello\n\nこんにちは", map[string]string{}))
	assertNoWebSearch(t)
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
// 内容: Discord へ質問つきの回答が返信される。Web は検索しない。
func TestAsk_ValidCommand_RepliesAnswerWithQuestion(t *testing.T) {
	resetFakes()
	bedrock.RespondWith("プログラミング言語です", "end_turn")
	body := commandBody(t, "ask", "question", "Go とは？")

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply("> Go とは？\n\nプログラミング言語です", map[string]string{}))
	assertNoWebSearch(t)
}

const (
	webSearchQuestion = "Go の最新版は？"
	webSearchQuery    = "Go 最新版"
	webSearchSources  = "出典:\n[1] Go のリリース <https://go.dev/doc/devel/release>"
	// defaultResultsPerSearch は、config.yaml の ask.web_search.results_per_search の値。
	defaultResultsPerSearch = 5
)

// searchThenAnswer は、Bedrock が Web 検索を 1 回求めてから answer を返し、
// Web 検索が「Go のリリース」のページを 1 件返すように、fake を準備する。
func searchThenAnswer(answer string) {
	bedrock.RespondWithToolUse(bedrockfake.ToolUse{ID: "tool-1", Name: "web_search", Input: map[string]string{"query": webSearchQuery}})
	bedrock.RespondWith(answer, "end_turn")
	webSearch.RespondWith(websearchfake.Result{Title: "Go のリリース", URL: "https://go.dev/doc/devel/release", Text: "go1.27 が公開されました。"})
}

// assertNoWebSearch は、Web 検索が 1 回も呼ばれていないことを確かめる。
func assertNoWebSearch(t *testing.T) {
	t.Helper()
	if requests := webSearch.Requests(); len(requests) != 0 {
		t.Errorf("web search requests = %+v, want none", requests)
	}
}

// 条件: Bedrock が web_search（query: Go 最新版）を求め、その後「最新版は 1.27 です[1]。」を end_turn で返す。Web 検索は 1 件のページを返す。/ask question:Go の最新版は？ を受ける。
// 内容: Discord へ、質問・回答・出典（タイトルと、山かっこで囲んだ URL）が返信される。Web 検索へは、ツールの ID・モデルが求めた検索文・設定の件数が届く。
func TestAsk_ModelSearchesWeb_RepliesAnswerWithSources(t *testing.T) {
	resetFakes()
	searchThenAnswer("最新版は 1.27 です[1]。")
	body := commandBody(t, "ask", "question", webSearchQuestion)

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply(
		"> Go の最新版は？\n\n最新版は 1.27 です[1]。\n\n"+webSearchSources,
		map[string]string{},
	))
	wantRequests := []websearchfake.Request{{WebSearchID: "web-search.v1", Query: webSearchQuery, MaxResults: defaultResultsPerSearch}}
	if requests := webSearch.Requests(); !reflect.DeepEqual(requests, wantRequests) {
		t.Errorf("Requests() = %+v, want %+v", requests, wantRequests)
	}
}

// 条件: Bedrock が Web 検索を求めた後、2000 文字に [1] を付けた回答を返す。Web 検索は 1 件のページを返す。
// 内容: 回答と出典を合わせると Discord の本文の上限を超えるので、質問・回答・出典の全体が result.md で返信される。
func TestAsk_AnswerWithSourcesOverContentLimit_AttachesResultFile(t *testing.T) {
	resetFakes()
	answer := strings.Repeat("あ", 2000) + "[1]"
	searchThenAnswer(answer)
	body := commandBody(t, "ask", "question", webSearchQuestion)

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply(
		"結果が長いため、ファイルに出力しました。",
		map[string]string{"result.md": "> Go の最新版は？\n\n" + answer + "\n\n" + webSearchSources},
	))
}

// 条件: worker にモデル ID の環境変数を渡さずに、/ask question:Go とは？ を受ける。
// 内容: 設定ファイルの text_model_id のモデルが 1 回だけ呼ばれる（7.6）。
func TestAsk_ValidCommand_InvokesTextModelFromConfig(t *testing.T) {
	resetFakes()
	body := commandBody(t, "ask", "question", "Go とは？")

	invokeIngress(t, body, ingressPrivate)

	waitForDiscordResponse(t)
	var modelIDs []string
	for _, request := range bedrock.Requests() {
		modelIDs = append(modelIDs, request.ModelID)
	}
	if want := []string{textModelID}; !reflect.DeepEqual(modelIDs, want) {
		t.Errorf("model IDs = %v, want %v", modelIDs, want)
	}
}

// 条件: プロンプトが書かれ、画像生成が画像を返す。/image prompt:ユーザー認証の流れ を受ける。
// 内容: ingress が 200 {"type":5} を返し、Discord へ原文と image.png が投稿される。画像生成の依頼は Core のモデル ID・us-west-2・縦横比 1:1 で届く。Web は検索しない。
func TestImage_PromptOnly_PostsImageWithOriginalPrompt(t *testing.T) {
	resetFakes()
	bedrock.RespondWith(writtenPrompt, "end_turn")
	bedrock.RespondWithImage([]byte(generatedImage))
	body := imageCommandBody(t, imagePrompt, "")

	got := invokeIngress(t, body, ingressPrivate)

	assertDeferred(t, got)
	assertDiscordResponse(t, discordReply(imagePrompt, map[string]string{imageFileName: generatedImage}))
	wantInvocations := []imageInvocation{{ModelID: imageCoreModelID, Region: imageRegion, AspectRatio: imageDefaultAspectRatio}}
	if invocations := imageInvocations(t); !reflect.DeepEqual(invocations, wantInvocations) {
		t.Errorf("画像生成の依頼 = %+v, want %+v", invocations, wantInvocations)
	}
	assertNoWebSearch(t)
}

// 条件: /image prompt:ユーザー認証の流れ visibility:private を受ける。
// 内容: ingress の応答が 200 {"type":5,"data":{"flags":64}} になり、処理中の表示が実行者だけになる。
func TestImage_PrivateVisibility_DefersEphemerally(t *testing.T) {
	resetFakes()
	bedrock.RespondWith(writtenPrompt, "end_turn")
	body := imageCommandBody(t, imagePrompt, "private")

	got := invokeIngress(t, body, ingressPrivate)
	waitForDiscordResponse(t)

	if got.StatusCode != http.StatusOK || got.Body != privateDeferredResponse {
		t.Errorf("ingress の応答 = %d %s, want 200 %s", got.StatusCode, got.Body, privateDeferredResponse)
	}
}

// 条件: プロンプトが書かれ、画像生成が内容を理由とする拒否（Filter reason）を返す。
// 内容: Discord へ内容が原因である旨の文言が返信される。
func TestImage_ContentFiltered_PostsContentNotice(t *testing.T) {
	resetFakes()
	bedrock.RespondWith(writtenPrompt, "end_turn")
	bedrock.RespondWithFinishReason(imageFilterReason)
	body := imageCommandBody(t, imagePrompt, "")

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply(imageFilteredNotice, map[string]string{}))
}

// 条件: プロンプトが書かれ、画像生成が画像を返す。64×64 の単色 PNG を元画像にして、/image prompt:ユーザー認証の流れ source:sketch.png を受ける。
// 内容: Discord へ原文と image.png が投稿される。画像生成の依頼は Control Sketch のモデル ID・us-west-2 で届き、本文に元画像の base64 がある。
func TestImage_WithSource_InvokesControlSketch(t *testing.T) {
	resetFakes()
	source := solidPNG(t)
	discord.AddAttachment(imageSourcePath, string(source))
	bedrock.RespondWith(writtenPrompt, "end_turn")
	bedrock.RespondWithImage([]byte(generatedImage))
	body := imageSourceCommandBody(t, imagePrompt, discordURL+imageSourcePath, len(source))

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply(imagePrompt, map[string]string{imageFileName: generatedImage}))
	wantInvocations := []imageInvocation{{ModelID: imageSketchModelID, Region: imageRegion, Image: base64.StdEncoding.EncodeToString(source)}}
	if invocations := imageInvocations(t); !reflect.DeepEqual(invocations, wantInvocations) {
		t.Errorf("画像生成の依頼 = %+v, want %+v", invocations, wantInvocations)
	}
}

// 条件: 画像でないバイト列を元画像にして、/image prompt:ユーザー認証の流れ source:sketch.png を受ける。
// 内容: Discord へ元画像を受け付けられない旨の文言が返信され、文章生成（Converse）も画像生成（InvokeModel）も呼ばれない。
func TestImage_InvalidSource_PostsSourceNotice(t *testing.T) {
	resetFakes()
	discord.AddAttachment(imageSourcePath, imageSourceNotAnImage)
	body := imageSourceCommandBody(t, imagePrompt, discordURL+imageSourcePath, len(imageSourceNotAnImage))

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply(imageSourceNotice, map[string]string{}))
	if requests := bedrock.Requests(); len(requests) != 0 {
		t.Errorf("文章生成の依頼 = %+v, want none", requests)
	}
	if invocations := imageInvocations(t); len(invocations) != 0 {
		t.Errorf("画像生成の依頼 = %+v, want none", invocations)
	}
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
