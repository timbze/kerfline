package media

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	PartText PartKind = iota
	PartFile

	maxOutboundFiles   = 20
	telegramPhotoMax   = 10_000_000
	telegramCaptionMax = 1024
)

type PartKind int

// OutboundPart is one piece of a Grok reply: remaining Markdown, or a workspace file to upload.
type OutboundPart struct {
	Kind     PartKind
	Text     string
	RelPath  string
	AbsPath  string
	FileName string
	Caption  string
	SendAs   string // "photo" | "document"
	Bytes    int64
}

var mdImage = regexp.MustCompile(`!\[([^\]]*)\]\(\s*(<[^>\n]+>|[^\s)]+)\s*(?:"([^"]*)")?\s*\)`)

// SplitOutbound turns Markdown images that point at workspace files into upload parts.
// Remote URLs and images inside code are left in the text. Failed local paths become a short note.
func SplitOutbound(workspace, markdown string, maxBytes int64) []OutboundPart {
	if maxBytes <= 0 {
		maxBytes = 20_000_000
	}
	s := strings.TrimSpace(markdown)
	if s == "" {
		return nil
	}
	if workspace == "" {
		return []OutboundPart{{Kind: PartText, Text: s}}
	}

	code := maskCode(s)
	matches := mdImage.FindAllStringSubmatchIndex(s, -1)
	var parts []OutboundPart
	var buf strings.Builder
	last := 0
	files := 0
	flush := func() {
		if t := strings.TrimSpace(buf.String()); t != "" {
			parts = append(parts, OutboundPart{Kind: PartText, Text: t})
		}
		buf.Reset()
	}

	for _, m := range matches {
		start, end := m[0], m[1]
		if start < last || inCode(code, start, end) {
			continue
		}
		alt := s[m[2]:m[3]]
		rawURL := s[m[4]:m[5]]
		title := ""
		if m[6] >= 0 {
			title = s[m[6]:m[7]]
		}
		target := unwrapURL(rawURL)
		if isRemote(target) {
			continue
		}
		buf.WriteString(s[last:start])
		if files >= maxOutboundFiles {
			buf.WriteString(sendNote(target, "too many files in one reply"))
			last = end
			continue
		}
		file, err := resolveOutbound(workspace, target, maxBytes)
		if err != nil {
			buf.WriteString(sendNote(displayPath(target), err.Error()))
			last = end
			continue
		}
		flush()
		file.Caption = firstNonEmpty(title, alt)
		parts = append(parts, file)
		files++
		last = end
	}
	buf.WriteString(s[last:])
	flush()
	if len(parts) == 0 {
		return []OutboundPart{{Kind: PartText, Text: s}}
	}
	return parts
}

func TruncateCaption(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) <= telegramCaptionMax {
		return s
	}
	r := []rune(s)
	return string(r[:telegramCaptionMax])
}

func sendKind(name string, size int64) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "jpg", "jpeg", "png", "webp":
		if size > 0 && size > telegramPhotoMax {
			return "document"
		}
		return "photo"
	default:
		return "document"
	}
}

func unwrapURL(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '<' && s[len(s)-1] == '>' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	if u, err := url.PathUnescape(s); err == nil {
		s = u
	}
	return s
}

func isRemote(u string) bool {
	l := strings.ToLower(strings.TrimSpace(u))
	if l == "" || strings.HasPrefix(l, "#") {
		return true
	}
	if strings.Contains(l, "://") || strings.HasPrefix(l, "//") {
		return true
	}
	return strings.HasPrefix(l, "mailto:")
}

func displayPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.TrimPrefix(p, "./")
}

func sendNote(path, reason string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "file"
	}
	return "*couldn't send `" + path + "`: " + reason + "*"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func inCode(mask []bool, start, end int) bool {
	if start < 0 || start >= len(mask) {
		return false
	}
	if end > len(mask) {
		end = len(mask)
	}
	for i := start; i < end; i++ {
		if mask[i] {
			return true
		}
	}
	return false
}

func maskCode(s string) []bool {
	mask := make([]bool, len(s))
	i := 0
	n := len(s)
	var fence string
	for i < n {
		lineStart := i
		lineEnd := i
		for lineEnd < n && s[lineEnd] != '\n' {
			lineEnd++
		}
		line := s[lineStart:lineEnd]
		if fence != "" {
			for k := lineStart; k < lineEnd; k++ {
				mask[k] = true
			}
			if fenceClose(line, fence) {
				fence = ""
			}
		} else if marker, ok := fenceOpen(line); ok {
			for k := lineStart; k < lineEnd; k++ {
				mask[k] = true
			}
			fence = marker
		} else {
			maskInline(line, lineStart, mask)
		}
		i = lineEnd
		if i < n && s[i] == '\n' {
			i++
		}
	}
	return mask
}

