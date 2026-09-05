package bot

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters/message"

	"kerfline/internal/config"
	"kerfline/internal/gate"
	"kerfline/internal/media"
	"kerfline/internal/runner"
	"kerfline/internal/session"
)

type gater interface {
	Decide(ctx context.Context, chat config.Chat, msg *gotgbot.Message, prompt string) (gate.Decision, error)
}

type turnExec interface {
	StartTyping() (stop func())
	PrepareSpeech(ctx context.Context) error
	SpeechTranscript() string
	Download() error
	Run() error
}

const (
	// telegramRichMax is under the 32768-character rich-message limit (counted in bytes here).
	telegramRichMax = 30000
	// telegramPlainMax is under the 4096-character sendMessage limit, used only as fallback.
	telegramPlainMax = 3900
	typingRefresh    = 4 * time.Second
	telegramUpload   = 90 * time.Second
)

type Bot struct {
	cfg     *config.Config
	log     *slog.Logger
	runner  *runner.Runner
	sess    *session.Store
	media   *media.Store
	stt     *media.STT
	updater *ext.Updater
	disp    *ext.Dispatcher
	gate    gater
}

func New(cfg *config.Config, log *slog.Logger, r *runner.Runner, sess *session.Store) *Bot {
	stt := &media.STT{BaseURL: cfg.STT.BaseURL}
	if d := cfg.STTTimeout(); d > 0 {
		stt.HTTP = &http.Client{Timeout: d}
	}
	b := &Bot{cfg: cfg, log: log, runner: r, sess: sess, media: media.NewStore(nil, log), stt: stt}
	b.gate = &replyGate{bot: b}
	return b
}

func (b *Bot) Run(ctx context.Context, tg *gotgbot.Bot) error {
	b.disp = ext.NewDispatcher(&ext.DispatcherOpts{
		Error: func(_ *gotgbot.Bot, _ *ext.Context, err error) ext.DispatcherAction {
			b.log.Error("handler", "err", err)
			return ext.DispatcherActionNoop
		},
		Logger: b.log,
	})
	b.updater = ext.NewUpdater(b.disp, &ext.UpdaterOpts{Logger: b.log})
	b.media.Files = media.NewTelegram(tg)
	b.disp.AddHandler(handlers.NewCommand("start", b.onStart))
	b.disp.AddHandler(handlers.NewCommand("chatid", b.onChatID))
	b.disp.AddHandler(handlers.NewMessage(message.Text, b.onUserMessage))
	b.disp.AddHandler(handlers.NewMessage(message.Photo, b.onUserMessage))
	b.disp.AddHandler(handlers.NewMessage(message.Document, b.onUserMessage))
	b.disp.AddHandler(handlers.NewMessage(message.Voice, b.onUserMessage))
	b.disp.AddHandler(handlers.NewMessage(message.Audio, b.onUserMessage))

	if b.cfg.UseWebhook() {
		return b.runWebhook(ctx, tg)
	}
	b.log.Info("starting long poll", "reason", pollReason(b.cfg))
	err := b.updater.StartPolling(tg, &ext.PollingOpts{
		EnableWebhookDeletion: true,
		DropPendingUpdates:    false,
		GetUpdatesOpts: &gotgbot.GetUpdatesOpts{
			Timeout: 9,
			RequestOpts: &gotgbot.RequestOpts{
				Timeout: 10 * time.Second,
			},
		},
	})
	if err != nil {
		return err
	}
	return b.idle(ctx)
}

func pollReason(cfg *config.Config) string {
	if cfg.Telegram.Mode == config.ModePoll {
		return "mode=poll"
	}
	return "webhook.public_url empty; falling back to poll"
}

