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

// These tests drive session.Manager directly, not the API: they act from a
// Broadcast hook, which runs synchronously inside prompt() after the turn is
// Working and before session/prompt is written. Over HTTP and WebSocket that
// moment can't be hit on purpose, only by chance (#56).

// startHooked starts a Session of the fake Agent running script, calls
// onUser (from the Broadcast hook) when the user item "go" is emitted, and
// waits until the Session is Idle. It returns the Manager, the Session ID,
// and the channel of turn ends.
func startHooked(t *testing.T, script map[string]any, onUser func(m *session.Manager, id string)) (*session.Manager, string, chan session.TurnEnd) {
	t.Helper()
	js, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	var m *session.Manager
	var id string
	ends := make(chan session.TurnEnd, 4)
	m = session.New(session.Config{
		LogDir: t.TempDir(),
		Broadcast: func(ev any) {
			e, _ := ev.(map[string]any)
			if it, ok := e["item"].(session.Item); ok && e["type"] == "item" && it.Type == "user" && it.Text == "go" {
				onUser(m, id)
			}
		},
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		StartTimeout: 10 * time.Second,
		TurnEnded:    func(e session.TurnEnd) { ends <- e },
	})
	t.Cleanup(m.StopAll)
	def := agent.Definition{ID: "fake", Name: "Fake", Command: []string{fakeAgentBin},
		Env: map[string]string{"FH_FAKE_SCRIPT": string(js)}}
	sum, err := m.Start(def, session.Launch{Project: t.TempDir(), Worktree: t.TempDir(), Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	id = sum.ID
	// Prompt queues while Starting; wait for Idle so "go" starts a turn.
	for deadline := time.Now().Add(10 * time.Second); detail(t, m, id).State != session.Idle; {
		if time.Now().After(deadline) {
			t.Fatalf("Session never became Idle: %v", detail(t, m, id).State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return m, id, ends
}

func detail(t *testing.T, m *session.Manager, id string) session.Detail {
	t.Helper()
	d, err := m.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func nextEnd(t *testing.T, ends chan session.TurnEnd, what string) session.TurnEnd {
	t.Helper()
	select {
	case e := <-ends:
		return e
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never ended: the cancel was lost", what)
		return session.TurnEnd{}
	}
}

// A cancel that comes after the Session shows Working but before fh has
// sent session/prompt must still end the turn (#56). The SDK applies a
// cancel only to prompts that arrived before it, so fh mustn't send that
// prompt at all.
func TestACancelBeforeThePromptIsSentEndsTheTurn(t *testing.T) {
	// The Agent ends a turn only when cancelled: a cancel it never applies
	// leaves the turn running.
	cancelled := make(chan error, 1)
	m, id, ends := startHooked(t, map[string]any{"default": []any{map[string]any{"waitCancel": true}}},
		func(m *session.Manager, id string) { cancelled <- m.Cancel(id) })
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
	if e := nextEnd(t, ends, "the turn"); e.StopReason != "cancelled" || e.Err != nil {
		t.Errorf("turn end = %+v, want cancelled", e)
	}
	if st := detail(t, m, id).State; st != session.Idle {
		t.Errorf("state = %v, want Idle", st)
	}
}

// Send now pressed before the prompt goes out ends that turn unsent and
// sends the Queued message as the next turn (#56).
func TestSendNowBeforeThePromptIsSentSendsTheQueuedMessage(t *testing.T) {
	// "go" ends only when cancelled, so if it were sent the cancel would be
	// lost and no turn would end; "next" answers and ends normally.
	sent := make(chan error, 1)
	m, id, ends := startHooked(t, map[string]any{"prompts": map[string]any{
		"go":   []any{map[string]any{"waitCancel": true}},
		"next": []any{map[string]any{"say": "got next"}},
	}}, func(m *session.Manager, id string) {
		if err := m.Prompt(id, "next"); err != nil {
			sent <- err
			return
		}
		_, err := m.Queued(id, session.QueuedSendNow)
		sent <- err
	})
	if err := m.Prompt(id, "go"); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-sent:
		if err != nil {
			t.Fatalf("queue and send now: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the user's message was never emitted")
	}
	if e := nextEnd(t, ends, "the first turn"); e.StopReason != "cancelled" || e.Err != nil {
		t.Errorf("first turn end = %+v, want cancelled", e)
	}
	if e := nextEnd(t, ends, "the Queued message's turn"); e.StopReason != "end_turn" || e.Err != nil {
		t.Errorf("second turn end = %+v, want end_turn", e)
	}
	d := detail(t, m, id)
	if d.Queued != "" || d.Unsent != "" {
		t.Errorf("queued = %q, unsent = %q; want both empty", d.Queued, d.Unsent)
	}
	// Start-up notices come before "go"; compare from "go" on.
	var got []string
	for _, it := range d.Transcript {
		if line := it.Type + ": " + it.Text; len(got) > 0 || line == "user: go" {
			got = append(got, line)
		}
	}
	want := []string{"user: go", "notice: Turn cancelled.", "user: next", "agent: got next"}
	if len(got) != len(want) {
		t.Fatalf("transcript = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("transcript = %q, want %q", got, want)
		}
	}
}

// A cancel written to the Agent just before session/prompt reaches it
// first, and the SDK drops a cancel that arrives before its prompt. fh
// must resend it so the turn still ends (#56, #155).
func TestACancelThatOvertakesThePromptStillEndsTheTurn(t *testing.T) {
	cancelled := make(chan error, 1)
	m, id, ends := startHooked(t, map[string]any{"default": []any{map[string]any{"waitCancel": true}}},
		func(*session.Manager, string) {})
	session.SetBeforePromptWrite(t, func(string) {
		cancelled <- m.Cancel(id)
		// Give the cancel time to reach the Agent before the prompt does.
		time.Sleep(50 * time.Millisecond)
	})
	if err := m.Prompt(id, "go"); err != nil {
		t.Fatal(err)
	}

	if err := <-cancelled; err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if e := nextEnd(t, ends, "the turn"); e.StopReason != "cancelled" || e.Err != nil {
		t.Errorf("turn end = %+v, want cancelled", e)
	}
	if st := detail(t, m, id).State; st != session.Idle {
		t.Errorf("state = %v, want Idle", st)
	}
}
