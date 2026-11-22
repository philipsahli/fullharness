// Package project keeps the user's Projects: the git repos added to fh, saved
// in projects.toml, each with its Worktrees as last read from git.
package project

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"

	"github.com/BurntSushi/toml"

	"github.com/philipsahli/fullharness/internal/fileutil"
	"github.com/philipsahli/fullharness/internal/worktree"
)

// fileName is written only by fh. The user's config.toml stays untouched,
// because rewriting TOML would drop the user's comments (#3).
const fileName = "projects.toml"

// Project is one added repo, as tabs see it.
type Project struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// Missing means the folder is gone (moved, deleted, unmounted). The
	// Project stays listed, with no Worktrees and no git calls, until it
	// comes back or the user removes it; fh never removes it on its own.
	Missing   bool                `json:"missing"`
	Worktrees []worktree.Worktree `json:"worktrees"`
}

// Registry is the list of Projects. It is safe for concurrent use.
type Registry struct {
	file     string
	onChange func([]Project)

	mu       sync.Mutex
	projects []Project
}

type stored struct {
	Project []struct {
		Path string `toml:"path"`
	} `toml:"project"`
}

// Open loads projects.toml from dir. onChange gets the new list after every
// change, for pushing to tabs.
func Open(dir string, onChange func([]Project)) (*Registry, error) {
	r := &Registry{file: filepath.Join(dir, fileName), onChange: onChange}
	var s stored
	if _, err := toml.DecodeFile(r.file, &s); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", r.file, err)
	}
	for _, p := range s.Project {
		r.projects = append(r.projects, read(p.Path))
	}
	return r, nil
}

// List returns the current Projects, never nil, so it encodes as [].
func (r *Registry) List() []Project {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Project{}, r.projects...)
}

// ErrExists means the repo is already a Project.
var ErrExists = errors.New("this repo is already a Project")

// Add adds the git repo containing path. It returns a
// *worktree.InvalidPathError for a path that isn't a repo, or ErrExists.
func (r *Registry) Add(path string) (Project, error) {
	top, err := worktree.TopLevel(path)
	if err != nil {
		return Project{}, err
	}
	p := read(top)
	r.mu.Lock()
	if slices.ContainsFunc(r.projects, func(q Project) bool { return q.Path == top }) {
		r.mu.Unlock()
		return Project{}, ErrExists
	}
	r.projects = append(r.projects, p)
	err = r.save()
	list := append([]Project{}, r.projects...)
	r.mu.Unlock()
	if err != nil {
		return Project{}, err
	}
	r.onChange(list)
	return p, nil
}

// ErrNotFound means no Project has that path.
var ErrNotFound = errors.New("no Project with that path")

// Remove drops the Project at path from fh. The repo on disk is untouched.
func (r *Registry) Remove(path string) error {
	r.mu.Lock()
	i := slices.IndexFunc(r.projects, func(p Project) bool { return p.Path == path })
	if i < 0 {
		r.mu.Unlock()
		return ErrNotFound
	}
	r.projects = slices.Delete(r.projects, i, i+1)
	err := r.save()
	list := append([]Project{}, r.projects...)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	r.onChange(list)
	return nil
}

// Refresh re-reads every Project from git and calls onChange only if
// something changed, so idle tabs get no traffic. git runs outside the lock;
// if a Project was added or removed meanwhile, this result is dropped and the
// next refresh picks the change up.
func (r *Registry) Refresh() {
	r.mu.Lock()
	paths := make([]string, len(r.projects))
	for i, p := range r.projects {
		paths[i] = p.Path
	}
	r.mu.Unlock()

	fresh := make([]Project, len(paths))
	for i, path := range paths {
		fresh[i] = read(path)
	}

	r.mu.Lock()
	same := len(r.projects) == len(paths)
	for i := 0; same && i < len(paths); i++ {
		same = r.projects[i].Path == paths[i]
	}
	if !same || reflect.DeepEqual(r.projects, fresh) {
		r.mu.Unlock()
		return
	}
	r.projects = fresh
	list := append([]Project{}, fresh...)
	r.mu.Unlock()
	r.onChange(list)
}

// save writes projects.toml; the caller holds r.mu.
func (r *Registry) save() error {
	var s stored
	for _, p := range r.projects {
		s.Project = append(s.Project, struct {
			Path string `toml:"path"`
		}{p.Path})
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(s); err != nil {
		return err
	}
	return fileutil.WriteAtomic(r.file, buf.Bytes())
}

// read builds a Project's current state from git.
func read(path string) Project {
	p := Project{Path: path, Name: filepath.Base(path), Worktrees: []worktree.Worktree{}}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		p.Missing = true
		return p
	}
	if wts, err := worktree.List(path); err == nil {
		p.Worktrees = wts
	}
	return p
}
