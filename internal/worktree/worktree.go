// Package worktree reads a Project's git Worktrees by running git and parsing
// its porcelain output. Only Create and Remove change a repo, and only when
// the user asked.
package worktree

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Worktree is one git worktree of a Project, as tabs see it.
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"` // empty when HEAD is detached
	// Main is the Project's own tree, the first entry git reports.
	Main bool `json:"main"`
	// Agent marks a Worktree fh didn't create (and isn't the main tree); fh
	// never starts a Session in one or removes it.
	Agent bool `json:"agent"`
	// Missing means git still lists the Worktree but its folder is gone
	// (`git worktree prune` would drop it). Its counts stay zero.
	Missing bool `json:"missing"`
	// Uncommitted counts changed, staged and untracked files.
	Uncommitted int `json:"uncommitted"`
	// Ahead and Behind count commits against Base: origin's default branch
	// when the repo has a remote, else the local default branch. fh never
	// fetches, so they are only as fresh as FetchedAt.
	Ahead  int    `json:"ahead"`
	Behind int    `json:"behind"`
	Base   string `json:"base"` // empty when no default branch was found
	// Merged means Base already has everything on HEAD: HEAD is part of
	// Base, or a squash commit on Base carries the same change. Read only
	// for Worktrees fh created, the only ones it offers to remove.
	Merged bool `json:"merged"`
	// FetchedAt is the last `git fetch` in this repo, nil if never.
	FetchedAt *time.Time `json:"fetchedAt"`
}

// List returns every Worktree of the repo at repo, main tree first.
// created reports whether fh created the Worktree at a path.
func List(repo string, created func(path string) bool) ([]Worktree, error) {
	out, err := run(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var wts []Worktree
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			wts = append(wts, Worktree{Path: strings.TrimPrefix(line, "worktree ")})
		case strings.HasPrefix(line, "branch ") && len(wts) > 0:
			wts[len(wts)-1].Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	base := defaultBase(repo)
	fetchedAt := lastFetch(repo)
	// Two git calls per Worktree, plus up to two for the merge check of
	// fh's own Worktrees (more when HEAD or the base moved); run Worktrees in
	// parallel so a refresh of 20 stays well under #3's 1 s target (measured
	// before the merge check, on an M-series Mac with small repositories:
	// about 480 ms one by one, 185 ms in parallel).
	sem := make(chan struct{}, parallelGit)
	var wg sync.WaitGroup
	for i := range wts {
		w := &wts[i]
		w.Main = i == 0
		w.Agent = i != 0 && !created(w.Path)
		w.Base = base
		w.FetchedAt = fetchedAt
		if _, err := os.Stat(w.Path); err != nil {
			w.Missing = true
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			// These fail only if the folder vanishes mid-refresh; the next
			// refresh marks it Missing, so zeros are fine meanwhile.
			w.Uncommitted, _ = uncommitted(w.Path)
			if base == "" {
				return
			}
			var err error
			// Merged stays false on an error: zero ahead from a failed
			// read must not offer removal.
			if w.Ahead, w.Behind, err = aheadBehind(w.Path, base); err == nil && !w.Main && !w.Agent {
				w.Merged = w.Ahead == 0 || squashMerged(w.Path, base)
			}
		})
	}
	wg.Wait()
	return wts, nil
}

// parallelGit caps concurrent git processes per repo.
const parallelGit = 8

// defaultBase is origin's default branch: origin/HEAD when set (by clone,
// or by fetch since git 2.48), else origin/main or origin/master. Without a
// remote it is local main, else local master, else "".
func defaultBase(repo string) string {
	if out, err := run(repo, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimSpace(string(out))
	}
	for _, ref := range []string{"remotes/origin/main", "remotes/origin/master", "heads/main", "heads/master"} {
		if _, err := run(repo, "rev-parse", "--verify", "-q", "refs/"+ref); err == nil {
			return strings.TrimPrefix(strings.TrimPrefix(ref, "remotes/"), "heads/")
		}
	}
	return ""
}

