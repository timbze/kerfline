package bot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"kerfline/internal/config"
	"kerfline/internal/gate"
	"kerfline/internal/media"
)

func TestSTTTokenPrefersEnv(t *testing.T) {
	t.Setenv("XAI_API_KEY", " env-tok ")
	b := &Bot{cfg: &config.Config{}}
	got, err := b.sttToken(context.Background(), config.Chat{})
	if err != nil || got != "env-tok" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestTranscribeStaged(t *testing.T) {
	t.Setenv("XAI_API_KEY", "tok")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "ogg-bytes") {
			t.Errorf("missing payload")
		}
		_, _ = w.Write([]byte(`{"text":"buy milk","language":"en","duration":2.5}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	abs := filepath.Join(dir, "77-voice.ogg")
	if err := os.WriteFile(abs, []byte("ogg-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &Bot{
		cfg: &config.Config{},
		stt: &media.STT{BaseURL: srv.URL, HTTP: srv.Client()},
	}
	tr, err := b.transcribeStaged(context.Background(), config.Chat{}, media.StagedFile{
		AbsPath: abs,
		RelPath: ".local/telegram-inbox/notes/77-voice.ogg",
		Ref:     media.AttachmentRef{Kind: "voice", MIME: "audio/ogg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Text != "buy milk" || tr.Language != "en" {
		t.Fatalf("%+v", tr)
	}
}

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

func TestRedactToken(t *testing.T) {
	token := "123:ABC"
	in := "failed to execute POST request to https://api.telegram.org/bot123:ABC/getFile: timeout"
	got := redactToken(in, token)
	if strings.Contains(got, token) || strings.Contains(got, "bot123") {
		t.Fatalf("leaked: %q", got)
	}
	if !strings.Contains(got, "bot<token>") && !strings.Contains(got, "<token>") {
		t.Fatalf("expected redaction: %q", got)
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

type stubGater struct {
	called int
	d      gate.Decision
	err    error
}

func (s *stubGater) Decide(context.Context, config.Chat, *gotgbot.Message, string) (gate.Decision, error) {
	s.called++
	return s.d, s.err
}

type recordingExec struct {
	events []string
}

func (r *recordingExec) StartTyping() func() {
	r.events = append(r.events, "typing")
	return func() { r.events = append(r.events, "typing-stop") }
}

func (r *recordingExec) Download() error {
	r.events = append(r.events, "download")
	return nil
}

func (r *recordingExec) Run() error {
	r.events = append(r.events, "run")
	return nil
}

func silentBot(t *testing.T, g gater) *Bot {
	t.Helper()
	return &Bot{
		cfg:  &config.Config{},
		log:  slog.New(slog.DiscardHandler),
		gate: g,
	}
}

func groupMsg(text string) *gotgbot.Message {
	return &gotgbot.Message{
		Text: text,
		From: &gotgbot.User{Id: 42},
		Chat: gotgbot.Chat{Id: -1001, Type: gotgbot.ChatTypeGroup},
	}
}

func privateMsg(text string) *gotgbot.Message {
	return &gotgbot.Message{
		Text: text,
		From: &gotgbot.User{Id: 42},
		Chat: gotgbot.Chat{Id: 42, Type: gotgbot.ChatTypePrivate},
	}
}

func assertEvents(t *testing.T, events, want []string) {
	t.Helper()
	if len(events) < len(want) {
		t.Fatalf("events %v, want prefix %v", events, want)
	}
	for i, w := range want {
		if events[i] != w {
			t.Fatalf("events %v, want prefix %v", events, want)
		}
	}
}

func TestOrchestrateGateDoesNotRunWhenRequireMention(t *testing.T) {
	g := &stubGater{d: gate.Skip}
	b := silentBot(t, g)
	exec := &recordingExec{}
	chat := config.Chat{Name: "notes", RequireMention: true}
	err := b.orchestrate(context.Background(), chat, groupMsg("/ask buy milk"), "buy milk", "notesbot", exec)
	if err != nil {
		t.Fatal(err)
	}
	if g.called != 0 {
		t.Fatalf("gate ran %d times", g.called)
	}
	assertEvents(t, exec.events, []string{"typing", "download", "run"})
}

func TestOrchestrateGateDoesNotRunWhenExplicitlyAddressed(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		prompt string
	}{
		{name: "ask", text: "/ask buy milk", prompt: "buy milk"},
		{name: "mention", text: "@notesbot add eggs", prompt: "add eggs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &stubGater{d: gate.Skip}
			b := silentBot(t, g)
			exec := &recordingExec{}
			chat := config.Chat{Name: "notes"}
			err := b.orchestrate(context.Background(), chat, groupMsg(tc.text), tc.prompt, "notesbot", exec)
			if err != nil {
				t.Fatal(err)
			}
			if g.called != 0 {
				t.Fatalf("gate ran %d times", g.called)
			}
			assertEvents(t, exec.events, []string{"typing", "download", "run"})
		})
	}
}

func TestOrchestrateGateDoesNotRunWhenDisabled(t *testing.T) {
	off := false
	g := &stubGater{d: gate.Skip}
	b := silentBot(t, g)
	b.cfg.Grok.Gate = &off
	exec := &recordingExec{}
	chat := config.Chat{Name: "notes"}
	err := b.orchestrate(context.Background(), chat, groupMsg("lol"), "lol", "notesbot", exec)
	if err != nil {
		t.Fatal(err)
	}
	if g.called != 0 {
		t.Fatalf("gate ran %d times", g.called)
	}
	assertEvents(t, exec.events, []string{"typing", "download", "run"})
}

func TestOrchestrateBareAskBypassesGate(t *testing.T) {
	g := &stubGater{d: gate.Skip}
	b := silentBot(t, g)
	exec := &recordingExec{}
	chat := config.Chat{Name: "notes"}
	err := b.orchestrate(context.Background(), chat, groupMsg("/ask"), "quoted body", "notesbot", exec)
	if err != nil {
		t.Fatal(err)
	}
	if g.called != 0 {
		t.Fatalf("gate ran %d times", g.called)
	}
	assertEvents(t, exec.events, []string{"typing", "download", "run"})
}

func TestOrchestrateSkipNoTypingNoRunner(t *testing.T) {
	g := &stubGater{d: gate.Skip}
	b := silentBot(t, g)
	exec := &recordingExec{}
	chat := config.Chat{Name: "notes"}
	err := b.orchestrate(context.Background(), chat, groupMsg("lol"), "lol", "notesbot", exec)
	if err != nil {
		t.Fatal(err)
	}
	if g.called != 1 {
		t.Fatalf("gate called %d times, want 1", g.called)
	}
	for _, e := range exec.events {
		if e == "typing" || e == "download" || e == "run" {
			t.Fatalf("skip must not type/download/run, got %v", exec.events)
		}
	}
}

func TestOrchestrateReplyTypesThenRuns(t *testing.T) {
	g := &stubGater{d: gate.Reply}
	b := silentBot(t, g)
	exec := &recordingExec{}
	chat := config.Chat{Name: "notes"}
	err := b.orchestrate(context.Background(), chat, groupMsg("buy milk"), "buy milk", "notesbot", exec)
	if err != nil {
		t.Fatal(err)
	}
	if g.called != 1 {
		t.Fatalf("gate called %d times, want 1", g.called)
	}
	assertEvents(t, exec.events, []string{"typing", "download", "run"})
}

func TestOrchestrateAskPhotoTypesBeforeDownload(t *testing.T) {
	g := &stubGater{d: gate.Skip}
	b := silentBot(t, g)
	exec := &recordingExec{}
	chat := config.Chat{Name: "notes"}
	msg := &gotgbot.Message{
		Caption: "/ask what is this",
		Photo:   []gotgbot.PhotoSize{{FileId: "fid", FileUniqueId: "uid"}},
		From:    &gotgbot.User{Id: 42},
		Chat:    gotgbot.Chat{Id: -1001, Type: gotgbot.ChatTypeGroup},
	}
	err := b.orchestrate(context.Background(), chat, msg, "what is this", "notesbot", exec)
	if err != nil {
		t.Fatal(err)
	}
	if g.called != 0 {
		t.Fatalf("gate ran for /ask photo: %d", g.called)
	}
	assertEvents(t, exec.events, []string{"typing", "download", "run"})
}

func TestOrchestratePrivateErrorFailOpen(t *testing.T) {
	g := &stubGater{err: errors.New("http 500")}
	b := silentBot(t, g)
	exec := &recordingExec{}
	chat := config.Chat{Name: "notes"}
	err := b.orchestrate(context.Background(), chat, privateMsg("lol"), "lol", "notesbot", exec)
	if err != nil {
		t.Fatal(err)
	}
	if g.called != 1 {
		t.Fatalf("gate called %d times, want 1", g.called)
	}
	assertEvents(t, exec.events, []string{"typing", "download", "run"})
}

func TestOrchestrateGroupErrorFailClosed(t *testing.T) {
	g := &stubGater{err: errors.New("http 500")}
	b := silentBot(t, g)
	exec := &recordingExec{}
	chat := config.Chat{Name: "notes"}
	err := b.orchestrate(context.Background(), chat, groupMsg("lol"), "lol", "notesbot", exec)
	if err != nil {
		t.Fatal(err)
	}
	if g.called != 1 {
		t.Fatalf("gate called %d times, want 1", g.called)
	}
	for _, e := range exec.events {
		if e == "typing" || e == "download" || e == "run" {
			t.Fatalf("group error must not type/download/run, got %v", exec.events)
		}
	}
}

func TestReplyGateVocativeNoHTTP(t *testing.T) {
	b := silentBot(t, nil)
	g := &replyGate{bot: b}
	d, err := g.Decide(context.Background(), config.Chat{Name: "notes"}, groupMsg("hey kerf save this"), "hey kerf save this")
	if err != nil {
		t.Fatal(err)
	}
	if d != gate.Reply {
		t.Fatalf("got %v, want Reply", d)
	}
}

func TestReplyGateReplyToBotNoHTTP(t *testing.T) {
	b := silentBot(t, nil)
	g := &replyGate{bot: b}
	msg := groupMsg("ok do it")
	msg.ReplyToMessage = &gotgbot.Message{
		From: &gotgbot.User{IsBot: true, FirstName: "Kerfline"},
		Text: "add milk?",
	}
	d, err := g.Decide(context.Background(), config.Chat{Name: "notes"}, msg, "ok do it")
	if err != nil {
		t.Fatal(err)
	}
	if d != gate.Reply {
		t.Fatalf("got %v, want Reply", d)
	}
}

func TestReplyGateHTTPSendsReasoningNone(t *testing.T) {
	t.Setenv("XAI_API_KEY", "tok")
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"respond\":true}"}}]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	hint := "keep shopping lists in this workspace"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(hint), 0o600); err != nil {
		t.Fatal(err)
	}

	b := silentBot(t, nil)
	b.cfg.STT.BaseURL = srv.URL
	b.cfg.Grok.GateModel = "grok-4.3"
	b.cfg.Grok.GateTimeout = "15s"
	b.cfg.Grok.GateReasoning = "none"
	g := &replyGate{bot: b}
	msg := groupMsg("buy milk")
	d, err := g.Decide(context.Background(), config.Chat{Name: "notes", Workspace: dir}, msg, "buy milk")
	if err != nil {
		t.Fatal(err)
	}
	if d != gate.Reply {
		t.Fatalf("got %v, want Reply", d)
	}
	if gotBody["model"] != "grok-4.3" {
		t.Fatalf("model %v", gotBody["model"])
	}
	if gotBody["reasoning_effort"] != "none" {
		t.Fatalf("reasoning_effort %v, want none", gotBody["reasoning_effort"])
	}
	raw, _ := json.Marshal(gotBody["messages"])
	if !strings.Contains(string(raw), "buy milk") {
		t.Fatalf("user text missing: %s", raw)
	}
	if !strings.Contains(string(raw), hint) {
		t.Fatalf("workspace hint missing: %s", raw)
	}
}

func TestReplyGateWorkspaceHintReadErrorEmpty(t *testing.T) {
	t.Setenv("XAI_API_KEY", "tok")
	var userContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, m := range body.Messages {
			if m.Role == "user" {
				userContent = m.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"respond\":false}"}}]}`))
	}))
	defer srv.Close()

	b := silentBot(t, nil)
	b.cfg.STT.BaseURL = srv.URL
	b.cfg.Grok.GateModel = "grok-4.3"
	b.cfg.Grok.GateReasoning = "none"
	g := &replyGate{bot: b}
	d, err := g.Decide(context.Background(), config.Chat{Name: "notes", Workspace: t.TempDir()}, groupMsg("lol"), "lol")
	if err != nil {
		t.Fatal(err)
	}
	if d != gate.Skip {
		t.Fatalf("got %v, want Skip", d)
	}
	if !strings.Contains(userContent, "workspace_hint:\n") {
		t.Fatalf("expected empty workspace_hint block, got %q", userContent)
	}
}
