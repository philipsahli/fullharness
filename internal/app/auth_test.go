package app_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestRequestWithoutCookieIsRejected(t *testing.T) {
	f := startFh(t, t.TempDir())

	resp := get(t, noRedirectClient(t), f.origin()+"/api/state")

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
}

func TestLaunchURLExchangesTokenForStrictHttpOnlyCookie(t *testing.T) {
	f := startFh(t, t.TempDir())

	resp := get(t, noRedirectClient(t), f.launchURL().String())

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("redirects to %q, want / (token must leave the address bar)", loc)
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie SameSite = %v, want Strict", c.SameSite)
	}
}

func TestRequestWithCookieIsAccepted(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()

	resp := get(t, c, f.origin()+"/api/state")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
}

func TestRequestWithWrongCookieIsRejected(t *testing.T) {
	f := startFh(t, t.TempDir())
	req, _ := http.NewRequest("GET", f.origin()+"/api/state", nil)
	req.AddCookie(&http.Cookie{Name: "fh_token", Value: "not-the-token"})

	resp, err := noRedirectClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	readBody(t, resp)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
}

func TestLaunchURLWithWrongTokenIsRejected(t *testing.T) {
	f := startFh(t, t.TempDir())

	resp := get(t, noRedirectClient(t), f.origin()+"/auth?token=wrong")

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Error("set a cookie for a wrong token")
	}
}

func TestAppShellIsServedWithCookie(t *testing.T) {
	f := startFh(t, t.TempDir())

	resp := get(t, f.authorisedClient(), f.origin()+"/")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type %q, want text/html", ct)
	}
}

func TestPageWithoutCookieTellsUserToRunFh(t *testing.T) {
	f := startFh(t, t.TempDir())

	resp, err := noRedirectClient(t).Get(f.origin() + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if !strings.Contains(body, "Run <code>fh</code>") {
		t.Errorf("body does not tell the user to run fh: %s", body)
	}
}

func TestCommandsFromAnotherOriginAreRejected(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()

	for _, origin := range []string{"https://evil.example", "null", ""} {
		req, _ := http.NewRequest("POST", f.origin()+"/api/projects/refresh", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		readBody(t, resp)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Origin %q: status %d, want 403", origin, resp.StatusCode)
		}
	}
}

func TestCommandsWithoutCookieAreRejected(t *testing.T) {
	f := startFh(t, t.TempDir())

	status, _ := f.post(noRedirectClient(t), "/api/projects/refresh", nil)

	if status != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", status)
	}
}
