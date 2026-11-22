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

	// change serialises Add, Remove and Refresh including their onChange
	// call, so tabs get events in the order the changes happened and an
	// older git read never overwrites a newer one.
	change sync.Mutex

	mu       sync.Mutex // guards projects for readers
	projects []Project
}

// stored is projects.toml: one [[project]] table per Project.
type stored struct {
	Project []storedProject `toml:"project"`
}

type storedProject struct {
	Path string `toml:"path"`
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
var ErrExists = errors.New("this git repository is already a Project")

// Add adds the git repo containing path. It returns a
// *worktree.InvalidPathError for a path that isn't a repo, or ErrExists.
func (r *Registry) Add(path string) (Project, error) {
	top, err := worktree.TopLevel(path)
	if err != nil {
		return Project{}, err
	}
	r.change.Lock()
	defer r.change.Unlock()
	if slices.ContainsFunc(r.List(), func(q Project) bool { return q.Path == top }) {
		return Project{}, ErrExists
	}
	p := read(top)
	if err := r.set(append(r.List(), p), true); err != nil {
		return Project{}, err
	}
	return p, nil
}

// ErrNotFound means no Project has that path.
var ErrNotFound = errors.New("no Project with that path")

// Remove drops the Project at path from fh. The repo on disk is untouched.
func (r *Registry) Remove(path string) error {
	r.change.Lock()
	defer r.change.Unlock()
	list := r.List()
	i := slices.IndexFunc(list, func(p Project) bool { return p.Path == path })
	if i < 0 {
		return ErrNotFound
	}
	return r.set(slices.Delete(list, i, i+1), true)
}

// Refresh re-reads every Project from git and calls onChange only if
// something changed, so idle tabs get no traffic.
func (r *Registry) Refresh() {
	r.change.Lock()
	defer r.change.Unlock()
	old := r.List()
	fresh := make([]Project, len(old))
	for i, p := range old {
		fresh[i] = read(p.Path)
	}
	if reflect.DeepEqual(old, fresh) {
		return
	}
	// The paths didn't change, so projects.toml stays as it is.
	_ = r.set(fresh, false) // returns an error only when saving
}

// set replaces the list, saves projects.toml if save, and tells onChange.
// The caller holds r.change.
func (r *Registry) set(list []Project, save bool) error {
	if save {
		if err := r.write(list); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.projects = list
	r.mu.Unlock()
	r.onChange(append([]Project{}, list...))
	return nil
}

// write saves the Project paths to projects.toml.
func (r *Registry) write(list []Project) error {
	var s stored
	for _, p := range list {
		s.Project = append(s.Project, storedProject{Path: p.Path})
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
	// A folder that is no longer a git repository lists no Worktrees; the
	// user sees an empty Project and can remove it.
	if wts, err := worktree.List(path); err == nil {
		p.Worktrees = wts
	}
	return p
}
