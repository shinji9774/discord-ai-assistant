package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"discord-ai-assistant/test/bedrockfake"
)

const (
	stabilityTestPrompt       = "A flowchart of a login flow."
	stabilityTestAspectRatio  = "16:9"
	stabilityTestUserNegative = "text, people"
)

var (
	stabilityTestImage  = []byte("generated png")
	stabilityTestSource = []byte("source sketch")
)

// newStabilityGenerator は本物の Bedrock SDK クライアントを fake サーバに向けた StabilityGenerator を作る。
func newStabilityGenerator(t *testing.T) (*StabilityGenerator, *bedrockfake.Server) {
	t.Helper()
	fake := bedrockfake.New()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := bedrockruntime.New(bedrockruntime.Options{
		Region:           testConfig.BedrockRegion,
		BaseEndpoint:     &server.URL,
		Credentials:      credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
		RetryMaxAttempts: 1,
	})
	return NewStabilityGenerator(client, testConfig), fake
}

// canonicalJSON は JSON をキー順の揃った文字列にして、本文の全体比較を差分で読めるようにする。
func canonicalJSON(t *testing.T, raw string) string {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", raw, err)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(canonical)
}

// assertInvokeRequest は fake が受けた InvokeModel 依頼が 1 件で、モデル ID・リージョン・本文が期待どおりかを比べる。
func assertInvokeRequest(t *testing.T, fake *bedrockfake.Server, wantModelID string, wantBody map[string]any) {
	t.Helper()
	got := fake.InvokeRequests()
	if len(got) != 1 {
		t.Fatalf("len(InvokeRequests()) = %d, want 1", len(got))
	}
	wantJSON, err := json.Marshal(wantBody)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if got[0].ModelID != wantModelID || got[0].Region != testConfig.BedrockRegion {
		t.Errorf("model/region = %q/%q, want %q/%q", got[0].ModelID, got[0].Region, wantModelID, testConfig.BedrockRegion)
	}
	if gotBody, want := canonicalJSON(t, got[0].Body), canonicalJSON(t, string(wantJSON)); gotBody != want {
		t.Errorf("body = %s, want %s", gotBody, want)
	}
}

// 条件: 指示文と縦横比を指定して FromText を呼ぶ。
// 内容: Stable Image Core に、指示文の末尾へ画風の句を足した prompt、aspect_ratio、設定の固定値だけの negative_prompt、output_format png を送り、画像を返す。
func TestFromText_BuildsCoreRequest(t *testing.T) {
	generator, fake := newStabilityGenerator(t)
	fake.RespondWithImage(stabilityTestImage)

	got, err := generator.FromText(context.Background(), Prompt{Positive: stabilityTestPrompt}, stabilityTestAspectRatio)

	if err != nil {
		t.Fatalf("FromText() error = %v", err)
	}
	if string(got) != string(stabilityTestImage) {
		t.Errorf("FromText() = %q, want %q", got, stabilityTestImage)
	}
	assertInvokeRequest(t, fake, "stability.stable-image-core-v1:1", map[string]any{
		"prompt":          stabilityTestPrompt + testConfig.DiagramStyleSuffix,
		"aspect_ratio":    stabilityTestAspectRatio,
		"negative_prompt": testConfig.NegativePrompt,
		"output_format":   "png",
	})
}

// 条件: 指示文と元の画像を指定して FromSketch を呼ぶ。
// 内容: Control Sketch の推論プロファイルに、prompt、base64 の image、negative_prompt、output_format png だけを送る（aspect_ratio は含めない）。
func TestFromSketch_BuildsSketchRequestWithBase64Image(t *testing.T) {
	generator, fake := newStabilityGenerator(t)
	fake.RespondWithImage(stabilityTestImage)

	got, err := generator.FromSketch(context.Background(), Prompt{Positive: stabilityTestPrompt}, stabilityTestSource)

	if err != nil {
		t.Fatalf("FromSketch() error = %v", err)
	}
	if string(got) != string(stabilityTestImage) {
		t.Errorf("FromSketch() = %q, want %q", got, stabilityTestImage)
	}
	assertInvokeRequest(t, fake, "us.stability.stable-image-control-sketch-v1:0", map[string]any{
		"prompt":          stabilityTestPrompt + testConfig.DiagramStyleSuffix,
		"image":           base64.StdEncoding.EncodeToString(stabilityTestSource),
		"negative_prompt": testConfig.NegativePrompt,
		"output_format":   "png",
	})
}

