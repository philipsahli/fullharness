package session_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/philipsahli/fullharness/internal/agent"
	"github.com/philipsahli/fullharness/internal/session"
)

// A cancel that comes after the Session shows Working but before fh has
// sent session/prompt must still end the turn (#56). The SDK applies a
// cancel only to prompts that arrived before it, so fh mustn't send that
// prompt at all.
func TestACancelBeforeThePromptIsSentEndsTheTurn(t *testing.T) {
	// The Agent ends a turn only when cancelled: a cancel it never applies
	// leaves the turn running.
	script, _ := json.Marshal(map[string]any{"default": []any{map[string]any{"waitCancel": true}}})
	var m *session.Manager
	var id string
	cancelled := make(chan error, 1)
	ends := make(chan session.TurnEnd, 1)
	m = session.New(session.Config{
		LogDir: t.TempDir(),
		// prompt() emits the user's message after the turn is Working and
		// before it sends session/prompt: cancel right there.
		Broadcast: func(ev any) {
			e, _ := ev.(map[string]any)
			if it, ok := e["item"].(session.Item); ok && e["type"] == "item" && it.Type == "user" && it.Text == "go" {
				cancelled <- m.Cancel(id)
			}
		},
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		StartTimeout: 10 * time.Second,
		TurnEnded:    func(e session.TurnEnd) { ends <- e },
	})
	t.Cleanup(m.StopAll)
	def := agent.Definition{ID: "fake", Name: "Fake", Command: []string{fakeAgentBin},
		Env: map[string]string{"FH_FAKE_SCRIPT": string(script)}}
	sum, err := m.Start(def, session.Launch{Project: t.TempDir(), Worktree: t.TempDir(), Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	id = sum.ID
	state := func() string {
		t.Helper()
		d, err := m.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		return d.State
	}
	// Prompt queues while Starting; wait for Idle so "go" starts a turn.
	for deadline := time.Now().Add(10 * time.Second); state() != session.Idle; {
		if time.Now().After(deadline) {
			t.Fatalf("Session never became Idle: %v", state())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.Prompt(id, "go"); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-cancelled:
		if err != nil {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the user's message was never emitted")
	}
	select {
	case e := <-ends:
		if e.StopReason != "cancelled" || e.Err != nil {
			t.Errorf("turn end = %+v, want cancelled", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never ended: the cancel was lost")
	}
	if st := state(); st != session.Idle {
		t.Errorf("state = %v, want Idle", st)
	}
}
