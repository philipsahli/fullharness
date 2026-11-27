package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/philipsahli/fullharness/internal/autopilot"
	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// prChanges caches each PR's changed files and description, which decide
// Screenshot review (ADR-0014), for ghCacheFor like the PR list: Autopilot
// looks at a reviewed Ticket on every interval.
type prChanges struct {
	mu sync.Mutex
	m  map[prKey]prChange
}

type prKey struct {
	repo string
	n    int
}

type prChange struct {
	pr tracker.PR
	at time.Time
}

func newPRChanges() *prChanges { return &prChanges{m: map[prKey]prChange{}} }

// read returns PR n's changes; ok is false when gh can't read them.
func (c *prChanges) read(ctx context.Context, repo string, n int) (tracker.PR, bool) {
	k := prKey{repo, n}
	c.mu.Lock()
	e, hit := c.m[k]
	c.mu.Unlock()
	if hit && time.Since(e.at) < ghCacheFor {
		return e.pr, true
	}
	pr, err := tracker.FetchPRChanges(ctx, repo, n)
	if err != nil {
		return tracker.PR{}, false
	}
	c.mu.Lock()
	c.m[k] = prChange{pr: pr, at: time.Now()}
	c.mu.Unlock()
	return pr, true
}

// screenshotDir is where an Agent saves its screenshots, in its Worktree
// (ADR-0014).
const screenshotDir = ".fh/screenshots"

// screenshotNote ends an implement prompt Autopilot sends: other Projects
// don't have fullharness's AGENTS.md, so the prompt says where the
// screenshots for Screenshot review go.
const screenshotNote = "\n\nIf you change a screen, save a PNG screenshot of each changed screen under " +
	screenshotDir + "/ in this Worktree before you open the PR; fh shows them to the user at Screenshot review. " +
	"If the PR changes no screen, say \"" + tracker.NoVisibleUIChange + "\" in its description."

// excludeFh adds .fh/ to repo's .git/info/exclude, which its Worktrees
// share, so screenshots are never committed. It runs before Autopilot
// starts a Session; a failure is logged and the Session starts anyway.
func excludeFh(repo string) error {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return err
	}
	file := filepath.Join(strings.TrimSpace(string(out)), "info", "exclude")
	b, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if slices.Contains(strings.Split(string(b), "\n"), ".fh/") {
		return nil
	}
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		b = append(b, '\n')
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, append(b, ".fh/\n"...), 0o644)
}

// reviewedTicket finds Ticket ticket of Feature feature on Project
// project's board, and the Project; status and msg say why not.
func reviewedTicket(reg *project.Registry, b *boards, sessions *session.Manager, projectPath, feature, ticket string) (p project.Project, t tracker.Ticket, status int, msg string) {
	p, ok := reg.Get(projectPath)
	if !ok {
		return p, t, http.StatusNotFound, "no such Project"
	}
	board, ok := b.read(p.Path, false)
	if !ok {
		return p, t, http.StatusBadGateway, board.Note
	}
	tracker.Derive(board.Features, sessions.Links(p.Path))
	for _, f := range board.Features {
		if f.ID != feature {
			continue
		}
		for _, tk := range f.Tickets {
			if tk.Number == ticket {
				return p, tk, 0, ""
			}
		}
	}
	return p, t, http.StatusNotFound, "no such Ticket"
}

// screenshotFolder is the screenshot folder of Ticket t's Worktree in p,
// or "" when t has no Worktree.
func screenshotFolder(p project.Project, t tracker.Ticket) string {
	for _, w := range p.Worktrees {
		if w.Branch == t.Branch && !w.Missing {
			return filepath.Join(w.Path, screenshotDir)
		}
	}
	return ""
}

// screenshots are the PNG files in dir, sorted: plain files only, so a
// link can't lead the Inbox to a file outside the Worktree.
func screenshots(dir string) []string {
	out := []string{}
	if dir == "" {
		return out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.Type().IsRegular() && strings.EqualFold(filepath.Ext(e.Name()), ".png") {
			out = append(out, e.Name())
		}
	}
	return out
}

