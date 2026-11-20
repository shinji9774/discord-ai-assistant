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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/disgoorg/snowflake/v2"

	"discord-ai-assistant/test/bedrockfake"
	"discord-ai-assistant/test/craigfake"
	"discord-ai-assistant/test/discordfake"
	"discord-ai-assistant/test/ssmfake"
	"discord-ai-assistant/test/transcribefake"
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

// resetFakes は各テストの先頭で呼び、Discord・Bedrock・Translate・Lambda・Web 検索・Craig・S3・Transcribe の fake を初期状態に戻す。
// ssmfake（worker 用と voicereporter 用）は、起動時に読む bot トークンと、その記録を検証に使うので初期化しない。
func resetFakes() {
	discord.Reset()
	bedrock.Reset()
	translateF.Reset()
	lambdaF.Reset()
	webSearch.Reset()
	craigF.Reset()
	s3F.Reset()
	transcribeF.Reset()
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

const (
	summarizeChannelID = "1300000000000000001"
	// botCanReadHistory は VIEW_CHANNEL（1<<10）と READ_MESSAGE_HISTORY（1<<16）の和。
	botCanReadHistory = "66560"
	// memberCanReadHistory は READ_MESSAGE_HISTORY（1<<16）。
	memberCanReadHistory = "65536"
)

// summarizeBody は署名前の /summarize の interaction の本文を作る。チャンネルは snowflake の summarizeChannelID にし、bot と実行者の権限を付ける。
func summarizeBody(t *testing.T, options []map[string]any, appPermissions, memberPermissions string) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(interactionBody(t, map[string]any{"name": "summarize", "options": options})), &body); err != nil {
		t.Fatal(err)
	}
	body["channel_id"] = summarizeChannelID
	body["app_permissions"] = appPermissions
	body["member"] = map[string]any{"permissions": memberPermissions}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// summarizePeriodOptions は 2026-11-18 09:00 〜 18:00（日本時間）を指定するオプション。
func summarizePeriodOptions() []map[string]any {
	return []map[string]any{
		{"name": "since", "type": 3, "value": "2026-11-18 09:00"},
		{"name": "until", "type": 3, "value": "2026-11-18 18:00"},
	}
}

// historyPost は、2026-11-18 の日本時間 hour:minute に author が投稿した会話。
func historyPost(hour, minute int, author discordfake.HistoryUser, content string) discordfake.HistoryMessage {
	sentAt := time.Date(2026, 11, 18, hour, minute, 0, 0, time.FixedZone("JST", 9*60*60)).UTC()
	return discordfake.HistoryMessage{ID: snowflake.New(sentAt).String(), Timestamp: sentAt, Author: author, Content: content}
}

var (
	satoUser   = discordfake.HistoryUser{ID: "2001", Username: "sato", GlobalName: "佐藤"}
	suzukiUser = discordfake.HistoryUser{ID: "2002", Username: "suzuki", GlobalName: "鈴木"}
	botUser    = discordfake.HistoryUser{ID: "9001", Username: "helper-bot", Bot: true}
)

// 条件: 2026-11-18 09:00〜18:00 に利用者の投稿 2 件と bot の投稿 1 件、期間の後に 1 件があるチャンネルで、/summarize を期間つきで実行する。
// 内容: 期間中の利用者の投稿だけがモデルに渡り、見出しつきの要約が返信される。
func TestSummarize_ValidCommand_RepliesSummaryOfChannel(t *testing.T) {
	resetFakes()
	discord.AddHistory(summarizeChannelID,
		historyPost(10, 0, satoUser, "会議は 10 時から"),
		historyPost(10, 5, botUser, "定期通知"),
		historyPost(11, 30, suzukiUser, "了解です"),
		historyPost(19, 0, satoUser, "期間の後"),
	)
	bedrock.RespondWith("## 会議\n- 佐藤: 10 時から", "end_turn")
	body := summarizeBody(t, summarizePeriodOptions(), botCanReadHistory, memberCanReadHistory)

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply(
		"**対象期間**: 2026-11-18 09:00 〜 2026-11-18 18:00（日本時間） / **メッセージ**: 2 件\n\n## 会議\n- 佐藤: 10 時から",
		map[string]string{}))
	var transcripts []string
	for _, r := range bedrock.Requests() {
		transcripts = append(transcripts, r.UserText)
	}
	if want := []string{"[2026-11-18 10:00] 佐藤: 会議は 10 時から\n[2026-11-18 11:30] 鈴木: 了解です"}; !reflect.DeepEqual(transcripts, want) {
		t.Errorf("transcripts = %q, want %q", transcripts, want)
	}
}

