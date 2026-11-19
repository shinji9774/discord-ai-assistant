// Package check は、設定値の検証で使う、キー名つきのエラーを作る。
package check

import (
	"errors"
	"fmt"
	"strings"
)

// Number は、1 以上かどうかを確かめられる数値の型。
type Number interface {
	~int | ~int32 | ~float64
}

const (
	minPositive = 1

	notPositiveFormat = "%s: must be 1 or greater (got %v)"
	emptyFormat       = "%s: must not be empty"
	prefixedFormat    = "%s.%s"
)

// Positive は value が 1 以上であることを確かめる。
func Positive[T Number](key string, value T) error {
	if value < minPositive {
		return fmt.Errorf(notPositiveFormat, key, value)
	}
	return nil
}

// NotEmpty は value が空白だけでないことを確かめる。
func NotEmpty(key, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf(emptyFormat, key)
	}
	return nil
}

// Under は err の中の各問題のキーの前に prefix を付ける。err が nil なら nil を返す。
func Under(prefix string, err error) error {
	if err == nil {
		return nil
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return fmt.Errorf(prefixedFormat, prefix, err.Error())
	}
	var prefixed []error
	for _, inner := range joined.Unwrap() {
		prefixed = append(prefixed, Under(prefix, inner))
	}
	return errors.Join(prefixed...)
}
