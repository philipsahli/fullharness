package app_test

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Autopilot's action history (#112): every action per Feature, kept in
// state.json so the Loop view (#114) can draw it after a restart.

// autopilotFeatures returns GET /api/autopilot?project=… as its Features.
func (f *fh) autopilotFeatures(c *http.Client, project string) []map[string]any {
	f.t.Helper()
	resp, err := c.Get(f.origin() + "/api/autopilot?project=" + url.QueryEscape(project))
	if err != nil {
		f.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("GET /api/autopilot: status %d", resp.StatusCode)
	}
	return objects(f.t, decodeObject(f.t, readBody(f.t, resp)), "features")
}

// eventsOf returns the events of Feature id, each cut down to keys.
func eventsOf(t *testing.T, features []map[string]any, id string, keys ...string) []map[string]any {
	t.Helper()
	for _, feat := range features {
		if feat["feature"] != id {
			continue
		}
		var out []map[string]any
		for _, a := range objects(t, feat, "events") {
			cut := map[string]any{}
			for _, k := range keys {
				if v, ok := a[k]; ok {
					cut[k] = v
				}
			}
			out = append(out, cut)
		}
		return out
	}
	t.Fatalf("no Feature %s in %v", id, features)
	return nil
}

// waitEvents waits until Feature id has n events.
func (f *fh) waitEvents(c *http.Client, project, id string, n int) []map[string]any {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var features []map[string]any
	for time.Now().Before(deadline) {
		features = f.autopilotFeatures(c, project)
		for _, feat := range features {
			if raw, _ := feat["events"].([]any); feat["feature"] == id && len(raw) >= n {
				return features
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatalf("Feature %s never had %d events: %v", id, n, features)
	return nil
}

func TestAutopilotActionsAppearInOrderAndSurviveARestart(t *testing.T) {
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"say": "Which login method?"}}}, "asking")
	c := f.authorisedClient()
	repo := twoTicketRepo(t)
	f.addProject(c, repo)
	f.setAutopilot(c, repo, true)
	id := f.waitAutopilotSessions(c, 1)[0]["id"].(string)

	keys := []string{"kind", "ticket", "skill", "session", "verdict", "reason"}
	want := []map[string]any{
		{"kind": "skill", "ticket": "01", "skill": "implement", "session": id},
		{"kind": "classified", "ticket": "01", "skill": "implement", "session": id, "verdict": "asking"},
		{"kind": "stop", "ticket": "01", "skill": "implement", "session": id, "reason": "asking"},
	}
	got := eventsOf(t, f.waitEvents(c, repo, "login", len(want)), "login", keys...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n%v\nwant\n%v", got, want)
	}

	f.stop()
	f = startFhWith(t, f.opts)
	c = f.authorisedClient()
	if got := eventsOf(t, f.autopilotFeatures(c, repo), "login", keys...); !reflect.DeepEqual(got, want) {
		t.Errorf("after a restart\n%v\nwant\n%v", got, want)
	}
}

func TestASpecWaitingForApprovalIsOneStopPoint(t *testing.T) {
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"say": "Wrote the tickets."}}}, "asking")
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** needs-triage\n")
	f.addProject(c, repo)
	f.setAutopilot(c, repo, true)
	time.Sleep(300 * time.Millisecond) // Autopilot looks every 50 ms; the stop is recorded once

	keys := []string{"kind", "skill", "reason"}
	want := []map[string]any{{"kind": "stop", "skill": "to-tickets", "reason": "spec-approval"}}
	if got := eventsOf(t, f.waitEvents(c, repo, "search", 1), "search", keys...); !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n%v\nwant\n%v", got, want)
	}

	scratch(t, repo, "search/spec.md", "# Search\n\n**Roadmap:** now\n\n**Status:** ready-for-agent\n")
	want = append(want, map[string]any{"kind": "skill", "skill": "to-tickets"})
	if got := eventsOf(t, f.waitEvents(c, repo, "search", 2), "search", keys...); !reflect.DeepEqual(got[:2], want) {
		t.Errorf("events after the approval\n%v\nwant them to start with\n%v", got, want)
	}
}