func (b *Bot) runWebhook(ctx context.Context, tg *gotgbot.Bot) error {
	if b.cfg.Telegram.ShouldDrain() {
		n, err := b.drain(ctx, tg)
		if err != nil {
			return fmt.Errorf("startup poll drain: %w", err)
		}
		b.log.Info("drained pending updates", "count", n)
	}

	opts := ext.WebhookOpts{
		ListenAddr:  b.cfg.Telegram.Listen,
		SecretToken: b.cfg.Telegram.SecretToken,
	}
	if err := b.updater.StartWebhook(tg, b.cfg.Telegram.Path, opts); err != nil {
		return err
	}
	url := b.cfg.Telegram.PublicURL
	if err := b.updater.SetAllBotWebhooks(url, &gotgbot.SetWebhookOpts{
		MaxConnections:     40,
		DropPendingUpdates: false,
		SecretToken:        b.cfg.Telegram.SecretToken,
	}); err != nil {
		return fmt.Errorf("setWebhook: %w", err)
	}
	b.log.Info("webhook listening", "listen", b.cfg.Telegram.Listen, "url", url+"/"+b.cfg.Telegram.Path)
	return b.idle(ctx)
}

func (b *Bot) drain(ctx context.Context, tg *gotgbot.Bot) (int, error) {
	if _, err := tg.DeleteWebhook(&gotgbot.DeleteWebhookOpts{DropPendingUpdates: false}); err != nil {
		return 0, err
	}
	var offset int64
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		updates, err := tg.GetUpdates(&gotgbot.GetUpdatesOpts{
			Offset:  offset,
			Limit:   100,
			Timeout: 0,
			RequestOpts: &gotgbot.RequestOpts{
				Timeout: 15 * time.Second,
			},
		})
		if err != nil {
			return total, err
		}
		if len(updates) == 0 {
			return total, nil
		}
		for i := range updates {
			u := updates[i]
			offset = u.UpdateId + 1
			total++
			if err := b.disp.ProcessUpdate(tg, &u, nil); err != nil {
				b.log.Error("drain handler", "update_id", u.UpdateId, "err", err)
			}
		}
	}
}

func (b *Bot) idle(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		b.updater.Idle()
		close(done)
	}()
	select {
	case <-ctx.Done():
		_ = b.updater.Stop()
		<-done
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (b *Bot) onStart(tg *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.EffectiveMessage
	text := "I only answer in allowlisted chats, with /ask or an @mention (unless that chat is a DM).\nUse /chatid to print this chat's id for config."
	_, err := msg.Reply(tg, text, replyOpts(msg))
	return err
}

func (b *Bot) onChatID(tg *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.EffectiveMessage
	_, err := msg.Reply(tg, formatChatIDReply(msg), replyOpts(msg))
	return err
}

func (b *Bot) onUserMessage(tg *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.EffectiveMessage
	if msg == nil || msg.From == nil {
		return nil
	}
	chat, kind := b.cfg.LookupTelegram(msg.Chat.Id, msg.MessageThreadId)
	switch kind {
	case config.TelegramUnknownChat:
		b.log.Info("ignored unknown chat", "chat_id", msg.Chat.Id, "user_id", msg.From.Id)
		return nil
	case config.TelegramIgnoredTopic:
		b.log.Info("ignored topic",
			"chat_id", msg.Chat.Id,
			"topic_id", msg.MessageThreadId,
			"user_id", msg.From.Id)
		return nil
	}
	if !chat.AllowsUser(msg.From.Id) {
		b.log.Info("ignored user", "chat", chat.Name, "user_id", msg.From.Id)
		return nil
	}
	prompt, want := BuildGrokPrompt(msg, tg.Username, chat.RequireMention)
	if !want && !gateShortSpeech(b.cfg, chat, msg, tg.Username) {
		return nil
	}
	return b.orchestrate(context.Background(), chat, msg, prompt, tg.Username, &liveTurn{
		bot:    b,
		tg:     tg,
		msg:    msg,
		chat:   chat,
		prompt: prompt,
	})
}

func gateApplies(cfg *config.Config, chat config.Chat, text, botUsername string) bool {
	if cfg == nil || !cfg.Grok.GateEnabled() {
		return false
	}
	if chat.RequireMention {
		return false
	}
	if ExplicitlyAddressed(text, botUsername) || addressedWithoutPrompt(text, botUsername) {
		return false
	}
	return true
}

func messageSpeech(msg *gotgbot.Message) (time.Duration, bool) {
	if msg == nil {
		return 0, false
	}
	if msg.Voice != nil {
		return time.Duration(msg.Voice.Duration) * time.Second, true
	}
	if msg.Audio != nil {
		return time.Duration(msg.Audio.Duration) * time.Second, true
	}
	return 0, false
}

func shortSpeech(msg *gotgbot.Message, max time.Duration) bool {
	d, ok := messageSpeech(msg)
	if !ok || d <= 0 || max <= 0 {
		return false
	}
	return d <= max
}

func gateShortSpeech(cfg *config.Config, chat config.Chat, msg *gotgbot.Message, botUsername string) bool {
	if msg == nil || !gateApplies(cfg, chat, msg.GetText(), botUsername) {
		return false
	}
	max := 2 * time.Minute
	if cfg != nil {
		max = cfg.Grok.SpeechMax()
	}
	return shortSpeech(msg, max)
}

func gateUserText(msg *gotgbot.Message, transcript string) string {
	text := ""
	if msg != nil {
		text = msg.GetText()
	}
	userText, ok := PromptFromMessage(text, "", false)
	if !ok || userText == "" {
		userText = strings.TrimSpace(text)
	}
	tr := strings.TrimSpace(transcript)
	switch {
	case tr == "":
		return userText
	case userText == "":
		return tr
	default:
		return userText + "\n" + tr
	}
}

func chatIsPrivate(msg *gotgbot.Message) bool {
	return msg != nil && msg.Chat.Type == gotgbot.ChatTypePrivate
}

func gateTimeout(cfg *config.Config) time.Duration {
	if cfg == nil {
		return 15 * time.Second
	}
	d, err := time.ParseDuration(cfg.Grok.GateTimeout)
	if err != nil || d <= 0 {
		return 15 * time.Second
	}
	return d
}

func readWorkspaceHint(workspace string) string {
	if workspace == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(workspace, "AGENTS.md"))
	if err != nil {
		return ""
	}
	if len(raw) > 2048 {
		raw = raw[:2048]
	}
	return string(raw)
}

