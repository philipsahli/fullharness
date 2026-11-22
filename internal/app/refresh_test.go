package app_test

import (
	"net/http"
	"path/filepath"
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
	if ev := readEvent(f.t, conn); ev["type"] != "snapshot" {
		f.t.Fatalf("first event %v, want snapshot", ev["type"])
	}
	return conn
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