func TestAReviewedTicketWaitsForTheMergeAsOneStopPoint(t *testing.T) {
	_, _, setPRs := fakeGhLogged(t, []ghIssue{
		{Number: 10, Title: "Login", State: "OPEN", Labels: []string{"roadmap:now", "ready-for-agent"}, SubIssues: []int{11}},
		{Number: 11, Title: "Form", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 10},
	}, nil, true)
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"sleepMs": 300}, map[string]any{"say": "Opened PR #5."}}}, "done")
	c := f.authorisedClient()
	repo := githubRepo(t)
	f.addProject(c, repo)
	f.setAutopilot(c, repo, true)
	s := f.waitAutopilotSessions(c, 1)[0]
	// The implement skill opens the Ticket's PR during its turn.
	setPRs([]map[string]any{{"number": 5, "headRefName": s["title"], "state": "OPEN"}})

	keys := []string{"kind", "ticket", "skill", "verdict", "reason"}
	want := []map[string]any{
		{"kind": "skill", "ticket": "11", "skill": "implement"},
		{"kind": "classified", "ticket": "11", "skill": "implement", "verdict": "done"},
		{"kind": "skill", "ticket": "11", "skill": "code-review"},
		{"kind": "classified", "ticket": "11", "skill": "code-review", "verdict": "done"},
		{"kind": "stop", "ticket": "11", "skill": "code-review", "reason": "merge"},
	}
	f.waitEvents(c, repo, "10", len(want))
	time.Sleep(300 * time.Millisecond) // more looks add no second merge stop
	if got := eventsOf(t, f.autopilotFeatures(c, repo), "10", keys...); !reflect.DeepEqual(got, want) {
		t.Errorf("events\n%v\nwant\n%v", got, want)
	}
}

func TestAStartThatFailsIsOneEventWhileAutopilotRetries(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "[agents.claude]\ncommand = [\""+filepath.Join(dir, "no-such-agent")+"\"]\n")
	opts := appOptions(dir)
	opts.AutopilotInterval = 50 * time.Millisecond
	f := startFhWith(t, opts)
	c := f.authorisedClient()
	repo := twoTicketRepo(t)
	f.addProject(c, repo)
	f.setAutopilot(c, repo, true)
	time.Sleep(300 * time.Millisecond) // Autopilot retries every 50 ms

	features := f.waitEvents(c, repo, "login", 2)
	// Each Ticket's start fails, once each however often Autopilot retries.
	want := []map[string]any{
		{"kind": "failed", "ticket": "01", "skill": "implement"},
		{"kind": "failed", "ticket": "02", "skill": "implement"},
	}
	if got := eventsOf(t, features, "login", "kind", "ticket", "skill"); !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n%v\nwant\n%v", got, want)
	}
	if e := eventsOf(t, features, "login", "error")[0]["error"]; !strings.Contains(fmt.Sprint(e), "can't start") {
		t.Errorf("error %q, want why the start failed", e)
	}
}

func TestAReviewThatEndedStuckIsNoMergeWaitOnceItsSessionIsClosed(t *testing.T) {
	_, _, setPRs := fakeGhLogged(t, []ghIssue{
		{Number: 10, Title: "Login", State: "OPEN", Labels: []string{"roadmap:now", "ready-for-agent"}, SubIssues: []int{11}},
		{Number: 11, Title: "Form", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 10},
	}, nil, true)
	// implement ends done, code-review ends stuck.
	f := startAutopilotFh(t, script{"default": []any{map[string]any{"sleepMs": 300}, map[string]any{"say": "Opened PR #5."}}}, "done", "stuck")
	c := f.authorisedClient()
	repo := githubRepo(t)
	f.addProject(c, repo)
	f.setAutopilot(c, repo, true)
	s := f.waitAutopilotSessions(c, 1)[0]
	id := s["id"].(string)
	setPRs([]map[string]any{{"number": 5, "headRefName": s["title"], "state": "OPEN"}})
	f.waitEvents(c, repo, "10", 5) // …, skill code-review, classified stuck, stop stuck

	// The user takes the stuck review over: the Project is free again, but
	// the PR doesn't wait for a merge, as no review ended done.
	if status, body := f.post(c, "/api/sessions/"+id+"/close", map[string]any{"confirm": true}); status >= 300 {
		t.Fatalf("close: status %d (%v)", status, body)
	}
	time.Sleep(300 * time.Millisecond)
	events := eventsOf(t, f.autopilotFeatures(c, repo), "10", "kind", "skill", "verdict", "reason")
	want := map[string]any{"kind": "stop", "skill": "code-review", "reason": "stuck"}
	if last := events[len(events)-1]; !reflect.DeepEqual(last, want) {
		t.Errorf("events %v, want them to end with %v", events, want)
	}
}