func (b *Bot) orchestrate(ctx context.Context, chat config.Chat, msg *gotgbot.Message, prompt, botUsername string, exec turnExec) error {
	var speechText string
	if gateShortSpeech(b.cfg, chat, msg, botUsername) {
		start := time.Now()
		err := exec.PrepareSpeech(ctx)
		if b.log != nil {
			attrs := []any{"chat", chat.Name, "duration", time.Since(start)}
			if err != nil {
				attrs = append(attrs, "err", err)
				b.log.Error("gate speech", attrs...)
			} else {
				b.log.Info("gate speech", attrs...)
			}
		}
		if err != nil {
			return nil
		}
		speechText = exec.SpeechTranscript()
	}

	if gateApplies(b.cfg, chat, msg.GetText(), botUsername) {
		start := time.Now()
		g := b.gate
		if g == nil {
			g = &replyGate{bot: b}
		}
		d, err := g.Decide(ctx, chat, msg, gateUserText(msg, speechText))
		decision := "reply"
		if err != nil {
			decision = "error"
		} else if d == gate.Skip {
			decision = "skip"
		}
		if b.log != nil {
			attrs := []any{"decision", decision, "chat", chat.Name, "chars", len(prompt), "duration", time.Since(start)}
			if err != nil {
				attrs = append(attrs, "err", err)
				b.log.Error("gate", attrs...)
			} else {
				b.log.Info("gate", attrs...)
			}
		}
		if err != nil {
			if !chatIsPrivate(msg) {
				return nil
			}
		} else if d == gate.Skip {
			return nil
		}
	}

	stop := exec.StartTyping()
	defer stop()
	if err := exec.Download(); err != nil {
		return err
	}
	return exec.Run()
}

type replyGate struct {
	bot *Bot
}