// lastFetch is FETCH_HEAD's time in the repo's shared git folder.
func lastFetch(repo string) *time.Time {
	out, err := run(repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil
	}
	info, err := os.Stat(filepath.Join(strings.TrimSpace(string(out)), "FETCH_HEAD"))
	if err != nil {
		return nil
	}
	t := info.ModTime().UTC()
	return &t
}

// uncommitted counts the lines of `git status --porcelain`: one per changed,
// staged or untracked file (a rename is one line).
func uncommitted(dir string) (int, error) {
	out, err := run(dir, "status", "--porcelain=v1")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if line != "" {
			n++
		}
	}
	return n, nil
}

// aheadBehind counts commits on HEAD but not base, and on base but not HEAD.
func aheadBehind(dir, base string) (ahead, behind int, err error) {
	out, err := run(dir, "rev-list", "--left-right", "--count", base+"...HEAD")
	if err != nil {
		return 0, 0, err
	}
	// Output is "<left: only in base>\t<right: only in HEAD>".
	if _, err := fmt.Sscan(string(out), &behind, &ahead); err != nil {
		return 0, 0, fmt.Errorf("parse rev-list output %q: %w", out, err)
	}
	return ahead, behind, nil
}

// squashMerged reports whether a commit on base since HEAD branched off has
// the same patch as all of HEAD's work together, which is what a squash
// merge (GitHub's "Squash and merge") leaves behind. HEAD stays ahead of
// base after one, so ahead/behind alone never sees it. Patch IDs are
// compared without writing to the repo; a squash commit that also changed
// other files, or resolved a conflict, doesn't match.
func squashMerged(dir, base string) bool {
	out, err := run(dir, "rev-parse", "HEAD", base)
	if err != nil {
		return false
	}
	key := strings.Join(strings.Fields(string(out)), " ")
	if v, ok := squashCache.Load(key); ok {
		return v.(bool)
	}
	merged := squashMergedUncached(dir, base)
	squashCache.Store(key, merged)
	return merged
}

// squashCache holds squashMerged's answer per HEAD and base commit pair,
// which can't change, so a refresh repeats the patch comparison only when
// either side moved.
var squashCache sync.Map

func squashMergedUncached(dir, base string) bool {
	mb, err := run(dir, "merge-base", base, "HEAD")
	if err != nil {
		return false
	}
	fork := strings.TrimSpace(string(mb))
	diff, err := run(dir, "diff", fork, "HEAD")
	if err != nil || len(diff) == 0 {
		return false
	}
	own, err := runStdin(dir, diff, "patch-id", "--stable")
	if err != nil {
		return false
	}
	id, _, _ := strings.Cut(strings.TrimSpace(string(own)), " ")
	if id == "" {
		return false
	}
	log, err := run(dir, "log", "-p", "--no-merges", "--format=commit %H", fork+".."+base)
	if err != nil {
		return false
	}
	theirs, err := runStdin(dir, log, "patch-id", "--stable")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(theirs), "\n") {
		if other, _, _ := strings.Cut(line, " "); other == id {
			return true
		}
	}
	return false
}

// run runs git in dir and returns stdout. The error carries git's stderr.
func run(dir string, args ...string) ([]byte, error) {
	return runStdin(dir, nil, args...)
}

