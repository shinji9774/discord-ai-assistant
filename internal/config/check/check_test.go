package check_test

import (
	"testing"

	"discord-ai-assistant/internal/config/check"
)

const noError = "<nil>"

func errorText(err error) string {
	if err == nil {
		return noError
	}
	return err.Error()
}

type rangeSample struct {
	Count int `yaml:"count" validate:"min=1,max=25"`
}

type notBlankSample struct {
	Name string `yaml:"name" validate:"notblank"`
}

type containsSample struct {
	Prompt string `yaml:"prompt" validate:"contains={today}"`
}

type httpURLSample struct {
	URL string `yaml:"url" validate:"http_url"`
}

type innerSample struct {
	Limit int `yaml:"limit,omitempty" validate:"min=1"`
}

type outerSample struct {
	Name  string      `yaml:"name" validate:"notblank"`
	Inner innerSample `yaml:"inner"`
}

// 条件: 各タグ（min / max / notblank / contains / http_url）に違反する値を 1 つずつ渡す。
// 内容: タグごとの文言で、yaml 名のキーつきのエラーになる。
func TestStruct_ViolatingEachTag_ReturnsMessageForTag(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"min", rangeSample{Count: 0}, "count: must be 1 or greater (got 0)"},
		{"max", rangeSample{Count: 26}, "count: must be 25 or less (got 26)"},
		{"notblank", notBlankSample{Name: "  "}, "name: must not be empty"},
		{"contains", containsSample{Prompt: "no placeholder"}, "prompt: must contain {today}"},
		{"http_url", httpURLSample{URL: "craig.horse"}, `url: must be an absolute http or https URL (got "craig.horse")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errorText(check.Struct(tt.value))

			if got != tt.want {
				t.Errorf("Struct error = %q, want %q", got, tt.want)
			}
		})
	}
}

// 条件: ネストした構造体のフィールドが違反している。
// 内容: キーは、先頭の型名を除き、yaml 名を "." でつないだ形になる。
func TestStruct_NestedFieldViolation_ReturnsDottedYAMLKey(t *testing.T) {
	value := outerSample{Name: "bot", Inner: innerSample{Limit: 0}}

	err := check.Struct(value)

	if got, want := errorText(err), "inner.limit: must be 1 or greater (got 0)"; got != want {
		t.Errorf("Struct error = %q, want %q", got, want)
	}
}

// 条件: 複数のフィールドが違反している。
// 内容: 違反を全件、フィールド定義順に改行でつないで返す。
func TestStruct_MultipleViolations_ReturnsAllJoined(t *testing.T) {
	value := outerSample{Name: "", Inner: innerSample{Limit: 0}}

	err := check.Struct(value)

	want := "name: must not be empty\n" +
		"inner.limit: must be 1 or greater (got 0)"
	if got := errorText(err); got != want {
		t.Errorf("Struct error = %q, want %q", got, want)
	}
}

// 条件: すべての値が検証を通る。
// 内容: nil を返す。
func TestStruct_NoViolation_ReturnsNil(t *testing.T) {
	value := outerSample{Name: "bot", Inner: innerSample{Limit: 1}}

	err := check.Struct(value)

	if got := errorText(err); got != noError {
		t.Errorf("Struct error = %q, want %q", got, noError)
	}
}
