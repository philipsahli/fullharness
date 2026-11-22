package app_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
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
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		f.t.Fatalf("launch URL: status %d, want 303", resp.StatusCode)
	}
	return c
}

func get(t *testing.T, c *http.Client, u string) *http.Response {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
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
		f.t.Cleanup(func() { conn.CloseNow() })
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
	t.Cleanup(func() { inst.Close() })
	return f
}
