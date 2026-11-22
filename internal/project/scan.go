package project

import (
	"os"
	"path/filepath"
	"slices"
)

// Suggestion is a repo in the scan folder that isn't a Project yet.
type Suggestion struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// scanDepth covers both ~/work/<repo> and ~/work/<org>/<repo> (#3).
const scanDepth = 2

// Suggest lists repos up to two levels below scanDir that aren't Projects
// yet. A repo is a folder with a .git directory; a .git file marks a linked
// worktree, which its main repo already covers. Nothing is added; the user
// picks. Never nil, so it encodes as [].
func (r *Registry) Suggest(scanDir string) []Suggestion {
	out := []Suggestion{}
	if scanDir == "" {
		return out
	}
	// Project paths come from git, which resolves symlinks; resolve the scan
	// folder too, or added Projects would still be suggested.
	if real, err := filepath.EvalSymlinks(scanDir); err == nil {
		scanDir = real
	}
	added := map[string]bool{}
	for _, p := range r.List() {
		added[p.Path] = true
	}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return // an unreadable folder has nothing to suggest
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if info, err := os.Stat(filepath.Join(path, ".git")); err == nil {
				if info.IsDir() && !added[path] {
					out = append(out, Suggestion{Path: path, Name: e.Name()})
				}
				continue // don't look inside a repo
			}
			if depth < scanDepth {
				walk(path, depth+1)
			}
		}
	}
	walk(scanDir, 1)
	slices.SortFunc(out, func(a, b Suggestion) int {
		if a.Path < b.Path {
			return -1
		}
		if a.Path > b.Path {
			return 1
		}
		return 0
	})
	return out
}
