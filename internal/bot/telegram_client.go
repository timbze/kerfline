package bot

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

const (
	// Telegram holds a getUpdates long-poll this long when the queue is empty.
	// Must stay below Telegram's 50s max. gotgbot warns that timeout=0 (or a
	// tiny HTTP deadline just above it) eventually makes Telegram delay replies.
	pollTelegramTimeout = 25
	// HTTP deadline for the whole getUpdates round trip, including the body.
	// Go does not apply request context to the body after headers arrive, so
	// this Client.Timeout is what stops a stalled long-poll from hanging forever.
	pollHTTPTimeout = 40 * time.Second
	// Slack so RTT + JSON decode still fit after Telegram's 25s hold.
	pollRequestTimeout = 35 * time.Second
	// Exit (systemd restarts) if getUpdates has not returned in this long.
	pollWatchdog   = 90 * time.Second
	apiHTTPTimeout = 90 * time.Second
)

// TelegramClient is a gotgbot BotClient with TCP keepalives and a hard timeout
// on getUpdates, including reading the response body.
type TelegramClient struct {
	poll     gotgbot.BaseBotClient
	api      gotgbot.BaseBotClient
	lastPoll atomic.Int64
}

func NewTelegramClient() *TelegramClient {
	return newTelegramClient(pollHTTPTimeout, apiHTTPTimeout)
}

func newTelegramClient(pollTimeout, apiTimeout time.Duration) *TelegramClient {
	c := &TelegramClient{
		poll: gotgbot.BaseBotClient{
			Client: http.Client{
				Transport: newTelegramTransport(pollRequestTimeout),
				Timeout:   pollTimeout,
			},
		},
		api: gotgbot.BaseBotClient{
			Client: http.Client{
				Transport: newTelegramTransport(0),
				Timeout:   apiTimeout,
			},
		},
	}
	c.touch()
	return c
}

func newTelegramTransport(responseHeaderTimeout time.Duration) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	t.TLSHandshakeTimeout = 10 * time.Second
	t.IdleConnTimeout = 90 * time.Second
	t.ResponseHeaderTimeout = responseHeaderTimeout
	return t
}

func (c *TelegramClient) RequestWithContext(ctx context.Context, token, method string, params map[string]any, opts *gotgbot.RequestOpts) (json.RawMessage, error) {
	if method == "getUpdates" {
		defer c.touch()
		return c.poll.RequestWithContext(ctx, token, method, params, opts)
	}
	return c.api.RequestWithContext(ctx, token, method, params, opts)
}

func (c *TelegramClient) GetAPIURL(opts *gotgbot.RequestOpts) string {
	return c.api.GetAPIURL(opts)
}

func (c *TelegramClient) FileURL(token, path string, opts *gotgbot.RequestOpts) string {
	return c.api.FileURL(token, path, opts)
}

func (c *TelegramClient) touch() {
	c.lastPoll.Store(time.Now().UnixNano())
}

func (c *TelegramClient) lastPollAt() time.Time {
	return time.Unix(0, c.lastPoll.Load())
}

func (c *TelegramClient) stale(now time.Time, max time.Duration) bool {
	return now.Sub(c.lastPollAt()) > max
}
