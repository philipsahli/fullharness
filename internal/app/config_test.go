package app_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/philipsahli/fullharness/internal/app"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestPortComesFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(fmt.Sprintf("port = %d\n", port)), 0o600)

	f := startFhWith(t, app.Options{Dir: dir})

	if got := f.launchURL().Port(); got != strconv.Itoa(port) {
		t.Fatalf("listening on port %s, want %d from config.toml", got, port)
	}
}

func TestInvalidConfigFileFailsLaunch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("port = \"not a number\"\n"), 0o600)

	_, err := app.Start(context.Background(), app.Options{Dir: dir, OpenBrowser: func(string) {}})

	if err == nil {
		t.Fatal("started with an invalid config.toml")
	}
}
