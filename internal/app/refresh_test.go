package app_test

import (
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/philipsahli/fullharness/internal/app"
)

// connectTab opens the event channel and consumes the snapshot.
func (f *fh) connectTab(c *http.Client) *websocket.Conn {
	f.t.Helper()
	conn, status, err := f.dialWS(c, f.origin())
	if err != nil {
		f.t.Fatalf("dial: status %d: %v", status, err)
	}
	ev := readEvent(f.t, conn)
	if ev["type"] != "snapshot" {
		f.t.Fatalf("first event %v, want snapshot", ev["type"])
	}
	snapshots.Store(conn, ev)
	return conn
}

// snapshots holds the snapshot connectTab read for each tab, so a wait that
// times out can say when the tab connected too late (see lateTab).
var snapshots sync.Map // *websocket.Conn → map[string]any

// lateTab explains a wait on tab that timed out when the tab's snapshot
// already showed what the wait wanted: the event happened before the tab
// connected, and a tab sees only events after its snapshot (#151). ok
// gets the snapshot. It returns "" otherwise.
func lateTab(tab *websocket.Conn, ok func(snap map[string]any) bool) string {
	snap, found := snapshots.Load(tab)
	if !found || !ok(snap.(map[string]any)) {
		return ""
	}
	return "; the tab's snapshot already showed it, so it happened before the tab connected: connect the tab before the step that gets it there (#151)"
}

func uncommittedIn(t *testing.T, ev map[string]any, repo string) any {
	t.Helper()
	if ev == nil || ev["type"] != "projects" {
		t.Fatalf("event = %v, want a projects event", ev)
	}
	return worktreeAt(t, projects(t, ev)[0], repo)["uncommitted"]
}

func TestAddingAProjectReachesConnectedTabs(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	tab := f.connectTab(c)
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))

	f.post(c, "/api/projects", map[string]string{"path": repo})

	ev := nextEvent(t, tab, 5*time.Second)
	if ev == nil || ev["type"] != "projects" || projects(t, ev)[0]["path"] != repo {
		t.Fatalf("event = %v, want projects with %s", ev, repo)
	}
}

func TestPeriodicRefreshPushesOnlyChanges(t *testing.T) {
	interval := 50 * time.Millisecond
	f := startFhWith(t, app.Options{Dir: t.TempDir(), Port: new(int), RefreshInterval: interval})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)

	writeFile(t, filepath.Join(repo, "new.txt"), "x\n")

	if got := uncommittedIn(t, nextEvent(t, tab, 5*time.Second), repo); got != float64(1) {
		t.Errorf("uncommitted after edit = %v, want 1", got)
	}
	if ev := nextEvent(t, tab, 10*interval); ev != nil {
		t.Errorf("unchanged refresh pushed %v, want nothing", ev)
	}
}

func TestRefreshCommandPushesChangesAtOnce(t *testing.T) {
	f := startFhWith(t, app.Options{Dir: t.TempDir(), Port: new(int), RefreshInterval: time.Hour})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	tab := f.connectTab(c)
	writeFile(t, filepath.Join(repo, "new.txt"), "x\n")

	status, _ := f.post(c, "/api/projects/refresh", nil)

	if status != http.StatusNoContent {
		t.Fatalf("status %d, want 204", status)
	}
	if got := uncommittedIn(t, nextEvent(t, tab, 5*time.Second), repo); got != float64(1) {
		t.Errorf("uncommitted = %v, want 1", got)
	}
}

// With no tab open nothing refreshes, so a tab that connects after a quiet
// spell must not be left with old state until the next tick.
func TestATabConnectingAfterAQuietSpellGetsCurrentState(t *testing.T) {
	f := startFhWith(t, app.Options{Dir: t.TempDir(), Port: new(int), RefreshInterval: time.Hour})
	c := f.authorisedClient()
	repo := realPath(t, newRepo(t, t.TempDir(), "shop"))
	f.addProject(c, repo)
	writeFile(t, filepath.Join(repo, "new.txt"), "x\n") // while no tab is open

	tab := f.connectTab(c)

	if got := uncommittedIn(t, nextEvent(t, tab, 5*time.Second), repo); got != float64(1) {
		t.Errorf("uncommitted = %v, want 1", got)
	}
}
