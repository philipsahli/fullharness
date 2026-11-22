package app_test

import (
	"context"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/philipsahli/fullharness/internal/app"
)

func TestSecondLaunchOpensAuthorisedTabOnRunningInstance(t *testing.T) {
	dir := t.TempDir()
	first := startFh(t, dir)

	second := startFh(t, dir)

	if got, want := second.launchURL().Host, first.launchURL().Host; got != want {
		t.Fatalf("second launch opened %s, want the running instance at %s", got, want)
	}
	c := second.authorisedClient()
	if resp := get(t, c, first.origin()+"/api/state"); resp.StatusCode != http.StatusOK {
		t.Fatalf("tab from second launch: status %d, want 200", resp.StatusCode)
	}
}

func TestSecondLaunchDoesNotOutliveTheFirst(t *testing.T) {
	dir := t.TempDir()
	first := startFh(t, dir)
	second := startFh(t, dir)
	c := second.authorisedClient()

	first.stop()
	// The second launch only opened a tab, so nothing serves fh now.
	if _, err := c.Get(first.origin() + "/api/state"); err == nil {
		t.Fatal("something still serves fh after the running instance closed")
	}
}

func TestLaunchFilesAreReadableOnlyByTheUser(t *testing.T) {
	dir := t.TempDir()
	startFh(t, dir)

	n := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		info, _ := d.Info()
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s has mode %o; group and others must have no access", path, perm)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("fh wrote no launch file")
	}
}

func TestLaunchAfterStaleInstanceStartsFreshServer(t *testing.T) {
	dir := t.TempDir()
	first := startFh(t, dir)
	first.stop()

	second := startFh(t, dir)

	if resp := get(t, second.authorisedClient(), second.origin()+"/api/state"); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
}

func TestCookieFromBeforeRestartIsRejected(t *testing.T) {
	dir := t.TempDir()
	first := startFh(t, dir)
	oldTab := first.authorisedClient()
	port, _ := strconv.Atoi(first.launchURL().Port())
	first.stop()

	restarted := startFhOnPort(t, dir, port)

	if restarted.origin() != first.origin() {
		t.Fatalf("restarted on %s, want %s", restarted.origin(), first.origin())
	}
	if resp := get(t, oldTab, restarted.origin()+"/api/state"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old tab: status %d, want 401", resp.StatusCode)
	}
	if tok := first.launchURL().Query().Get("token"); tok == restarted.launchURL().Query().Get("token") {
		t.Fatal("restart reused the token")
	}
}

func TestSimultaneousLaunchesEndWithOneServer(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)

	type result struct {
		inst   *app.Instance
		opened string
		err    error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			var opened string
			inst, err := app.Start(context.Background(), app.Options{
				Dir: dir, Port: &port,
				OpenBrowser: func(u string) { opened = u },
			})
			results <- result{inst, opened, err}
		}()
	}
	var urls []string
	for range 2 {
		r := <-results
		if r.err != nil {
			t.Fatalf("a launch failed: %v", r.err)
		}
		t.Cleanup(func() { stopFh(t, r.inst) })
		urls = append(urls, r.opened)
	}

	if urls[0] != urls[1] {
		t.Fatalf("launches opened different URLs %q and %q, want both on the one server", urls[0], urls[1])
	}
}