// 条件: bot に「メッセージ履歴を読む」権限が無いチャンネルで /summarize を実行する（app_permissions は VIEW_CHANNEL だけ）。
// 内容: bot の権限不足の案内が返信され、モデルは呼ばれない。
func TestSummarize_BotLacksPermission_RepliesGuidance(t *testing.T) {
	resetFakes()
	discord.AddHistory(summarizeChannelID, historyPost(10, 0, satoUser, "会議は 10 時から"))
	body := summarizeBody(t, summarizePeriodOptions(), "1024", memberCanReadHistory)

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply("bot にこのチャンネルの会話履歴を読む権限がないため、要約できません。サーバの管理者に相談してください。", map[string]string{}))
	if got := bedrock.Requests(); len(got) != 0 {
		t.Errorf("bedrock requests = %+v, want none", got)
	}
}

// 条件: 実行者に「メッセージ履歴を読む」権限が無い（member.permissions が 0）。
// 内容: 実行者の権限不足の案内が返信され、モデルは呼ばれない。
func TestSummarize_MemberLacksPermission_RepliesGuidance(t *testing.T) {
	resetFakes()
	discord.AddHistory(summarizeChannelID, historyPost(10, 0, satoUser, "会議は 10 時から"))
	body := summarizeBody(t, summarizePeriodOptions(), botCanReadHistory, "0")

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply("このチャンネルの会話履歴を読む権限がないため、要約できません。", map[string]string{}))
	if got := bedrock.Requests(); len(got) != 0 {
		t.Errorf("bedrock requests = %+v, want none", got)
	}
}

// 条件: Bedrock が 400 で失敗する。
// 内容: 失敗の定型文が返信され、worker のログに会話の本文が出ない。
func TestSummarize_ModelFails_RepliesFailureWithoutLoggingConversation(t *testing.T) {
	const (
		conversationText = "ログ漏えい検査用の本文"
		commandFailedLog = "command failed" // worker の notifyFailure が出すログのメッセージ
	)
	resetFakes()
	discord.AddHistory(summarizeChannelID, historyPost(10, 0, satoUser, conversationText))
	bedrock.FailWith(http.StatusBadRequest)
	body := summarizeBody(t, summarizePeriodOptions(), botCanReadHistory, memberCanReadHistory)

	invokeIngress(t, body, ingressPrivate)

	assertDiscordResponse(t, discordReply("処理に失敗しました。時間をおいてもう一度実行してください。", map[string]string{}))
	logs := workerLogs(t)
	type logFacts struct{ ContainsConversation, ContainsFailureLog bool }
	want := logFacts{ContainsConversation: false, ContainsFailureLog: true}
	got := logFacts{ContainsConversation: strings.Contains(logs, conversationText), ContainsFailureLog: strings.Contains(logs, commandFailedLog)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("worker logs facts = %+v, want %+v:\n%s", got, want, logs)
	}
}

const (
	// voiceReporterSucceeded は、voicereporter が nil を返したときの RIE の応答の本文。
	voiceReporterSucceeded = "null"
	// unknownVoiceJobName は、マニフェストのない依頼のジョブ名（接頭辞は voicesummary.JobNamePrefix）。
	unknownVoiceJobName = "summarize-voice-1499999999999999999-1-1"
)

// invokeVoiceReporter は、jobName の文字起こしが完了したときの EventBridge のイベントを voicereporter の RIE へ送り、応答の本文を返す。
func invokeVoiceReporter(t *testing.T, jobName string) string {
	t.Helper()
	detail, err := json.Marshal(map[string]string{"TranscriptionJobName": jobName, "TranscriptionJobStatus": "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(events.EventBridgeEvent{
		Version:    "0",
		Source:     "aws.transcribe",
		DetailType: "Transcribe Job State Change",
		Region:     "ap-northeast-1",
		Detail:     detail,
	})
	if err != nil {
		t.Fatal(err)
	}
	return invokeRIE(t, voiceReporterURL, payload)
}

