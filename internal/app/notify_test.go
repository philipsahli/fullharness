package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// notifyEvents reads tab's events for d and returns the "notify" events.
func notifyEvents(t *testing.T, tab *websocket.Conn, d time.Duration) []map[string]any {
	t.Helper()
	var out []map[string]any
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		ev := nextEvent(t, tab, time.Until(deadline))
		if ev == nil {
			break
		}
		if ev["type"] == "notify" {
			out = append(out, ev)
		}
	}
	return out
}

func TestEnteringWaitingNotifiesThroughAConnectedTab(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "done"}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})

	got := notifyEvents(t, tab, 2*time.Second)

	if len(got) != 1 || got[0]["session"] != id || !strings.Contains(got[0]["body"].(string), "finished") {
		t.Fatalf("notify events = %v, want one for Session %s saying its turn finished", got, id)
	}
}

func TestAtMostOneNotificationPerSessionPerMinute(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "done"}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	for _, text := range []string{"one", "two"} {
		f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": text})
		waitTurn(t, tab, id)
	}
	tab2 := f.connectTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "three"})

	if got := notifyEvents(t, tab2, 2*time.Second); len(got) != 0 {
		t.Fatalf("notify events within the minute = %v, want none after the first", got)
	}
}

func TestNoNotificationForTheSessionInView(t *testing.T) {
	f := startFhWithAgent(t, askScript(nil))
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectTab(c)
	f.viewing(c, id, "tab-1", true)

	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "ask"})

	if got := notifyEvents(t, tab, 2*time.Second); len(got) != 0 {
		t.Fatalf("notify events = %v, want none for the Session you're looking at", got)
	}
}

func TestWithoutATabFhNotifiesNatively(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "osascript-calls")
	writeFile(t, filepath.Join(bin, "osascript"), "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \""+calls+"\"\n")
	if err := os.Chmod(filepath.Join(bin, "osascript"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "done"}}})
	c, _, id, _ := newSessionIn(t, f, "")
	// newSessionIn's tab is still open; let fh see it go.
	f.closeTabs()
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})

	var got []byte
	for range 100 {
		if b, err := os.ReadFile(calls); err == nil && len(b) > 0 {
			got = b
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(string(got), "display notification") || !strings.Contains(string(got), "finished") {
		t.Fatalf("osascript got %q, want a display notification about the finished turn", got)
	}
}

func TestANativeNotificationQuotesTheTitle(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "osascript-calls")
	writeFile(t, filepath.Join(bin, "osascript"), "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \""+calls+"\"\n")
	if err := os.Chmod(filepath.Join(bin, "osascript"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"title": `Fix "login" \ page`}}})
	c, _, id, _ := newSessionIn(t, f, "")
	f.closeTabs()
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})

	var got []byte
	for range 100 {
		if b, err := os.ReadFile(calls); err == nil && len(b) > 0 {
			got = b
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if want := `with title "fh: Fix \"login\" \\ page"`; !strings.Contains(string(got), want) {
		t.Fatalf("osascript got %q, want it to contain %q", got, want)
	}
}
