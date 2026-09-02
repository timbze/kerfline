package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWS(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSendKind(t *testing.T) {
	if sendKind("x.jpg", 100) != "photo" {
		t.Fatal("jpg")
	}
	if sendKind("X.PNG", 100) != "photo" {
		t.Fatal("png")
	}
	if sendKind("x.webp", 100) != "photo" {
		t.Fatal("webp")
	}
	if sendKind("x.jpg", telegramPhotoMax+1) != "document" {
		t.Fatal("large jpg")
	}
	if sendKind("x.pdf", 100) != "document" {
		t.Fatal("pdf")
	}
	if sendKind("x.heic", 100) != "document" {
		t.Fatal("heic")
	}
}

func TestSplitOutboundImageOnly(t *testing.T) {
	ws := writeWS(t, map[string][]byte{"a.jpg": []byte("x")})
	parts := SplitOutbound(ws, "![solo](a.jpg)", 0)
	if len(parts) != 1 || parts[0].Kind != PartFile || parts[0].Caption != "solo" {
		t.Fatalf("%+v", parts)
	}
}

func TestSplitOutboundPhoto(t *testing.T) {
	ws := writeWS(t, map[string][]byte{
		"scans/xray.jpg": []byte("jpeg-bytes"),
	})
	md := "Yes, this is the x-ray.\n\n![lab photo, 24 Aug 2026](scans/xray.jpg)\n"
	parts := SplitOutbound(ws, md, 0)
	if len(parts) != 2 {
		t.Fatalf("parts: %+v", parts)
	}
	if parts[0].Kind != PartText || !strings.Contains(parts[0].Text, "Yes, this is the x-ray") {
		t.Fatalf("text: %+v", parts[0])
	}
	if parts[1].Kind != PartFile || parts[1].SendAs != "photo" {
		t.Fatalf("file: %+v", parts[1])
	}
	if parts[1].RelPath != "scans/xray.jpg" {
		t.Fatalf("rel: %s", parts[1].RelPath)
	}
	if parts[1].Caption != "lab photo, 24 Aug 2026" {
		t.Fatalf("caption: %q", parts[1].Caption)
	}
	if parts[1].FileName != "xray.jpg" || parts[1].Bytes != 10 {
		t.Fatalf("meta: %+v", parts[1])
	}
}

func TestSplitOutboundDocumentAndTitle(t *testing.T) {
	ws := writeWS(t, map[string][]byte{"report.pdf": []byte("%PDF")})
	parts := SplitOutbound(ws, `See ![](<report.pdf> "Lab report")`, 0)
	if len(parts) != 2 || parts[0].Text != "See" {
		t.Fatalf("%+v", parts)
	}
	if parts[1].SendAs != "document" || parts[1].Caption != "Lab report" {
		t.Fatalf("%+v", parts[1])
	}
}

func TestSplitOutboundSkipsCodeAndRemote(t *testing.T) {
	ws := writeWS(t, map[string][]byte{"a.jpg": []byte("x")})
	md := "```\n![no](a.jpg)\n```\n\ninline `![no](a.jpg)` and ![ok](https://example.com/a.jpg)\n"
	parts := SplitOutbound(ws, md, 0)
	if len(parts) != 1 || parts[0].Kind != PartText {
		t.Fatalf("%+v", parts)
	}
	if strings.Count(parts[0].Text, "a.jpg") < 3 {
		t.Fatalf("should keep all three mentions: %q", parts[0].Text)
	}
}

func TestSplitOutboundMissingAndTraversal(t *testing.T) {
	ws := writeWS(t, map[string][]byte{"ok.jpg": []byte("x")})
	outside := filepath.Join(ws, "..", "outside.jpg")
	if err := os.WriteFile(outside, []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}

	parts := SplitOutbound(ws, "![a](missing.jpg) then ![b](../outside.jpg)", 0)
	if len(parts) != 1 || parts[0].Kind != PartText {
		t.Fatalf("%+v", parts)
	}
	if !strings.Contains(parts[0].Text, "couldn't send `missing.jpg`: not found") {
		t.Fatalf("missing: %q", parts[0].Text)
	}
	if !strings.Contains(parts[0].Text, "not in the workspace") && !strings.Contains(parts[0].Text, "not found") {
		t.Fatalf("traversal: %q", parts[0].Text)
	}
	if strings.Contains(parts[0].Text, outside) {
		t.Fatalf("abs path leaked: %q", parts[0].Text)
	}
}

func TestSplitOutboundBlocksInbox(t *testing.T) {
	ws := writeWS(t, map[string][]byte{
		".local/telegram-inbox/x.jpg": []byte("x"),
		".git/HEAD":                   []byte("ref"),
	})
	parts := SplitOutbound(ws, "![a](.local/telegram-inbox/x.jpg) ![b](.git/HEAD)", 0)
	if len(parts) != 1 || strings.Contains(parts[0].Text, "photo") {
		t.Fatalf("%+v", parts)
	}
	if !strings.Contains(parts[0].Text, "not in the workspace") {
		t.Fatalf("%q", parts[0].Text)
	}
}

func TestSplitOutboundTooLarge(t *testing.T) {
	ws := writeWS(t, map[string][]byte{"big.jpg": []byte("12345")})
	parts := SplitOutbound(ws, "![a](big.jpg)", 3)
	if len(parts) != 1 || !strings.Contains(parts[0].Text, "too large") {
		t.Fatalf("%+v", parts)
	}
}

func TestSplitOutboundAbsoluteInsideWorkspace(t *testing.T) {
	ws := writeWS(t, map[string][]byte{"a.jpg": []byte("x")})
	abs := filepath.Join(ws, "a.jpg")
	parts := SplitOutbound(ws, "![inside]("+abs+")", 0)
	if len(parts) != 1 || parts[0].Kind != PartFile || parts[0].RelPath != "a.jpg" {
		t.Fatalf("%+v", parts)
	}
}

func TestTruncateCaption(t *testing.T) {
	if TruncateCaption("  hi  ") != "hi" {
		t.Fatal("trim")
	}
	long := strings.Repeat("é", telegramCaptionMax+8)
	got := TruncateCaption(long)
	if strings.Count(got, "é") != telegramCaptionMax {
		t.Fatalf("runes: %d", strings.Count(got, "é"))
	}
}

func TestSplitOutboundEmptyWorkspace(t *testing.T) {
	parts := SplitOutbound("", "![a](a.jpg)", 0)
	if len(parts) != 1 || parts[0].Kind != PartText {
		t.Fatalf("%+v", parts)
	}
}

func TestSplitOutboundTextAfterAndTwoFiles(t *testing.T) {
	ws := writeWS(t, map[string][]byte{
		"a.jpg": []byte("a"),
		"b.pdf": []byte("b"),
	})
	md := "before\n\n![one](a.jpg)\n\nmid\n\n![two](b.pdf)\n\nafter"
	parts := SplitOutbound(ws, md, 0)
	if len(parts) != 5 {
		t.Fatalf("n=%d %+v", len(parts), parts)
	}
	want := []PartKind{PartText, PartFile, PartText, PartFile, PartText}
	for i, k := range want {
		if parts[i].Kind != k {
			t.Fatalf("part %d: %+v", i, parts[i])
		}
	}
	if parts[0].Text != "before" || parts[2].Text != "mid" || parts[4].Text != "after" {
		t.Fatalf("text: %+v", parts)
	}
	if parts[1].SendAs != "photo" || parts[3].SendAs != "document" {
		t.Fatalf("kinds: %+v %+v", parts[1], parts[3])
	}
}

func TestSplitOutboundSymlinkEscape(t *testing.T) {
	ws := writeWS(t, map[string][]byte{"ok.jpg": []byte("x")})
	outside := filepath.Join(t.TempDir(), "secret.jpg")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, "link.jpg")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	parts := SplitOutbound(ws, "![x](link.jpg)", 0)
	if len(parts) != 1 || parts[0].Kind != PartText || !strings.Contains(parts[0].Text, "not in the workspace") {
		t.Fatalf("symlink escape: %+v", parts)
	}
}
