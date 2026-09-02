package bot

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

// PromptFromMessage extracts the Grok prompt from a Telegram message.
// ok is false when this message should be ignored (ordinary group chatter).
func PromptFromMessage(text, botUsername string, requireMention bool) (prompt string, ok bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}

	if cmd, rest, isCmd := splitCommand(text); isCmd {
		if cmd == "ask" {
			rest = strings.TrimSpace(rest)
			if rest == "" {
				return "", false
			}
			return rest, true
		}
		return "", false
	}

	if botUsername != "" {
		mention := "@" + strings.TrimPrefix(botUsername, "@")
		if stripped, found := stripMention(text, mention); found {
			stripped = strings.TrimSpace(stripped)
			if stripped == "" {
				return "", false
			}
			return stripped, true
		}
	}

	if !requireMention {
		return text, true
	}
	return "", false
}

// BuildGrokPrompt turns a Telegram message into the Grok prompt.
// ok is false when this message should be ignored (ordinary group chatter).
// If the message is a reply, the original body is included before the user's text.
func BuildGrokPrompt(msg *gotgbot.Message, botUsername string, requireMention bool) (prompt string, ok bool) {
	if msg == nil {
		return "", false
	}
	text := msg.GetText()
	userText, want := PromptFromMessage(text, botUsername, requireMention)
	quoted := replyContext(msg)
	if !want {
		if quoted == "" || !addressedWithoutPrompt(text, botUsername) {
			return "", false
		}
		return quoted, true
	}
	if quoted == "" {
		return userText, true
	}
	return quoted + "\n\nUser:\n" + userText, true
}

func addressedWithoutPrompt(text, botUsername string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if cmd, rest, isCmd := splitCommand(text); isCmd {
		return cmd == "ask" && strings.TrimSpace(rest) == ""
	}
	if botUsername == "" {
		return false
	}
	mention := "@" + strings.TrimPrefix(botUsername, "@")
	stripped, found := stripMention(text, mention)
	return found && strings.TrimSpace(stripped) == ""
}

func replyContext(msg *gotgbot.Message) string {
	if msg == nil || msg.ReplyToMessage == nil {
		return ""
	}
	orig := msg.ReplyToMessage
	if skipReplyOriginal(orig) {
		return ""
	}
	body := strings.TrimSpace(orig.GetRawText())
	if body == "" {
		body = mediaLabel(orig)
	}
	if body == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("The user is replying to this Telegram message:\nFrom: ")
	b.WriteString(senderName(orig))
	b.WriteByte('\n')
	b.WriteString(body)
	if msg.Quote != nil && msg.Quote.IsManual {
		excerpt := strings.TrimSpace(msg.Quote.Text)
		if excerpt != "" {
			b.WriteString("\n\nQuoted excerpt:\n")
			b.WriteString(excerpt)
		}
	}
	return b.String()
}

func skipReplyOriginal(m *gotgbot.Message) bool {
	if m == nil {
		return true
	}
	return m.ForumTopicCreated != nil ||
		m.ForumTopicEdited != nil ||
		m.ForumTopicClosed != nil ||
		m.ForumTopicReopened != nil ||
		m.GeneralForumTopicHidden != nil ||
		m.GeneralForumTopicUnhidden != nil ||
		m.PinnedMessage != nil
}

func senderName(m *gotgbot.Message) string {
	if m == nil || m.From == nil {
		return "unknown"
	}
	if m.From.IsBot {
		return "the bot"
	}
	name := strings.TrimSpace(m.From.FirstName + " " + m.From.LastName)
	if name != "" {
		return name
	}
	if m.From.Username != "" {
		return m.From.Username
	}
	return "unknown"
}

func mediaLabel(m *gotgbot.Message) string {
	if m == nil {
		return ""
	}
	switch {
	case len(m.Photo) > 0:
		p := m.Photo[len(m.Photo)-1]
		if p.Width > 0 && p.Height > 0 {
			return fmt.Sprintf("[photo %dx%d]", p.Width, p.Height)
		}
		return "[photo]"
	case m.Animation != nil:
		return "[animation]"
	case m.Video != nil:
		return "[video]"
	case m.VideoNote != nil:
		return "[video note]"
	case m.Voice != nil:
		return "[voice]"
	case m.Audio != nil:
		if m.Audio.Title != "" {
			return "[audio: " + m.Audio.Title + "]"
		}
		if m.Audio.FileName != "" {
			return "[audio: " + m.Audio.FileName + "]"
		}
		return "[audio]"
	case m.Document != nil:
		return documentLabel(m.Document)
	case m.Sticker != nil:
		return "[sticker]"
	case m.Poll != nil && m.Poll.Question != "":
		return "[poll: " + m.Poll.Question + "]"
	case m.Location != nil:
		return "[location]"
	case m.Contact != nil:
		return "[contact]"
	default:
		return ""
	}
}

func documentLabel(d *gotgbot.Document) string {
	if d == nil {
		return "[document]"
	}
	if d.FileName != "" && d.MimeType == "" && d.FileSize == 0 {
		return "[document: " + d.FileName + "]"
	}
	if d.FileName == "" && d.MimeType == "" && d.FileSize == 0 {
		return "[document]"
	}
	var b strings.Builder
	b.WriteString("[document")
	if d.FileName != "" {
		b.WriteString(": ")
		b.WriteString(d.FileName)
	}
	inner := strings.TrimSpace(d.MimeType)
	if d.FileSize > 0 {
		if inner != "" {
			inner += ", "
		}
		inner += formatFileSize(d.FileSize)
	}
	if inner != "" {
		if d.FileName == "" {
			b.WriteString(": ")
		} else {
			b.WriteByte(' ')
		}
		b.WriteByte('(')
		b.WriteString(inner)
		b.WriteByte(')')
	}
	b.WriteByte(']')
	return b.String()
}

func formatFileSize(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fMB", float64(n)/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%.1fKB", float64(n)/1000)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func splitCommand(text string) (cmd, rest string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	body := text[1:]
	i := strings.IndexFunc(body, func(r rune) bool {
		return unicode.IsSpace(r)
	})
	token, rest := body, ""
	if i >= 0 {
		token, rest = body[:i], body[i+1:]
	}
	cmd, _, _ = strings.Cut(token, "@")
	cmd = strings.ToLower(cmd)
	if cmd == "" {
		return "", "", false
	}
	return cmd, rest, true
}

func stripMention(text, mention string) (string, bool) {
	if mention == "" || mention == "@" {
		return text, false
	}
	lowerText := strings.ToLower(text)
	lowerMention := strings.ToLower(mention)
	idx := strings.Index(lowerText, lowerMention)
	if idx < 0 {
		return text, false
	}
	before := strings.TrimSpace(text[:idx])
	after := strings.TrimSpace(text[idx+len(mention):])
	after = strings.TrimPrefix(after, ",")
	after = strings.TrimSpace(after)
	switch {
	case before == "":
		return after, true
	case after == "":
		return before, true
	default:
		return strings.TrimSpace(before + " " + after), true
	}
}
