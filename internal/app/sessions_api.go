package app

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/philipsahli/fullharness/internal/agent"
	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/state"
	"github.com/philipsahli/fullharness/internal/worktree"
)

type sessionDeps struct {
	projects *project.Registry
	sessions *session.Manager
	state    *state.Store
	agents   []agent.Definition
	log      *slog.Logger
}

// agentInfo is an Agent as the New Session dialog lists it.
type agentInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Missing string `json:"missing,omitempty"` // why it can't start, with the install command
}

func registerSessionRoutes(srv *server.Server, d sessionDeps) {
	srv.Handle("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		out := []agentInfo{}
		for _, a := range d.agents {
			out = append(out, agentInfo{ID: a.ID, Name: a.Name, Missing: a.Missing()})
		}
		server.WriteJSON(w, http.StatusOK, out)
	})

	srv.Handle("POST /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Project  string `json:"project"`
			Worktree string `json:"worktree"` // an existing Worktree, or
			Branch   string `json:"branch"`   // a new Worktree on this new branch
			Agent    string `json:"agent"`
			Prompt   string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			server.WriteError(w, http.StatusBadRequest, "send {project, worktree or branch, agent, prompt}")
			return
		}
		status, msg, sum := d.start(req.Project, req.Worktree, req.Branch, req.Agent, req.Prompt)
		if status != http.StatusCreated {
			server.WriteError(w, status, msg)
			return
		}
		server.WriteJSON(w, http.StatusCreated, sum)
	})

	srv.Handle("GET /api/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		det, err := d.sessions.Get(r.PathValue("id"))
		if err != nil {
			server.WriteError(w, http.StatusNotFound, err.Error())
			return
		}
		server.WriteJSON(w, http.StatusOK, det)
	})

	srv.Handle("POST /api/sessions/{id}/prompt", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
			server.WriteError(w, http.StatusBadRequest, `send {"text": "…"}`)
			return
		}
		writeSessionResult(w, d.sessions.Prompt(r.PathValue("id"), req.Text))
	})

	srv.Handle("POST /api/sessions/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		writeSessionResult(w, d.sessions.Cancel(r.PathValue("id")))
	})
}

func writeSessionResult(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, session.ErrNotFound):
		server.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, session.ErrBusy), errors.Is(err, session.ErrNotRunning):
		server.WriteError(w, http.StatusConflict, err.Error())
	default:
		server.WriteError(w, http.StatusInternalServerError, err.Error())
	}
}

// start checks a New Session request and starts it. It returns the HTTP
// status, an error message for anything but 201, and the new Session.
func (d sessionDeps) start(projectPath, wtPath, branch, agentID, prompt string) (int, string, session.Summary) {
	p, ok := d.projects.Get(projectPath)
	if !ok {
		return http.StatusBadRequest, "no Project at " + projectPath, session.Summary{}
	}
	i := slices.IndexFunc(d.agents, func(a agent.Definition) bool { return a.ID == agentID })
	if i < 0 {
		return http.StatusBadRequest, "no Agent " + agentID, session.Summary{}
	}
	def := d.agents[i]
	if missing := def.Missing(); missing != "" {
		return http.StatusBadRequest, def.Name + " can't start: " + missing, session.Summary{}
	}
	if (wtPath == "") == (branch == "") {
		return http.StatusBadRequest, "pick an existing Worktree or a new branch, not both", session.Summary{}
	}
	if branch != "" {
		path, err := worktree.Create(p.Path, branch)
		var invalid *worktree.InvalidPathError
		if errors.As(err, &invalid) {
			return http.StatusBadRequest, invalid.Reason, session.Summary{}
		}
		if err != nil {
			return http.StatusInternalServerError, err.Error(), session.Summary{}
		}
		if err := d.state.AddWorktree(path); err != nil {
			return http.StatusInternalServerError, err.Error(), session.Summary{}
		}
		d.log.Info("worktree created", "path", path)
		d.projects.Refresh()
		wtPath = path
	} else {
		j := slices.IndexFunc(p.Worktrees, func(w worktree.Worktree) bool { return w.Path == wtPath })
		if j < 0 {
			return http.StatusBadRequest, wtPath + " is not a Worktree of " + p.Name, session.Summary{}
		}
		wt := p.Worktrees[j]
		switch {
		case wt.Agent:
			return http.StatusBadRequest, "fh never starts a Session in an Agent worktree", session.Summary{}
		case wt.Missing:
			return http.StatusBadRequest, "the Worktree's folder is gone", session.Summary{}
		}
		branch = wt.Branch
	}
	sum, err := d.sessions.Start(def, p.Path, wtPath, branch, prompt)
	if errors.Is(err, session.ErrWorktreeUse) {
		return http.StatusConflict, err.Error(), session.Summary{}
	}
	if err != nil {
		return http.StatusInternalServerError, err.Error(), session.Summary{}
	}
	return http.StatusCreated, "", sum
}