// invokeRIE は payload を RIE へ送り、応答の本文を返す。
// RIE は同時に 1 件しか処理できないので、worker の呼び出しと同じ mutex を取る。
func invokeRIE(t *testing.T, url string, payload []byte) string {
	t.Helper()
	rieInvocationMu.Lock()
	defer rieInvocationMu.Unlock()
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// invokeVoiceFailure は、やり直しを使い切った voicereporter の失敗の宛先の invocation record を、voicefailure の RIE へ送り、応答の本文を返す。
// requestPayload は、jobName の文字起こしのイベント。
func invokeVoiceFailure(t *testing.T, jobName string) string {
	t.Helper()
	detail, err := json.Marshal(map[string]string{"TranscriptionJobName": jobName, "TranscriptionJobStatus": "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"requestContext": map[string]any{"condition": "RetriesExhausted", "approximateInvokeCount": 3},
		"requestPayload": events.EventBridgeEvent{
			Version:    "0",
			Source:     "aws.transcribe",
			DetailType: "Transcribe Job State Change",
			Region:     "ap-northeast-1",
			Detail:     detail,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return invokeRIE(t, voiceFailureURL, payload)
}

// 条件: マニフェストのない依頼のジョブ名で、完了のイベントを voicereporter に渡す。
// 内容: voicereporter が起動して正常に終わり、チャンネルには何も投稿しない。
func TestVoiceReporter_UnknownRequest_EndsWithoutPosting(t *testing.T) {
	resetFakes()

	response := invokeVoiceReporter(t, unknownVoiceJobName)

	type reporterFacts struct {
		Response  string
		PostCount int
	}
	want := reporterFacts{Response: voiceReporterSucceeded, PostCount: 0}
	got := reporterFacts{Response: response, PostCount: len(discord.ChannelMessages())}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("voicereporter = %+v, want %+v", got, want)
	}
}

const (
	// disgo の REST と Craig の API が snowflake を要求するため、数字だけの ID を使う。
	voiceGuildID       = "1100000000000000001"
	voiceChannelID     = "1300000000000000002"
	voiceInteractionID = "1400000000000000001" // 依頼 ID になる
	voiceRequesterID   = "1500000000000000001"

	voiceRecordingID  = "AbCdEfGhIjKl"
	voiceRecordingKey = "XyZ123secret"
	// voiceRecordingDeleteKey は削除用のリンクの delete の値。
	voiceRecordingDeleteKey = "DeLeTe456secret"

	// botCanPostResults は VIEW_CHANNEL（1<<10）、SEND_MESSAGES（1<<11）、ATTACH_FILES（1<<15）の和。
	botCanPostResults = "35840"
	// textChannelType は Discord のチャンネルの種類のうち、サーバのテキストチャンネル（GUILD_TEXT）。
	textChannelType = 0

	voiceAccepted = "録音の要約を受け付けました。完了したら、このチャンネルに結果を投稿します。"
	voiceSummary  = "## 予算\n- たろう: 予算の話"
	// voiceHeading は voiceRecording の録音の見出し（開始 2026-11-18 05:00 UTC、長さ 4980 秒、bot を除く話者 2 人）。
	voiceHeading = "**録音開始**: 2026-11-18 14:00（日本時間） / **長さ**: 1 時間 23 分 / **話者**: たろう、はなこ"
	// voiceTranscript は、たろう（トラック 1）とはなこ（トラック 2）の発話を開始時刻の順に並べたもの。754 秒 = 00:12:34。
	voiceTranscript = "[00:00:00] たろう: 始めます\n[00:00:05] はなこ: よろしくお願いします\n[00:12:34] たろう: 次は予算の話です"
)

// voiceLink は voiceRecording の録音リンク。ホストは worker の CRAIG_BASE_URL と同じ craigfake にする。
func voiceLink() string {
	return craigURL + "/rec/" + voiceRecordingID + "?key=" + voiceRecordingKey
}

// voiceDeleteLink は voiceLink に削除の鍵を付けた、削除用の録音リンク。
func voiceDeleteLink() string {
	return voiceLink() + "&delete=" + voiceRecordingDeleteKey
}

// voiceJobName は、voiceInteractionID の依頼の track の文字起こしのジョブ名（"summarize-voice-{依頼 ID}-{トラック}-1"）。
func voiceJobName(track int) string {
	return "summarize-voice-" + voiceInteractionID + "-" + strconv.Itoa(track) + "-1"
}

// voiceRetryJobName は、voiceJobName の track のジョブが失敗したときに開き直すジョブの名前（試行 2）。
func voiceRetryJobName(track int) string {
	return "summarize-voice-" + voiceInteractionID + "-" + strconv.Itoa(track) + "-2"
}

// voiceKey は、voiceInteractionID の依頼の S3 のキー（"requests/{依頼 ID}/" + rest）。
func voiceKey(rest string) string {
	return "requests/" + voiceInteractionID + "/" + rest
}

// voiceBody は署名前の /summarize-voice の interaction の本文を作る。
// サーバのテキストチャンネルで、結果を投稿できる権限を bot に付け、実行者を voiceRequesterID にする。
func voiceBody(t *testing.T, link string) string {
	t.Helper()
	data := map[string]any{
		"name":    "summarize-voice",
		"options": []map[string]any{{"name": "link", "type": 3, "value": link}},
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(interactionBody(t, data)), &body); err != nil {
		t.Fatal(err)
	}
	body["id"] = voiceInteractionID
	body["guild_id"] = voiceGuildID
	body["channel_id"] = voiceChannelID
	body["channel"] = map[string]any{"id": voiceChannelID, "type": textChannelType}
	body["app_permissions"] = botCanPostResults
	body["member"] = map[string]any{"user": map[string]any{"id": voiceRequesterID}, "permissions": "0"}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// voiceRecording は、停止済みで、たろう・はなこ・音楽 bot の 3 トラックがある 1 時間 23 分の録音。
func voiceRecording() craigfake.Recording {
	return craigfake.Recording{
		ID:              voiceRecordingID,
		Key:             voiceRecordingKey,
		DeleteKey:       voiceRecordingDeleteKey,
		GuildID:         voiceGuildID,
		StartTime:       time.Date(2026, 11, 18, 5, 0, 0, 0, time.UTC),
		DurationSeconds: 4980,
		Users: []craigfake.User{
			{Track: 1, Username: "taro", GlobalName: "たろう"},
			{Track: 2, Username: "hanako", GlobalName: "はなこ"},
			{Track: 3, Username: "music-bot", Bot: true},
		},
		Tracks: map[int][]byte{1: []byte("flac-taro"), 2: []byte("flac-hanako"), 3: []byte("flac-music-bot")},
	}
}

// arrangeVoiceRecording は craigfake に録音を置き、FLAC の zip の変換を完了済みにする（変換を待たないため）。
func arrangeVoiceRecording() {
	craigF.AddRecording(voiceRecording())
	craigF.SetJob(voiceRecordingID, craigfake.Job{Status: "complete", OutputFileName: voiceRecordingID + ".flac.zip"})
}

// audioSegment は Amazon Transcribe の結果の results.audio_segments[] の 1 要素（使う項目だけ）。実物と同じく、時刻は秒の文字列。
type audioSegment struct {
	ID         int    `json:"id"`
	Transcript string `json:"transcript"`
	StartTime  string `json:"start_time"`
	EndTime    string `json:"end_time"`
}

// transcribeResult は、segments を発話に持つ Amazon Transcribe の結果の JSON を作る。
func transcribeResult(t *testing.T, segments ...audioSegment) []byte {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"status":  "COMPLETED",
		"results": map[string]any{"audio_segments": segments},
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// voiceTranscriptionJob は、voiceInteractionID の依頼の track の、開始直後の文字起こしのジョブ。
func voiceTranscriptionJob(track int) transcribefake.Job {
	return transcribefake.Job{
		Name:         voiceJobName(track),
		LanguageCode: "ja-JP",
		MediaFormat:  "flac",
		MediaURI:     "s3://" + voiceBucketName + "/" + voiceKey("audio/"+strconv.Itoa(track)+".flac"),
		OutputBucket: voiceBucketName,
		OutputKey:    voiceKey("transcripts/" + strconv.Itoa(track) + ".json"),
		Status:       "IN_PROGRESS",
	}
}

// completedJob は job を COMPLETED にしたもの。
func completedJob(job transcribefake.Job) transcribefake.Job {
	job.Status = "COMPLETED"
	return job
}

// 条件: 停止済みの録音（人の話者 2 人と音楽 bot）の録音リンクで、結果を投稿できるテキストチャンネルから /summarize-voice を実行する。
// 内容: 処理中の表示と受け付けの返信が実行者だけに見える形で返り、人の話者の音声とマニフェストだけが S3 に置かれ、人の話者ごとに日本語の文字起こしが始まる。
func TestSummarizeVoice_ValidLink_AcceptsAndStartsTranscriptionPerHumanSpeaker(t *testing.T) {
	resetFakes()
	arrangeVoiceRecording()

	response := invokeIngress(t, voiceBody(t, voiceLink()), ingressPrivate)

	type acceptanceFacts struct {
		IngressStatus int
		IngressBody   string
		Reply         discordfake.Response
		StoredKeys    []string
		Jobs          []transcribefake.Job
	}
	want := acceptanceFacts{
		IngressStatus: http.StatusOK,
		IngressBody:   privateDeferredResponse,
		Reply:         discordReply(voiceAccepted, map[string]string{}),
		StoredKeys:    []string{voiceKey("audio/1.flac"), voiceKey("audio/2.flac"), voiceKey("manifest.json")},
		Jobs:          []transcribefake.Job{voiceTranscriptionJob(1), voiceTranscriptionJob(2)},
	}
	got := acceptanceFacts{
		IngressStatus: response.StatusCode,
		IngressBody:   response.Body,
		Reply:         waitForDiscordResponse(t),
		StoredKeys:    s3F.Keys(voiceBucketName),
		Jobs:          transcribeF.Jobs(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("受け付け = %+v,\nwant %+v", got, want)
	}
}

// 条件: 有効な録音リンクで /summarize-voice を実行し、worker の処理が終わるまで待つ。
// 内容: 録音の鍵が worker のログにもマニフェストにも含まれない（マニフェストは置かれている）。
func TestSummarizeVoice_ValidLink_DoesNotLeakRecordingKey(t *testing.T) {
	resetFakes()
	arrangeVoiceRecording()
	invokeIngress(t, voiceBody(t, voiceLink()), ingressPrivate)
	waitForDiscordResponse(t)
	waitForRIEIdle()

	logs := workerLogs(t)

	manifest, stored := s3F.Object(voiceBucketName, voiceKey("manifest.json"))
	type leakFacts struct{ ManifestStored, LogsContainKey, ManifestContainsKey bool }
	want := leakFacts{ManifestStored: true, LogsContainKey: false, ManifestContainsKey: false}
	got := leakFacts{
		ManifestStored:      stored,
		LogsContainKey:      strings.Contains(logs, voiceRecordingKey),
		ManifestContainsKey: strings.Contains(string(manifest), voiceRecordingKey),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("鍵の漏れ = %+v, want %+v", got, want)
	}
}

// 条件: 受け付けたあと、トラック 1 の文字起こしだけが終わった時点で 1 回目の完了イベントを渡し、続いてトラック 2 も終わらせ、その完了イベントを 2 回（重複配信）渡す。
// 内容: 1 回目のイベントでは投稿しない。全トラックが終わると、依頼者への通知つきの見出しと要約が 1 回だけ投稿され、transcript.txt が添付される。
// 　　 モデルには話者名と経過時刻つきの文字起こしが 1 回だけ渡り、S3 には report.claim のほか何も残らない。
func TestSummarizeVoice_AllJobsCompleted_PostsSummaryOnceAndDiscardsData(t *testing.T) {
	resetFakes()
	arrangeVoiceRecording()
	bedrock.RespondWith(voiceSummary, "end_turn")
	invokeIngress(t, voiceBody(t, voiceLink()), ingressPrivate)
	waitForDiscordResponse(t)
	transcribeF.Complete(voiceJobName(1), transcribeResult(t,
		audioSegment{ID: 0, Transcript: "始めます", StartTime: "0.0", EndTime: "1.2"},
		audioSegment{ID: 1, Transcript: "次は予算の話です", StartTime: "754.0", EndTime: "756.5"},
	))
	invokeVoiceReporter(t, voiceJobName(1))
	postsWhilePending := len(discord.ChannelMessages())
	transcribeF.Complete(voiceJobName(2), transcribeResult(t,
		audioSegment{ID: 0, Transcript: "よろしくお願いします", StartTime: "5.0", EndTime: "6.8"},
	))

	responses := []string{invokeVoiceReporter(t, voiceJobName(2)), invokeVoiceReporter(t, voiceJobName(2))}

	var transcriptsToModel []string
	for _, r := range bedrock.Requests() {
		transcriptsToModel = append(transcriptsToModel, r.UserText)
	}
	type reportFacts struct {
		Responses          []string
		PostsWhilePending  int
		Posts              []discordfake.ChannelMessage
		TranscriptsToModel []string
		RemainingKeys      []string
	}
	want := reportFacts{
		Responses:         []string{voiceReporterSucceeded, voiceReporterSucceeded},
		PostsWhilePending: 0,
		Posts: []discordfake.ChannelMessage{{
			ChannelID:      voiceChannelID,
			Authorization:  "Bot " + botTokenValue,
			Content:        "<@" + voiceRequesterID + ">\n" + voiceHeading + "\n\n" + voiceSummary,
			AllowedUserIDs: []string{voiceRequesterID},
			Files:          map[string]string{"transcript.txt": voiceTranscript},
		}},
		TranscriptsToModel: []string{voiceTranscript},
		RemainingKeys:      []string{voiceKey("report.claim")},
	}
	got := reportFacts{
		Responses:          responses,
		PostsWhilePending:  postsWhilePending,
		Posts:              discord.ChannelMessages(),
		TranscriptsToModel: transcriptsToModel,
		RemainingKeys:      s3F.Keys(voiceBucketName),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("完了後 = %+v,\nwant %+v", got, want)
	}
}

// 条件: 削除用のリンクで /summarize-voice を実行する。
// 内容: 受け付けの文に削除の案内が付き、Craig の fake からジョブ、録音の順に消える。
func TestSummarizeVoice_DeleteLink_DeletesCraigRecordingAfterAccepting(t *testing.T) {
	resetFakes()
	arrangeVoiceRecording()

	invokeIngress(t, voiceBody(t, voiceDeleteLink()), ingressPrivate)

	type deletionFacts struct {
		Reply     discordfake.Response
		Deletions []craigfake.Deletion
		Jobs      []transcribefake.Job
	}
	want := deletionFacts{
		Reply: discordReply(voiceAccepted+"\nCraig 側の録音は削除しました。同じ録音ではもう再実行できません。", map[string]string{}),
		Deletions: []craigfake.Deletion{
			{RecordingID: voiceRecordingID, Target: craigfake.DeletionTargetJob},
			{RecordingID: voiceRecordingID, Target: craigfake.DeletionTargetRecording},
		},
		Jobs: []transcribefake.Job{voiceTranscriptionJob(1), voiceTranscriptionJob(2)},
	}
	got := deletionFacts{Reply: waitForDiscordResponse(t), Deletions: craigF.Deletions(), Jobs: transcribeF.Jobs()}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("削除 = %+v,\nwant %+v", got, want)
	}
}

// 条件: 受け付けた依頼で、トラックの試行 1 の文字起こしが失敗し、voicereporter にそのイベントが届く。試行 2 が完了し、そのイベントが届く。
// 内容: 試行 2 のジョブが開始され、最後に要約が依頼者への通知つきで 1 回投稿され、S3 から依頼のデータが消える。
func TestSummarizeVoice_FirstTranscriptionFails_RetriesThenReports(t *testing.T) {
	resetFakes()
	arrangeVoiceRecording()
	bedrock.RespondWith(voiceSummary, "end_turn")
	invokeIngress(t, voiceBody(t, voiceLink()), ingressPrivate)
	waitForDiscordResponse(t)
	transcribeF.Complete(voiceJobName(2), transcribeResult(t,
		audioSegment{ID: 0, Transcript: "よろしくお願いします", StartTime: "5.0", EndTime: "6.8"},
	))
	transcribeF.Fail(voiceJobName(1))
	failedResponse := invokeVoiceReporter(t, voiceJobName(1))
	jobsAfterRetryStart := transcribeF.Jobs()
	postsWhileRetrying := len(discord.ChannelMessages())
	transcribeF.Complete(voiceRetryJobName(1), transcribeResult(t,
		audioSegment{ID: 0, Transcript: "始めます", StartTime: "0.0", EndTime: "1.2"},
		audioSegment{ID: 1, Transcript: "次は予算の話です", StartTime: "754.0", EndTime: "756.5"},
	))

	finalResponse := invokeVoiceReporter(t, voiceRetryJobName(1))

	type retryFacts struct {
		Responses          []string
		JobsAfterRetry     []transcribefake.Job
		PostsWhileRetrying int
		Posts              []discordfake.ChannelMessage
		RemainingKeys      []string
	}
	failedJob, retryJob := voiceTranscriptionJob(1), voiceTranscriptionJob(1)
	failedJob.Status = "FAILED"
	retryJob.Name = voiceRetryJobName(1)
	want := retryFacts{
		Responses:          []string{voiceReporterSucceeded, voiceReporterSucceeded},
		JobsAfterRetry:     []transcribefake.Job{failedJob, completedJob(voiceTranscriptionJob(2)), retryJob},
		PostsWhileRetrying: 0,
		Posts: []discordfake.ChannelMessage{{
			ChannelID:      voiceChannelID,
			Authorization:  "Bot " + botTokenValue,
			Content:        "<@" + voiceRequesterID + ">\n" + voiceHeading + "\n\n" + voiceSummary,
			AllowedUserIDs: []string{voiceRequesterID},
			Files:          map[string]string{"transcript.txt": voiceTranscript},
		}},
		RemainingKeys: []string{voiceKey("report.claim")},
	}
	got := retryFacts{
		Responses:          []string{failedResponse, finalResponse},
		JobsAfterRetry:     jobsAfterRetryStart,
		PostsWhileRetrying: postsWhileRetrying,
		Posts:              discord.ChannelMessages(),
		RemainingKeys:      s3F.Keys(voiceBucketName),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("再試行 = %+v,\nwant %+v", got, want)
	}
}

// 条件: 受け付けた依頼について、voicefailure に invocation record（requestPayload に Transcribe のイベント）を 2 回渡す。
// 内容: 失敗の通知が依頼者への通知つきで 1 回だけ届き、S3 には report.claim だけが残る。
func TestVoiceFailure_RetriesExhausted_PostsFailureOnceAndDiscards(t *testing.T) {
	resetFakes()
	arrangeVoiceRecording()
	invokeIngress(t, voiceBody(t, voiceLink()), ingressPrivate)
	waitForDiscordResponse(t)

	responses := []string{invokeVoiceFailure(t, voiceJobName(1)), invokeVoiceFailure(t, voiceJobName(1))}

	type failureFacts struct {
		Responses     []string
		Posts         []discordfake.ChannelMessage
		RemainingKeys []string
	}
	want := failureFacts{
		Responses: []string{voiceReporterSucceeded, voiceReporterSucceeded},
		Posts: []discordfake.ChannelMessage{{
			ChannelID:      voiceChannelID,
			Authorization:  "Bot " + botTokenValue,
			Content:        "<@" + voiceRequesterID + ">\n処理に失敗しました。時間をおいてもう一度実行してください。",
			AllowedUserIDs: []string{voiceRequesterID},
			Files:          map[string]string{},
		}},
		RemainingKeys: []string{voiceKey("report.claim")},
	}
	got := failureFacts{Responses: responses, Posts: discord.ChannelMessages(), RemainingKeys: s3F.Keys(voiceBucketName)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("失敗の通知 = %+v,\nwant %+v", got, want)
	}
}