func (g *replyGate) Decide(ctx context.Context, chat config.Chat, msg *gotgbot.Message, prompt string) (gate.Decision, error) {
	if msg == nil {
		return gate.Skip, fmt.Errorf("gate: nil message")
	}
	text := msg.GetText()
	if Vocative(text) {
		return gate.Reply, nil
	}
	if orig := msg.ReplyToMessage; orig != nil && orig.From != nil && orig.From.IsBot {
		return gate.Reply, nil
	}

	timeout := gateTimeout(g.bot.cfg)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	token, err := g.bot.authToken(ctx, chat)
	if err != nil {
		return gate.Skip, err
	}

	userText := strings.TrimSpace(prompt)
	if userText == "" {
		extracted, ok := PromptFromMessage(text, "", false)
		if !ok || extracted == "" {
			userText = strings.TrimSpace(text)
		} else {
			userText = extracted
		}
	}

	client := &gate.Client{Token: token, Timeout: timeout}
	if cfg := g.bot.cfg; cfg != nil {
		client.BaseURL = cfg.STT.BaseURL
		client.Model = cfg.Grok.GateModel
		client.Reasoning = cfg.Grok.GateReasoning
	}
	if client.Reasoning == "" {
		client.Reasoning = "none"
	}

	return gate.ShouldReply(ctx, client, gate.Input{
		ChatName:      chat.Name,
		Private:       chatIsPrivate(msg),
		WorkspaceHint: readWorkspaceHint(chat.Workspace),
		Quoted:        replyContext(msg),
		UserText:      userText,
	})
}

type liveTurn struct {
	bot            *Bot
	tg             *gotgbot.Bot
	msg            *gotgbot.Message
	chat           config.Chat
	prompt         string
	req            runner.Request
	mediaAttrs     []any
	aborted        bool
	speechPrepared bool
	transcript     media.Transcript
}

func (t *liveTurn) StartTyping() func() {
	return keepTyping(t.tg, t.msg)
}

func (t *liveTurn) PrepareSpeech(ctx context.Context) error {
	if t.speechPrepared {
		return nil
	}
	if err := t.stageMedia(ctx, true); err != nil {
		return err
	}
	t.speechPrepared = true
	return nil
}

func (t *liveTurn) SpeechTranscript() string {
	return strings.TrimSpace(t.transcript.Text)
}

func (t *liveTurn) Download() error {
	if t.speechPrepared {
		return nil
	}
	return t.stageMedia(context.Background(), false)
}

