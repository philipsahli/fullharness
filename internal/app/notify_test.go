package app_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// connectNotifyingTab opens an event channel the way a tab with browser
// notification permission does.
func (f *fh) connectNotifyingTab(c *http.Client) *websocket.Conn {
	f.t.Helper()
	conn, status, err := f.dialWSPath(c, f.origin(), "/ws?notify=1")
	if err != nil {
		f.t.Fatalf("dial: status %d: %v", status, err)
	}
	if ev := readEvent(f.t, conn); ev["type"] != "snapshot" {
		f.t.Fatalf("first event %v, want snapshot", ev["type"])
	}
	return conn
}

// notifyOnWaiting reads tab's events until Session id starts Waiting, and
// returns the notify event fh sends with it, or nil if none follows. fh
// sends the notification right after the state, so a short wait suffices.
func notifyOnWaiting(t *testing.T, tab *websocket.Conn, id string) map[string]any {
	t.Helper()
	waitState(t, tab, id, "waiting")
	for {
		ev := nextEvent(t, tab, 300*time.Millisecond)
		if ev == nil {
			return nil
		}
		if ev["type"] == "notify" && ev["session"] == id {
			return ev
		}
	}
}

// fakeOsascript puts an osascript first on PATH that appends its arguments
// to a file, and returns a function that waits for them.
func fakeOsascript(t *testing.T) func() string {
	t.Helper()
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "osascript-calls")
	writeFile(t, filepath.Join(bin, "osascript"), "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \""+calls+"\"\n")
	if err := os.Chmod(filepath.Join(bin, "osascript"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string {
		// Up to 10 s: a loaded machine (or CI) can take seconds to end the turn.
		for range 500 {
			if b, err := os.ReadFile(calls); err == nil && len(b) > 0 {
				return string(b)
			}
			time.Sleep(20 * time.Millisecond)
		}
		return ""
	}
}

func TestAFinishedTurnNotifiesThroughATabThatCan(t *testing.T) {
	t.Parallel()
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "done"}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectNotifyingTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})

	n := notifyOnWaiting(t, tab, id)

	if n == nil || !strings.Contains(n["body"].(string), "finished") {
		t.Fatalf("notify = %v, want one saying the turn finished", n)
	}
}

func TestAPermissionRequestNotifies(t *testing.T) {
	t.Parallel()
	f := startFhWithAgent(t, askScript(nil))
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectNotifyingTab(c)
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "ask"})

	n := notifyOnWaiting(t, tab, id)

	if n == nil || !strings.Contains(n["body"].(string), "asks to run rm -rf build") {
		t.Fatalf("notify = %v, want one naming the request", n)
	}
}

func TestAtMostOneNotificationPerSessionPerMinute(t *testing.T) {
	t.Parallel()
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "done"}}})
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectNotifyingTab(c)

	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "one"})
	if n := notifyOnWaiting(t, tab, id); n == nil {
		t.Fatal("first turn: no notification")
	}
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "two"})
	waitState(t, tab, id, "working")

	if n := notifyOnWaiting(t, tab, id); n != nil {
		t.Fatalf("second turn within the minute notified: %v", n)
	}
}

func TestNoNotificationForTheSessionInView(t *testing.T) {
	t.Parallel()
	f := startFhWithAgent(t, askScript(nil))
	c, _, id, _ := newSessionIn(t, f, "")
	tab := f.connectNotifyingTab(c)
	f.viewing(c, id, "tab-1", true)

	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "ask"})

	if n := notifyOnWaiting(t, tab, id); n != nil {
		t.Fatalf("notified for the Session in view: %v", n)
	}
}

func TestATabThatCantNotifyFallsBackToNative(t *testing.T) {
	calls := fakeOsascript(t)
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"say": "done"}}})
	c, tab, id, _ := newSessionIn(t, f, "") // a tab without notification permission
	_ = tab
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})

	got := calls()

	if !strings.Contains(got, "display notification") || !strings.Contains(got, "finished") {
		t.Fatalf("osascript got %q, want a display notification about the finished turn", got)
	}
}

func TestANativeNotificationQuotesTheTitle(t *testing.T) {
	calls := fakeOsascript(t)
	f := startFhWithAgent(t, script{"default": []any{map[string]any{"title": `Fix "login" \ page`}}})
	c, _, id, _ := newSessionIn(t, f, "")
	f.post(c, "/api/sessions/"+id+"/prompt", map[string]string{"text": "go"})

	if want := `with title "fh: Fix \"login\" \\ page"`; !strings.Contains(calls(), want) {
		t.Fatalf("osascript calls don't contain %q", want)
	}
}