func fenceOpen(line string) (string, bool) {
	indent := 0
	i := 0
	for i < len(line) && indent < 4 && (line[i] == ' ' || line[i] == '\t') {
		if line[i] == '\t' {
			indent = 4
		} else {
			indent++
		}
		i++
	}
	if indent > 3 || i >= len(line) {
		return "", false
	}
	ch := line[i]
	if ch != '`' && ch != '~' {
		return "", false
	}
	n := 0
	for i+n < len(line) && line[i+n] == ch {
		n++
	}
	if n < 3 {
		return "", false
	}
	return line[i : i+n], true
}

func fenceClose(line, marker string) bool {
	if marker == "" {
		return false
	}
	indent := 0
	i := 0
	for i < len(line) && indent < 4 && (line[i] == ' ' || line[i] == '\t') {
		if line[i] == '\t' {
			indent = 4
		} else {
			indent++
		}
		i++
	}
	if indent > 3 || i >= len(line) || line[i] != marker[0] {
		return false
	}
	n := 0
	for i+n < len(line) && line[i+n] == marker[0] {
		n++
	}
	if n < len(marker) {
		return false
	}
	return strings.TrimSpace(line[i+n:]) == ""
}

func maskInline(line string, base int, mask []bool) {
	i := 0
	for i < len(line) {
		if line[i] != '`' {
			i++
			continue
		}
		n := 0
		for i+n < len(line) && line[i+n] == '`' {
			n++
		}
		j := i + n
		found := false
		for j < len(line) {
			if line[j] != '`' {
				j++
				continue
			}
			m := 0
			for j+m < len(line) && line[j+m] == '`' {
				m++
			}
			if m == n {
				for k := i; k < j+m; k++ {
					mask[base+k] = true
				}
				i = j + m
				found = true
				break
			}
			j += m
		}
		if !found {
			i += n
		}
	}
}

func resolveOutbound(workspace, raw string, maxBytes int64) (OutboundPart, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return OutboundPart{}, errReason("not found")
	}
	ws, err := filepath.Abs(workspace)
	if err != nil {
		return OutboundPart{}, errReason("not in the workspace")
	}
	if resolved, err := filepath.EvalSymlinks(ws); err == nil {
		ws = resolved
	}

	p := filepath.FromSlash(raw)
	if !filepath.IsAbs(p) {
		p = filepath.Join(ws, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return OutboundPart{}, errReason("not found")
	}
	rel, err := filepath.Rel(ws, abs)
	if err != nil || !relInside(rel) || blockedRel(rel) {
		return OutboundPart{}, errReason("not in the workspace")
	}
	askedRel := filepath.ToSlash(rel)

	info, err := os.Lstat(abs)
	if err != nil {
		return OutboundPart{}, errReason("not found")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return OutboundPart{}, errReason("not found")
		}
		rel, err = filepath.Rel(ws, resolved)
		if err != nil || !relInside(rel) || blockedRel(rel) {
			return OutboundPart{}, errReason("not in the workspace")
		}
		abs = resolved
		info, err = os.Stat(abs)
		if err != nil {
			return OutboundPart{}, errReason("not found")
		}
	}
	if !info.Mode().IsRegular() {
		return OutboundPart{}, errReason("not a file")
	}
	if info.Size() > maxBytes {
		return OutboundPart{}, errReason("too large")
	}

	name := filepath.Base(abs)
	return OutboundPart{
		Kind:     PartFile,
		RelPath:  askedRel,
		AbsPath:  abs,
		FileName: name,
		SendAs:   sendKind(name, info.Size()),
		Bytes:    info.Size(),
	}, nil
}

func relInside(rel string) bool {
	if rel == "." {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func blockedRel(rel string) bool {
	for _, p := range strings.Split(rel, string(filepath.Separator)) {
		switch p {
		case ".git", ".local", ".jailbee":
			return true
		}
	}
	return false
}

type reasonError string

func (e reasonError) Error() string { return string(e) }

func errReason(s string) error { return reasonError(s) }
