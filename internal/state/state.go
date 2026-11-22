// Package state keeps fh's runtime-state file, state.json: facts fh needs
// across restarts that aren't the user's configuration. In #4 that is the
// Worktrees fh created; Sessions follow with Reopen (#9).
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/philipsahli/fullharness/internal/fileutil"
)

const fileName = "state.json"

// Store is state.json. It is safe for concurrent use.
type Store struct {
	file string

	mu   sync.Mutex
	data data
}

type data struct {
	// CreatedWorktrees are the Worktree paths fh created. Every other
	// non-main Worktree is an Agent worktree.
	CreatedWorktrees []string `json:"createdWorktrees"`
}

// Open reads state.json from dir; a missing file is an empty state.
func Open(dir string) (*Store, error) {
	s := &Store{file: filepath.Join(dir, fileName)}
	b, err := os.ReadFile(s.file)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, fmt.Errorf("read %s: %w", s.file, err)
	}
	return s, nil
}

// AddWorktree records that fh created the Worktree at path.
func (s *Store) AddWorktree(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Contains(s.data.CreatedWorktrees, path) {
		return nil
	}
	s.data.CreatedWorktrees = append(s.data.CreatedWorktrees, path)
	return s.save()
}

// save writes state.json; the caller holds s.mu.
func (s *Store) save() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteAtomic(s.file, b)
}

// RemoveWorktree forgets path, after creating it failed.
func (s *Store) RemoveWorktree(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.CreatedWorktrees = slices.DeleteFunc(s.data.CreatedWorktrees, func(p string) bool { return p == path })
	return s.save()
}

// CreatedWorktree reports whether fh created the Worktree at path.
func (s *Store) CreatedWorktree(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.data.CreatedWorktrees, path)
}
