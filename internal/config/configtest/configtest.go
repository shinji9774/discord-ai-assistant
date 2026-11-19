// Package configtest は、同梱の設定をテストで読むためのヘルパーを提供する。
package configtest

import (
	"testing"

	"discord-ai-assistant/internal/config"
)

// Load は同梱の設定を読む。読めなければテストを失敗させる。
func Load(t testing.TB) config.Config {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}
