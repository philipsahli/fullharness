package app

import (
	"net/http"

	"github.com/philipsahli/fullharness/internal/classify"
	"github.com/philipsahli/fullharness/internal/server"
)

// registerClassifierRoutes shows how the turn-end classifier does (ADR-0010):
// its Provider, how often the user overrode it, its latency and its cost.
func registerClassifierRoutes(srv *server.Server, c *classify.Classifier) {
	srv.Handle("GET /api/classifier", func(w http.ResponseWriter, r *http.Request) {
		stats, err := c.Stats()
		if err != nil {
			server.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		server.WriteJSON(w, http.StatusOK, struct {
			Provider string `json:"provider"`
			classify.Stats
		}{c.Provider(), stats})
	})
}
