package app

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/state"
	"github.com/philipsahli/fullharness/internal/worktree"
)

type worktreeDeps struct {
	projects *project.Registry
	sessions *session.Manager
	state    *state.Store
	log      *slog.Logger
}

// registerWorktreeRoutes adds Remove Worktree. fh only removes a Worktree
// when the user asks and confirms; nothing calls this on its own (#1,
// stories 20 and 21).
func registerWorktreeRoutes(srv *server.Server, d worktreeDeps) {
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
		wt, status, reason := d.find(req.Project, req.Path)
		if reason != "" {
			server.WriteError(w, status, reason)
			return
		}
		if wt.Main || wt.Agent {
			server.WriteError(w, http.StatusBadRequest, wt.Removal)
			return
		}
		// From here until the removal is done no Session can start in the
		// Worktree, and none is linked to it.
		release, err := d.sessions.ReserveRemoval(req.Path)
		if errors.Is(err, session.ErrLinked) || errors.Is(err, session.ErrRemoving) {
			server.WriteError(w, http.StatusConflict, err.Error())
			return
		} else if err != nil {
			server.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer release()
		// The last refresh may be 10 s old; decide on what git says now.
		d.projects.Refresh()
		if wt, status, reason = d.find(req.Project, req.Path); reason != "" {
			server.WriteError(w, status, reason)
			return
		}
		if wt.Removal != "" {
			server.WriteError(w, http.StatusConflict, wt.Removal)
			return
		}
		if !req.Confirm {
			d.askToConfirm(w, req.Path)
			return
		}
		if err := worktree.Remove(req.Project, req.Path); err != nil {
			server.WriteError(w, http.StatusConflict, err.Error())
			return
		}
		if err := d.state.RemoveWorktree(req.Path); err != nil {
			d.log.Warn("forget removed worktree", "path", req.Path, "err", err)
		}
		d.log.Info("worktree removed", "path", req.Path)
		d.projects.Refresh()
		w.WriteHeader(http.StatusNoContent)
	})
}

// find looks up the Worktree at path in project, or says why it can't.
func (d worktreeDeps) find(projectPath, path string) (worktree.Worktree, int, string) {
	p, ok := d.projects.Get(projectPath)
	if !ok {
		return worktree.Worktree{}, http.StatusNotFound, "no Project at " + projectPath
	}
	i := slices.IndexFunc(p.Worktrees, func(wt worktree.Worktree) bool { return wt.Path == path })
	if i < 0 {
		return worktree.Worktree{}, http.StatusBadRequest, path + " is not a Worktree of " + p.Name
	}
	return p.Worktrees[i], 0, ""
}

// askToConfirm answers an unconfirmed removal with what it would delete
// that git doesn't track: `git worktree remove` takes ignored files such as
// .env or node_modules with the folder, and the confirmation names them.
func (d worktreeDeps) askToConfirm(w http.ResponseWriter, path string) {
	ignored, err := worktree.Ignored(path)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	server.WriteJSON(w, http.StatusConflict, map[string]any{
		"error":   "confirm to remove the Worktree",
		"confirm": true,
		"ignored": append([]string{}, ignored...), // [] rather than null
	})
}
