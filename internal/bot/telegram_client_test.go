package bot

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

func TestGetUpdatesTimesOutStalledBody(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "getUpdates") {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	c := newTelegramClient(200*time.Millisecond, time.Second)
	c.poll.DefaultRequestOpts = &gotgbot.RequestOpts{APIURL: srv.URL, Timeout: -1}

	start := time.Now()
	_, err := c.RequestWithContext(context.Background(), "tok", "getUpdates", map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("hung %s, want Client.Timeout to abort stalled body", time.Since(start))
	}
	select {
	case <-started:
	default:
		t.Fatal("server never saw getUpdates")
	}
}

func TestGetUpdatesTouchClearsStale(t *testing.T) {
	c := newTelegramClient(time.Second, time.Second)
	c.lastPoll.Store(time.Now().Add(-time.Hour).UnixNano())
	if !c.stale(time.Now(), pollWatchdog) {
		t.Fatal("expected stale")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
	}))
	defer srv.Close()
	c.poll.DefaultRequestOpts = &gotgbot.RequestOpts{APIURL: srv.URL, Timeout: time.Second}
	if _, err := c.RequestWithContext(context.Background(), "tok", "getUpdates", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	if c.stale(time.Now(), pollWatchdog) {
		t.Fatal("getUpdates should have touched lastPoll")
	}
}

func TestPollTimeoutsHaveSlack(t *testing.T) {
	if time.Duration(pollTelegramTimeout)*time.Second >= pollRequestTimeout {
		t.Fatalf("HTTP request timeout %s must exceed Telegram hold %ds", pollRequestTimeout, pollTelegramTimeout)
	}
	if pollRequestTimeout >= pollHTTPTimeout {
		t.Fatalf("Client.Timeout %s must exceed request timeout %s so body reads are still bounded", pollHTTPTimeout, pollRequestTimeout)
	}
	if pollHTTPTimeout*2 > pollWatchdog {
		t.Fatalf("watchdog %s should allow at least two poll HTTP timeouts", pollWatchdog)
	}
}
