package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/philipsahli/fullharness/internal/agent"
	"github.com/philipsahli/fullharness/internal/autopilot"
	"github.com/philipsahli/fullharness/internal/classify"
	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// defaultAutopilotAgent is the Agent Autopilot starts in a Project that
// names none (#107).
const defaultAutopilotAgent = "claude"

// newAutopilot builds Autopilot on fh's own parts: the board as tabs see
// it, and Sessions started the way the board's buttons start them.
func newAutopilot(d sessionDeps, b *boards, c *classify.Classifier) (*autopilot.Runner, error) {
	return autopilot.New(autopilot.Deps{
		Projects: func() []autopilot.Project { return autopilotProjects(d.projects) },
		Board: func(path string, refresh bool) ([]tracker.Feature, bool) {
			board, ok := b.read(path, refresh)
			tracker.Derive(board.Features, d.sessions.Links(path))
			return board.Features, ok
		},
		Sessions: d.sessions,
		Start: func(s autopilot.Start) (string, error) {
			prompt, err := d.skillPrompt(s.Agent, s.Skill, s.Ref)
			if err != nil {
				return "", err
			}
			link := s.Link
			req := newSession{Project: s.Project, Agent: s.Agent, Prompt: prompt, Link: &link, autopilot: true}
			req.Worktree, req.Branch = d.worktreeFor(s.Project, s.Branch)
			sum, err := d.start(req)
			return sum.ID, err
		},
		FollowUp: func(id, skill, ref string) error {
			det, err := d.sessions.Get(id)
			if err != nil {
				return err
			}
			prompt, err := d.skillPrompt(det.Agent, skill, ref)
			if err != nil {
				return err
			}
			return d.sessions.Prompt(id, prompt)
		},
		Classify: c.Classify,
		Load:     func(r *autopilot.Record) error { return d.state.Autopilot(r) },
		Save:     func(r autopilot.Record) error { return d.state.SetAutopilot(r) },
		Log:      d.log,
	})
}

// skillPrompt is the prompt that runs skill on ref with Agent agentID, as
// the board's buttons write it.
func (d sessionDeps) skillPrompt(agentID, skill, ref string) (string, error) {
	i := slices.IndexFunc(d.agents, func(a agent.Definition) bool { return a.ID == agentID })
	if i < 0 {
		return "", errors.New("no Agent " + agentID)
	}
	cmd, _, err := d.skillCommand(d.agents[i], skill)
	if err != nil {
		return "", err
	}
	return cmd + " " + ref, nil
}

// worktreeFor picks where a step runs: the main tree for no branch, the
// branch's Worktree when it has one, else a new Worktree on the branch.
func (d sessionDeps) worktreeFor(project, branch string) (worktree, newBranch string) {
	if branch == "" {
		return project, ""
	}
	if p, ok := d.projects.Get(project); ok {
		for _, w := range p.Worktrees {
			if w.Branch == branch && !w.Missing && !w.Agent {
				return w.Path, ""
			}
		}
	}
	return "", branch
}

func autopilotProjects(reg *project.Registry) []autopilot.Project {
	var out []autopilot.Project
	for _, p := range reg.List() {
		if !p.Autopilot || p.Missing {
			continue
		}
		a := p.Agent
		if a == "" {
			a = defaultAutopilotAgent
		}
		out = append(out, autopilot.Project{Path: p.Path, Agent: a})
	}
	return out
}

// registerAutopilotRoutes adds the per-Project switch. Turning Autopilot on
// looks at the Project at once rather than at the next tick.
func registerAutopilotRoutes(srv *server.Server, reg *project.Registry, agents []agent.Definition, pilot *autopilot.Runner) {
	srv.Handle("POST /api/projects/autopilot", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path  string `json:"path"`
			On    bool   `json:"on"`
			Agent string `json:"agent"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
			server.WriteError(w, http.StatusBadRequest, `send {"path": "…", "on": true | false, "agent": "…"}`)
			return
		}
		if req.Agent != "" && !slices.ContainsFunc(agents, func(a agent.Definition) bool { return a.ID == req.Agent }) {
			server.WriteError(w, http.StatusBadRequest, "no Agent "+req.Agent)
			return
		}
		switch err := reg.SetAutopilot(strings.TrimSpace(req.Path), project.Settings{Autopilot: req.On, Agent: req.Agent}); {
		case errors.Is(err, project.ErrNotFound):
			server.WriteError(w, http.StatusNotFound, err.Error())
			return
		case err != nil:
			server.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		for _, p := range autopilotProjects(reg) {
			if p.Path == req.Path {
				go pilot.Step(p, true)
			}
		}
	})
}

// registerAutopilotHistoryRoute shows what Autopilot did per Feature (#112),
// for the Loop view; ?project= narrows it to one Project.
func registerAutopilotHistoryRoute(srv *server.Server, pilot *autopilot.Runner) {
	srv.Handle("GET /api/autopilot", func(w http.ResponseWriter, r *http.Request) {
		server.WriteJSON(w, http.StatusOK, map[string]any{"features": pilot.History(r.URL.Query().Get("project"))})
	})
}

// runAutopilot looks at the Projects every interval until stop closes.
func runAutopilot(pilot *autopilot.Runner, interval time.Duration, stop <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-stop; cancel() }()
	pilot.Run(ctx, interval)
}
