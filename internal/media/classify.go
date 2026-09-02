package media

import (
	"strings"
	"unicode"
)

type Class int

const (
	ClassSave Class = iota
	ClassLook
	ClassMixed
)

func (c Class) String() string {
	switch c {
	case ClassLook:
		return "look"
	case ClassMixed:
		return "mixed"
	default:
		return "save"
	}
}

var saveUnigrams = map[string]struct{}{
	"save": {}, "store": {}, "keep": {}, "file": {}, "archive": {}, "records": {},
}

var lookUnigrams = map[string]struct{}{
	"look": {}, "looking": {}, "transcribe": {}, "extract": {}, "describe": {}, "diagnose": {},
}

var saveNGrams = [][]string{
	{"put", "this"},
	{"put", "it"},
}

var lookNGrams = [][]string{
	{"what", "is"},
	{"what", "does"},
	{"whats", "in"},
	{"whats", "this"},
	{"whats", "that"},
	{"tell", "me"},
	{"can", "you", "see"},
}

// Classify tokenizes text and returns save, look, or mixed.
// When unsure (no look phrases), the result is ClassSave.
func Classify(text string) Class {
	tokens := tokenize(text)
	save := hasUnigram(tokens, saveUnigrams) || hasNGram(tokens, saveNGrams)
	look := hasUnigram(tokens, lookUnigrams) || hasNGram(tokens, lookNGrams)
	switch {
	case save && look:
		return ClassMixed
	case look:
		return ClassLook
	default:
		return ClassSave
	}
}

func tokenize(text string) []string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if r == '\'' || r == '’' {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	s := b.String()
	var tokens []string
	start := -1
	for i, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			tokens = append(tokens, s[start:i])
			start = -1
		}
	}
	if start >= 0 {
		tokens = append(tokens, s[start:])
	}
	return tokens
}

func hasUnigram(tokens []string, set map[string]struct{}) bool {
	for _, t := range tokens {
		if _, ok := set[t]; ok {
			return true
		}
	}
	return false
}

func hasNGram(tokens []string, phrases [][]string) bool {
	for _, phrase := range phrases {
		n := len(phrase)
		if n == 0 || len(tokens) < n {
			continue
		}
		for i := 0; i+n <= len(tokens); i++ {
			match := true
			for j := range phrase {
				if tokens[i+j] != phrase[j] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

const (
	VisionAuto   = "auto"
	VisionAlways = "always"
	VisionNever  = "never"
)

// VisionMIME is true for types xAI image understanding accepts in v1.
func VisionMIME(mime string) bool {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg", "image/png":
		return true
	default:
		return false
	}
}

// AttachVision reports whether pixels should go in the model request, and
// whether a look-only turn should be skipped (vision=never).
func AttachVision(class Class, mime, vision string) (attach bool, skipLook bool) {
	vis := VisionMIME(mime)
	switch vision {
	case VisionAlways:
		return vis, false
	case VisionNever:
		if class == ClassLook {
			return false, true
		}
		return false, false
	default:
		if class == ClassLook || class == ClassMixed {
			return vis, false
		}
		return false, false
	}
}
