package check_test

import (
	"errors"
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

// 条件: 値が 0。
// 内容: キー名と値を含むエラーになる。
func TestPositive_Zero_ReturnsErrorWithKeyAndValue(t *testing.T) {
	err := check.Positive("max_searches", 0)

	if got, want := errorText(err), "max_searches: must be 1 or greater (got 0)"; got != want {
		t.Errorf("Positive error = %q, want %q", got, want)
	}
}

// 条件: 値が 1。
// 内容: エラーにならない。
func TestPositive_One_ReturnsNil(t *testing.T) {
	err := check.Positive("max_searches", 1)

	if got := errorText(err); got != noError {
		t.Errorf("Positive error = %q, want %q", got, noError)
	}
}

// 条件: 値が空白だけ。
// 内容: キー名を含むエラーになる。
func TestNotEmpty_BlankOnly_ReturnsErrorWithKey(t *testing.T) {
	err := check.NotEmpty("text_model_id", "  ")

	if got, want := errorText(err), "text_model_id: must not be empty"; got != want {
		t.Errorf("NotEmpty error = %q, want %q", got, want)
	}
}

// 条件: 2 つの問題をまとめたエラー。
// 内容: それぞれのキーの前に、親のキーが付く。
func TestUnder_JoinedErrors_PrefixesEachKey(t *testing.T) {
	joined := errors.Join(errors.New("a: x"), errors.New("b: y"))

	err := check.Under("ask", joined)

	if got, want := errorText(err), "ask.a: x\nask.b: y"; got != want {
		t.Errorf("Under error = %q, want %q", got, want)
	}
}

// 条件: 問題が無い（nil）。
// 内容: nil のまま返す。
func TestUnder_Nil_ReturnsNil(t *testing.T) {
	err := check.Under("ask", nil)

	if got := errorText(err); got != noError {
		t.Errorf("Under error = %q, want %q", got, noError)
	}
}