// 条件: 利用者が避けたい要素（Negative）を持つ Prompt で FromText と FromSketch を呼ぶ。
// 内容: どちらも negative_prompt が、設定の固定値の後ろに ", " で利用者分を足した文字列になる。
func TestFromText_UserNegativePrompt_AppendsToConfiguredNegativePrompt(t *testing.T) {
	prompt := Prompt{Positive: stabilityTestPrompt, Negative: stabilityTestUserNegative}
	wantNegative := testConfig.NegativePrompt + ", " + stabilityTestUserNegative
	generator, fake := newStabilityGenerator(t)
	fake.RespondWithImage(stabilityTestImage)

	_, _ = generator.FromText(context.Background(), prompt, stabilityTestAspectRatio)
	_, _ = generator.FromSketch(context.Background(), prompt, stabilityTestSource)

	var gotNegatives []string
	for _, request := range fake.InvokeRequests() {
		var body struct {
			NegativePrompt string `json:"negative_prompt"`
		}
		if err := json.Unmarshal([]byte(request.Body), &body); err != nil {
			t.Fatalf("json.Unmarshal(%q) error = %v", request.Body, err)
		}
		gotNegatives = append(gotNegatives, body.NegativePrompt)
	}
	if want := []string{wantNegative, wantNegative}; !reflect.DeepEqual(gotNegatives, want) {
		t.Errorf("negative_prompt = %q, want %q", gotNegatives, want)
	}
}

// 条件: 終了理由が 3 種の Filter reason のいずれかである。
// 内容: FromText も FromSketch も ErrContentFiltered を返す。
func TestFromText_FilterReason_ReturnsErrContentFiltered(t *testing.T) {
	for _, reason := range []string{"Filter reason: prompt", "Filter reason: output image", "Filter reason: input image"} {
		t.Run(reason, func(t *testing.T) {
			generator, fake := newStabilityGenerator(t)
			fake.RespondWithFinishReason(reason)

			_, textErr := generator.FromText(context.Background(), Prompt{Positive: stabilityTestPrompt}, stabilityTestAspectRatio)
			_, sketchErr := generator.FromSketch(context.Background(), Prompt{Positive: stabilityTestPrompt}, stabilityTestSource)

			if !errors.Is(textErr, ErrContentFiltered) || !errors.Is(sketchErr, ErrContentFiltered) {
				t.Errorf("errors = %v, %v, want both ErrContentFiltered", textErr, sketchErr)
			}
		})
	}
}

// 条件: 終了理由が Inference error である。
// 内容: ErrContentFiltered ではない通常の error を返す。
func TestFromText_InferenceError_ReturnsError(t *testing.T) {
	generator, fake := newStabilityGenerator(t)
	fake.RespondWithFinishReason("Inference error")

	_, err := generator.FromText(context.Background(), Prompt{Positive: stabilityTestPrompt}, stabilityTestAspectRatio)

	if err == nil || errors.Is(err, ErrContentFiltered) {
		t.Errorf("FromText() error = %v, want non-filter error", err)
	}
}

// 条件: 終了理由が null なのに画像が 1 枚も無い。
// 内容: ErrContentFiltered ではない通常の error を返す。
func TestFromText_NoImage_ReturnsError(t *testing.T) {
	generator, fake := newStabilityGenerator(t)
	fake.RespondWithImage(nil)

	_, err := generator.FromText(context.Background(), Prompt{Positive: stabilityTestPrompt}, stabilityTestAspectRatio)

	if err == nil || errors.Is(err, ErrContentFiltered) {
		t.Errorf("FromText() error = %v, want non-filter error", err)
	}
}
