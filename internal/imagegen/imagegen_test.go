package imagegen_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"reflect"
	"testing"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/imagegen"
	"discord-ai-assistant/internal/imagegen/imagegentest"
	"discord-ai-assistant/internal/llm"
	"discord-ai-assistant/internal/llm/llmtest"
	"discord-ai-assistant/internal/translate/translatetest"
)

const (
	japanesePrompt = "ログインの流れを図にして"
	englishPrompt  = "A flowchart of a login flow."
)

var (
	allowVerdict  = llm.Result{Text: `{"verdict":"allow","english_prompt":"` + englishPrompt + `"}`}
	rejectVerdict = llm.Result{Text: `{"verdict":"reject","english_prompt":""}`}
	pngBytes      = []byte("generated-png")
)

const (
	sourceURL           = "https://cdn.example.com/rough.png"
	sourceSide          = 64
	overModelPixelsWide = 4032 // スマートフォンの写真 4032×3024 は総画素数の上限 9,437,184 を超える
	overModelPixelsHigh = 3024
	maxModelPixels      = 9_437_184
	sourceBytesLimit    = 10_000_000
	sourceNotice        = "元にする画像を受け付けられませんでした。受け付けられるのは、PNG または JPEG で、10 MB 以下、各辺 64 ピクセル以上、総画素数 25,000,000 以下（例: 5712×4284）、縦横比 1:2.5 から 2.5:1 の画像です。"
)

func sourceInvocation(size int, options map[string]string) command.Invocation {
	inv := imageInvocation(options)
	inv.Attachments = map[string]command.Attachment{"source": {Filename: "rough.png", URL: sourceURL, ContentType: "image/png", Size: size}}
	return inv
}

func encodePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func imageInvocation(options map[string]string) command.Invocation {
	return command.Invocation{CommandName: "image", Options: options}
}

