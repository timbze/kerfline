package bot

import (
	"regexp"
	"strings"
)

// Optional greeting, then kerf/kerfline/grok as a word at the start, optional comma.
var vocativeStart = regexp.MustCompile(`(?i)^(?:(?:hey|hi|hei)\s+)?(?:kerfline|kerf|grok)\b,?\s*`)

// Vocative reports whether text opens with a nick address (optional hey/hi/hei,
// then kerf/kerfline/grok, optional comma). Mid-sentence names do not count.
func Vocative(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	return vocativeStart.MatchString(text)
}
