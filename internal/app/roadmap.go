package app

import (
	"net/http"
	"sync"

	"github.com/philipsahli/fullharness/internal/gitremote"
	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/server"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// roadmapView is the Roadmap tab (#197): every open Feature of every
// Project with a tracker, each in its Roadmap group (ADR 0005), in one
// response so the tab makes a single request.
type roadmapView struct {
	// Projects are the filter chips: the Projects with a tracker, in the
	// order they were added. Note says why one has no Features, such as
	// gh being logged out.
	Projects []roadmapProject `json:"projects"`
	Features []roadmapFeature `json:"features"`
}

type roadmapProject struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Tracker string `json:"tracker"`
	Note    string `json:"note,omitempty"`
}

type roadmapFeature struct {
	Project string `json:"project"` // the Project's path
	ID      string `json:"id"`
	Title   string `json:"title"`
	Ref     string `json:"ref"`
	URL     string `json:"url,omitempty"`
	Stage   string `json:"stage"`
	// Roadmap is now, next or later; a Spec with no group, or one fh
	// doesn't know, is in Later.
	Roadmap string `json:"roadmap"`
	// NoGroup: the Spec names no group, so its card says why it is in Later.
	NoGroup bool `json:"noGroup,omitempty"`
}

// registerRoadmapRoutes adds GET /api/roadmap. It reads each Project's
// board the way GET /api/board does, from the same gh cache, and leaves
// out shipped Features: a closed Spec is not on the Roadmap (#197).
func registerRoadmapRoutes(srv *server.Server, projects *project.Registry, b *boards, sessions *session.Manager) {
	srv.Handle("GET /api/roadmap", func(w http.ResponseWriter, r *http.Request) {
		list := projects.List()
		// Each Project's gh read can take a second; read them side by side.
		read := make([]tracker.Board, len(list))
		var wg sync.WaitGroup
		for i, p := range list {
			wg.Add(1)
			go func() {
				defer wg.Done()
				read[i], _ = b.read(p.Path, false) // a failed read keeps its Note
			}()
		}
		wg.Wait()

		v := roadmapView{Projects: []roadmapProject{}, Features: []roadmapFeature{}}
		for i, p := range list {
			board := read[i]
			if board.Tracker != tracker.KindGitHub && board.Tracker != tracker.KindLocal {
				continue
			}
			rp := roadmapProject{Path: p.Path, Name: p.Name, Tracker: board.Tracker}
			if len(board.Features) == 0 {
				rp.Note = board.Note
			}
			v.Projects = append(v.Projects, rp)
			tracker.Derive(board.Features, sessions.Links(p.Path))
			if g, ok := gitremote.ParseGitHub(p.URL); ok && board.Tracker == tracker.KindGitHub {
				tracker.LinkIssues(&board, g)
			}
			for _, f := range board.Features {
				if f.Closed {
					continue
				}
				v.Features = append(v.Features, roadmapFeature{
					Project: p.Path, ID: f.ID, Title: f.Title, Ref: f.Ref, URL: f.URL,
					Stage: f.Stage, Roadmap: roadmapGroup(f.Roadmap), NoGroup: f.Roadmap == "",
				})
			}
		}
		server.WriteJSON(w, http.StatusOK, v)
	})
}

// roadmapGroup is the Roadmap group a Spec's label or line names; anything
// else, empty included, is Later.
func roadmapGroup(g string) string {
	switch g {
	case tracker.RoadmapNow, "next":
		return g
	}
	return "later"
}
