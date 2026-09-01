package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

func TestFormatChatIDReply(t *testing.T) {
	msg := &gotgbot.Message{
		Chat: gotgbot.Chat{Id: -1001},
		From: &gotgbot.User{Id: 42},
	}
	got := formatChatIDReply(msg)
	if got != "chat_id = -1001\nuser_id = 42" {
		t.Fatalf("plain: %q", got)
	}
	if strings.Contains(got, "is_topic_message") || strings.Contains(got, "is_forum") {
		t.Fatalf("v1 must not print debug booleans: %q", got)
	}

	msg.MessageThreadId = 3
	got = formatChatIDReply(msg)
	want := "chat_id = -1001\nuser_id = 42\ntopic_id = 3"
	if got != want {
		t.Fatalf("topic: got %q want %q", got, want)
	}
	if strings.Contains(got, "is_topic_message") || strings.Contains(got, "is_forum") {
		t.Fatalf("v1 must not print debug booleans: %q", got)
	}
}

func TestReplyOpts(t *testing.T) {
	omit := []gotgbot.Message{
		{},
		{MessageThreadId: 1, IsTopicMessage: true},
		{MessageThreadId: 3, IsTopicMessage: false},
		{MessageThreadId: 0, IsTopicMessage: true},
	}
	for _, msg := range omit {
		if id := topicThreadID(&msg); id != 0 {
			t.Fatalf("should omit thread on %+v, got %d", msg, id)
		}
		if opts := replyOpts(&msg); opts.MessageThreadId != 0 {
			t.Fatalf("plain should omit thread on %+v, got %d", msg, opts.MessageThreadId)
		}
		if opts := richReplyOpts(&msg); opts.MessageThreadId != 0 {
			t.Fatalf("rich should omit thread on %+v, got %d", msg, opts.MessageThreadId)
		}
	}
	topic := &gotgbot.Message{MessageThreadId: 3, IsTopicMessage: true}
	if topicThreadID(topic) != 3 {
		t.Fatalf("user topic: got %d", topicThreadID(topic))
	}
	if opts := replyOpts(topic); opts.MessageThreadId != 3 {
		t.Fatalf("plain user topic: got %d", opts.MessageThreadId)
	}
	if opts := richReplyOpts(topic); opts.MessageThreadId != 3 {
		t.Fatalf("rich user topic: got %d", opts.MessageThreadId)
	}
}

func TestSplitTelegram(t *testing.T) {
	if got := splitTelegram("  hi  ", 10); len(got) != 1 || got[0] != "hi" {
		t.Fatalf("short: %q", got)
	}
	long := strings.Repeat("a\n", 20)
	parts := splitTelegram(long, 10)
	if len(parts) < 2 {
		t.Fatalf("expected split, got %d parts", len(parts))
	}
	for _, p := range parts {
		if len(p) > 10 {
			t.Fatalf("chunk too long: %d", len(p))
		}
	}
	joined := strings.ReplaceAll(strings.Join(parts, ""), "\n", "")
	want := strings.Repeat("a", 20)
	if joined != want {
		t.Fatalf("joined %q want %q", joined, want)
	}
}

func TestChatActionOpts(t *testing.T) {
	if chatActionOpts(&gotgbot.Message{}) != nil {
		t.Fatal("zero thread should omit opts")
	}
	opts := chatActionOpts(&gotgbot.Message{MessageThreadId: 1})
	if opts == nil || opts.MessageThreadId != 1 {
		t.Fatalf("typing should pass through 1: %+v", opts)
	}
	opts = chatActionOpts(&gotgbot.Message{MessageThreadId: 3})
	if opts == nil || opts.MessageThreadId != 3 {
		t.Fatalf("typing topic 3: %+v", opts)
	}
}

func TestTypingLoopSendsUntilCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sends := make(chan struct{}, 8)
	go typingLoop(ctx, func() { sends <- struct{}{} }, 20*time.Millisecond)

	select {
	case <-sends:
	case <-time.After(time.Second):
		t.Fatal("expected immediate typing send")
	}
	select {
	case <-sends:
	case <-time.After(time.Second):
		t.Fatal("expected typing refresh before 5s expiry")
	}
	cancel()

	deadline := time.After(80 * time.Millisecond)
	extra := 0
	for {
		select {
		case <-sends:
			extra++
		case <-deadline:
			if extra > 1 {
				t.Fatalf("kept sending after cancel: extra=%d", extra)
			}
			return
		}
	}
}
