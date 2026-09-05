package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestIDNewThenResumeWithinTTL(t *testing.T) {
	s := openEmpty(t)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }

	id, resume := s.ID("notes")
	if resume {
		t.Fatal("first turn must not resume")
	}
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("id %q: %v", id, err)
	}
	if err := s.Remember("notes", id); err != nil {
		t.Fatal(err)
	}

	now = now.Add(DefaultIdleTTL - time.Second)
	got, resume := s.ID("notes")
	if !resume || got != id {
		t.Fatalf("got %q resume=%v want %q true", got, resume, id)
	}
}

func TestIDNewAfterIdleTTL(t *testing.T) {
	s := openEmpty(t)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }

	id, _ := s.ID("notes")
	if err := s.Remember("notes", id); err != nil {
		t.Fatal(err)
	}

	now = now.Add(DefaultIdleTTL)
	got, resume := s.ID("notes")
	if resume {
		t.Fatal("idle TTL elapsed; must start a new session")
	}
	if got == id {
		t.Fatalf("new session reused old id %q", id)
	}
	if _, err := uuid.Parse(got); err != nil {
		t.Fatalf("id %q: %v", got, err)
	}
}

func TestRememberExtendsIdleWindow(t *testing.T) {
	s := openEmpty(t)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }

	id, _ := s.ID("notes")
	if err := s.Remember("notes", id); err != nil {
		t.Fatal(err)
	}

	now = now.Add(3 * time.Hour)
	got, resume := s.ID("notes")
	if !resume || got != id {
		t.Fatalf("got %q resume=%v", got, resume)
	}
	if err := s.Remember("notes", got); err != nil {
		t.Fatal(err)
	}

	now = now.Add(3 * time.Hour)
	got, resume = s.ID("notes")
	if !resume || got != id {
		t.Fatalf("window should have reset: got %q resume=%v", got, resume)
	}
}

func TestRememberPersistsLastUsed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	id, _ := s.ID("notes")
	if err := s.Remember("notes", id); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]entry
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	e, ok := got["notes"]
	if !ok || e.ID != id || !e.LastUsed.Equal(now) {
		t.Fatalf("persisted %+v", got)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Now = func() time.Time { return now.Add(time.Minute) }
	gotID, resume := reopened.ID("notes")
	if !resume || gotID != id {
		t.Fatalf("reopen got %q resume=%v", gotID, resume)
	}
}

func TestOpenLegacyIDsAreExpired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	legacyID := "11111111-1111-1111-1111-111111111111"
	body := `{"notes": "` + legacyID + `"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, resume := s.ID("notes")
	if resume {
		t.Fatal("legacy id without last_used must not resume")
	}
	if got == legacyID {
		t.Fatal("must mint a new id, not reuse the legacy one")
	}
}

func TestPathUsesXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
	got := Path()
	want := filepath.Join("/tmp/xdg-state", "kerfline", "sessions.json")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func openEmpty(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
