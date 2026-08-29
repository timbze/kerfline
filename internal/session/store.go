package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
)

type Store struct {
	path string
	mu   sync.Mutex
	ids  map[string]string
}

func Path() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "telegram-jailbee", "sessions.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "sessions.json"
	}
	return filepath.Join(home, ".local", "state", "telegram-jailbee", "sessions.json")
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, ids: map[string]string{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.ids); err != nil {
		return nil, err
	}
	if s.ids == nil {
		s.ids = map[string]string{}
	}
	return s, nil
}

func (s *Store) ID(chatName string) (id string, resume bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.ids[chatName]; ok {
		return existing, true
	}
	id = uuid.NewSHA1(uuid.NameSpaceURL, []byte("telegram-jailbee:"+chatName)).String()
	return id, false
}

func (s *Store) Remember(chatName, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids[chatName] == id {
		return nil
	}
	s.ids[chatName] = id
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.ids, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