func (t *liveTurn) stageMedia(ctx context.Context, silent bool) error {
	b := t.bot
	tg := t.tg
	msg := t.msg
	chat := t.chat
	if inaccessibleReplyMedia(msg) {
		if silent {
			return fmt.Errorf("media gone")
		}
		_, err := msg.Reply(tg, "I don't have that file anymore; send it again.", replyOpts(msg))
		t.aborted = true
		return err
	}

	t.req = runner.Request{Prompt: t.prompt}
	ref := media.Extract(msg)
	if ref == nil {
		return nil
	}
	class := media.Classify(classifyInput(msg, ref))
	speech := media.IsSpeech(*ref)
	if !speech {
		vision := b.cfg.EffectiveVision(chat)
		_, skipLook := media.AttachVision(class, ref.MIME, vision)
		if skipLook {
			b.log.Info("media skip", "reason", "look_disabled", "chat", chat.Name)
			if silent {
				return fmt.Errorf("look disabled")
			}
			_, err := msg.Reply(tg, "Looking at pictures is off for this chat.", replyOpts(msg))
			t.aborted = true
			return err
		}
	}
	dlTimeout := 90 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remain := time.Until(deadline); remain > 0 && remain < dlTimeout {
			dlTimeout = remain
		}
	}
	dlCtx, cancel := context.WithTimeout(ctx, dlTimeout)
	staged, err := b.media.Materialize(dlCtx, chat, *ref, b.cfg.Media.MaxFileBytes, b.cfg.InboxTTL())
	cancel()
	if err != nil {
		b.log.Error("media download", "chat", chat.Name, "err", redactToken(err.Error(), tg.Token))
		if silent {
			return err
		}
		_, sendErr := msg.Reply(tg, redactToken(media.UserMessage(err), tg.Token), replyOpts(msg))
		t.aborted = true
		return sendErr
	}
	b.log.Info("media download",
		"chat", chat.Name,
		"message_id", ref.MessageID,
		"file_unique_id", ref.FileUniqueID,
		"bytes", staged.Bytes,
		"kind", ref.Kind)
	promptLine := media.VisionPromptLine(class, ref.MIME, false)
	if speech {
		promptLine = media.SpeechPromptLine()
		sttTimeout := b.cfg.STTTimeout()
		sttCtx, sttCancel := context.WithTimeout(ctx, sttTimeout)
		tr, err := b.transcribeStaged(sttCtx, chat, staged)
		sttCancel()
		if err != nil {
			b.log.Error("stt", "chat", chat.Name, "err", redactToken(err.Error(), tg.Token))
			if silent {
				return err
			}
			_, sendErr := msg.Reply(tg, redactToken(media.UserMessage(err), tg.Token), replyOpts(msg))
			t.aborted = true
			return sendErr
		}
		b.log.Info("stt",
			"chat", chat.Name,
			"language", tr.Language,
			"duration", tr.Duration,
			"chars", len(tr.Text))
		t.transcript = tr
		t.req.Prompt += media.FormatTranscript(tr)
	}
	t.req.Prompt += media.FormatAttachment(staged, promptLine)
	t.req.Deny = media.InboxDenyRules()
	t.mediaAttrs = []any{"attachments", 1, "class", class.String(), "vision", false, "deny_inbox", true, "speech", speech}
	return nil
}

func (t *liveTurn) Run() error {
	if t.aborted {
		return nil
	}
	b := t.bot
	tg := t.tg
	msg := t.msg
	chat := t.chat
	attrs := []any{"chat", chat.Name, "user_id", msg.From.Id}
	attrs = append(attrs, t.mediaAttrs...)
	attrs = append(attrs, "chars", len(t.req.Prompt))
	if msg.MessageThreadId != 0 {
		attrs = append(attrs, "topic_id", msg.MessageThreadId)
	}
	sessionID, resume := b.sess.ID(chat.Name)
	attrs = append(attrs, "session", sessionID, "resume", resume)
	b.log.Info("grok turn", attrs...)

	res, err := b.runner.Run(context.Background(), b.cfg, chat, t.req, sessionID, resume)
	if err != nil {
		b.log.Error("grok failed", "chat", chat.Name, "err", redactToken(err.Error(), tg.Token), "duration", res.Duration)
		_, sendErr := msg.Reply(tg, "Grok failed: "+redactToken(err.Error(), tg.Token), replyOpts(msg))
		return sendErr
	}
	if err := b.sess.Remember(chat.Name, sessionID); err != nil {
		b.log.Error("session persist", "err", err)
	}
	reply := res.Stdout
	if isAckReply(reply) {
		b.log.Info("grok done", "chat", chat.Name, "duration", res.Duration, "ack", true)
		return b.ackMessage(tg, msg)
	}
	if reply == "" {
		reply = "(empty reply)"
	}
	b.log.Info("grok done", "chat", chat.Name, "duration", res.Duration, "out_chars", len(reply))
	return b.replyChunks(tg, msg, chat.Workspace, reply)
}

const ackThumb = "👍"

func isAckReply(s string) bool {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\uFE0F", "")
	s = strings.ReplaceAll(s, "\uFE0E", "")
	return s == ackThumb
}

func (b *Bot) ackMessage(tg *gotgbot.Bot, msg *gotgbot.Message) error {
	opts := &gotgbot.SetMessageReactionOpts{
		Reaction: []gotgbot.ReactionType{
			gotgbot.ReactionTypeEmoji{Emoji: ackThumb},
		},
	}
	if _, err := msg.SetReaction(tg, opts); err != nil {
		if b.log != nil {
			b.log.Error("ack reaction failed; sending thumbs-up text", "err", err)
		}
		return b.sendTextChunks(tg, msg, ackThumb)
	}
	return nil
}

