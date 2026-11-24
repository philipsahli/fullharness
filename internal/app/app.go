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
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/philipsahli/fullharness/internal/agent"
	"github.com/philipsahli/fullharness/internal/classify"
	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/state"
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
	// Dev keeps the token of the previous launch in Dir across restarts, so
	// an open tab stays authorised and only reloads (make dev). The token
	// still lives only in the 0600 launch file. cmd/fh allows it only with an
	// explicit --dir, never for the real config folder.
	Dev bool
	// Stderr receives fh's log lines next to fh.log; nil discards them.
	Stderr io.Writer
	// Verbose logs at debug level.
	Verbose bool
	// AgentStartTimeout bounds an Agent's start-up (initialize and
	// session/new); zero means DefaultAgentStartTimeout.
	AgentStartTimeout time.Duration
	// RefreshInterval is how often Worktree state is re-read while a tab is
	// connected; zero means DefaultRefreshInterval.
	RefreshInterval time.Duration
	// BeforeWorktreeRemove runs right before git removes a Worktree, while
	// the removal is reserved; tests use it to hold a removal open. nil
	// means nothing runs.
	BeforeWorktreeRemove func()
}

// DefaultAgentStartTimeout leaves room for npx to download the Claude Code
// adapter on its first start.
const DefaultAgentStartTimeout = 2 * time.Minute

// DefaultRefreshInterval keeps Worktree state about as fresh as a glance
// needs, at a few git calls per Worktree per tick (#3).
const DefaultRefreshInterval = 10 * time.Second

// Instance is the result of a launch. If another fh was already running, the
// Instance only opened a tab on it: Wait returns at once and Close does nothing.
type Instance struct {
	dir   string
	token string
	dev   bool           // keep the launch file on Close, for the next launch's token
	srv   *server.Server // nil when the launch handed off to a running instance

	sessions *session.Manager
	logFile  io.Closer

	done      chan struct{}
	stop      chan struct{} // ends the refresh loop
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

	token, err := launchToken(opts)
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

	inst := &Instance{dir: opts.Dir, token: token, dev: opts.Dev, done: make(chan struct{})}
	fail := func(err error) (*Instance, error) {
		_ = ln.Close()                        // the original error is the one worth reporting
		_ = removeLaunchFile(opts.Dir, token) // as above
		return nil, err
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	log, logFile, err := newLogger(opts.Dir, stderr, opts.Verbose)
	if err != nil {
		return fail(fmt.Errorf("open fh.log: %w", err))
	}
	inst.logFile = logFile
	logDir := filepath.Join(opts.Dir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fail(err)
	}
	st, err := state.Open(opts.Dir)
	if err != nil {
		return fail(err)
	}
	var srv *server.Server
	// sessions doesn't exist yet when Open reads the Projects; a Relabel
	// once sessions are restored fills in the Session rule.
	var sessions *session.Manager
	linked := func(path string) bool { return sessions != nil && sessions.Linked(path) }
	reg, err := project.Open(opts.Dir, st.CreatedWorktree, linked, func(ps []project.Project) {
		srv.Broadcast(map[string]any{"type": "projects", "projects": ps})
	})
	if err != nil {
		return fail(err)
	}
	var notes *notifier // set once srv exists; nothing enters Waiting before that
	startTimeout := opts.AgentStartTimeout
	if startTimeout == 0 {
		startTimeout = DefaultAgentStartTimeout
	}
	sessions = session.New(session.Config{
		StartTimeout: startTimeout,
		LogDir:       logDir,
		Broadcast:    func(ev any) { srv.Broadcast(ev) },
		// A turn usually changes files; show it without waiting for a tick.
		TurnEnded: func(session.TurnEnd) { reg.Refresh() },
		Persist: func(saved []session.Saved) {
			if err := st.SetSessions(saved); err != nil {
				log.Info("save sessions failed", "err", err)
			}
		},
		Commands: func(agentID string, names []string) {
			if err := st.SetCommands(agentID, names); err != nil {
				log.Info("save agent commands failed", "agent", agentID, "err", err)
			}
		},
		EnteredWaiting: func(sum session.Summary, what string, viewed bool) {
			notes.enteredWaiting(sum, what, viewed)
		},
		Changed: reg.Relabel,
		Log:     log,
	})
	inst.sessions = sessions
	agents := agent.Resolve(cfg.Agents)
	var saved []session.Saved
	if err := st.Sessions(&saved); err != nil {
		return fail(fmt.Errorf("read sessions from state.json: %w", err))
	}
	sessions.Restore(saved, func(id string) (agent.Definition, bool) {
		i := slices.IndexFunc(agents, func(a agent.Definition) bool { return a.ID == id })
		if i < 0 {
			return agent.Definition{}, false
		}
		return agents[i], true
	})
	sessions.SweepLogs()
	srv = server.New(ln, token, web.Dist(), func() map[string]any {
		// dev tells the tab to reload on a restart instead of stopping.
		return map[string]any{"projects": reg.List(), "sessions": sessions.List(), "inbox": sessions.Inbox(), "dev": opts.Dev}
	})
	reg.Relabel() // broadcasts, so only once srv exists
	notes = newNotifier(srv, log)
	registerProjectRoutes(srv, reg, cfg.ScanFolder)
	boards := &boards{cache: map[string]*ghEntry{}}
	registerBoardRoutes(srv, reg, boards, sessions)
	registerWorktreeRoutes(srv, worktreeDeps{projects: reg, sessions: sessions, state: st, log: log, beforeRemove: opts.BeforeWorktreeRemove})
	classifier, err := classify.New(cfg.Classifier.provider(), opts.Dir, log)
	if err != nil {
		return fail(err)
	}
	registerClassifierRoutes(srv, classifier)
	registerSessionRoutes(srv, sessionDeps{
		projects: reg,
		sessions: sessions,
		state:    st,
		agents:   agents,
		specs:    boards.specs,
		log:      log,
	})
	log.Info("fh started", "port", port, "dir", opts.Dir, "dev", opts.Dev)
	// Nothing refreshes while no tab is open, so a tab that connects after a
	// quiet spell gets the last state read, then the current one at once.
	srv.OnConnect(reg.Refresh)
	inst.srv = srv
	inst.stop = make(chan struct{})
	interval := opts.RefreshInterval
	if interval == 0 {
		interval = DefaultRefreshInterval
	}
	go refreshLoop(reg, srv, interval, inst.stop)
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

// refreshLoop re-reads Worktree state every interval while a tab is
// connected; with no tab open nobody would see the result. A tab that
// connects gets the last state read, and the next tick updates it.
func refreshLoop(reg *project.Registry, srv *server.Server, interval time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if srv.Tabs() > 0 {
				reg.Refresh()
			}
		}
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
		close(i.stop)
		// Agent processes end with fh (ADR 0002).
		i.sessions.StopAll()
		err = i.srv.Close()
		defer func() { _ = i.logFile.Close() }() // nothing left to log the error to
		if i.dev {
			return // the next dev launch reuses the token from this file
		}
		if rmErr := removeLaunchFile(i.dir, i.token); err == nil {
			err = rmErr
		}
	})
	return err
}

// launchToken returns the token for this launch: new each time, except in
// Dev mode, where the previous (no longer running) launch's token is kept.
func launchToken(opts Options) (string, error) {
	if opts.Dev {
		if li, ok := readLaunchFile(opts.Dir); ok {
			return li.Token, nil
		}
	}
	return newToken()
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
