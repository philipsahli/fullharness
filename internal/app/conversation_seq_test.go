package app_test

import (
	"testing"
	"time"
)

// #69: a tab that fetches a conversation keeps the events it gets meanwhile
// and applies only those the fetch doesn't hold. That needs every
// conversation event numbered, and the fetch to say up to which number it
// holds.
func TestConversationEventsAreNumberedAndTheFetchSaysWhichItHolds(t *testing.T) {
	t.Parallel()
	f := startFhWithAgent(t, script{"prompts": map[string]any{"hello": []any{
		map[string]any{"say": "Hi "},
		map[string]any{"say": "there"},
		map[string]any{"tool": map[string]any{"id": "t1", "title": "ls", "kind": "execute", "status": "pending"}},
		map[string]any{"toolUpdate": map[string]any{"id": "t1", "status": "completed"}},
	}}})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	id := f.startSession(c, map[string]any{"project": repo, "worktree": repo, "agent": "claude", "prompt": "hello"})

	var seqs []int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ev := nextEvent(t, tab, time.Until(deadline))
		if ev == nil {
			t.Fatal("the turn never ended")
		}
		switch ev["type"] {
		case "item", "chunk":
			if ev["session"] == id {
				n, ok := ev["seq"].(float64)
				if !ok {
					t.Fatalf("%s event without a seq: %v", ev["type"], ev)
				}
				seqs = append(seqs, int(n))
			}
			continue
		case "session":
			s, _ := ev["session"].(map[string]any)
			if s["id"] != id || s["waiting"] != "turn" {
				continue
			}
		default:
			continue
		}
		break
	}

	// user, "Hi ", "there", tool, tool update: five events, numbered 1 to 5
	// in the order fh made the changes.
	if len(seqs) != 5 {
		t.Fatalf("seqs %v, want 5 conversation events", seqs)
	}
	seen := map[int]bool{}
	for _, n := range seqs {
		if n < 1 || n > 5 || seen[n] {
			t.Fatalf("seqs %v, want 1 to 5 once each", seqs)
		}
		seen[n] = true
	}
	if seq := f.sessionDetail(c, id)["seq"]; seq != float64(5) {
		t.Errorf("detail seq = %v, want 5: the fetch holds every event so far", seq)
	}
}
