package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters/message"

	"kerfline/internal/config"
	"kerfline/internal/media"
	"kerfline/internal/runner"
	"kerfline/internal/session"
)

const (
	// telegramRichMax is under the 32768-character rich-message limit (counted in bytes here).
	telegramRichMax = 30000
	// telegramPlainMax is under the 4096-character sendMessage limit, used only as fallback.
	telegramPlainMax = 3900
	typingRefresh    = 4 * time.Second
)

type Bot struct {
	cfg     *config.Config
	log     *slog.Logger
	runner  *runner.Runner
	sess    *session.Store
	media   *media.Store
	updater *ext.Updater
	disp    *ext.Dispatcher
}

func New(cfg *config.Config, log *slog.Logger, r *runner.Runner, sess *session.Store) *Bot {
	return &Bot{cfg: cfg, log: log, runner: r, sess: sess, media: media.NewStore(nil, log)}
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
	if !want {
		return nil
	}
	if inaccessibleReplyMedia(msg) {
		_, err := msg.Reply(tg, "I don't have that file anymore; send it again.", replyOpts(msg))
		return err
	}

	req := runner.Request{Prompt: prompt}
	attrs := []any{"chat", chat.Name, "user_id", msg.From.Id}

	if ref := media.Extract(msg); ref != nil {
		class := media.Classify(classifyInput(msg, ref))
		vision := b.cfg.EffectiveVision(chat)
		_, skipLook := media.AttachVision(class, ref.MIME, vision)
		if skipLook {
			b.log.Info("media skip", "reason", "look_disabled", "chat", chat.Name)
			_, err := msg.Reply(tg, "Looking at pictures is off for this chat.", replyOpts(msg))
			return err
		}
		dlCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		staged, err := b.media.Materialize(dlCtx, chat, *ref, b.cfg.Media.MaxFileBytes, b.cfg.InboxTTL())
		cancel()
		if err != nil {
			b.log.Error("media download", "chat", chat.Name, "err", redactToken(err.Error(), tg.Token))
			_, sendErr := msg.Reply(tg, redactToken(media.UserMessage(err), tg.Token), replyOpts(msg))
			return sendErr
		}
		b.log.Info("media download",
			"chat", chat.Name,
			"message_id", ref.MessageID,
			"file_unique_id", ref.FileUniqueID,
			"bytes", staged.Bytes,
			"kind", ref.Kind)
		req.Prompt += media.FormatAttachment(staged, media.VisionPromptLine(class, ref.MIME, false))
		req.Deny = media.InboxDenyRules()
		attrs = append(attrs, "attachments", 1, "class", class.String(), "vision", false, "deny_inbox", true)
	}

	attrs = append(attrs, "chars", len(req.Prompt))
	if msg.MessageThreadId != 0 {
		attrs = append(attrs, "topic_id", msg.MessageThreadId)
	}
	b.log.Info("grok turn", attrs...)
	stopTyping := keepTyping(tg, msg)
	defer stopTyping()

	sessionID, resume := b.sess.ID(chat.Name)
	res, err := b.runner.Run(context.Background(), b.cfg, chat, req, sessionID, resume)
	if err != nil {
		b.log.Error("grok failed", "chat", chat.Name, "err", redactToken(err.Error(), tg.Token), "duration", res.Duration)
		_, sendErr := msg.Reply(tg, "Grok failed: "+redactToken(err.Error(), tg.Token), replyOpts(msg))
		return sendErr
	}
	if err := b.sess.Remember(chat.Name, sessionID); err != nil {
		b.log.Error("session persist", "err", err)
	}
	reply := res.Stdout
	if reply == "" {
		reply = "(empty reply)"
	}
	b.log.Info("grok done", "chat", chat.Name, "duration", res.Duration, "out_chars", len(reply))
	return b.replyChunks(tg, msg, reply)
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

func (b *Bot) replyChunks(tg *gotgbot.Bot, msg *gotgbot.Message, text string) error {
	richOpts := richReplyOpts(msg)
	plainOpts := replyOpts(msg)
	for _, chunk := range splitTelegram(text, telegramRichMax) {
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
