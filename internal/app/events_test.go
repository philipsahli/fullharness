package app_test

import (
	"net/http"
	"strconv"
	"testing"
)

func TestEventChannelSendsEmptySnapshotOnConnect(t *testing.T) {
	f := startFh(t, t.TempDir())

	conn, status, err := f.dialWS(f.authorisedClient(), f.origin())
	if err != nil {
		t.Fatalf("dial: status %d: %v", status, err)
	}
	ev := readEvent(t, conn)

	if ev["type"] != "snapshot" {
		t.Fatalf("first event type = %v, want snapshot", ev["type"])
	}
	for _, key := range []string{"sessions", "inbox"} {
		list, ok := ev[key].([]any)
		if !ok || len(list) != 0 {
			t.Errorf("snapshot %s = %v, want empty list", key, ev[key])
		}
	}
}

func TestEventChannelWithoutCookieIsRejected(t *testing.T) {
	f := startFh(t, t.TempDir())

	_, status, err := f.dialWS(noRedirectClient(t), f.origin())

	if err == nil || status != http.StatusUnauthorized {
		t.Fatalf("dial: status %d, err %v; want 401", status, err)
	}
}

func TestEventChannelFromOtherOriginIsRejected(t *testing.T) {
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()

	for _, origin := range []string{"https://evil.example", "http://127.0.0.1:1", "null", ""} {
		_, status, err := f.dialWS(c, origin)
		if err == nil || status != http.StatusForbidden {
			t.Errorf("origin %q: status %d, err %v; want 403", origin, status, err)
		}
	}
}

// A tab can't tell a restart from a dropped connection by the cookie alone:
// the restarted fh opens a new tab whose cookie replaces the old one for every
// tab on 127.0.0.1. The snapshot names the launch so the tab can compare.
func TestSnapshotNamesTheLaunchAndChangesOnRestart(t *testing.T) {
	dir := t.TempDir()
	first := startFh(t, dir)
	conn, _, err := first.dialWS(first.authorisedClient(), first.origin())
	if err != nil {
		t.Fatal(err)
	}
	before := readEvent(t, conn)["launch"]
	port, _ := strconv.Atoi(first.launchURL().Port())
	first.stop()

	restarted := startFhOnPort(t, dir, port)
	conn, _, err = restarted.dialWS(restarted.authorisedClient(), restarted.origin())
	if err != nil {
		t.Fatal(err)
	}
	after := readEvent(t, conn)["launch"]

	if before == nil || before == "" {
		t.Fatalf("snapshot launch = %v, want an ID", before)
	}
	if before == after {
		t.Fatalf("launch ID %v did not change across a restart", before)
	}
}
