package imagegentest_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"discord-ai-assistant/internal/imagegen"
	"discord-ai-assistant/internal/imagegen/imagegentest"
)

var (
	pngBytes            = []byte("png")
	errGenerationFailed = errors.New("boom")
)

func TestGenerator_FromTextWithImage_ReturnsImageAndRecordsRequest(t *testing.T) {
	// Arrange: 決めた画像を返す fake
	fake := &imagegentest.Generator{Image: pngBytes}
	prompt := imagegen.Prompt{Positive: "a cat", Negative: "dog"}
	const aspectRatio = "16:9"

	// Act
	got, err := fake.FromText(context.Background(), prompt, aspectRatio)

	// Assert: 画像を返し、依頼は FromText の記録だけに残る
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, pngBytes) {
		t.Errorf("image = %q, want %q", got, pngBytes)
	}
	if want := []imagegentest.TextCall{{Prompt: prompt, AspectRatio: aspectRatio}}; !reflect.DeepEqual(fake.TextRequests, want) {
		t.Errorf("TextRequests = %+v, want %+v", fake.TextRequests, want)
	}
	if len(fake.SketchRequests) != 0 {
		t.Errorf("SketchRequests = %+v, want empty", fake.SketchRequests)
	}
}

func TestGenerator_FromSketchWithImage_ReturnsImageAndRecordsRequest(t *testing.T) {
	// Arrange: 決めた画像を返す fake
	fake := &imagegentest.Generator{Image: pngBytes}
	prompt := imagegen.Prompt{Positive: "a cat", Negative: "dog"}
	source := []byte("jpeg")

	// Act
	got, err := fake.FromSketch(context.Background(), prompt, source)

	// Assert: 画像を返し、依頼は FromSketch の記録だけに残る
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, pngBytes) {
		t.Errorf("image = %q, want %q", got, pngBytes)
	}
	if want := []imagegentest.SketchCall{{Prompt: prompt, Source: source}}; !reflect.DeepEqual(fake.SketchRequests, want) {
		t.Errorf("SketchRequests = %+v, want %+v", fake.SketchRequests, want)
	}
	if len(fake.TextRequests) != 0 {
		t.Errorf("TextRequests = %+v, want empty", fake.TextRequests)
	}
}

func TestGenerator_ContentFilteredErr_IsDistinguishableFromOtherErrors(t *testing.T) {
	// Arrange: 内容による拒否と、それ以外の失敗を返す fake
	filtered := &imagegentest.Generator{Err: imagegen.ErrContentFiltered}
	other := &imagegentest.Generator{Err: errGenerationFailed}

	// Act
	_, filteredErr := filtered.FromText(context.Background(), imagegen.Prompt{}, "")
	_, otherErr := other.FromSketch(context.Background(), imagegen.Prompt{}, nil)

	// Assert: 拒否だけが ErrContentFiltered と判別でき、失敗のとき画像は返らない
	if !errors.Is(filteredErr, imagegen.ErrContentFiltered) {
		t.Errorf("filteredErr = %v, want ErrContentFiltered", filteredErr)
	}
	if errors.Is(otherErr, imagegen.ErrContentFiltered) || !errors.Is(otherErr, errGenerationFailed) {
		t.Errorf("otherErr = %v, want errGenerationFailed only", otherErr)
	}
}

func TestGenerator_CalledTwice_RecordsRequestsInOrder(t *testing.T) {
	// Arrange
	fake := &imagegentest.Generator{Image: pngBytes}
	first := imagegen.Prompt{Positive: "first"}
	second := imagegen.Prompt{Positive: "second"}

	// Act
	_, _ = fake.FromText(context.Background(), first, "1:1")
	_, _ = fake.FromText(context.Background(), second, "1:1")

	// Assert
	if want := []imagegentest.TextCall{{Prompt: first, AspectRatio: "1:1"}, {Prompt: second, AspectRatio: "1:1"}}; !reflect.DeepEqual(fake.TextRequests, want) {
		t.Errorf("TextRequests = %+v, want %+v", fake.TextRequests, want)
	}
}
