// Package server serves fh's HTTP API, WebSocket event channel and embedded
// UI on 127.0.0.1. Every request must carry the per-launch token: the browser
// opens /auth?token=…, which trades it for a cookie and redirects to /.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// cookieName holds the launch token. Cookies are scoped by host, not port, so
// a cookie left over from an older launch is rejected as a wrong token.
const cookieName = "fh_token"

// Server is one fh HTTP server bound to a listener.
type Server struct {
	token     string
	launch    string          // random ID of this launch, sent in every snapshot
	origins   map[string]bool // Origin values allowed on /ws and on commands
	http      *http.Server
	mux       *http.ServeMux
	ln        net.Listener
	state     func() map[string]any // extra snapshot fields, e.g. "projects"
	onConnect func()                // runs when a tab opens its event channel

	mu    sync.Mutex
	conns map[*websocket.Conn]bool
}

// New returns a Server that requires token on every request and serves the
// UI from ui. state returns the snapshot fields owned by the rest of fh; it
// may be nil.
func New(ln net.Listener, token string, ui fs.FS, state func() map[string]any) *Server {
	port := ln.Addr().(*net.TCPAddr).Port
	s := &Server{
		token:  token,
		launch: rand.Text(),
		ln:     ln,
		origins: map[string]bool{
			fmt.Sprintf("http://127.0.0.1:%d", port): true,
			fmt.Sprintf("http://localhost:%d", port): true,
		},
		conns: map[*websocket.Conn]bool{},
		state: state,
	}
	mux := http.NewServeMux()
	s.mux = mux
	mux.HandleFunc("GET /auth", s.handleAuth)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.Handle("GET /", uiHandler(ui))
	s.http = &http.Server{Handler: s.requireToken(mux)}
	return s
}

// Handle adds a route behind the token check. Call it before Serve.
func (s *Server) Handle(pattern string, h http.HandlerFunc) {
	s.mux.HandleFunc(pattern, h)
}

// Broadcast sends ev to every open event channel. A tab that can't take it
// within a second is dropped; it reconnects and gets a fresh snapshot.
func (s *Server) Broadcast(ev any) {
	s.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := wsjson.Write(ctx, c, ev); err != nil {
			_ = c.CloseNow() // the tab reconnects and gets a snapshot
		}
		cancel()
	}
}

// OnConnect sets a function to run (in its own goroutine) each time a tab
// opens its event channel. Call it before Serve.
func (s *Server) OnConnect(f func()) { s.onConnect = f }

// Tabs returns how many event channels are open.
func (s *Server) Tabs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// Serve blocks until the server is closed.
func (s *Server) Serve() error { return s.http.Serve(s.ln) }

// Close stops the server and drops every open event channel.
func (s *Server) Close() error {
	err := s.http.Close()
	// http.Server.Close does not touch hijacked connections. CloseNow, not
	// Close: Close waits up to 5 s per tab for the close handshake, which
	// would stall Ctrl+C. Tabs treat any close the same way.
	s.mu.Lock()
	for c := range s.conns {
		_ = c.CloseNow() // fails only if the tab already went away
	}
	s.mu.Unlock()
	return err
}

// handleAuth runs after requireToken accepted ?token=; it moves the token into
// a cookie so it doesn't stay in the address bar or browser history.
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// An error here means the client went away; there is no one to tell.
	_ = json.NewEncoder(w).Encode(s.snapshot())
}

// snapshot is the full state a tab needs to render. Later tickets fill in
// Sessions and the Inbox; the lists must stay non-nil so they encode as [].
//
// launch lets a tab notice a restart. The cookie can't: a restarted fh opens a
// new tab whose cookie replaces the old one for every tab on 127.0.0.1, so an
// old tab would otherwise reconnect without noticing.
func (s *Server) snapshot() map[string]any {
	snap := map[string]any{
		"type":     "snapshot",
		"launch":   s.launch,
		"sessions": []any{},
		"inbox":    []any{},
	}
	if s.state != nil {
		for k, v := range s.state() {
			snap[k] = v
		}
	}
	return snap
}

// handleWS opens one tab's event channel. A browser always sends Origin on a
// WebSocket upgrade, and cookies ride along regardless of which page opened
// it, so the Origin check is what stops another web page from connecting.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.origins[r.Header.Get("Origin")] {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	// The Origin was checked above against the exact allowed list.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.CloseNow() // fails only if already closed
	}()

	ctx := conn.CloseRead(r.Context()) // the client sends nothing; commands are HTTP POSTs
	// Snapshot and registration happen under s.mu, which Broadcast also
	// takes: a change is either in the snapshot or sent as an event after
	// it, never sent before it and then overwritten by an older snapshot.
	s.mu.Lock()
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = wsjson.Write(wctx, conn, s.snapshot())
	cancel()
	if err == nil {
		s.conns[conn] = true
	}
	s.mu.Unlock()
	if err != nil {
		return
	}
	if s.onConnect != nil {
		go s.onConnect()
	}
	<-ctx.Done()
}

func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api := strings.HasPrefix(r.URL.Path, "/api/")
		if !s.authorised(r) {
			if api {
				WriteError(w, http.StatusUnauthorized, "not authorised; run fh to open a new tab")
			} else {
				writeUnauthorised(w)
			}
			return
		}
		// SameSite=Strict already keeps the cookie off cross-site requests;
		// the Origin check is the second lock spec #1 asks for on anything
		// that changes state. Browsers always send Origin on a POST.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !s.origins[r.Header.Get("Origin")] {
			WriteError(w, http.StatusForbidden, "forbidden origin")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WriteJSON answers an API request with v as JSON.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // an error means the client went away
}

// WriteError answers an API request with {"error": message}; the UI shows
// message as is.
func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, map[string]string{"error": message})
}

func (s *Server) authorised(r *http.Request) bool {
	if s.validToken(r.URL.Query().Get("token")) {
		return true
	}
	c, err := r.Cookie(cookieName)
	return err == nil && s.validToken(c.Value)
}

func (s *Server) validToken(got string) bool {
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}
