package app

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/state"
	"github.com/philipsahli/fullharness/internal/worktree"
)

// registerWorktreeRoutes adds Remove Worktree. fh only removes a Worktree
// when the user asks and confirms; nothing calls this on its own (#1,
// stories 20 and 21).
func registerWorktreeRoutes(srv *server.Server, reg *project.Registry, sessions *session.Manager, st *state.Store, log *slog.Logger) {
	srv.Handle("POST /api/worktrees/remove", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Project string `json:"project"`
			Path    string `json:"path"`
			Confirm bool   `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Project == "" || req.Path == "" {
			server.WriteError(w, http.StatusBadRequest, `send {"project": "…", "path": "…", "confirm": true}`)
			return
		}
		// The last refresh may be 10 s old; decide on what git says now.
		reg.Refresh()
		p, ok := reg.Get(req.Project)
		if !ok {
			server.WriteError(w, http.StatusBadRequest, "no Project at "+req.Project)
			return
		}
		i := slices.IndexFunc(p.Worktrees, func(wt worktree.Worktree) bool { return wt.Path == req.Path })
		if i < 0 {
			server.WriteError(w, http.StatusBadRequest, req.Path+" is not a Worktree of "+p.Name)
			return
		}
		if status, reason := removeRefusal(p.Worktrees[i], sessions.List()); reason != "" {
			server.WriteError(w, status, reason)
			return
		}
		if !req.Confirm {
			server.WriteError(w, http.StatusConflict, "confirm to remove the Worktree")
			return
		}
		if err := worktree.Remove(p.Path, req.Path); err != nil {
			server.WriteError(w, http.StatusConflict, err.Error())
			return
		}
		if err := st.RemoveWorktree(req.Path); err != nil {
			log.Info("forget removed worktree", "path", req.Path, "err", err)
		}
		log.Info("worktree removed", "path", req.Path)
		reg.Refresh()
		w.WriteHeader(http.StatusNoContent)
	})
}

// removeRefusal says why fh won't offer to remove wt, with the status to
// answer, or "" when it may. web/src/Projects.tsx shows the same reasons.
func removeRefusal(wt worktree.Worktree, sessions []session.Summary) (int, string) {
	switch {
	case wt.Main:
		return http.StatusBadRequest, "the main tree is the Project itself; fh never removes it"
	case wt.Agent:
		return http.StatusBadRequest, "fh never removes an Agent worktree"
	case wt.Missing:
		return http.StatusConflict, "the Worktree's folder is gone"
	case wt.Uncommitted > 0:
		return http.StatusConflict, fmt.Sprintf("it has %d uncommitted %s", wt.Uncommitted, plural(wt.Uncommitted, "file", "files"))
	case wt.Base == "":
		return http.StatusConflict, "no main branch to check the merge against"
	case !wt.Merged:
		return http.StatusConflict, "its branch isn't merged into " + wt.Base
	case slices.ContainsFunc(sessions, func(s session.Summary) bool { return s.Worktree == wt.Path }):
		return http.StatusConflict, "a Session is linked to it; close the Session first"
	}
	return 0, ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
