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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"kerfline/internal/config"
	"kerfline/internal/gate"
	"kerfline/internal/media"
	"kerfline/internal/runner"
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

func TestTranscribeStagedRefreshesOn403(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	var auths []string
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		n++
		if n == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"unauthenticated:bad-credentials"}`))
			return
		}
		_, _ = w.Write([]byte(`{"text":"buy milk","language":"en","duration":2.5}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	abs := filepath.Join(dir, "77-voice.ogg")
	if err := os.WriteFile(abs, []byte("ogg-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var refresh int
	var cmds []string
	b := &Bot{
		cfg:    &config.Config{Jailbee: config.Jailbee{Binary: "jailbee"}, STT: config.STT{AuthPath: "/home/dev/.grok/auth.json"}},
		stt:    &media.STT{BaseURL: srv.URL, HTTP: srv.Client()},
		runner: fakeAuthRunner(t, "old-tok", "new-tok", &refresh, &cmds),
		log:    slog.New(slog.DiscardHandler),
	}
	tr, err := b.transcribeStaged(context.Background(), config.Chat{Workspace: dir, JailbeeContainer: "main"}, media.StagedFile{
		AbsPath: abs,
		RelPath: ".local/telegram-inbox/notes/77-voice.ogg",
		Ref:     media.AttachmentRef{Kind: "voice", MIME: "audio/ogg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Text != "buy milk" {
		t.Fatalf("%+v", tr)
	}
	if refresh != 1 {
		t.Fatalf("refresh %d, want 1; cmds=%v", refresh, cmds)
	}
	if len(auths) != 2 || auths[0] != "Bearer old-tok" || auths[1] != "Bearer new-tok" {
		t.Fatalf("auths %v", auths)
	}
}

func TestTranscribeStagedEnvKeySkipsRefresh(t *testing.T) {
	t.Setenv("XAI_API_KEY", "env-tok")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"unauthenticated:bad-credentials"}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	abs := filepath.Join(dir, "77-voice.ogg")
	if err := os.WriteFile(abs, []byte("ogg-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var refresh int
	var cmds []string
	b := &Bot{
		cfg:    &config.Config{Jailbee: config.Jailbee{Binary: "jailbee"}, STT: config.STT{AuthPath: "/home/dev/.grok/auth.json"}},
		stt:    &media.STT{BaseURL: srv.URL, HTTP: srv.Client()},
		runner: fakeAuthRunner(t, "old-tok", "new-tok", &refresh, &cmds),
		log:    slog.New(slog.DiscardHandler),
	}
	_, err := b.transcribeStaged(context.Background(), config.Chat{Workspace: dir, JailbeeContainer: "main"}, media.StagedFile{
		AbsPath: abs,
		RelPath: ".local/telegram-inbox/notes/77-voice.ogg",
		Ref:     media.AttachmentRef{Kind: "voice", MIME: "audio/ogg"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if refresh != 0 {
		t.Fatalf("env key must not refresh, got %d cmds=%v", refresh, cmds)
	}
	if media.UserMessage(err) != "Couldn't transcribe that voice note." {
		t.Fatalf("user msg %q", media.UserMessage(err))
	}
}

func TestTranscribeStagedServerErrorSkipsRefresh(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	abs := filepath.Join(dir, "77-voice.ogg")
	if err := os.WriteFile(abs, []byte("ogg-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var refresh int
	var cmds []string
	b := &Bot{
		cfg:    &config.Config{Jailbee: config.Jailbee{Binary: "jailbee"}, STT: config.STT{AuthPath: "/home/dev/.grok/auth.json"}},
		stt:    &media.STT{BaseURL: srv.URL, HTTP: srv.Client()},
		runner: fakeAuthRunner(t, "old-tok", "new-tok", &refresh, &cmds),
		log:    slog.New(slog.DiscardHandler),
	}
	_, err := b.transcribeStaged(context.Background(), config.Chat{Workspace: dir, JailbeeContainer: "main"}, media.StagedFile{
		AbsPath: abs,
		RelPath: ".local/telegram-inbox/notes/77-voice.ogg",
		Ref:     media.AttachmentRef{Kind: "voice", MIME: "audio/ogg"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if refresh != 0 {
		t.Fatalf("500 must not refresh, got %d cmds=%v", refresh, cmds)
	}
}

func fakeAuthRunner(t *testing.T, oldTok, newTok string, refresh *int, cmds *[]string) *runner.Runner {
	t.Helper()
	cats := 0
	r := runner.New()
	r.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	r.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		joined := strings.Join(args, " ")
		*cmds = append(*cmds, joined)
		switch {
		case strings.Contains(joined, "grok models"):
			*refresh++
			return exec.CommandContext(ctx, "true")
		case strings.Contains(joined, "cat --"):
			cats++
			tok := oldTok
			if cats > 1 {
				tok = newTok
			}
			body := `{"https://auth.x.ai::x":{"key":"` + tok + `","create_time":"2026-01-01T00:00:00Z"}}`
			return exec.CommandContext(ctx, "printf", "%s", body)
		default:
			t.Fatalf("unexpected jailbee args: %q", joined)
			return exec.CommandContext(ctx, "false")
		}
	}
	return r
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
	called       int
	d            gate.Decision
	err          error
	lastUserText string
}

func (s *stubGater) Decide(_ context.Context, _ config.Chat, _ *gotgbot.Message, userText string) (gate.Decision, error) {
	s.called++
	s.lastUserText = userText
	return s.d, s.err
}

type recordingExec struct {
	events     []string
	transcript string
	speechErr  error
}

func (r *recordingExec) StartTyping() func() {
	r.events = append(r.events, "typing")
	return func() { r.events = append(r.events, "typing-stop") }
}

func (r *recordingExec) PrepareSpeech(context.Context) error {
	r.events = append(r.events, "speech")
	return r.speechErr
}

func (r *recordingExec) SpeechTranscript() string {
	return r.transcript
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

func voiceMsg(seconds int, caption string) *gotgbot.Message {
	return &gotgbot.Message{
		Caption: caption,
		Voice:   &gotgbot.Voice{FileId: "v", Duration: int64(seconds)},
		From:    &gotgbot.User{Id: 42},
		Chat:    gotgbot.Chat{Id: -1001, Type: gotgbot.ChatTypeGroup},
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

func TestReplyGateRefreshesOn403(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	var auths []string
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		n++
		if n == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"unauthenticated:bad-credentials"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"respond\":true}"}}]}`))
	}))
	defer srv.Close()

	var refresh int
	var cmds []string
	b := silentBot(t, nil)
	b.cfg.Jailbee.Binary = "jailbee"
	b.cfg.STT.BaseURL = srv.URL
	b.cfg.STT.AuthPath = "/home/dev/.grok/auth.json"
	b.cfg.Grok.GateModel = "grok-4.3"
	b.cfg.Grok.GateTimeout = "15s"
	b.cfg.Grok.GateReasoning = "none"
	b.runner = fakeAuthRunner(t, "old-tok", "new-tok", &refresh, &cmds)
	g := &replyGate{bot: b}
	d, err := g.Decide(context.Background(), config.Chat{Name: "notes", Workspace: t.TempDir(), JailbeeContainer: "main"}, groupMsg("buy milk"), "buy milk")
	if err != nil {
		t.Fatal(err)
	}
	if d != gate.Reply {
		t.Fatalf("got %v, want Reply", d)
	}
	if refresh != 1 {
		t.Fatalf("refresh %d, want 1; cmds=%v", refresh, cmds)
	}
	if len(auths) != 2 || auths[0] != "Bearer old-tok" || auths[1] != "Bearer new-tok" {
		t.Fatalf("auths %v", auths)
	}
}

func TestShortSpeech(t *testing.T) {
	max := 2 * time.Minute
	if shortSpeech(groupMsg("hi"), max) {
		t.Fatal("text is not speech")
	}
	if !shortSpeech(voiceMsg(30, ""), max) {
		t.Fatal("30s voice should be short")
	}
	if !shortSpeech(voiceMsg(120, ""), max) {
		t.Fatal("120s voice should be at the max")
	}
	if shortSpeech(voiceMsg(121, ""), max) {
		t.Fatal("121s voice should be long")
	}
	if shortSpeech(voiceMsg(0, ""), max) {
		t.Fatal("zero duration should not count")
	}
	audio := voiceMsg(45, "")
	audio.Voice = nil
	audio.Audio = &gotgbot.Audio{FileId: "a", Duration: 45}
	if !shortSpeech(audio, max) {
		t.Fatal("45s audio should be short")
	}
}

func TestGateShortSpeech(t *testing.T) {
	cfg := &config.Config{}
	chat := config.Chat{Name: "notes"}
	if !gateShortSpeech(cfg, chat, voiceMsg(30, ""), "notesbot") {
		t.Fatal("short bare voice should open the gate speech path")
	}
	if gateShortSpeech(cfg, chat, voiceMsg(180, ""), "notesbot") {
		t.Fatal("long bare voice should not")
	}
	if gateShortSpeech(cfg, config.Chat{RequireMention: true}, voiceMsg(30, ""), "notesbot") {
		t.Fatal("require_mention skips gate speech")
	}
	if gateShortSpeech(cfg, chat, voiceMsg(30, "/ask save this"), "notesbot") {
		t.Fatal("/ask voice bypasses the gate, so no pre-gate STT")
	}
	off := false
	cfg.Grok.Gate = &off
	if gateShortSpeech(cfg, chat, voiceMsg(30, ""), "notesbot") {
		t.Fatal("gate disabled")
	}
}

func TestGateUserText(t *testing.T) {
	if got := gateUserText(groupMsg("buy milk"), ""); got != "buy milk" {
		t.Fatalf("got %q", got)
	}
	if got := gateUserText(voiceMsg(30, ""), "buy milk"); got != "buy milk" {
		t.Fatalf("transcript only: %q", got)
	}
	if got := gateUserText(voiceMsg(30, "also eggs"), "buy milk"); got != "also eggs\nbuy milk" {
		t.Fatalf("caption+transcript: %q", got)
	}
}

func TestOrchestrateShortVoiceSkipNoTyping(t *testing.T) {
	g := &stubGater{d: gate.Skip}
	b := silentBot(t, g)
	exec := &recordingExec{transcript: "buy milk"}
	err := b.orchestrate(context.Background(), config.Chat{Name: "notes"}, voiceMsg(30, ""), "", "notesbot", exec)
	if err != nil {
		t.Fatal(err)
	}
	if g.called != 1 {
		t.Fatalf("gate called %d times, want 1", g.called)
	}
	if g.lastUserText != "buy milk" {
		t.Fatalf("gate user text %q", g.lastUserText)
	}
	assertEvents(t, exec.events, []string{"speech"})
	for _, e := range exec.events {
		if e == "typing" || e == "download" || e == "run" {
			t.Fatalf("skip must not type/download/run, got %v", exec.events)
		}
	}
}

func TestOrchestrateShortVoiceReplyReusesTranscript(t *testing.T) {
	g := &stubGater{d: gate.Reply}
	b := silentBot(t, g)
	exec := &recordingExec{transcript: "buy milk"}
	err := b.orchestrate(context.Background(), config.Chat{Name: "notes"}, voiceMsg(30, "please"), "please", "notesbot", exec)
	if err != nil {
		t.Fatal(err)
	}
	if g.lastUserText != "please\nbuy milk" {
		t.Fatalf("gate user text %q", g.lastUserText)
	}
	assertEvents(t, exec.events, []string{"speech", "typing", "download", "run"})
}

func TestOrchestrateLongVoiceNoSpeechPrep(t *testing.T) {
	g := &stubGater{d: gate.Skip}
	b := silentBot(t, g)
	exec := &recordingExec{transcript: "should not be used"}
	err := b.orchestrate(context.Background(), config.Chat{Name: "notes"}, voiceMsg(180, "lol"), "lol", "notesbot", exec)
	if err != nil {
		t.Fatal(err)
	}
	if g.lastUserText != "lol" {
		t.Fatalf("long voice gates on caption only, got %q", g.lastUserText)
	}
	for _, e := range exec.events {
		if e == "speech" {
			t.Fatalf("long voice must not transcribe before gate, got %v", exec.events)
		}
	}
}

func TestOrchestrateAskVoiceNoSpeechPrep(t *testing.T) {
	g := &stubGater{d: gate.Skip}
	b := silentBot(t, g)
	exec := &recordingExec{}
	msg := voiceMsg(30, "/ask save this")
	err := b.orchestrate(context.Background(), config.Chat{Name: "notes"}, msg, "save this", "notesbot", exec)
	if err != nil {
		t.Fatal(err)
	}
	if g.called != 0 {
		t.Fatalf("gate ran for /ask voice: %d", g.called)
	}
	assertEvents(t, exec.events, []string{"typing", "download", "run"})
}

func TestOrchestrateShortVoiceSTTErrorSilent(t *testing.T) {
	g := &stubGater{d: gate.Reply}
	b := silentBot(t, g)
	exec := &recordingExec{speechErr: errors.New("stt failed")}
	err := b.orchestrate(context.Background(), config.Chat{Name: "notes"}, voiceMsg(30, ""), "", "notesbot", exec)
	if err != nil {
		t.Fatal(err)
	}
	if g.called != 0 {
		t.Fatalf("gate must not run after STT error, called %d", g.called)
	}
	assertEvents(t, exec.events, []string{"speech"})
	for _, e := range exec.events {
		if e == "typing" || e == "download" || e == "run" {
			t.Fatalf("STT error must skip the turn, got %v", exec.events)
		}
	}
}

func TestIsAckReply(t *testing.T) {
	yes := []string{"👍", "  👍  \n", "👍\uFE0F", "\uFE0F👍"}
	for _, s := range yes {
		if !isAckReply(s) {
			t.Fatalf("want ack for %q", s)
		}
	}
	no := []string{
		"",
		"(empty reply)",
		"👍 saved",
		"saved 👍",
		"ok",
		"ACK",
		"👍\n👍",
	}
	for _, s := range no {
		if isAckReply(s) {
			t.Fatalf("did not want ack for %q", s)
		}
	}
}

type stubBotClient struct {
	method string
	params map[string]any
}

func (s *stubBotClient) RequestWithContext(_ context.Context, _ string, method string, params map[string]any, _ *gotgbot.RequestOpts) (json.RawMessage, error) {
	s.method = method
	s.params = params
	return json.RawMessage(`true`), nil
}

func (s *stubBotClient) GetAPIURL(*gotgbot.RequestOpts) string { return "" }

func (s *stubBotClient) FileURL(string, string, *gotgbot.RequestOpts) string { return "" }

func TestAckMessageSetsReaction(t *testing.T) {
	stub := &stubBotClient{}
	tg := &gotgbot.Bot{Token: "tok", BotClient: stub}
	b := silentBot(t, nil)
	msg := &gotgbot.Message{MessageId: 7, Chat: gotgbot.Chat{Id: -100}}
	if err := b.ackMessage(tg, msg); err != nil {
		t.Fatal(err)
	}
	if stub.method != "setMessageReaction" {
		t.Fatalf("method %q", stub.method)
	}
	if stub.params["chat_id"] != int64(-100) {
		t.Fatalf("chat_id %v", stub.params["chat_id"])
	}
	if stub.params["message_id"] != int64(7) {
		t.Fatalf("message_id %v", stub.params["message_id"])
	}
	raw, err := json.Marshal(stub.params["reaction"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"emoji":"👍"`) {
		t.Fatalf("reaction %s", raw)
	}
}

func TestLiveTurnDownloadSkipsAfterPrepare(t *testing.T) {
	turn := &liveTurn{speechPrepared: true}
	if err := turn.Download(); err != nil {
		t.Fatal(err)
	}
}
