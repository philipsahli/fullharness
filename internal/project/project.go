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
	"github.com/philipsahli/fullharness/internal/gitremote"
	"github.com/philipsahli/fullharness/internal/tracker"
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
	Missing bool `json:"missing"`
	// URL is the repo's GitHub page, from its origin remote; empty when
	// origin is missing or not on GitHub, and the UI shows plain text.
	URL       string              `json:"url,omitempty"`
	Worktrees []worktree.Worktree `json:"worktrees"`
	Settings
}

// Settings are what the user set for a Project in fh; projects.toml keeps
// them.
type Settings struct {
	// Autopilot: fh starts the next step of the Project's Features in
	// Roadmap Now by itself (ADR-0009).
	Autopilot bool `json:"autopilot" toml:"autopilot,omitempty"`
	// Agent is the Agent Autopilot starts; empty means Claude Code.
	Agent string `json:"agent,omitempty" toml:"agent,omitempty"`
}

// Registry is the list of Projects. It is safe for concurrent use.
type Registry struct {
	file     string
	onChange func([]Project)
	created  func(path string) bool // whether fh created a Worktree
	linked   func(path string) bool // whether a Session is linked to a Worktree
	prs      func(repo string) ([]tracker.PR, bool)

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
	Settings
}

// Open loads projects.toml from dir. created reports whether fh created a
// Worktree (those aren't Agent worktrees); linked whether a Session, live or
// Exited, is linked to one. onChange gets the new list after every change,
// for pushing to tabs.
func Open(dir string, created, linked func(path string) bool, onChange func([]Project)) (*Registry, error) {
	r := &Registry{file: filepath.Join(dir, fileName), onChange: onChange, created: created, linked: linked}
	var s stored
	if _, err := toml.DecodeFile(r.file, &s); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", r.file, err)
	}
	for _, p := range s.Project {
		r.projects = append(r.projects, r.read(p.Path, p.Settings))
	}
	return r, nil
}

// ReadPRs makes every later read give each Worktree of a GitHub Project
// its branch's Pull request, from prs: a repo's PRs, and false when gh
// couldn't read them. It is set after Open, so starting fh waits for no gh
// call; the first Refresh, when a tab connects, fills the PRs in.
func (r *Registry) ReadPRs(prs func(repo string) ([]tracker.PR, bool)) {
	r.change.Lock()
	defer r.change.Unlock()
	r.prs = prs
}

// List returns the current Projects, never nil, so it encodes as [].
func (r *Registry) List() []Project {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Project{}, r.projects...)
}

// Get returns the Project at path.
func (r *Registry) Get(path string) (Project, bool) {
	for _, p := range r.List() {
		if p.Path == path {
			return p, true
		}
	}
	return Project{}, false
}

// ErrExists means the repo is already a Project.
var ErrExists = errors.New("this git repository is already a Project")

// Add adds the git repo containing path. It returns a
// *worktree.RefusedError for a path that isn't a repo, or ErrExists.
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
	p := r.read(top, Settings{})
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

// SetAutopilot sets the Project's Autopilot settings and saves them.
func (r *Registry) SetAutopilot(path string, settings Settings) error {
	r.change.Lock()
	defer r.change.Unlock()
	list := r.List()
	i := slices.IndexFunc(list, func(p Project) bool { return p.Path == path })
	if i < 0 {
		return ErrNotFound
	}
	list[i].Settings = settings
	return r.set(list, true)
}

// Refresh re-reads every Project from git and calls onChange only if
// something changed, so idle tabs get no traffic.
func (r *Registry) Refresh() {
	r.change.Lock()
	defer r.change.Unlock()
	old := r.List()
	fresh := make([]Project, len(old))
	for i, p := range old {
		fresh[i] = r.read(p.Path, p.Settings)
	}
	if reflect.DeepEqual(old, fresh) {
		return
	}
	// The paths and settings didn't change, so projects.toml stays as it is.
	_ = r.set(fresh, false) // returns an error only when saving
}

// Relabel recomputes each Worktree's Removal without calling git, for when
// a Session was added or closed, and tells onChange if one changed.
func (r *Registry) Relabel() {
	r.change.Lock()
	defer r.change.Unlock()
	old := r.List()
	fresh := make([]Project, len(old))
	changed := false
	for i, p := range old {
		p.Worktrees = slices.Clone(p.Worktrees)
		for j := range p.Worktrees {
			w := &p.Worktrees[j]
			if reason := r.removal(*w); reason != w.Removal {
				w.Removal, changed = reason, true
			}
		}
		fresh[i] = p
	}
	if changed {
		_ = r.set(fresh, false) // returns an error only when saving
	}
}

// removal says why fh won't remove w, or "" when it would: only a Worktree
// fh created, with nothing on it that could be lost (#1, story 20).
// Everything but the Session rule comes from git.
func (r *Registry) removal(w worktree.Worktree) string {
	switch {
	case w.Main:
		return "the main tree is the Project itself; fh never removes it"
	case w.Agent:
		return "fh never removes an Agent worktree"
	case w.Missing:
		return "the Worktree's folder is gone"
	case w.Uncommitted > 0:
		return fmt.Sprintf("it has %d uncommitted %s", w.Uncommitted, plural(w.Uncommitted, "file", "files"))
	case w.Base == "":
		return "no main branch to check the merge against"
	case w.Merge == worktree.NotMerged:
		return "its branch isn't merged into " + w.Base
	case r.linked(w.Path):
		return "a Session is linked to it; close the Session first"
	}
	return ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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
		s.Project = append(s.Project, storedProject{Path: p.Path, Settings: p.Settings})
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(s); err != nil {
		return err
	}
	return fileutil.WriteAtomic(r.file, buf.Bytes())
}

// read builds a Project's current state from git, with its settings.
func (r *Registry) read(path string, settings Settings) Project {
	p := Project{Path: path, Name: filepath.Base(path), Worktrees: []worktree.Worktree{}, Settings: settings}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		p.Missing = true
		return p
	}
	// A folder that is no longer a git repository lists no Worktrees; the
	// user sees an empty Project and can remove it.
	if wts, err := worktree.List(path, r.created); err == nil {
		for i := range wts {
			wts[i].Removal = r.removal(wts[i])
		}
		p.Worktrees = wts
	}
	if g, ok := gitremote.ParseGitHub(worktree.Origin(path)); ok {
		p.URL = g.URL()
		// A branch links only if origin has it, so the link doesn't 404 for
		// a branch that was never pushed.
		remote := worktree.RemoteBranches(path)
		for i := range p.Worktrees {
			if b := p.Worktrees[i].Branch; b != "" && remote[b] {
				p.Worktrees[i].URL = g.BranchURL(b)
			}
		}
		r.addPRs(path, p.Worktrees)
	}
	return p
}

// addPRs gives each Worktree on a branch its Pull request, or NoPR, when
// the repo's PRs can be read; otherwise they carry nothing, so the UI
// doesn't claim "No PR yet" when it can't know.
func (r *Registry) addPRs(repo string, wts []worktree.Worktree) {
	if r.prs == nil {
		return
	}
	prs, ok := r.prs(repo)
	if !ok {
		return
	}
	for i := range wts {
		if wts[i].Branch == "" {
			continue
		}
		if pr, ok := tracker.BranchPR(prs, wts[i].Branch); ok {
			wts[i].PR = &pr
		} else {
			wts[i].NoPR = true
		}
	}
}
