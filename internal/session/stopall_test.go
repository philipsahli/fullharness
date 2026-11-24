package session_test

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/philipsahli/fullharness/internal/agent"
	"github.com/philipsahli/fullharness/internal/session"
)

// StopAll returns only once the Sessions are done saving: a Session that
// sees its Agent exit saves itself, and that save used to land after fh had
// closed, while a test's TempDir cleanup was removing fh's folder (#68).
func TestStopAllReturnsAfterTheSessionsLastSave(t *testing.T) {
	var mu sync.Mutex
	var lastSave time.Time
	m := session.New(session.Config{
		LogDir:       t.TempDir(),
		Broadcast:    func(any) {},
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		StartTimeout: 10 * time.Second,
		// A slow save, so one that is still running when StopAll returns
		// ends visibly after it.
		Persist: func([]session.Saved) {
			time.Sleep(100 * time.Millisecond)
			mu.Lock()
			lastSave = time.Now()
			mu.Unlock()
		},
	})
	def := agent.Definition{ID: "fake", Name: "Fake", Command: []string{fakeAgentBin}}
	sum, err := m.Start(def, session.Launch{Project: t.TempDir(), Worktree: t.TempDir(), Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if d, err := m.Get(sum.ID); err == nil && d.State == session.Idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the Session never became Idle")
		}
	}

	m.StopAll()
	stopped := time.Now()
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if lastSave.After(stopped) {
		t.Errorf("a save ended %v after StopAll returned", lastSave.Sub(stopped))
	}
}