func (b *Bot) transcribeStaged(ctx context.Context, chat config.Chat, staged media.StagedFile) (media.Transcript, error) {
	token, err := b.sttToken(ctx, chat)
	if err != nil {
		return media.Transcript{}, &media.StageError{Reason: "stt", Msg: "Couldn't transcribe that voice note."}
	}
	f, err := os.Open(staged.AbsPath)
	if err != nil {
		return media.Transcript{}, &media.StageError{Reason: "stt", Msg: "Couldn't transcribe that voice note."}
	}
	defer f.Close()
	filename := filepath.Base(staged.RelPath)
	return b.stt.Transcribe(ctx, token, f, filename, staged.Ref.MIME)
}

func (b *Bot) sttToken(ctx context.Context, chat config.Chat) (string, error) {
	return b.authToken(ctx, chat)
}

func (b *Bot) authToken(ctx context.Context, chat config.Chat) (string, error) {
	if k := strings.TrimSpace(os.Getenv("XAI_API_KEY")); k != "" {
		return k, nil
	}
	raw, err := b.runner.ReadContainerFile(ctx, b.cfg, chat, b.cfg.STT.AuthPath)
	if err != nil {
		return "", err
	}
	return media.ParseGrokAuthJSON(raw)
}

func classifyInput(msg *gotgbot.Message, ref *media.AttachmentRef) string {
	user := strings.TrimSpace(msg.GetText())
	if ref == nil {
		return user
	}
	cap := strings.TrimSpace(ref.Caption)
	if ref.Source == "reply_to" && cap != "" && cap != user {
		return strings.TrimSpace(cap + "\n" + user)
	}
	return user
}

func inaccessibleReplyMedia(msg *gotgbot.Message) bool {
	if media.FromMessage(msg) != nil {
		return false
	}
	orig := msg.ReplyToMessage
	if orig == nil || skipReplyOriginal(orig) {
		return false
	}
	if media.FromMessage(orig) != nil {
		return false
	}
	return orig.Date == 0
}

func redactToken(s, token string) string {
	if token == "" || s == "" {
		return s
	}
	s = strings.ReplaceAll(s, "bot"+token, "bot<token>")
	s = strings.ReplaceAll(s, token, "<token>")
	return s
}

func formatChatIDReply(msg *gotgbot.Message) string {
	text := fmt.Sprintf("chat_id = %d\nuser_id = %d", msg.Chat.Id, msg.From.Id)
	if msg.MessageThreadId != 0 {
		text += fmt.Sprintf("\ntopic_id = %d", msg.MessageThreadId)
	}
	return text
}

func topicThreadID(msg *gotgbot.Message) int64 {
	if msg != nil && msg.IsTopicMessage && msg.MessageThreadId != 0 && msg.MessageThreadId != 1 {
		return msg.MessageThreadId
	}
	return 0
}

func replyOpts(msg *gotgbot.Message) *gotgbot.SendMessageOpts {
	opts := &gotgbot.SendMessageOpts{}
	if id := topicThreadID(msg); id != 0 {
		opts.MessageThreadId = id
	}
	return opts
}

func richReplyOpts(msg *gotgbot.Message) *gotgbot.SendRichMessageOpts {
	opts := &gotgbot.SendRichMessageOpts{}
	if id := topicThreadID(msg); id != 0 {
		opts.MessageThreadId = id
	}
	return opts
}

func chatActionOpts(msg *gotgbot.Message) *gotgbot.SendChatActionOpts {
	if msg == nil || msg.MessageThreadId == 0 {
		return nil
	}
	return &gotgbot.SendChatActionOpts{MessageThreadId: msg.MessageThreadId}
}

