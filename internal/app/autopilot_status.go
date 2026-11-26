package app

import (
	"context"
	"reflect"
	"sync"
	"time"

	"github.com/philipsahli/fullharness/internal/autopilot"
	"github.com/philipsahli/fullharness/internal/project"
	"github.com/philipsahli/fullharness/internal/session"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// autopilotStatuses keeps every Feature's Autopilot status (ADR-0013) for
// the snapshot, and sends an "autopilot" event with all of them when one
// changes. It works them out again in its own goroutine whenever it is
// kicked: the history, a Session or a Project changed. The snapshot reads
// the last result, so a tab connecting never waits on a gh call.
type autopilotStatuses struct {
	kick chan struct{}

	mu  sync.Mutex
	all []autopilot.Status
}

func newAutopilotStatuses() *autopilotStatuses {
	return &autopilotStatuses{kick: make(chan struct{}, 1), all: []autopilot.Status{}}
}

// Kick asks for the statuses to be worked out again. It never blocks:
// kicks that arrive while one waits are one.
func (a *autopilotStatuses) Kick() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

// List is the last statuses worked out; never nil.
func (a *autopilotStatuses) List() []autopilot.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.all
}

// run works the statuses out on each kick, and each interval for tracker
// changes nobody kicks for, until ctx ends. It broadcasts them only when
// they changed.
func (a *autopilotStatuses) run(ctx context.Context, interval time.Duration, compute func(prev []autopilot.Status) []autopilot.Status, broadcast func(any)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.kick:
		case <-t.C:
		}
		next := compute(a.List())
		a.mu.Lock()
		same := reflect.DeepEqual(a.all, next)
		if !same {
			a.all = next
		}
		a.mu.Unlock()
		if !same {
			broadcast(map[string]any{"type": "autopilot", "statuses": next})
		}
	}
}

// computeAutopilotStatuses works out the statuses of every Project. Only a
// Project with Autopilot on has its board read; in the others every
// Feature is off but one whose turn still runs, which needs no board. A
// board that can't be read (gh failed) keeps the Project's prev statuses,
// rather than turning every badge off until gh answers again.
func computeAutopilotStatuses(reg *project.Registry, b *boards, sessions *session.Manager, pilot *autopilot.Runner, prev []autopilot.Status) []autopilot.Status {
	out := []autopilot.Status{}
	all := sessions.List()
	for _, p := range reg.List() {
		if p.Missing {
			continue
		}
		in := autopilot.StatusInput{Project: p.Path, On: p.Autopilot, Sessions: all}
		for _, h := range pilot.History(p.Path) {
			in.History = append(in.History, h.Events...)
		}
		if p.Autopilot {
			board, ok := b.read(p.Path, false)
			if !ok {
				for _, s := range prev {
					if s.Project == p.Path {
						out = append(out, s)
					}
				}
				continue
			}
			tracker.Derive(board.Features, sessions.Links(p.Path))
			in.Features = board.Features
		}
		out = append(out, autopilot.Statuses(in)...)
	}
	return out
}
