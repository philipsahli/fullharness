// Package server serves fh's HTTP API, WebSocket event channel and embedded
// UI on 127.0.0.1. Every request must carry the per-launch token: the browser
// opens /auth?token=…, which trades it for a cookie and redirects to /.
package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sync"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// cookieName holds the launch token. Cookies are scoped by host, not port, so
// a cookie left over from an older launch is rejected as a wrong token.
const cookieName = "fh_token"

// Server is one fh HTTP server bound to a listener.
type Server struct {
	token   string
	launch  string          // random ID of this launch, sent in every snapshot
	origins map[string]bool // Origin values allowed to open the event channel
	http    *http.Server
	ln      net.Listener

	mu    sync.Mutex
	conns map[*websocket.Conn]bool
}

// New returns a Server that requires token on every request and serves the
// UI from ui.
func New(ln net.Listener, token string, ui fs.FS) *Server {
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
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth", s.handleAuth)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.Handle("GET /", uiHandler(ui))
	s.http = &http.Server{Handler: s.requireToken(mux)}
	return s
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
	return map[string]any{
		"type":     "snapshot",
		"launch":   s.launch,
		"sessions": []any{},
		"inbox":    []any{},
	}
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
	s.mu.Lock()
	s.conns[conn] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.CloseNow() // fails only if already closed
	}()

	ctx := conn.CloseRead(r.Context()) // the client sends nothing; commands are HTTP POSTs
	if err := wsjson.Write(ctx, conn, s.snapshot()); err != nil {
		return
	}
	<-ctx.Done()
}

func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorised(r) {
			writeUnauthorised(w)
			return
		}
		next.ServeHTTP(w, r)
	})
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
