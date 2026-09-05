package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DefaultIdleTTL is how long a chat keeps the same Grok session after the last
// successful turn. After this, the next turn starts a new session.
const DefaultIdleTTL = 4 * time.Hour

type entry struct {
	ID       string    `json:"id"`
	LastUsed time.Time `json:"last_used"`
}

type Store struct {
	path    string
	mu      sync.Mutex
	entries map[string]entry
	Now     func() time.Time
	IdleTTL time.Duration
}

func Path() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "kerfline", "sessions.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "sessions.json"
	}
	return filepath.Join(home, ".local", "state", "kerfline", "sessions.json")
}

func Open(path string) (*Store, error) {
	s := &Store{
		path:    path,
		entries: map[string]entry{},
		Now:     time.Now,
		IdleTTL: DefaultIdleTTL,
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	entries, err := decodeEntries(raw)
	if err != nil {
		return nil, err
	}
	s.entries = entries
	return s, nil
}

func decodeEntries(raw []byte) (map[string]entry, error) {
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	out := map[string]entry{}
	for name, val := range generic {
		val = trimJSON(val)
		if len(val) == 0 {
			continue
		}
		if val[0] == '"' {
			var id string
			if err := json.Unmarshal(val, &id); err != nil {
				return nil, err
			}
			if id != "" {
				// Legacy map[chat]id has no timestamp; treat as expired.
				out[name] = entry{ID: id}
			}
			continue
		}
		var e entry
		if err := json.Unmarshal(val, &e); err != nil {
			return nil, err
		}
		if e.ID != "" {
			out[name] = e
		}
	}
	return out, nil
}

func trimJSON(raw json.RawMessage) json.RawMessage {
	i, j := 0, len(raw)
	for i < j && (raw[i] == ' ' || raw[i] == '\n' || raw[i] == '\r' || raw[i] == '\t') {
		i++
	}
	for j > i && (raw[j-1] == ' ' || raw[j-1] == '\n' || raw[j-1] == '\r' || raw[j-1] == '\t') {
		j--
	}
	return raw[i:j]
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) ttl() time.Duration {
	if s.IdleTTL > 0 {
		return s.IdleTTL
	}
	return DefaultIdleTTL
}

func (s *Store) ID(chatName string) (id string, resume bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.entries[chatName]; ok && existing.ID != "" && !existing.LastUsed.IsZero() {
		if s.now().Sub(existing.LastUsed) < s.ttl() {
			return existing.ID, true
		}
	}
	return uuid.New().String(), false
}

func (s *Store) Remember(chatName, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[chatName] = entry{ID: id, LastUsed: s.now()}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
