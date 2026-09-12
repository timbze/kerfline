package media

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptJSONRelPath(t *testing.T) {
	got := PromptJSONRelPath(".local/telegram-inbox/notes/7-uniq1.jpg")
	want := ".local/telegram-inbox/notes/7-uniq1.prompt.json"
	if got != want {
		t.Fatalf("%s", got)
	}
}

func TestEncodePromptJSON(t *testing.T) {
	raw := []byte{0xff, 0xd8, 0xff}
	body, err := EncodePromptJSON("what's this\n\n---\nAttachment", "image/jpeg", raw)
	if err != nil {
		t.Fatal(err)
	}
	var blocks []map[string]string
	if err := json.Unmarshal(body, &blocks); err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks %d", len(blocks))
	}
	if blocks[0]["type"] != "text" || !strings.Contains(blocks[0]["text"], "what's this") {
		t.Fatalf("text %+v", blocks[0])
	}
	if blocks[1]["type"] != "image" || blocks[1]["mimeType"] != "image/jpeg" {
		t.Fatalf("image %+v", blocks[1])
	}
	got, err := base64.StdEncoding.DecodeString(blocks[1]["data"])
	if err != nil || string(got) != string(raw) {
		t.Fatalf("data %q err=%v", blocks[1]["data"], err)
	}
}

func TestEncodePromptJSONRejectsEmpty(t *testing.T) {
	if _, err := EncodePromptJSON("x", "image/jpeg", nil); err == nil {
		t.Fatal("expected error")
	}
	if _, err := EncodePromptJSON("x", "", []byte("a")); err == nil {
		t.Fatal("expected error")
	}
}

func TestWritePromptJSON(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "7-uniq1.jpg")
	if err := os.WriteFile(abs, []byte("jpeg-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := StagedFile{
		AbsPath: abs,
		RelPath: ".local/telegram-inbox/notes/7-uniq1.jpg",
		Ref:     AttachmentRef{MIME: "image/jpeg"},
	}
	rel, err := WritePromptJSON(staged, "describe this")
	if err != nil {
		t.Fatal(err)
	}
	if rel != ".local/telegram-inbox/notes/7-uniq1.prompt.json" {
		t.Fatalf("rel %s", rel)
	}
	out := filepath.Join(dir, "7-uniq1.prompt.json")
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"type":"image"`) || !strings.Contains(string(body), "describe this") {
		t.Fatalf("%s", body)
	}
}
