package bedrockfake_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"discord-ai-assistant/test/bedrockfake"
)

const (
	coreModelID    = "stability.stable-image-core-v1:1"
	sketchModelID  = "us.stability.stable-image-control-sketch-v1:0"
	invokeBody     = `{"prompt":"a cat","output_format":"png"}`
	usWest2Auth    = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20261118/us-west-2/bedrock/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc"
	filterOnPrompt = "Filter reason: prompt"
)

type invokeResponse struct {
	Images        []*string `json:"images"`
	Seeds         []int     `json:"seeds"`
	FinishReasons []*string `json:"finish_reasons"`
}

func postInvoke(t *testing.T, srv *httptest.Server, modelID, auth, body string) invokeResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/model/"+modelID+"/invoke", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	var got invokeResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal %s: %v", raw, err)
	}
	return got
}

func newServer(t *testing.T) (*bedrockfake.Server, *httptest.Server) {
	t.Helper()
	fake := bedrockfake.New()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return fake, srv
}

func TestInvoke_WithConfiguredImage_ReturnsBase64ImageAndNullFinishReason(t *testing.T) {
	// Arrange: 決めた画像を返す設定
	fake, srv := newServer(t)
	png := []byte("png-bytes")
	fake.RespondWithImage(png)

	// Act
	got := postInvoke(t, srv, coreModelID, usWest2Auth, invokeBody)

	// Assert: images[0] が決めた画像の base64、finish_reasons[0] が null
	want := base64.StdEncoding.EncodeToString(png)
	if len(got.Images) != 1 || got.Images[0] == nil || *got.Images[0] != want {
		t.Errorf("images = %v, want [%q]", got.Images, want)
	}
	if len(got.FinishReasons) != 1 || got.FinishReasons[0] != nil {
		t.Errorf("finish_reasons = %v, want [null]", got.FinishReasons)
	}
}

func TestInvoke_WithContentFilterReason_ReturnsFinishReasonWithoutImage(t *testing.T) {
	// Arrange: 内容による拒否を返す設定
	fake, srv := newServer(t)
	fake.RespondWithFinishReason(filterOnPrompt)

	// Act
	got := postInvoke(t, srv, coreModelID, usWest2Auth, invokeBody)

	// Assert: 画像が無く、finish_reasons[0] が決めた理由
	if len(got.Images) != 0 {
		t.Errorf("images = %v, want empty", got.Images)
	}
	if len(got.FinishReasons) != 1 || got.FinishReasons[0] == nil || *got.FinishReasons[0] != filterOnPrompt {
		t.Errorf("finish_reasons = %v, want [%q]", got.FinishReasons, filterOnPrompt)
	}
}

func TestInvoke_WithSigV4Authorization_RecordsModelIDRegionAndBody(t *testing.T) {
	// Arrange: us-west-2 の credential scope を持つ署名で Core を呼ぶ
	fake, srv := newServer(t)

	// Act
	postInvoke(t, srv, coreModelID, usWest2Auth, invokeBody)

	// Assert: モデル ID、credential scope から読んだリージョン、本文が記録される
	want := []bedrockfake.InvokeRequest{{ModelID: coreModelID, Region: "us-west-2", Body: invokeBody}}
	if got := fake.InvokeRequests(); !reflect.DeepEqual(got, want) {
		t.Errorf("InvokeRequests() = %+v, want %+v", got, want)
	}
}

func TestInvoke_EscapedModelID_RecordsUnescapedModelID(t *testing.T) {
	// Arrange: コロンを含むモデル ID を %3A でエスケープして呼ぶ
	fake, srv := newServer(t)

	// Act
	postInvoke(t, srv, strings.ReplaceAll(sketchModelID, ":", "%3A"), usWest2Auth, invokeBody)

	// Assert: エスケープを戻したモデル ID が記録される
	want := []bedrockfake.InvokeRequest{{ModelID: sketchModelID, Region: "us-west-2", Body: invokeBody}}
	if got := fake.InvokeRequests(); !reflect.DeepEqual(got, want) {
		t.Errorf("InvokeRequests() = %+v, want %+v", got, want)
	}
}

func TestReset_AfterInvoke_ClearsInvokeRequestsAndConfiguredResponse(t *testing.T) {
	// Arrange: 設定と記録がある状態
	fake, srv := newServer(t)
	fake.RespondWithFinishReason(filterOnPrompt)
	postInvoke(t, srv, coreModelID, usWest2Auth, invokeBody)

	// Act
	fake.Reset()

	// Assert: 記録が空で、応答は既定の画像に戻る
	if got := fake.InvokeRequests(); len(got) != 0 {
		t.Errorf("InvokeRequests() = %+v, want empty", got)
	}
	got := postInvoke(t, srv, coreModelID, usWest2Auth, invokeBody)
	if len(got.Images) != 1 || got.Images[0] == nil {
		t.Errorf("images = %v, want default image", got.Images)
	}
}
