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
	"slices"
	"strings"
	"sync"
	"time"
)

// Worktree is one git worktree of a Project, as tabs see it.
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"` // empty when HEAD is detached
	// URL is the branch's GitHub page, set by package project only when
	// origin is on GitHub and origin/<Branch> exists as of the last fetch.
	URL string `json:"url,omitempty"`
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
	// Merge says whether Base has everything on HEAD, and how. It is read
	// only for Worktrees fh created, the only ones it offers to remove.
	Merge Merge `json:"merge"`
	// Removal is why fh won't remove the Worktree, or "" when it would.
	// Package project fills it in, because it depends on Sessions.
	Removal string `json:"removal"`
	// FetchedAt is the last `git fetch` in this repo, nil if never.
	FetchedAt *time.Time `json:"fetchedAt"`
}

// Merge is how much of a Worktree's work its Base already has.
type Merge string

const (
	// NotMerged: HEAD has commits Base lacks, or fh didn't check.
	NotMerged Merge = ""
	// Merged: the branch has commits of its own and Base has them all, as
	// the same commits, as rebased copies (`git cherry`), or as one squash
	// commit with the same change.
	Merged Merge = "merged"
	// NoOwnCommits: HEAD is still where the branch was created.
	NoOwnCommits Merge = "no-own-commits"
	// InBase: HEAD is part of Base, but without a reflog fh can't tell a
	// fast-forward merge from a branch that never had commits.
	InBase Merge = "in-base"
)

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
	// Two git calls per Worktree, plus one or two for the merge check of
	// fh's own Worktrees (more when HEAD or the base moved); run Worktrees in
	// parallel so a refresh of 20 stays well under #3's 1 s target (measured
	// before the merge check, on an M-series Mac with small repositories:
	// about 480 ms one by one, 185 ms in parallel).
	sem := make(chan struct{}, parallelGit)
	var wg sync.WaitGroup
	var usedMu sync.Mutex
	used := map[string]bool{} // mergeCache keys this List read
	defer func() { mergeCache.keep(repo, used) }()
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
			// Both stay false on an error: zero ahead from a failed read
			// must not offer removal.
			if w.Ahead, w.Behind, err = aheadBehind(w.Path, base); err != nil || w.Main || w.Agent {
				return
			}
			if w.Ahead == 0 {
				w.Merge = inBase(w.Path, w.Branch)
				return
			}
			key, merged := mergedAhead(repo, w.Path, base)
			if merged {
				w.Merge = Merged
			}
			if key != "" {
				usedMu.Lock()
				used[key] = true
				usedMu.Unlock()
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

// inBase tells, for a HEAD that is part of base, a fresh branch from a
// fast-forward merge: a branch still at the oldest entry of its reflog
// never had commits of its own. Without a reflog (detached HEAD, reflogs
// turned off, or the entries expired) it can't tell, and says InBase.
func inBase(dir, branch string) Merge {
	if branch == "" {
		return InBase
	}
	out, err := run(dir, "log", "-g", "--format=%H", "refs/heads/"+branch, "--")
	entries := strings.Fields(string(out))
	if err != nil || len(entries) == 0 {
		return InBase
	}
	head, err := run(dir, "rev-parse", "HEAD")
	if err != nil {
		return InBase
	}
	if entries[len(entries)-1] == strings.TrimSpace(string(head)) {
		return NoOwnCommits
	}
	return Merged
}

// mergedAhead reports whether base has all of HEAD's commits although HEAD
// is ahead of it: as rebased copies, or as one squash commit. The answer
// is cached per HEAD and base commit pair, which can't change; key is
// that pair, "" when it couldn't be read.
func mergedAhead(repo, dir, base string) (key string, merged bool) {
	out, err := run(dir, "rev-parse", "HEAD", base)
	if err != nil {
		return "", false
	}
	key = strings.Join(strings.Fields(string(out)), " ")
	if v, ok := mergeCache.get(repo, key); ok {
		return key, v
	}
	merged = rebaseMerged(dir, base) || squashMerged(dir, base)
	mergeCache.put(repo, key, merged)
	return key, merged
}

// rebaseMerged reports whether every commit on HEAD has a copy with the
// same patch on base, which is what GitHub's "Rebase and merge" leaves:
// `git cherry` marks those "-" and the rest "+".
func rebaseMerged(dir, base string) bool {
	out, err := run(dir, "cherry", base, "HEAD")
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "+") {
			return false
		}
	}
	return true
}