// runStdin is run with stdin, for git commands that read a patch.
func runStdin(dir string, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	// Background reads like `git status` may otherwise refresh .git/index,
	// writing to the user's repo and contending for its lock with the
	// user's own git commands; fh only reads.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// RefusedError explains why fh refused what the user typed: a path that
// can't become a Project, or a branch name a new Worktree can't use. Its
// message is shown to the user as is.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return e.Reason }

// TopLevel returns the top folder of the git repo containing path, so a
// subfolder adds its repo. A linked worktree is refused: it belongs to its
// main repo, which lists it anyway.
func TopLevel(path string) (string, error) {
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return "", &RefusedError{Reason: path + " is not a folder"}
	}
	out, err := run(path, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-dir", "--git-common-dir")
	if err != nil {
		// --show-toplevel fails in a bare repository, which has no working
		// folder for a Session to run in.
		if bare, err := run(path, "rev-parse", "--is-bare-repository"); err == nil && strings.TrimSpace(string(bare)) == "true" {
			return "", &RefusedError{Reason: path + " is a bare git repository; add a clone with a working folder"}
		}
		return "", &RefusedError{Reason: path + " is not a git repository"}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 {
		return "", fmt.Errorf("git rev-parse in %s: unexpected output %q", path, out)
	}
	top, gitDir, commonDir := lines[0], lines[1], lines[2]
	if gitDir != commonDir {
		// The common dir of a linked worktree is the main repo's .git folder.
		main := filepath.Dir(commonDir)
		return "", &RefusedError{Reason: fmt.Sprintf("%s is a worktree of %s; add that instead", top, main)}
	}
	return top, nil
}

// NewPath is where Create puts the Worktree for branch: next to the
// Project's folder, in <project>.worktrees/<branch>, so the Project's own
// tree stays clean (#1, story 19).
func NewPath(repo, branch string) string {
	return filepath.Join(filepath.Dir(repo), filepath.Base(repo)+".worktrees", filepath.FromSlash(branch))
}

// Create adds a Worktree on a new branch at NewPath. The branch starts from
// the same base ahead/behind uses, so a new Worktree starts from what is
// shipped, not from whatever the main tree has checked out. It doesn't track
// that base: a plain `git push` should create the branch upstream, not push
// to main. It returns the Worktree's path.
func Create(repo, branch string) (string, error) {
	if _, err := run(repo, "check-ref-format", "--branch", branch); err != nil {
		return "", &RefusedError{Reason: fmt.Sprintf("%q is not a valid branch name", branch)}
	}
	if _, err := run(repo, "rev-parse", "--verify", "-q", "refs/heads/"+branch); err == nil {
		return "", &RefusedError{Reason: fmt.Sprintf("branch %q already exists; pick its Worktree instead, or another name", branch)}
	}
	path := NewPath(repo, branch)
	if _, err := os.Stat(path); err == nil {
		return "", &RefusedError{Reason: path + " already exists"}
	}
	args := []string{"worktree", "add", "-q", "--no-track", "-b", branch, path}
	if base := defaultBase(repo); base != "" {
		args = append(args, base)
	}
	if _, err := run(repo, args...); err != nil {
		return "", err
	}
	return path, nil
}

// BranchesAhead lists the repo's local branches that have commits the
// Project's base doesn't, for the board's Implementing Stage. One
// for-each-ref call counts them all (git 2.41+); older git falls back to
// one rev-list per branch.
func BranchesAhead(repo string) []string {
	base := defaultBase(repo)
	if base == "" {
		return nil
	}
	var ahead []string
	if out, err := run(repo, "for-each-ref", "--format=%(refname:short) %(ahead-behind:"+base+")", "refs/heads/"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			// "<branch> <ahead> <behind>"
			if f := strings.Fields(line); len(f) == 3 && f[1] != "0" {
				ahead = append(ahead, f[0])
			}
		}
		return ahead
	}
	out, err := run(repo, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil
	}
	for _, b := range strings.Fields(string(out)) {
		if n, err := run(repo, "rev-list", "--count", base+".."+b); err == nil && strings.TrimSpace(string(n)) != "0" {
			ahead = append(ahead, b)
		}
	}
	return ahead
}

// Remove runs `git worktree remove` on the Worktree at path, without
// --force, so git itself refuses one with uncommitted or untracked files
// even if they appeared after fh's checks. The branch stays.
func Remove(repo, path string) error {
	_, err := run(repo, "worktree", "remove", path)
	return err
}
