package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/philipsahli/fullharness/internal/app"
)

// fh is one in-process fh launch against a temp config/state folder. Tests
// drive it only over HTTP and WebSocket, the way the UI does.
type fh struct {
	t      *testing.T
	inst   *app.Instance
	opened []string // URLs fh asked the browser to open
}

// startFh launches fh on any free port, so tests can run in parallel.
func startFh(t *testing.T, dir string) *fh {
	t.Helper()
	return startFhOnPort(t, dir, 0)
}

// launchURL is the last URL fh opened in the browser.
func (f *fh) launchURL() *url.URL {
	f.t.Helper()
	if len(f.opened) == 0 {
		f.t.Fatal("fh opened no browser tab")
	}
	u, err := url.Parse(f.opened[len(f.opened)-1])
	if err != nil {
		f.t.Fatal(err)
	}
	return u
}

// origin is the base URL of the launch, e.g. http://127.0.0.1:4815.
func (f *fh) origin() string {
	u := f.launchURL()
	return u.Scheme + "://" + u.Host
}

// noRedirect is a client that keeps cookies and does not follow redirects.
func noRedirectClient(t *testing.T) *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// authorisedClient opens the launch URL the way the browser does and returns
// a client holding the resulting cookie.
func (f *fh) authorisedClient() *http.Client {
	f.t.Helper()
	c := noRedirectClient(f.t)
	resp, err := c.Get(f.launchURL().String())
	if err != nil {
		f.t.Fatal(err)
	}
	readBody(f.t, resp)
	if resp.StatusCode != http.StatusSeeOther {
		f.t.Fatalf("launch URL: status %d, want 303", resp.StatusCode)
	}
	return c
}

// readBody reads and closes resp's body, failing the test on a read error.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func get(t *testing.T, c *http.Client, u string) *http.Response {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	readBody(t, resp)
	return resp
}

// dialWS opens the event channel with the given client's cookies and Origin
// header. It returns the HTTP status of the upgrade response when the dial is
// refused.
func (f *fh) dialWS(c *http.Client, origin string) (*websocket.Conn, int, error) {
	f.t.Helper()
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	wsURL := "ws" + strings.TrimPrefix(f.origin(), "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: c, HTTPHeader: h})
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	if conn != nil {
		f.t.Cleanup(func() { _ = conn.CloseNow() }) // already closed if fh stopped first
	}
	return conn, status, err
}

// readEvent reads one JSON event from the event channel.
func readEvent(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ev map[string]any
	if err := wsjson.Read(ctx, conn, &ev); err != nil {
		t.Fatalf("read event: %v", err)
	}
	return ev
}

func startFhOnPort(t *testing.T, dir string, port int) *fh {
	t.Helper()
	return startFhWith(t, app.Options{Dir: dir, Port: &port})
}

func startFhWith(t *testing.T, opts app.Options) *fh {
	t.Helper()
	f := &fh{t: t}
	opts.OpenBrowser = func(u string) { f.opened = append(f.opened, u) }
	inst, err := app.Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("start fh: %v", err)
	}
	f.inst = inst
	t.Cleanup(func() { stopFh(t, inst) })
	return f
}

// stop stops this launch the way Ctrl+C does.
func (f *fh) stop() {
	f.t.Helper()
	stopFh(f.t, f.inst)
}

// stopFh closes inst and fails the test if that goes wrong (for example, the
// launch file could not be removed). Closing twice is fine.
func stopFh(t *testing.T, inst *app.Instance) {
	t.Helper()
	if err := inst.Close(); err != nil {
		t.Errorf("stop fh: %v", err)
	}
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// post sends a JSON command the way the UI does: with the cookie and fh's own
// Origin. It returns the status and the decoded JSON body (nil when empty).
func (f *fh) post(c *http.Client, path string, body any) (int, map[string]any) {
	f.t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		f.t.Fatal(err)
	}
	req, err := http.NewRequest("POST", f.origin()+path, bytes.NewReader(b))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", f.origin())
	resp, err := c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	return resp.StatusCode, decodeObject(f.t, readBody(f.t, resp))
}

// state returns the current snapshot from GET /api/state.
func (f *fh) state(c *http.Client) map[string]any {
	f.t.Helper()
	resp, err := c.Get(f.origin() + "/api/state")
	if err != nil {
		f.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("GET /api/state: status %d", resp.StatusCode)
	}
	return decodeObject(f.t, readBody(f.t, resp))
}

func decodeObject(t *testing.T, body string) map[string]any {
	t.Helper()
	if strings.TrimSpace(body) == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return m
}

// projects returns the "projects" list of a snapshot or projects event.
func projects(t *testing.T, ev map[string]any) []map[string]any {
	t.Helper()
	return objects(t, ev, "projects")
}

// worktrees returns a project's "worktrees" list.
func worktrees(t *testing.T, project map[string]any) []map[string]any {
	t.Helper()
	return objects(t, project, "worktrees")
}

// objects returns m[key] as a list of JSON objects.
func objects(t *testing.T, m map[string]any, key string) []map[string]any {
	t.Helper()
	raw, ok := m[key].([]any)
	if !ok {
		t.Fatalf("%s = %v, want a list", key, m[key])
	}
	out := make([]map[string]any, len(raw))
	for i, v := range raw {
		out[i] = v.(map[string]any)
	}
	return out
}

// nextEvent reads one event, or returns nil if none arrives within d. A read
// that times out closes the connection, so call it last on a conn when
// expecting nil.
func nextEvent(t *testing.T, conn *websocket.Conn, d time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var ev map[string]any
	if err := wsjson.Read(ctx, conn, &ev); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		t.Fatalf("read event: %v", err)
	}
	return ev
}