// registerScreenshotReviewRoutes adds Screenshot review to the Inbox
// (#226, ADR-0014): the Agent's screenshots, served from the Ticket's
// Worktree only and never uploaded, and Looks good.
func registerScreenshotReviewRoutes(srv *server.Server, reg *project.Registry, b *boards, sessions *session.Manager, pilot *autopilot.Runner, background func(func(context.Context))) {
	// The screenshots of one Ticket's PR, and the PR.
	srv.Handle("GET /api/autopilot/screenshots", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p, t, status, msg := reviewedTicket(reg, b, sessions, q.Get("project"), q.Get("feature"), q.Get("ticket"))
		if status != 0 {
			server.WriteError(w, status, msg)
			return
		}
		out := map[string]any{"screenshots": screenshots(screenshotFolder(p, t))}
		if pr, ok := openPR(t); ok {
			out["pr"] = pr
		}
		server.WriteJSON(w, http.StatusOK, out)
	})
	// One screenshot. name must be one listed above: a plain .png file
	// right in the Worktree's screenshot folder.
	srv.Handle("GET /api/autopilot/screenshot", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p, t, status, msg := reviewedTicket(reg, b, sessions, q.Get("project"), q.Get("feature"), q.Get("ticket"))
		if status != 0 {
			server.WriteError(w, status, msg)
			return
		}
		dir, name := screenshotFolder(p, t), q.Get("name")
		if dir == "" || !slices.Contains(screenshots(dir), name) {
			server.WriteError(w, http.StatusNotFound, "no such screenshot")
			return
		}
		// O_NOFOLLOW: a link swapped in after the listing isn't followed.
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			server.WriteError(w, http.StatusNotFound, "no such screenshot")
			return
		}
		defer func() { _ = f.Close() }() // read only: nothing to flush
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			server.WriteError(w, http.StatusNotFound, "no such screenshot")
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store") // the Agent may take it again
		http.ServeContent(w, r, name, info.ModTime(), f)
	})
	// Looks good: the screens are right, so the PR moves on to the merge
	// Stop point. fh says so on the PR, records it as the user's, and
	// steps the Project at once.
	srv.Handle("POST /api/autopilot/looks-good", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Project string `json:"project"`
			Feature string `json:"feature"`
			Ticket  string `json:"ticket"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Project == "" || req.Feature == "" || req.Ticket == "" {
			server.WriteError(w, http.StatusBadRequest, `send {"project": "…", "feature": "…", "ticket": "…"}`)
			return
		}
		p, t, status, msg := reviewedTicket(reg, b, sessions, req.Project, req.Feature, req.Ticket)
		if status != 0 {
			server.WriteError(w, status, msg)
			return
		}
		stop, waits := pilot.Waiting(p.Path, req.Feature, req.Ticket)
		pr, hasPR := openPR(t)
		if !waits || stop.Reason != autopilot.ReasonScreenshot || !hasPR {
			server.WriteError(w, http.StatusConflict, "this Ticket's PR doesn't wait at Screenshot review")
			return
		}
		// The write runs to the end even if the tab goes away (#215).
		if err := tracker.CommentPR(context.WithoutCancel(r.Context()), p.Path, pr.Number, looksGoodComment); err != nil {
			server.WriteError(w, http.StatusBadGateway, err.Error())
			return
		}
		pilot.Answered(stop.Step, autopilot.AnswerLooksGood)
		w.WriteHeader(http.StatusNoContent)
		for _, ap := range autopilotProjects(reg) {
			if ap.Path == p.Path {
				background(func(context.Context) { pilot.Step(ap, false) })
			}
		}
	})
}

// looksGoodComment is what Looks good says on the PR, so GitHub shows
// that someone looked (ADR-0014).
const looksGoodComment = "Screen reviewed in fh"

// openPR is t's Open or Draft PR, the newest; ok is false when it has none.
func openPR(t tracker.Ticket) (tracker.PRRecord, bool) {
	for _, p := range t.PRs { // newest first
		if p.State == tracker.PROpen || p.State == tracker.PRDraft {
			return p, true
		}
	}
	return tracker.PRRecord{}, false
}

// screenReview reports whether Ticket t's PR, reviewed done, stops at
// Screenshot review in Project p; ok is false when fh can't tell yet (no
// open PR, or gh failed), so Autopilot records nothing and looks again.
func screenReview(ctx context.Context, c *prChanges, p project.Project, t tracker.Ticket) (stops, ok bool) {
	rec, ok := openPR(t)
	if !ok {
		return false, false
	}
	pr, ok := c.read(ctx, p.Path, rec.Number)
	if !ok {
		return false, false
	}
	return pr.ChangesScreen(p.ScreenPaths()), true
}
