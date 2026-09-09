package media

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/timbze/kerfline/internal/config"
)

type Files interface {
	Lookup(ctx context.Context, fileID string) (filePath string, size int64, err error)
	Fetch(ctx context.Context, filePath string) (io.ReadCloser, error)
}

type StageError struct {
	Reason string
	Msg    string
}

func (e *StageError) Error() string { return e.Msg }

func UserMessage(err error) string {
	var se *StageError
	if errors.As(err, &se) && se.Msg != "" {
		return se.Msg
	}
	return "Couldn't download that file."
}

type Store struct {
	mu          sync.Mutex
	Files       Files
	CheckIgnore func(workspace string) (ignored, gitMissing bool, err error)
	Now         func() time.Time
	Log         *slog.Logger
}

func NewStore(files Files, log *slog.Logger) *Store {
	return &Store{
		Files:       files,
		CheckIgnore: checkIgnore,
		Now:         time.Now,
		Log:         log,
	}
}

func checkIgnore(workspace string) (ignored, gitMissing bool, err error) {
	cmd := exec.Command("git", "-C", workspace, "check-ignore", "-q", ".local/telegram-inbox")
	err = cmd.Run()
	if err == nil {
		return true, false, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return false, true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return false, false, nil
	}
	return false, false, err
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) Materialize(ctx context.Context, chat config.Chat, ref AttachmentRef, maxBytes int64, ttl time.Duration) (StagedFile, error) {
	if maxBytes <= 0 {
		maxBytes = 20_000_000
	}
	check := s.CheckIgnore
	if check == nil {
		check = checkIgnore
	}
	ignored, gitMissing, err := check(chat.Workspace)
	if err != nil {
		return StagedFile{}, &StageError{Reason: "not_ignored", Msg: "refusing to stage: `.local/` is not gitignored."}
	}
	if gitMissing {
		if s.Log != nil {
			s.Log.Warn("git not found; staging under .local anyway", "workspace", chat.Workspace)
		}
	} else if !ignored {
		return StagedFile{}, &StageError{Reason: "not_ignored", Msg: "refusing to stage: `.local/` is not gitignored."}
	}

	if ref.FileSize > 0 && ref.FileSize > maxBytes {
		return StagedFile{}, &StageError{Reason: "too_large", Msg: "That file is too large (max 20 MB)."}
	}
	if s.Files == nil {
		return StagedFile{}, &StageError{Reason: "download", Msg: "Couldn't download that file."}
	}

	filePath, size, err := s.Files.Lookup(ctx, ref.FileID)
	if err != nil {
		return StagedFile{}, &StageError{Reason: "download", Msg: "Couldn't download that file."}
	}
	if filePath == "" {
		return StagedFile{}, &StageError{Reason: "missing", Msg: "I don't have that file anymore; send it again."}
	}
	if size > maxBytes {
		return StagedFile{}, &StageError{Reason: "too_large", Msg: "That file is too large (max 20 MB)."}
	}

	body, err := s.Files.Fetch(ctx, filePath)
	if err != nil {
		return StagedFile{}, &StageError{Reason: "download", Msg: "Couldn't download that file."}
	}
	defer body.Close()

	rel := RelPath(chat.Name, ref)
	abs := filepath.Join(chat.Workspace, filepath.FromSlash(rel))
	dir := filepath.Dir(abs)
	turnStart := s.now()

	n, err := s.writeAndPrune(dir, abs, body, maxBytes, chat.Workspace, rel, ttl, turnStart)
	if err != nil {
		return StagedFile{}, err
	}
	return StagedFile{Ref: ref, RelPath: rel, AbsPath: abs, Bytes: n}, nil
}

func (s *Store) writeAndPrune(dir, abs string, body io.Reader, maxBytes int64, workspace, keepRel string, ttl time.Duration, turnStart time.Time) (int64, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, &StageError{Reason: "download", Msg: "Couldn't download that file."}
	}
	tmp, err := os.CreateTemp(dir, ".part-*")
	if err != nil {
		return 0, &StageError{Reason: "download", Msg: "Couldn't download that file."}
	}
	tmpName := tmp.Name()
	n, copyErr := io.Copy(tmp, io.LimitReader(body, maxBytes+1))
	chmodErr := tmp.Chmod(0o600)
	closeErr := tmp.Close()
	if copyErr != nil || chmodErr != nil || closeErr != nil || n > maxBytes {
		_ = os.Remove(tmpName)
		if n > maxBytes {
			return 0, &StageError{Reason: "too_large", Msg: "That file is too large (max 20 MB)."}
		}
		return 0, &StageError{Reason: "download", Msg: "Couldn't download that file."}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Rename(tmpName, abs); err != nil {
		_ = os.Remove(tmpName)
		return 0, &StageError{Reason: "download", Msg: "Couldn't download that file."}
	}
	s.pruneLocked(workspace, keepRel, ttl, turnStart)
	return n, nil
}

func (s *Store) pruneLocked(workspace, keepRel string, ttl time.Duration, turnStart time.Time) {
	if ttl <= 0 {
		return
	}
	root := filepath.Join(workspace, filepath.FromSlash(InboxDir))
	keep := filepath.Join(workspace, filepath.FromSlash(keepRel))
	cutoff := turnStart.Add(-ttl)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if path == keep {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		mod := info.ModTime()
		if !mod.Before(turnStart) {
			return nil
		}
		if mod.Before(cutoff) {
			_ = os.Remove(path)
		}
		return nil
	})
}
