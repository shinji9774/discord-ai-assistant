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
	req := imagegen.TextRequest{Prompt: "a cat", AspectRatio: "16:9"}

	// Act
	got, err := fake.FromText(context.Background(), req)

	// Assert: 画像を返し、依頼は FromText の記録だけに残る
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, pngBytes) {
		t.Errorf("image = %q, want %q", got, pngBytes)
	}
	if want := []imagegen.TextRequest{req}; !reflect.DeepEqual(fake.TextRequests, want) {
		t.Errorf("TextRequests = %+v, want %+v", fake.TextRequests, want)
	}
	if len(fake.SketchRequests) != 0 {
		t.Errorf("SketchRequests = %+v, want empty", fake.SketchRequests)
	}
}

func TestGenerator_FromSketchWithImage_ReturnsImageAndRecordsRequest(t *testing.T) {
	// Arrange: 決めた画像を返す fake
	fake := &imagegentest.Generator{Image: pngBytes}
	req := imagegen.SketchRequest{Prompt: "a cat", Source: []byte("jpeg")}

	// Act
	got, err := fake.FromSketch(context.Background(), req)

	// Assert: 画像を返し、依頼は FromSketch の記録だけに残る
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, pngBytes) {
		t.Errorf("image = %q, want %q", got, pngBytes)
	}
	if want := []imagegen.SketchRequest{req}; !reflect.DeepEqual(fake.SketchRequests, want) {
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
	_, filteredErr := filtered.FromText(context.Background(), imagegen.TextRequest{})
	_, otherErr := other.FromSketch(context.Background(), imagegen.SketchRequest{})

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
	first := imagegen.TextRequest{Prompt: "first"}
	second := imagegen.TextRequest{Prompt: "second"}

	// Act
	_, _ = fake.FromText(context.Background(), first)
	_, _ = fake.FromText(context.Background(), second)

	// Assert
	if want := []imagegen.TextRequest{first, second}; !reflect.DeepEqual(fake.TextRequests, want) {
		t.Errorf("TextRequests = %+v, want %+v", fake.TextRequests, want)
	}
}
