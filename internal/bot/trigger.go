package bot

import (
	"strings"
	"unicode"
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
