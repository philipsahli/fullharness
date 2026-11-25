package server_test

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/philipsahli/fullharness/internal/server"
)

// started is one Server listening on 127.0.0.1.
type started struct {
	origin string
	port   int
	token  string
}

func start(t *testing.T, token string) started {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>app</title>")}}
	s := server.New(ln, token, ui, nil)
	go func() { _ = s.Serve() }() // returns an error once Close runs
	t.Cleanup(func() { _ = s.Close() })
	port := ln.Addr().(*net.TCPAddr).Port
	return started{origin: fmt.Sprintf("http://127.0.0.1:%d", port), port: port, token: token}
}

func (s started) signIn(t *testing.T, c *http.Client) {
	t.Helper()
	resp := do(t, c, s.origin+"/auth?token="+s.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign in to %s: status %d, want 200 after the redirect", s.origin, resp.StatusCode)
	}
}

func do(t *testing.T, c *http.Client, url string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close() // the body is already read
	if err != nil {
		t.Fatal(err)
	}
	resp.Body = io.NopCloser(strings.NewReader(string(body)))
	return resp
}

func bodyOf(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Cookies are scoped by host, not port, and Go's cookiejar follows the same
// rule as browsers, so one jar stands in for one browser (#133).
func TestTwoServersOnOneHostStaySignedInThroughOneCookieJar(t *testing.T) {
	a := start(t, "token-a")
	b := start(t, "token-b")
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar}

	a.signIn(t, c)
	b.signIn(t, c)

	for _, s := range []started{a, b} {
		if resp := do(t, c, s.origin+"/api/state", nil); resp.StatusCode != http.StatusOK {
			t.Errorf("%s/api/state: status %d, want 200", s.origin, resp.StatusCode)
		}
	}
}

func TestWrongTokenInThisPortsCookieIsRejected(t *testing.T) {
	s := start(t, "token")
	cookie := &http.Cookie{Name: fmt.Sprintf("fh_token_%d", s.port), Value: "not-the-token"}

	api := do(t, http.DefaultClient, s.origin+"/api/state", cookie)
	if api.StatusCode != http.StatusUnauthorized {
		t.Errorf("/api/state: status %d, want 401", api.StatusCode)
	}
	if ct := api.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("/api/state: Content-Type %q, want application/json", ct)
	}
	if body := bodyOf(t, api); !strings.Contains(body, `"error"`) {
		t.Errorf("/api/state: body %q, want a JSON error", body)
	}

	pg := do(t, http.DefaultClient, s.origin+"/", cookie)
	if pg.StatusCode != http.StatusUnauthorized {
		t.Errorf("/: status %d, want 401", pg.StatusCode)
	}
	if body := bodyOf(t, pg); !strings.Contains(body, "older launch") {
		t.Errorf("/: body %q, want the unauthorised page", body)
	}
}

func TestCookieForAnotherPortIsRejected(t *testing.T) {
	s := start(t, "token")
	cookie := &http.Cookie{Name: fmt.Sprintf("fh_token_%d", s.port+1), Value: s.token}

	resp := do(t, http.DefaultClient, s.origin+"/api/state", cookie)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
}
