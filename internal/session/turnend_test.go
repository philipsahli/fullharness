package session_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/philipsahli/fullharness/internal/agent"
	"github.com/philipsahli/fullharness/internal/session"
)

var fakeAgentBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fh-fakeagent-")
	if err != nil {
		panic(err)
	}
	fakeAgentBin = filepath.Join(dir, "fakeagent")
	out, err := exec.Command("go", "build", "-o", fakeAgentBin, "github.com/philipsahli/fullharness/internal/acp/fakeagent").CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("build fake agent: %v\n%s", err, out))
	}
	code := m.Run()
	_ = os.RemoveAll(dir) // a leftover temp dir is harmless
	os.Exit(code)
}

// This test drives session.Manager directly, not the API: the TurnEnded hook
// it checks is a Config callback (app.go hands it to Autopilot) that no HTTP
// or WebSocket response shows.
func TestEachTurnEndCallsTheHookWithItsSessionAndStopReason(t *testing.T) {
	script, err := json.Marshal(map[string]any{"prompts": map[string]any{
		"stop":   []any{map[string]any{"stop": "cancelled"}},
		"fail":   []any{map[string]any{"fail": "boom"}},
		"tokens": []any{map[string]any{"stop": "max_tokens"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ends := make(chan session.TurnEnd, 4)
	m := session.New(session.Config{
		LogDir:       t.TempDir(),
		Broadcast:    func(any) {},
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		StartTimeout: 10 * time.Second,
		TurnEnded:    func(e session.TurnEnd) { ends <- e },
	})
	t.Cleanup(m.StopAll)
	def := agent.Definition{ID: "fake", Name: "Fake", Command: []string{fakeAgentBin},
		Env: map[string]string{"FH_FAKE_SCRIPT": string(script)}}
	sum, err := m.Start(def, session.Launch{Project: t.TempDir(), Worktree: t.TempDir(), Branch: "main", Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	next := func() session.TurnEnd {
		t.Helper()
		select {
		case e := <-ends:
			return e
		case <-time.After(10 * time.Second):
			t.Fatal("no turn end within 10 s")
			return session.TurnEnd{}
		}
	}
	prompt := func(text string) {
		t.Helper()
		// Prompt is refused until the Session is Idle again after the hook.
		for deadline := time.Now().Add(5 * time.Second); ; {
			if err := m.Prompt(sum.ID, text); err == nil {
				return
			} else if time.Now().After(deadline) {
				t.Fatalf("prompt %q: %v", text, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	if e := next(); e.Session != sum.ID || e.StopReason != "end_turn" || e.Err != nil {
		t.Errorf("first turn: %+v, want %s end_turn", e, sum.ID)
	}
	for _, c := range []struct{ prompt, reason string }{{"stop", "cancelled"}, {"tokens", "max_tokens"}} {
		prompt(c.prompt)
		if e := next(); e.Session != sum.ID || e.StopReason != c.reason || e.Err != nil {
			t.Errorf("%s: %+v, want %s %s", c.prompt, e, sum.ID, c.reason)
		}
	}
	prompt("fail")
	if e := next(); e.Session != sum.ID || e.StopReason != "" || e.Err == nil {
		t.Errorf("fail: %+v, want %s with an error and no stop reason", e, sum.ID)
	}
}
