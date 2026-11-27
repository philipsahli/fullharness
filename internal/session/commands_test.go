package session_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/philipsahli/fullharness/internal/agent"
	"github.com/philipsahli/fullharness/internal/session"
)

// This test drives session.Manager directly, not the API: the race it checks
// is between two Config callbacks, Broadcast and Commands, and the window
// between them is too short for an HTTP or WebSocket test to hit reliably
// (#234). Both callbacks run synchronously, so the order is checked exactly.
func TestTheAgentsCommandsAreSavedBeforeTheSummaryWithThemIsPublished(t *testing.T) {
	script, err := json.Marshal(map[string]any{
		"onStart": []any{map[string]any{"commands": []string{"grill-me", "tdd"}}},
		"prompts": map[string]any{"install": []any{map[string]any{"commands": []string{"grill-me", "tdd", "retro"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var saved []string // what Commands last saved: the cache skill resolution reads
	var early [][]string
	lists := make(chan []string, 8)
	m := session.New(session.Config{
		LogDir: t.TempDir(),
		Broadcast: func(e any) {
			ev, ok := e.(map[string]any)
			if !ok || ev["type"] != "session" {
				return
			}
			sum := ev["session"].(session.Summary)
			if len(sum.Commands) == 0 {
				return
			}
			var names []string
			for _, c := range sum.Commands {
				names = append(names, c.Name)
			}
			mu.Lock()
			if !slices.Equal(names, saved) {
				early = append(early, names)
			}
			mu.Unlock()
			select {
			case lists <- names:
			default:
			}
		},
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		StartTimeout: 10 * time.Second,
		Commands: func(_ string, names []string) {
			mu.Lock()
			saved = slices.Clone(names)
			mu.Unlock()
		},
	})
	t.Cleanup(m.StopAll)
	def := agent.Definition{ID: "fake", Name: "Fake", Command: []string{fakeAgentBin},
		Env: map[string]string{"FH_FAKE_SCRIPT": string(script)}}
	sum, err := m.Start(def, session.Launch{Project: t.TempDir(), Worktree: t.TempDir(), Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	waitList := func(n int) {
		t.Helper()
		deadline := time.After(20 * time.Second) // bounds the wait only
		for {
			select {
			case l := <-lists:
				if len(l) == n {
					return
				}
			case <-deadline:
				t.Fatalf("no summary with %d commands", n)
			}
		}
	}
	waitList(2)
	// Prompt is refused until the Session is Idle.
	for deadline := time.Now().Add(20 * time.Second); ; {
		if err := m.Prompt(sum.ID, "install"); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("prompt: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitList(3)

	mu.Lock()
	defer mu.Unlock()
	if len(early) > 0 {
		t.Errorf("summaries published before their commands were saved: %v", early)
	}
}
