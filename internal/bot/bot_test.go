package bot

import (
	"strings"
	"testing"

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
		opts := replyOpts(&msg)
		if opts.MessageThreadId != 0 {
			t.Fatalf("should omit thread on %+v, got %d", msg, opts.MessageThreadId)
		}
	}
	opts := replyOpts(&gotgbot.Message{MessageThreadId: 3, IsTopicMessage: true})
	if opts.MessageThreadId != 3 {
		t.Fatalf("user topic: got %d", opts.MessageThreadId)
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
