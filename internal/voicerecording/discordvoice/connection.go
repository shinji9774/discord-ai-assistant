// Package discordvoice は、voicerecording.VoiceConnection を disgo（Gateway・Voice）と dave-go（DAVE の復号）で実装する。
// Gateway と DAVE の依存をここに閉じ込めるため、import してよいのは cmd/recorder と tools/voiceprobe だけである。
package discordvoice

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"github.com/thomas-vilte/dave-go/session"

	"discord-ai-assistant/internal/voicerecording"
)

const (
	selfMute = true
	selfDeaf = false
	// unknownSpeaker は、話者を特定できないフレームのユーザー ID。
	unknownSpeaker   snowflake.ID = 0
	humanCountBuffer              = 1
	// joinCleanupTimeout は、Join の失敗後に開いたものを閉じる時間の上限。
	joinCleanupTimeout = 10 * time.Second
)

// Connection は、1 つのサーバのボイスチャンネルへの接続。
// Join・Receive・Leave は、1 つの goroutine から順に呼ぶ。
type Connection struct {
	client         *bot.Client
	conn           voice.Conn
	receiver       *frameReceiver
	closeOnce      sync.Once
	humans         chan int
	disconnected   chan struct{}
	disconnectOnce sync.Once
	leaving        atomic.Bool

	mu        sync.Mutex
	guildID   snowflake.ID
	channelID snowflake.ID
}

var _ voicerecording.VoiceConnection = (*Connection)(nil)

func New(botToken string, logger *slog.Logger) (*Connection, error) {
	c := &Connection{humans: make(chan int, humanCountBuffer), disconnected: make(chan struct{})}
	client, err := disgo.New(botToken,
		bot.WithGatewayConfigOpts(gateway.WithIntents(gateway.IntentGuilds, gateway.IntentGuildVoiceStates)),
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagVoiceStates, cache.FlagMembers)),
		bot.WithVoiceManagerConfigOpts(voice.WithDaveSessionCreateFunc(session.CreateFunc())),
		bot.WithEventListenerFunc(c.onVoiceStateUpdate),
		bot.WithLogger(logger),
	)
	if err != nil {
		return nil, fmt.Errorf("create discord client: %w", err)
	}
	c.client = client
	return c, nil
}

func (c *Connection) Join(ctx context.Context, guildID, channelID string) error {
	gid, err := snowflake.Parse(guildID)
	if err != nil {
		return fmt.Errorf("parse guild id: %w", err)
	}
	cid, err := snowflake.Parse(channelID)
	if err != nil {
		return fmt.Errorf("parse channel id: %w", err)
	}
	if err := c.client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("open gateway: %w", err)
	}
	c.conn = c.client.VoiceManager.CreateConn(gid)
	if err := c.conn.Open(ctx, cid, selfMute, selfDeaf); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), joinCleanupTimeout)
		defer cancel()
		c.closeAll(cleanupCtx)
		return fmt.Errorf("open voice connection: %w", err)
	}
	c.mu.Lock()
	c.guildID, c.channelID = gid, cid
	c.mu.Unlock()
	c.publishHumanCount()
	return nil
}

func (c *Connection) Receive(frames voicerecording.FrameSink) {
	c.receiver = &frameReceiver{sink: frames, onClose: c.markDisconnected}
	c.conn.SetOpusFrameReceiver(c.receiver)
}

func (c *Connection) HumanCount() <-chan int {
	return c.humans
}

func (c *Connection) Disconnected() <-chan struct{} {
	return c.disconnected
}

func (c *Connection) Leave(ctx context.Context) {
	c.leaving.Store(true)
	if c.receiver != nil {
		c.receiver.stop()
	}
	c.closeAll(ctx)
}

// closeAll は、開いた接続と Gateway を 1 度だけ閉じる。Join が失敗した後や、Join の前でも呼べる。
func (c *Connection) closeAll(ctx context.Context) {
	c.closeOnce.Do(func() {
		if c.conn != nil {
			c.conn.Close(ctx)
		}
		c.client.Close(ctx)
	})
}

func (c *Connection) onVoiceStateUpdate(*events.GuildVoiceStateUpdate) {
	c.publishHumanCount()
}

// publishHumanCount は、最新の参加者数だけを残して流す。
func (c *Connection) publishHumanCount() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.channelID == 0 {
		return
	}
	select {
	case <-c.humans:
	default:
	}
	c.humans <- c.countHumans()
}

func (c *Connection) countHumans() int {
	humans := 0
	for state := range c.client.Caches.VoiceStates(c.guildID) {
		if state.ChannelID == nil || *state.ChannelID != c.channelID || c.isBot(state.UserID) {
			continue
		}
		humans++
	}
	return humans
}

func (c *Connection) isBot(userID snowflake.ID) bool {
	member, ok := c.client.Caches.Member(c.guildID, userID)
	return userID == c.client.ID() || (ok && member.User.Bot)
}

func (c *Connection) markDisconnected() {
	if c.leaving.Load() {
		return
	}
	c.disconnectOnce.Do(func() { close(c.disconnected) })
}

// frameReceiver は、disgo の受信ループ（DAVE の復号まで済んだ Opus）を FrameSink につなぐ。
type frameReceiver struct {
	sink    voicerecording.FrameSink
	onClose func()

	mu      sync.Mutex
	stopped bool
}

// stop は、処理中の ReceiveOpusFrame が終わるのを待ち、以後のフレームを捨てる。
func (r *frameReceiver) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
}

func (r *frameReceiver) ReceiveOpusFrame(userID snowflake.ID, packet *voice.Packet) error {
	if userID == unknownSpeaker {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return nil
	}
	r.sink.WriteFrame(userID.String(), packet.Opus, time.Now())
	return nil
}

func (r *frameReceiver) CleanupUser(snowflake.ID) {}

func (r *frameReceiver) Close() {
	r.onClose()
}