// keepTyping refreshes sendChatAction until stop; Telegram drops typing after ~5s.
func keepTyping(tg *gotgbot.Bot, msg *gotgbot.Message) func() {
	ctx, cancel := context.WithCancel(context.Background())
	go typingLoop(ctx, func() {
		_, _ = tg.SendChatAction(msg.Chat.Id, "typing", chatActionOpts(msg))
	}, typingRefresh)
	return cancel
}

func typingLoop(ctx context.Context, send func(), interval time.Duration) {
	send()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			send()
		}
	}
}

func (b *Bot) replyChunks(tg *gotgbot.Bot, msg *gotgbot.Message, workspace, text string) error {
	parts := media.SplitOutbound(workspace, text, b.cfg.Media.MaxFileBytes)
	if len(parts) == 0 {
		return b.sendTextChunks(tg, msg, "(empty reply)")
	}
	for _, p := range parts {
		if p.Kind == media.PartFile {
			if err := b.sendOutboundFile(tg, msg, p); err != nil {
				b.log.Error("outbound file", "path", p.RelPath, "err", redactToken(err.Error(), tg.Token))
				note := "Couldn't send `" + p.RelPath + "`."
				if sendErr := b.sendTextChunks(tg, msg, note); sendErr != nil {
					return sendErr
				}
			}
			continue
		}
		if err := b.sendTextChunks(tg, msg, p.Text); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bot) sendTextChunks(tg *gotgbot.Bot, msg *gotgbot.Message, text string) error {
	richOpts := richReplyOpts(msg)
	plainOpts := replyOpts(msg)
	for _, chunk := range splitTelegram(text, telegramRichMax) {
		if chunk == "" {
			continue
		}
		_, err := msg.ReplyRichMessage(tg, gotgbot.InputRichMessage{Markdown: chunk}, richOpts)
		if err == nil {
			continue
		}
		b.log.Error("rich reply failed; sending plain", "err", err, "chars", len(chunk))
		for _, plain := range splitTelegram(chunk, telegramPlainMax) {
			if _, err := msg.Reply(tg, plain, plainOpts); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *Bot) sendOutboundFile(tg *gotgbot.Bot, msg *gotgbot.Message, p media.OutboundPart) error {
	f, err := os.Open(p.AbsPath)
	if err != nil {
		return err
	}
	defer f.Close()

	input := gotgbot.InputFileByReader(p.FileName, f)
	caption := media.TruncateCaption(p.Caption)
	timeout := &gotgbot.RequestOpts{Timeout: telegramUpload}
	thread := topicThreadID(msg)

	sendDoc := func() error {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		opts := &gotgbot.SendDocumentOpts{Caption: caption, RequestOpts: timeout}
		if thread != 0 {
			opts.MessageThreadId = thread
		}
		_, err := msg.ReplyDocument(tg, input, opts)
		return err
	}

	if p.SendAs == "photo" {
		opts := &gotgbot.SendPhotoOpts{Caption: caption, RequestOpts: timeout}
		if thread != 0 {
			opts.MessageThreadId = thread
		}
		_, err := msg.ReplyPhoto(tg, input, opts)
		if err == nil {
			b.log.Info("outbound file", "path", p.RelPath, "as", "photo", "bytes", p.Bytes)
			return nil
		}
		b.log.Error("photo send failed; sending as document", "path", p.RelPath, "err", redactToken(err.Error(), tg.Token))
		if err := sendDoc(); err != nil {
			return err
		}
		b.log.Info("outbound file", "path", p.RelPath, "as", "document", "bytes", p.Bytes)
		return nil
	}

	if err := sendDoc(); err != nil {
		return err
	}
	b.log.Info("outbound file", "path", p.RelPath, "as", "document", "bytes", p.Bytes)
	return nil
}

func splitTelegram(s string, max int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return []string{s}
	}
	if max <= 0 {
		max = telegramPlainMax
	}
	var parts []string
	for len(s) > max {
		cut := strings.LastIndex(s[:max], "\n")
		if cut < max/2 {
			cut = max
		}
		parts = append(parts, s[:cut])
		s = strings.TrimSpace(s[cut:])
	}
	if s != "" {
		parts = append(parts, s)
	}
	return parts
}
