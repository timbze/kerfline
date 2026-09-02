package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"kerfline/internal/config"
)

type fakeFiles struct {
	path      string
	size      int64
	body      []byte
	lookupErr error
	fetched   bool
}

func (f *fakeFiles) Lookup(context.Context, string) (string, int64, error) {
	if f.lookupErr != nil {
		return "", 0, f.lookupErr
	}
	return f.path, f.size, nil
}

func (f *fakeFiles) Fetch(context.Context, string) (io.ReadCloser, error) {
	f.fetched = true
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

func TestExtractPhotoAndReply(t *testing.T) {
	photo := &gotgbot.Message{
		MessageId: 4821,
		Date:      1,
		Chat:      gotgbot.Chat{Id: -100},
		Caption:   "a red truck",
		Photo: []gotgbot.PhotoSize{
			{FileId: "small", FileUniqueId: "u1", Width: 90, Height: 90},
			{FileId: "big", FileUniqueId: "AgAD-ok", Width: 1280, Height: 960, FileSize: 184320},
		},
	}
	ref := FromMessage(photo)
	if ref == nil || ref.FileID != "big" || ref.MIME != "image/jpeg" || ref.Width != 1280 {
		t.Fatalf("photo: %+v", ref)
	}
	reply := &gotgbot.Message{Text: "/ask save this", ReplyToMessage: photo}
	got := Extract(reply)
	if got == nil || got.Source != "reply_to" || got.FileID != "big" {
		t.Fatalf("reply: %+v", got)
	}
	self := Extract(photo)
	if self == nil || self.Source != "message" {
		t.Fatalf("self: %+v", self)
	}
}

func TestExtractDocument(t *testing.T) {
	msg := &gotgbot.Message{
		MessageId: 9,
		Document: &gotgbot.Document{
			FileId:       "d1",
			FileUniqueId: "uniq",
			FileName:     "xray.pdf",
			MimeType:     "application/pdf",
			FileSize:     1200,
		},
	}
	ref := FromMessage(msg)
	if ref == nil || ref.Kind != "document" || ref.FileName != "xray.pdf" {
		t.Fatalf("%+v", ref)
	}
}

func TestRelPathSanitizes(t *testing.T) {
	ref := AttachmentRef{MessageID: 12, FileUniqueID: "AgAD_ok", MIME: "image/jpeg"}
	got := RelPath("notes", ref)
	if got != ".local/telegram-inbox/notes/12-AgAD_ok.jpg" {
		t.Fatalf("safe: %s", got)
	}
	unsafe := RelPath("../other", AttachmentRef{MessageID: 1, FileUniqueID: "x/y", MIME: "application/pdf"})
	if strings.Contains(unsafe, "..") || strings.Contains(unsafe, "/") && !strings.HasPrefix(unsafe, InboxDir) {
		t.Fatalf("unsafe leaked: %s", unsafe)
	}
	if !strings.HasSuffix(unsafe, ".pdf") {
		t.Fatalf("ext: %s", unsafe)
	}
	parts := strings.Split(unsafe, "/")
	for _, p := range parts {
		if p == ".." || strings.Contains(p, "/") {
			t.Fatalf("component %q", p)
		}
	}
}

func TestExtForMIME(t *testing.T) {
	if ExtForMIME("image/jpeg") != "jpg" || ExtForMIME("application/pdf") != "pdf" || ExtForMIME("foo") != "bin" {
		t.Fatal("ext mapping")
	}
}

func TestFormatAttachment(t *testing.T) {
	staged := StagedFile{
		Ref: AttachmentRef{
			Source:    "reply_to",
			MessageID: 4821,
			Kind:      "photo",
			Width:     1280,
			Height:    960,
			Date:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Unix(),
		},
		RelPath: ".local/telegram-inbox/notes/4821-AgADxxxx.jpg",
		Bytes:   184320,
	}
	got := FormatAttachment(staged, VisionPromptLine(ClassSave, "image/jpeg", false))
	for _, s := range []string{
		"handle: .local/telegram-inbox/notes/4821-AgADxxxx.jpg",
		"photo (Telegram-compressed JPEG)",
		"source: reply_to  message_id=4821  date=2026-09-01",
		"size: 1280x960  bytes=184320",
		"vision: not attached",
		"Do not Read/Grep/open",
	} {
		if !strings.Contains(got, s) {
			t.Fatalf("missing %q in %q", s, got)
		}
	}
	if strings.Contains(got, "attached.") && !strings.Contains(got, "not attached") {
		t.Fatal("should not claim attached")
	}
}

func gitWorkspace(t *testing.T, ignore bool) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	if ignore {
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".local/\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestMaterializeWritesAndIgnoresGit(t *testing.T) {
	ws := gitWorkspace(t, true)
	body := []byte("jpeg-bytes")
	files := &fakeFiles{path: "photos/x.jpg", size: int64(len(body)), body: body}
	s := NewStore(files, nil)
	chat := config.Chat{Name: "notes", Workspace: ws}
	ref := AttachmentRef{MessageID: 7, FileID: "f", FileUniqueID: "uniq1", MIME: "image/jpeg", Kind: "photo"}
	staged, err := s.Materialize(context.Background(), chat, ref, 1000, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(staged.AbsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, body) {
		t.Fatalf("bytes %q", raw)
	}
	info, err := os.Stat(staged.AbsPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	if staged.RelPath != ".local/telegram-inbox/notes/7-uniq1.jpg" {
		t.Fatalf("rel %s", staged.RelPath)
	}
}

func TestMaterializeRefusesIfNotIgnored(t *testing.T) {
	ws := gitWorkspace(t, false)
	s := NewStore(&fakeFiles{path: "x", body: []byte("a")}, nil)
	_, err := s.Materialize(context.Background(), config.Chat{Name: "n", Workspace: ws},
		AttachmentRef{MessageID: 1, FileID: "f", FileUniqueID: "u", MIME: "image/jpeg"}, 1000, time.Hour)
	if err == nil || UserMessage(err) != "refusing to stage: `.local/` is not gitignored." {
		t.Fatalf("err=%v", err)
	}
}

func TestMaterializeTooLargeUsesLimitReader(t *testing.T) {
	ws := gitWorkspace(t, true)
	files := &fakeFiles{path: "x", size: 0, body: bytes.Repeat([]byte("a"), 50)}
	s := NewStore(files, nil)
	_, err := s.Materialize(context.Background(), config.Chat{Name: "n", Workspace: ws},
		AttachmentRef{MessageID: 1, FileID: "f", FileUniqueID: "u", MIME: "image/jpeg"}, 10, time.Hour)
	if err == nil || !errors.As(err, new(*StageError)) {
		t.Fatalf("err=%v", err)
	}
	if UserMessage(err) != "That file is too large (max 20 MB)." {
		t.Fatalf("msg %q", UserMessage(err))
	}
	if !files.fetched {
		t.Fatal("expected fetch when FileSize omitted")
	}
}

func TestMaterializeRejectsDeclaredSizeBeforeFetch(t *testing.T) {
	ws := gitWorkspace(t, true)
	files := &fakeFiles{path: "x", body: []byte("a")}
	s := NewStore(files, nil)
	_, err := s.Materialize(context.Background(), config.Chat{Name: "n", Workspace: ws},
		AttachmentRef{MessageID: 1, FileID: "f", FileUniqueID: "u", MIME: "image/jpeg", FileSize: 99}, 10, time.Hour)
	if err == nil {
		t.Fatal("expected too large")
	}
	if files.fetched {
		t.Fatal("must not fetch when declared size exceeds cap")
	}
}

func TestMaterializeTTL(t *testing.T) {
	ws := gitWorkspace(t, true)
	oldDir := filepath.Join(ws, InboxDir, "n")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(oldDir, "old.bin")
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	s := NewStore(&fakeFiles{path: "x", body: []byte("new")}, nil)
	_, err := s.Materialize(context.Background(), config.Chat{Name: "n", Workspace: ws},
		AttachmentRef{MessageID: 2, FileID: "f", FileUniqueID: "fresh", MIME: "image/jpeg"}, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old file should be pruned, err=%v", err)
	}
}

func TestTelegramFetchErrorHasNoToken(t *testing.T) {
	tfiles := &TelegramFiles{
		Token: "SECRETTOKEN",
		HTTP:  &http.Client{Timeout: time.Millisecond},
	}
	_, err := tfiles.Fetch(context.Background(), "photos/x.jpg")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "SECRETTOKEN") || strings.Contains(err.Error(), "botSECRET") {
		t.Fatalf("token leaked: %v", err)
	}
}

func TestInboxDenyRules(t *testing.T) {
	rules := InboxDenyRules()
	joined := strings.Join(rules, " ")
	for _, s := range []string{
		"Read(.local/telegram-inbox/**)",
		"Grep(.local/telegram-inbox/**)",
		"Read(**/.local/telegram-inbox/**)",
	} {
		if !strings.Contains(joined, s) {
			t.Fatalf("missing %s", s)
		}
	}
}
