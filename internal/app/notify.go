package app

import (
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/session"
)

// notifyEvery limits notifications to one per Session per minute, so ten
// Sessions working in parallel don't flood you (#1, story 50).
const notifyEvery = time.Minute

// notifier tells you when a Session starts Waiting: through the open tabs
// as a browser notification, or, with no tab open, as a native macOS
// notification. Nothing for the Session you're looking at.
type notifier struct {
	srv *server.Server
	log *slog.Logger

	mu   sync.Mutex
	last map[string]time.Time // Session ID → last notification
}

func newNotifier(srv *server.Server, log *slog.Logger) *notifier {
	return &notifier{srv: srv, log: log, last: map[string]time.Time{}}
}

func (n *notifier) enteredWaiting(sum session.Summary, what string, viewed bool) {
	if viewed {
		return
	}
	n.mu.Lock()
	if time.Since(n.last[sum.ID]) < notifyEvery {
		n.mu.Unlock()
		return
	}
	n.last[sum.ID] = time.Now()
	n.mu.Unlock()

	title := "fh: " + sum.Title
	if n.srv.Tabs() > 0 {
		// Every tab shows it with the Session ID as its tag; the browser
		// keeps one notification per tag, so several tabs show it once.
		n.srv.Broadcast(map[string]any{"type": "notify", "session": sum.ID, "title": title, "body": what})
		return
	}
	go n.native(title, what)
}

// native shows a macOS notification with osascript.
func (n *notifier) native(title, body string) {
	script := "display notification " + appleString(body) + " with title " + appleString(title)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput(); err != nil {
		n.log.Info("native notification failed", "err", err, "output", strings.TrimSpace(string(out)))
	}
}

// appleString quotes s as an AppleScript string literal.
func appleString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