// squashMerged reports whether a commit on base since HEAD branched off has
// the same patch as all of HEAD's work together, which is what a squash
// merge (GitHub's "Squash and merge") leaves behind. Patch IDs are compared
// without writing to the repo; a squash commit that also changed other
// files, or resolved a conflict, doesn't match.
func squashMerged(dir, base string) bool {
	mb, err := run(dir, "merge-base", base, "HEAD")
	if err != nil {
		return false
	}
	fork := strings.TrimSpace(string(mb))
	// The user's diff settings must not change the patch: no external
	// diff tool, textconv filter, colour or signature lines.
	plain := []string{"--no-ext-diff", "--no-textconv", "--no-color"}
	diff, err := run(dir, append(append([]string{"diff"}, plain...), fork, "HEAD")...)
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
	log, err := run(dir, append(append([]string{"log", "-p", "--no-merges", "--no-show-signature", "--format=commit %H"}, plain...), fork+".."+base)...)
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

// mergeCache holds mergedAhead's answers per repo. List keeps only the
// keys it read, so entries for commits no Worktree is on any more go.
var mergeCache = &answerCache{byRepo: map[string]map[string]bool{}}

type answerCache struct {
	mu     sync.Mutex
	byRepo map[string]map[string]bool
}

func (c *answerCache) get(repo, key string) (v, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok = c.byRepo[repo][key]
	return v, ok
}

func (c *answerCache) put(repo, key string, v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byRepo[repo] == nil {
		c.byRepo[repo] = map[string]bool{}
	}
	c.byRepo[repo][key] = v
}

// keep drops repo's entries that aren't in used.
func (c *answerCache) keep(repo string, used map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.byRepo[repo] {
		if !used[k] {
			delete(c.byRepo[repo], k)
		}
	}
	if len(c.byRepo[repo]) == 0 {
		delete(c.byRepo, repo)
	}
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

// RemoteBranches returns the branches origin had at the last fetch, read
// from the local refs/remotes/origin/ refs in one git call, with no network.
// It is empty when the repo has no such refs or git fails.
func RemoteBranches(repo string) map[string]bool {
	branches := map[string]bool{}
	out, err := run(repo, "for-each-ref", "--format=%(refname)", "refs/remotes/origin/")
	if err != nil {
		return branches
	}
	for line := range strings.Lines(string(out)) {
		name := strings.TrimPrefix(strings.TrimSpace(line), "refs/remotes/origin/")
		if name != "" && name != "HEAD" {
			branches[name] = true
		}
	}
	return branches
}

// Origin returns the URL of the repo's origin remote, or "" when it has none.
// It reads the local config only; there is no network call.
func Origin(repo string) string {
	out, err := run(repo, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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

// Ignored lists the top-level entries in the Worktree at path that git
// ignores, such as .env or node_modules, which `git worktree remove`
// deletes along with the folder.
func Ignored(path string) ([]string, error) {
	out, err := run(path, "status", "--porcelain=v1", "-z", "--ignored")
	if err != nil {
		return nil, err
	}
	var top []string
	for _, entry := range strings.Split(string(out), "\x00") {
		name, ok := strings.CutPrefix(entry, "!! ")
		if !ok {
			continue
		}
		first, _, _ := strings.Cut(name, "/")
		if !slices.Contains(top, first) {
			top = append(top, first)
		}
	}
	slices.Sort(top)
	return top, nil
}

// Remove runs `git worktree remove` on the Worktree at path, without
// --force, so git itself refuses one with uncommitted or untracked files
// even if they appeared after fh's checks. The branch stays.
func Remove(repo, path string) error {
	_, err := run(repo, "worktree", "remove", path)
	return err
}
