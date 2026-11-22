// Package app wires one fh launch: it picks the port, generates the token,
// starts the server and opens the browser, or, when an fh instance already
// runs for the same folder, opens a tab on that one instead.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/web"
)

// DefaultPort is the port fh listens on unless configured otherwise.
const DefaultPort = 4815

// Options configure one launch.
type Options struct {
	// Dir holds config.toml and the runtime state files.
	Dir string
	// Port overrides config.toml's port. nil means config.toml, else
	// DefaultPort; 0 picks a free port.
	Port *int
	// OpenBrowser opens a URL in the user's browser.
	OpenBrowser func(url string)
}

// Instance is the result of a launch. If another fh was already running, the
// Instance only opened a tab on it: Wait returns at once and Close does nothing.
type Instance struct {
	dir   string
	token string
	srv   *server.Server // nil when the launch handed off to a running instance

	done      chan struct{}
	serveErr  error
	closeOnce sync.Once
}

// Start launches fh and opens the browser on an authorised URL.
func Start(ctx context.Context, opts Options) (*Instance, error) {
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, err
	}
	cfg, err := loadConfig(opts.Dir)
	if err != nil {
		return nil, err
	}
	port := DefaultPort
	switch {
	case opts.Port != nil:
		port = *opts.Port
	case cfg.Port != nil:
		port = *cfg.Port
	}

	if li, ok := readLaunchFile(opts.Dir); ok && running(li) {
		return handOff(li, opts), nil
	}

	token, err := newToken()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		// Another fh launched at the same moment may hold the port without
		// having written its launch file yet.
		if li, ok := waitForRunning(opts.Dir, handOffWait); ok {
			return handOff(li, opts), nil
		}
		return nil, fmt.Errorf("listen on 127.0.0.1:%d (set another port with --port or in config.toml): %w", port, err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	if err := writeLaunchFile(opts.Dir, launchInfo{PID: os.Getpid(), Port: port, Token: token}); err != nil {
		_ = ln.Close() // the launch file error is the one worth reporting
		return nil, fmt.Errorf("write launch file: %w", err)
	}

	inst := &Instance{dir: opts.Dir, token: token, done: make(chan struct{})}
	inst.srv = server.New(ln, token, web.Dist())
	go func() {
		inst.serveErr = inst.srv.Serve()
		close(inst.done)
	}()
	opts.OpenBrowser(authURL(port, token))
	return inst, nil
}

// handOffWait is how long a launch that lost the port waits for the winner's
// launch file. The winner writes it right after binding, so this is generous.
const handOffWait = 2 * time.Second

// handOff opens a tab on the running instance li instead of starting a server.
func handOff(li launchInfo, opts Options) *Instance {
	opts.OpenBrowser(authURL(li.Port, li.Token))
	done := make(chan struct{})
	close(done)
	return &Instance{done: done}
}

func waitForRunning(dir string, d time.Duration) (launchInfo, bool) {
	deadline := time.Now().Add(d)
	for {
		if li, ok := readLaunchFile(dir); ok && running(li) {
			return li, true
		}
		if time.Now().After(deadline) {
			return launchInfo{}, false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Wait blocks until the server stops.
func (i *Instance) Wait() error {
	<-i.done
	if i.serveErr == nil || errors.Is(i.serveErr, http.ErrServerClosed) {
		return nil
	}
	return i.serveErr
}

// Close stops the server and removes the launch file.
func (i *Instance) Close() error {
	if i.srv == nil {
		return nil
	}
	var err error
	i.closeOnce.Do(func() {
		err = i.srv.Close()
		if rmErr := removeLaunchFile(i.dir, i.token); err == nil {
			err = rmErr
		}
	})
	return err
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
