//go:build !race

// The race detector slows everything 2-10x, which would make this timing
// meaningless; make test runs it in a separate pass without -race.

package app_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestTenStreamingSessionsReachATabQuickly is the "10 Sessions without lag"
// target (#1, #7): 10 Sessions each stream 20 chunks a second for 5 s, about
// 200 events a second, and a tab gets each within 100 ms at p95.
func TestTenStreamingSessionsReachATabQuickly(t *testing.T) {
	const sessions, perSecond, seconds = 10, 20, 5
	f := startFhWithAgent(t, script{"default": []any{map[string]any{
		"stream": map[string]any{"count": perSecond * seconds, "perSecond": perSecond},
	}}})
	c := f.authorisedClient()
	repos := make([]string, sessions)
	for i := range repos {
		repos[i] = realPath(t, newRepo(t, t.TempDir(), "p"+strconv.Itoa(i)))
		f.addProject(c, repos[i])
	}
	tab := f.connectTab(c)
	for _, repo := range repos {
		f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "stream"})
	}

	var latencies []time.Duration
	want := sessions * perSecond * seconds
	deadline := time.Now().Add(time.Duration(seconds)*time.Second + 15*time.Second)
	for len(latencies) < want && time.Now().Before(deadline) {
		ev := nextEvent(t, tab, time.Until(deadline))
		if ev == nil {
			break
		}
		received := time.Now()
		var text string
		switch ev["type"] {
		case "chunk":
			text, _ = ev["text"].(string)
		case "item":
			if it, _ := ev["item"].(map[string]any); it["type"] == "agent" {
				text, _ = it["text"].(string)
			}
		}
		for _, part := range strings.Split(text, ";") {
			if ns, ok := strings.CutPrefix(part, "t="); ok {
				sent, err := strconv.ParseInt(ns, 10, 64)
				if err == nil {
					latencies = append(latencies, received.Sub(time.Unix(0, sent)))
				}
			}
		}
	}

	if len(latencies) < want {
		t.Fatalf("got %d of %d chunks", len(latencies), want)
	}
	slices.Sort(latencies)
	p95 := latencies[len(latencies)*95/100]
	p99 := latencies[len(latencies)*99/100]
	t.Logf("%d chunks from %d Sessions: p95 %v, p99 %v, max %v", len(latencies), sessions, p95, p99, latencies[len(latencies)-1])
	if p95 > 100*time.Millisecond {
		t.Errorf("p95 %v, want under 100ms", p95)
	}
}
