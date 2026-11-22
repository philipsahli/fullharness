package app

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/worktree"
)

// registerProjectRoutes adds the Project commands. Commands are HTTP POSTs;
// their results reach every tab as events (spec #1, UI ↔ server).
func registerProjectRoutes(srv *server.Server, reg *project.Registry, scanFolder string) {
	srv.Handle("POST /api/projects", func(w http.ResponseWriter, r *http.Request) {
		path, ok := decodePath(w, r)
		if !ok {
			return
		}
		p, err := reg.Add(path)
		var invalid *worktree.InvalidPathError
		switch {
		case errors.As(err, &invalid):
			server.WriteError(w, http.StatusBadRequest, invalid.Reason)
			return
		case errors.Is(err, project.ErrExists):
			server.WriteError(w, http.StatusConflict, err.Error())
			return
		case err != nil:
			server.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		server.WriteJSON(w, http.StatusCreated, p)
	})

	srv.Handle("POST /api/projects/remove", func(w http.ResponseWriter, r *http.Request) {
		path, ok := decodePath(w, r)
		if !ok {
			return
		}
		switch err := reg.Remove(path); {
		case errors.Is(err, project.ErrNotFound):
			server.WriteError(w, http.StatusNotFound, err.Error())
		case err != nil:
			server.WriteError(w, http.StatusInternalServerError, err.Error())
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})

	srv.Handle("GET /api/projects/suggestions", func(w http.ResponseWriter, r *http.Request) {
		server.WriteJSON(w, http.StatusOK, reg.Suggest(scanFolder))
	})

	srv.Handle("POST /api/projects/refresh", func(w http.ResponseWriter, r *http.Request) {
		reg.Refresh()
		w.WriteHeader(http.StatusNoContent)
	})
}

// decodePath reads {"path": "…"}, answering 400 itself when it's missing.
func decodePath(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		server.WriteError(w, http.StatusBadRequest, `send {"path": "…"}`)
		return "", false
	}
	return req.Path, true
}