// 条件: 審査が許可し、生成が成功する。
// 内容: 本文が原文だけで image.png が 1 枚付き、生成の呼び出しは 1 回だけになる。
func TestExecute_AllowedPrompt_ReturnsOriginalPromptWithImage(t *testing.T) {
	text := &llmtest.Generator{Result: allowVerdict}
	images := &imagegentest.Generator{Image: pngBytes}
	cmd := imagegen.New(text, images, nil)

	got, err := cmd.Execute(context.Background(), imageInvocation(map[string]string{"prompt": japanesePrompt}))

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := command.Result{Text: japanesePrompt, Files: []command.File{{Name: "image.png", Content: pngBytes}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Execute() = %+v, want %+v", got, want)
	}
	if len(images.TextRequests) != 1 || len(images.SketchRequests) != 0 {
		t.Errorf("calls = text %d, sketch %d, want text 1, sketch 0", len(images.TextRequests), len(images.SketchRequests))
	}
}

// 条件: 日本語の文章を実行する。
// 内容: 生成に渡る指示文は審査が返した英語で、投稿の本文は日本語の原文のままになる。
func TestExecute_JapanesePrompt_PassesEnglishPromptToGenerator(t *testing.T) {
	text := &llmtest.Generator{Result: allowVerdict}
	images := &imagegentest.Generator{Image: pngBytes}
	cmd := imagegen.New(text, images, nil)

	got, _ := cmd.Execute(context.Background(), imageInvocation(map[string]string{"prompt": japanesePrompt}))

	want := []imagegen.TextRequest{{Prompt: englishPrompt, AspectRatio: "1:1"}}
	if !reflect.DeepEqual(images.TextRequests, want) {
		t.Errorf("TextRequests = %+v, want %+v", images.TextRequests, want)
	}
	if got.Text != japanesePrompt {
		t.Errorf("Text = %q, want %q", got.Text, japanesePrompt)
	}
}

// 条件: aspect_ratio に選択肢の値（16:9）を指定する。
// 内容: その縦横比が生成に渡る。
func TestExecute_AspectRatioChosen_PassesItToGenerator(t *testing.T) {
	text := &llmtest.Generator{Result: allowVerdict}
	images := &imagegentest.Generator{Image: pngBytes}
	cmd := imagegen.New(text, images, nil)

	_, _ = cmd.Execute(context.Background(), imageInvocation(map[string]string{"prompt": japanesePrompt, "aspect_ratio": "16:9"}))

	want := []imagegen.TextRequest{{Prompt: englishPrompt, AspectRatio: "16:9"}}
	if !reflect.DeepEqual(images.TextRequests, want) {
		t.Errorf("TextRequests = %+v, want %+v", images.TextRequests, want)
	}
}

// 条件: aspect_ratio を省略する。
// 内容: 1:1 が生成に渡る。
func TestExecute_NoAspectRatio_UsesOneToOne(t *testing.T) {
	text := &llmtest.Generator{Result: allowVerdict}
	images := &imagegentest.Generator{Image: pngBytes}
	cmd := imagegen.New(text, images, nil)

	_, _ = cmd.Execute(context.Background(), imageInvocation(map[string]string{"prompt": japanesePrompt}))

	want := []imagegen.TextRequest{{Prompt: englishPrompt, AspectRatio: "1:1"}}
	if !reflect.DeepEqual(images.TextRequests, want) {
		t.Errorf("TextRequests = %+v, want %+v", images.TextRequests, want)
	}
}

// 条件: aspect_ratio に選択肢にない値（4:3）が来る。
// 内容: error が返り、審査も生成も呼ばれない。
func TestExecute_UnknownAspectRatio_ReturnsErrorWithoutScreeningOrGenerating(t *testing.T) {
	text := &llmtest.Generator{Result: allowVerdict}
	images := &imagegentest.Generator{Image: pngBytes}
	cmd := imagegen.New(text, images, nil)

	_, err := cmd.Execute(context.Background(), imageInvocation(map[string]string{"prompt": japanesePrompt, "aspect_ratio": "4:3"}))

	if err == nil || len(text.Requests) != 0 || len(images.TextRequests) != 0 {
		t.Errorf("Execute() error = %v, screenings = %d, generations = %d, want error and none", err, len(text.Requests), len(images.TextRequests))
	}
}

// 条件: 審査が拒否する。
// 内容: 理由を示す文言が成功として返り（error は nil）、生成は 0 回で、文言に原文を含まない。
func TestExecute_RejectedPrompt_ReturnsRejectionNoticeWithoutGenerating(t *testing.T) {
	text := &llmtest.Generator{Result: rejectVerdict}
	images := &imagegentest.Generator{Image: pngBytes}
	cmd := imagegen.New(text, images, nil)

	got, err := cmd.Execute(context.Background(), imageInvocation(map[string]string{"prompt": "猫の写真を撮って"}))

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := command.Result{Text: "この依頼は図以外の画像を求めているため、生成しませんでした。" +
		"生成できるのは、図解・模式図、フローチャート・構成図、アイコン・ピクトグラム・ロゴです。"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Execute() = %+v, want %+v", got, want)
	}
	if len(images.TextRequests) != 0 {
		t.Errorf("generations = %d, want 0", len(images.TextRequests))
	}
}

// 条件: 審査の結果を得られない（文章生成モデルが失敗する）。
// 内容: error が返り、生成は呼ばれない。
func TestExecute_ScreeningFails_ReturnsErrorWithoutGenerating(t *testing.T) {
	text := &llmtest.Generator{Err: errors.New("model unavailable")}
	images := &imagegentest.Generator{Image: pngBytes}
	cmd := imagegen.New(text, images, nil)

	_, err := cmd.Execute(context.Background(), imageInvocation(map[string]string{"prompt": japanesePrompt}))

	if err == nil || len(images.TextRequests) != 0 {
		t.Errorf("Execute() error = %v, generations = %d, want error and none", err, len(images.TextRequests))
	}
}

// 条件: 画像生成モデルが内容を理由に拒む（ErrContentFiltered）。
// 内容: 理由を示す文言が成功として返り（error は nil）、原文も画像も含まない。
func TestExecute_ContentFiltered_ReturnsContentNotice(t *testing.T) {
	text := &llmtest.Generator{Result: allowVerdict}
	images := &imagegentest.Generator{Err: imagegen.ErrContentFiltered}
	cmd := imagegen.New(text, images, nil)

	got, err := cmd.Execute(context.Background(), imageInvocation(map[string]string{"prompt": japanesePrompt}))

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := command.Result{Text: "内容が原因で、画像を生成できませんでした。文章または元にする画像を変えて、もう一度実行してください。"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Execute() = %+v, want %+v", got, want)
	}
}

// 条件: 画像生成が内容の拒否以外の理由で失敗する。
// 内容: その error が返る（基盤の失敗通知に任せる）。
func TestExecute_GenerationFails_ReturnsError(t *testing.T) {
	failure := errors.New("inference error")
	text := &llmtest.Generator{Result: allowVerdict}
	images := &imagegentest.Generator{Err: failure}
	cmd := imagegen.New(text, images, nil)

	_, err := cmd.Execute(context.Background(), imageInvocation(map[string]string{"prompt": japanesePrompt}))

	if !errors.Is(err, failure) {
		t.Errorf("Execute() error = %v, want %v", err, failure)
	}
}

// 条件: 元画像（64×64 の PNG）があり、aspect_ratio も指定する。
// 内容: FromSketch だけが 1 回呼ばれ（FromText は呼ばれない）、英語の指示文と取得した元画像がそのまま渡る。縦横比は読まないので、選択肢にない値でも error にならない。
func TestExecute_SourceGiven_CallsFromSketchWithoutAspectRatio(t *testing.T) {
	source := encodePNG(t, sourceSide, sourceSide)
	text := &llmtest.Generator{Result: allowVerdict}
	images := &imagegentest.Generator{Image: pngBytes}
	fetcher := &translatetest.AttachmentFetcher{Content: source}
	cmd := imagegen.New(text, images, fetcher)

	got, err := cmd.Execute(context.Background(), sourceInvocation(len(source), map[string]string{"prompt": japanesePrompt, "aspect_ratio": "4:3"}))

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := command.Result{Text: japanesePrompt, Files: []command.File{{Name: "image.png", Content: pngBytes}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Execute() = %+v, want %+v", got, want)
	}
	wantSketch := []imagegen.SketchRequest{{Prompt: englishPrompt, Source: source}}
	if !reflect.DeepEqual(images.SketchRequests, wantSketch) || len(images.TextRequests) != 0 {
		t.Errorf("SketchRequests = %+v, TextRequests = %+v, want %+v and none", images.SketchRequests, images.TextRequests, wantSketch)
	}
	if !reflect.DeepEqual(fetcher.URLs, []string{sourceURL}) {
		t.Errorf("fetched URLs = %v, want [%s]", fetcher.URLs, sourceURL)
	}
}

// 条件: 元画像があり、審査が許可する。
// 内容: 文章生成モデルへの依頼は 1 回で、UserText が利用者の文章だけになる（画像は渡らない）。
func TestExecute_SourceGiven_ScreensPromptOnly(t *testing.T) {
	source := encodePNG(t, sourceSide, sourceSide)
	text := &llmtest.Generator{Result: allowVerdict}
	images := &imagegentest.Generator{Image: pngBytes}
	cmd := imagegen.New(text, images, &translatetest.AttachmentFetcher{Content: source})

	_, _ = cmd.Execute(context.Background(), sourceInvocation(len(source), map[string]string{"prompt": japanesePrompt}))

	userTexts := make([]string, 0, len(text.Requests))
	for _, req := range text.Requests {
		userTexts = append(userTexts, req.UserText)
	}
	if want := []string{japanesePrompt}; !reflect.DeepEqual(userTexts, want) {
		t.Errorf("screening UserTexts = %q, want %q", userTexts, want)
	}
}

// 条件: Discord が伝えた元画像の大きさが上限（10,000,000 バイト）を 1 バイト超える。
// 内容: 受け付けられない旨の文言が成功として返り（error は nil）、取得も審査も生成もしない。
func TestExecute_SourceTooLarge_ReturnsSourceNoticeWithoutFetching(t *testing.T) {
	text := &llmtest.Generator{Result: allowVerdict}
	images := &imagegentest.Generator{Image: pngBytes}
	fetcher := &translatetest.AttachmentFetcher{}
	cmd := imagegen.New(text, images, fetcher)

	got, err := cmd.Execute(context.Background(), sourceInvocation(sourceBytesLimit+1, map[string]string{"prompt": japanesePrompt}))

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if want := (command.Result{Text: sourceNotice}); !reflect.DeepEqual(got, want) {
		t.Errorf("Execute() = %+v, want %+v", got, want)
	}
	if len(fetcher.URLs) != 0 || len(text.Requests) != 0 || len(images.SketchRequests) != 0 {
		t.Errorf("fetches = %d, screenings = %d, generations = %d, want none", len(fetcher.URLs), len(text.Requests), len(images.SketchRequests))
	}
}

// 条件: 取得した元画像が画像として読めない（テキスト）。
// 内容: 受け付けられない旨の文言が成功として返り（error は nil）、審査も生成もしない。
func TestExecute_SourceNotImage_ReturnsSourceNoticeWithoutScreening(t *testing.T) {
	notImage := []byte("not an image")
	text := &llmtest.Generator{Result: allowVerdict}
	images := &imagegentest.Generator{Image: pngBytes}
	cmd := imagegen.New(text, images, &translatetest.AttachmentFetcher{Content: notImage})

	got, err := cmd.Execute(context.Background(), sourceInvocation(len(notImage), map[string]string{"prompt": japanesePrompt}))

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if want := (command.Result{Text: sourceNotice}); !reflect.DeepEqual(got, want) {
		t.Errorf("Execute() = %+v, want %+v", got, want)
	}
	if len(text.Requests) != 0 || len(images.SketchRequests) != 0 {
		t.Errorf("screenings = %d, generations = %d, want none", len(text.Requests), len(images.SketchRequests))
	}
}

// 条件: 元画像の取得が失敗する。
// 内容: その error が返り、審査も生成もしない（基盤の失敗通知に任せる）。
func TestExecute_SourceFetchFails_ReturnsErrorWithoutScreening(t *testing.T) {
	failure := errors.New("fetch failed")
	text := &llmtest.Generator{Result: allowVerdict}
	images := &imagegentest.Generator{Image: pngBytes}
	cmd := imagegen.New(text, images, &translatetest.AttachmentFetcher{Err: failure})

	_, err := cmd.Execute(context.Background(), sourceInvocation(1, map[string]string{"prompt": japanesePrompt}))

	if !errors.Is(err, failure) || len(text.Requests) != 0 || len(images.SketchRequests) != 0 {
		t.Errorf("Execute() error = %v, screenings = %d, generations = %d, want %v and none", err, len(text.Requests), len(images.SketchRequests), failure)
	}
}

// 条件: 元画像が総画素数の上限（9,437,184）を超える 4032×3024 の JPEG。
// 内容: FromSketch に渡る画像は、総画素数が上限以下の JPEG になる。
func TestExecute_LargeSource_PassesShrunkSourceToGenerator(t *testing.T) {
	source := encodeJPEG(t, overModelPixelsWide, overModelPixelsHigh)
	text := &llmtest.Generator{Result: allowVerdict}
	images := &imagegentest.Generator{Image: pngBytes}
	cmd := imagegen.New(text, images, &translatetest.AttachmentFetcher{Content: source})

	_, _ = cmd.Execute(context.Background(), sourceInvocation(len(source), map[string]string{"prompt": japanesePrompt}))

	if len(images.SketchRequests) != 1 {
		t.Fatalf("SketchRequests = %d, want 1", len(images.SketchRequests))
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(images.SketchRequests[0].Source))
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" || config.Width*config.Height > maxModelPixels {
		t.Errorf("sent source = %s %dx%d, want jpeg within %d pixels", format, config.Width, config.Height, maxModelPixels)
	}
}

// 条件: 依存（llm・Generator・SourceFetcher）を渡さずに定義を取得する。
// 内容: サーバ内専用で、prompt（必須・最大 1000 文字）・aspect_ratio（5 つの選択肢）・
// source（任意の添付）・visibility（全員 / 実行者だけの 2 つ）を持つ定義になる。
func TestDefinition_WithoutDependencies_IsGuildOnlyWithPromptAspectRatioSourceAndVisibility(t *testing.T) {
	cmd := imagegen.New(nil, nil, nil)

	got, err := json.Marshal(cmd.Definition())

	want := `{"type":1,"name":"image","description":"文章から図を生成します",` +
		`"options":[` +
		`{"type":3,"name":"prompt","description":"図にしたい内容の文章","required":true,"max_length":1000},` +
		`{"type":3,"name":"aspect_ratio","description":"図の縦横比（省略時は 1:1）","choices":[` +
		`{"name":"1:1","value":"1:1"},{"name":"16:9","value":"16:9"},{"name":"3:2","value":"3:2"},` +
		`{"name":"2:3","value":"2:3"},{"name":"9:16","value":"9:16"}]},` +
		`{"type":11,"name":"source","description":"元にする画像（PNG または JPEG）"},` +
		`{"type":3,"name":"visibility","description":"結果を見られる人（省略時は全員）","choices":[` +
		`{"name":"全員","value":"public"},{"name":"実行者だけ","value":"private"}]}],` +
		`"contexts":[0]}`
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	assertJSONEqual(t, string(got), want)
}

func assertJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("got is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("want is not JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}
