// Package discordapi は disgo の REST クライアントの共通設定を持つ。
package discordapi

import (
	"log/slog"

	"github.com/disgoorg/disgo/rest"
)

// EnvBaseURL は Discord の API の URL を差し替える環境変数名（テスト用）。
const EnvBaseURL = "DISCORD_API_BASE_URL"

// ClientOpts は rest.NewClient に渡す共通のオプションを返す。baseURL が空なら本番の URL を使う。
// disgo の既定ロガーは 429 などで interaction token を含む URL を出すため、ログは捨てる。
// rate limiter のロガーはクライアントとは別設定なので両方に渡す。
func ClientOpts(baseURL string) []rest.ClientConfigOpt {
	discard := slog.New(slog.DiscardHandler)
	opts := []rest.ClientConfigOpt{
		rest.WithLogger(discard),
		rest.WithRateLimiterConfigOpts(rest.WithRateLimiterLogger(discard)),
	}
	if baseURL != "" {
		opts = append(opts, rest.WithURL(baseURL))
	}
	return opts
}
