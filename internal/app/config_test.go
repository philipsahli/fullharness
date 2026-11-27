package app_test

import (
	"context"
	"fmt"
	"net"
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
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestPortComesFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	writeConfig(t, dir, fmt.Sprintf("port = %d\n", port))

	f := startFhWith(t, app.Options{Dir: dir})

	if got := f.launchURL().Port(); got != strconv.Itoa(port) {
		t.Fatalf("listening on port %s, want %d from config.toml", got, port)
	}
}

func TestInvalidConfigFileFailsLaunch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeConfig(t, dir, "port = \"not a number\"\n")

	_, err := app.Start(context.Background(), app.Options{Dir: dir, OpenBrowser: func(string) {}})

	if err == nil {
		t.Fatal("started with an invalid config.toml")
	}
}
