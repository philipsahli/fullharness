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

// newSession is a New Session request: an existing Worktree, or a new one on
// Branch.
type newSession struct {
	Project  string `json:"project"`
	Worktree string `json:"worktree"`
	Branch   string `json:"branch"`
	Agent    string `json:"agent"`
	Prompt   string `json:"prompt"`
	// AutoApprove starts the Agent in its auto mode instead of asking.
	AutoApprove bool `json:"autoApprove"`
}

// badRequest is a New Session request fh refuses, with the reason.
type badRequest string

func (e badRequest) Error() string { return string(e) }

func registerSessionRoutes(srv *server.Server, d sessionDeps) {
	srv.Handle("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		out := []agentInfo{}
		for _, a := range d.agents {
			out = append(out, agentInfo{ID: a.ID, Name: a.Name, Missing: a.Missing()})
		}
		server.WriteJSON(w, http.StatusOK, out)
	})

	srv.Handle("POST /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		var req newSession
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			server.WriteError(w, http.StatusBadRequest, "send {project, worktree or branch, agent, prompt}")
			return
		}
		sum, err := d.start(req)
		if err != nil {
			writeSessionError(w, err)
			return
		}
		server.WriteJSON(w, http.StatusCreated, sum)
	})

	srv.Handle("GET /api/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		det, err := d.sessions.Get(r.PathValue("id"))
		if err != nil {
			writeSessionError(w, err)
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
		accepted(w, d.sessions.Prompt(r.PathValue("id"), req.Text))
	})

	srv.Handle("POST /api/sessions/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		accepted(w, d.sessions.Cancel(r.PathValue("id")))
	})

	srv.Handle("POST /api/sessions/{id}/reopen", func(w http.ResponseWriter, r *http.Request) {
		accepted(w, d.sessions.Reopen(r.PathValue("id")))
	})

	// Close needs {"confirm": true} for a live Session: it stops the Agent.
	srv.Handle("POST /api/sessions/{id}/close", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Confirm bool `json:"confirm"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req) // an empty body means no confirmation
		if err := d.sessions.Close(r.PathValue("id"), req.Confirm); err != nil {
			writeSessionError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// A tab says every 15 s that it's looking at a Session (its conversation
	// is open and the tab visible), and when it stops. Looking clears a
	// finished turn's wait; a turn that ends while someone looks never waits.
	srv.Handle("POST /api/sessions/{id}/viewing", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tab     string `json:"tab"`
			Viewing bool   `json:"viewing"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Tab == "" {
			server.WriteError(w, http.StatusBadRequest, `send {"tab": "…", "viewing": true | false}`)
			return
		}
		if err := d.sessions.Viewing(r.PathValue("id"), req.Tab, req.Viewing); err != nil {
			writeSessionError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// Answering from the Inbox and from a Session card is the same request.
	srv.Handle("POST /api/inbox/{id}/answer", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Choice string `json:"choice"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			server.WriteError(w, http.StatusBadRequest, `send {"choice": "allow" | "deny" | "always"}`)
			return
		}
		if err := d.sessions.Answer(r.PathValue("id"), req.Choice); err != nil {
			writeSessionError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func accepted(w http.ResponseWriter, err error) {
	if err != nil {
		writeSessionError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// writeSessionError maps every Session error to its status in one place.
func writeSessionError(w http.ResponseWriter, err error) {
	var bad badRequest
	var refused *worktree.RefusedError
	switch {
	case errors.As(err, &bad), errors.As(err, &refused), errors.Is(err, session.ErrChoice):
		server.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, session.ErrNotFound), errors.Is(err, session.ErrNoRequest):
		server.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, session.ErrBusy), errors.Is(err, session.ErrNotRunning), errors.Is(err, session.ErrWorktreeUse),
		errors.Is(err, session.ErrAnswered), errors.Is(err, session.ErrConfirm), errors.Is(err, session.ErrNoReopen),
		errors.Is(err, session.ErrNotExited):
		server.WriteError(w, http.StatusConflict, err.Error())
	default:
		server.WriteError(w, http.StatusInternalServerError, err.Error())
	}
}

// start checks a New Session request and starts the Session.
func (d sessionDeps) start(req newSession) (session.Summary, error) {
	p, ok := d.projects.Get(req.Project)
	if !ok {
		return session.Summary{}, badRequest("no Project at " + req.Project)
	}
	i := slices.IndexFunc(d.agents, func(a agent.Definition) bool { return a.ID == req.Agent })
	if i < 0 {
		return session.Summary{}, badRequest("no Agent " + req.Agent)
	}
	def := d.agents[i]
	if missing := def.Missing(); missing != "" {
		return session.Summary{}, badRequest(def.Name + " can't start: " + missing)
	}
	if (req.Worktree == "") == (req.Branch == "") {
		return session.Summary{}, badRequest("pick an existing Worktree or a new branch, not both")
	}
	wtPath, branch := req.Worktree, req.Branch
	if branch != "" {
		path, err := d.createWorktree(p.Path, branch)
		if err != nil {
			return session.Summary{}, err
		}
		wtPath = path
	} else {
		j := slices.IndexFunc(p.Worktrees, func(w worktree.Worktree) bool { return w.Path == wtPath })
		if j < 0 {
			return session.Summary{}, badRequest(wtPath + " is not a Worktree of " + p.Name)
		}
		switch wt := p.Worktrees[j]; {
		case wt.Agent:
			return session.Summary{}, badRequest("fh never starts a Session in an Agent worktree")
		case wt.Missing:
			return session.Summary{}, badRequest("the Worktree's folder is gone")
		default:
			branch = wt.Branch
		}
	}
	return d.sessions.Start(def, p.Path, wtPath, branch, req.Prompt, req.AutoApprove)
}

// createWorktree records the new Worktree in state.json before git creates
// it, so it can never exist unrecorded and show up as an Agent worktree.
func (d sessionDeps) createWorktree(repo, branch string) (string, error) {
	path := worktree.NewPath(repo, branch)
	if err := d.state.AddWorktree(path); err != nil {
		return "", err
	}
	if _, err := worktree.Create(repo, branch); err != nil {
		if rmErr := d.state.RemoveWorktree(path); rmErr != nil {
			d.log.Info("forget failed worktree", "path", path, "err", rmErr)
		}
		return "", err
	}
	d.log.Info("worktree created", "path", path)
	d.projects.Refresh()
	return path, nil
}
