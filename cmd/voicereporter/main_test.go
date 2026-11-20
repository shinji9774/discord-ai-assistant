package main

import (
	"context"
	"testing"
	"time"
)

// 条件: Lambda の期限がある ctx。
// 内容: 期限の 30 秒前を期限にした ctx を返す。
func TestWithCleanupReserve_LambdaDeadline_EndsThirtySecondsEarlier(t *testing.T) {
	lambdaDeadline := time.Now().Add(15 * time.Minute)
	parent, cancelParent := context.WithDeadline(t.Context(), lambdaDeadline)
	defer cancelParent()

	ctx, cancel := withCleanupReserve(parent)
	defer cancel()

	if got, _ := ctx.Deadline(); !got.Equal(lambdaDeadline.Add(-30 * time.Second)) {
		t.Errorf("Deadline() = %v; want %v", got, lambdaDeadline.Add(-30*time.Second))
	}
}
