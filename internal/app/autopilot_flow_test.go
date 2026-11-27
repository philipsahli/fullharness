package app_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// The flow graph (#206, ADR-0013): fh works out one Feature's Stages, its
// Tickets' lanes and a dot per skill Autopilot ran, and the UI only draws
// it. The rules are table-tested in internal/autopilot/flow; this checks
// the route puts the board, the history and the Autopilot status together.

func (f *fh) flow(c *http.Client, project, feature string) (int, map[string]any) {
	f.t.Helper()
	resp, err := c.Get(f.origin() + "/api/autopilot/flow?project=" + url.QueryEscape(project) + "&feature=" + url.QueryEscape(feature))
	if err != nil {
		f.t.Fatal(err)
	}
	body := readBody(f.t, resp)
	var g map[string]any
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &g); err != nil {
			f.t.Fatal(err)
		}
	}
	return resp.StatusCode, g
}

func TestTheFlowGraphShowsWhereAutopilotIs(t *testing.T) {
	t.Parallel()
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"say": "Which field names?"}}}, "asking")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n")
	scratch(t, repo, "search/issues/01-index.md", ticket("01", "Index", "ready-for-agent", "None"))
	scratch(t, repo, "search/issues/02-query.md", ticket("02", "Query", "ready-for-agent", "01"))
	f.addProject(c, repo)
	f.setAutopilot(c, repo, true)

	// Autopilot runs implement on 01, whose turn ends asking: a Stop point.
	// The node is current once the Autopilot status catches up with the
	// history, a recompute later. Under make stress, with every test in
	// parallel, the turn end and that recompute can take seconds, so the
	// deadline is generous: it only bounds a wait for progress.
	var g map[string]any
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, g = f.flow(c, repo, "search")
		lanes, _ := g["lanes"].([]any)
		if len(lanes) == 2 {
			nodes, _ := lanes[0].(map[string]any)["nodes"].([]any)
			if n := len(nodes); n > 0 && nodes[n-1].(map[string]any)["current"] == true && nodes[n-1].(map[string]any)["name"] == "asking" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("flow graph never showed 01 waiting at asking: %v", g)
		}
		time.Sleep(50 * time.Millisecond)
	}

	lanes := g["lanes"].([]any)
	first, second := lanes[0].(map[string]any), lanes[1].(map[string]any)
	nodes := first["nodes"].([]any)
	ran := nodes[0].(map[string]any)
	if first["ticket"] != "01" || ran["name"] != "implement" || ran["state"] != "passed" || ran["session"] == "" {
		t.Errorf("lane 01 = %v, want implement passed in a Session, then asking", first)
	}
	if stop := nodes[len(nodes)-1].(map[string]any); stop["kind"] != "stop" || stop["state"] != "waiting" {
		t.Errorf("last node %v, want a waiting Stop point", stop)
	}
	if blocked, _ := second["blockedBy"].([]any); len(blocked) != 1 || blocked[0] != "01" {
		t.Errorf("lane 02 blockedBy = %v, want [01]", second["blockedBy"])
	}
	main := g["main"].([]any)
	if approve := main[2].(map[string]any); approve["name"] != "spec-approval" || approve["state"] != "passed" {
		t.Errorf("Approve spec node = %v, want passed: the Spec is approved", approve)
	}
}

func TestTheFlowGraphOfAnUnknownFeatureIsNotFound(t *testing.T) {
	t.Parallel()
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", "# Search\n")
	f.addProject(c, repo)

	if code, _ := f.flow(c, repo, "nope"); code != http.StatusNotFound {
		t.Errorf("unknown Feature: status %d, want 404", code)
	}
	if code, _ := f.flow(c, "/no/such/project", "search"); code != http.StatusNotFound {
		t.Errorf("unknown Project: status %d, want 404", code)
	}
}
