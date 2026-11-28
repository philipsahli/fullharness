// Package switcher works out what the Switcher (⌘K, GLOSSARY.md) lists for
// a query besides Sessions (Spec #201). A number finds the Spec, Ticket,
// PR or issue with that number in every Project (#254), or in one repo for
// owner/repo#N or a pasted link (#256), closed and merged ones too (#255);
// words find the current Project's open Tickets by title (#195). It reads
// the board data fh already has. Only a number the board doesn't have is
// looked up on GitHub, and only through a Source's Lookup.
package switcher

import (
	"cmp"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/philipsahli/fullharness/internal/gitremote"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// Kind is what a result is; the Switcher shows it as a grey tag.
type Kind string

const (
	KindSpec   Kind = "spec"
	KindTicket Kind = "ticket"
	KindPR     Kind = "pr"
	KindIssue  Kind = "issue"
	// An Idea is a grilling Session in fh and has no number, so a number
	// never finds one; words find it with the Sessions.
)

// Place is where picking a result goes.
type Place string

const (
	PlaceFeature Place = "feature" // the Feature page on the board
	PlaceTicket  Place = "ticket"  // the Feature page, with the Ticket's row
	PlaceTriage  Place = "triage"  // the issue's row under Needs triage
	PlaceGitHub  Place = "github"  // fh doesn't show it: its page on GitHub
)

// State is how a closed result is closed; the Switcher dims it. An open
// result has none.
type State string

const (
	StateClosed  State = "closed"  // a closed Ticket, issue or PR
	StateMerged  State = "merged"  // a merged PR
	StateShipped State = "shipped" // a closed Spec, or a Ticket of one
)

// Opens is where a result goes and what the board needs to show it there.
type Opens struct {
	Place   Place  `json:"place"`
	Feature string `json:"feature,omitempty"`
	Ticket  string `json:"ticket,omitempty"`
	Ref     string `json:"ref,omitempty"` // an Untriaged issue's Ref
	URL     string `json:"url,omitempty"`
}

// Result is one thing the Switcher lists.
type Result struct {
	Kind        Kind   `json:"kind"`
	Project     string `json:"project"` // the Project's path
	ProjectName string `json:"projectName"`
	Number      string `json:"number"`
	Title       string `json:"title"`
	// Detail says where the result belongs: a Ticket's Feature, or the
	// Ticket a PR belongs to.
	Detail string `json:"detail,omitempty"`
	State  State  `json:"state,omitempty"`
	Opens  Opens  `json:"opens"`
}

// Source is one Project's data: its board and its PRs, as fh has them
// cached. A board that couldn't be read is an empty board.
type Source struct {
	Project string
	Name    string
	// Repo is the Project's GitHub repo, "owner/repo", from its origin;
	// empty without one. owner/repo#N and pasted links find the Project by
	// it.
	Repo  string
	Board tracker.Board
	PRs   []tracker.PR
	// Lookup, when set, asks GitHub for a number the board doesn't have:
	// a closed issue, or a PR older than the ones fh reads.
	Lookup Lookup
}

// Lookup asks a Project's GitHub repository for the issue or PR numbered
// n. ok is false when it has none.
type Lookup func(n string) (f Found, ok bool, err error)

// Found is what GitHub answers for one number.
type Found struct {
	PR    bool
	Title string
	State State // empty when open
	URL   string
}

// Resolve lists what query finds, current being the Project the board
// shows.
//
// A number, with or without "#", finds the Specs, Tickets, PRs and
// Untriaged issues with that number in every Project: current's first,
// then the others by name. In each Project the open items come first, then
// the closed and merged ones, each in board order. Leading zeros don't
// count, so "1" finds local Ticket "01". A Project whose board has nothing
// with that number asks its Lookup, side by side with the others; a
// lookup that fails adds nothing (#255).
//
// The number can also name its repo, as owner/repo#N or a pasted
// github.com link to an issue or a PR: then only that Project's items
// count, only GitHub's numbers, so local Ticket "01" isn't o/shop#1, and
// only that Project's Lookup runs. A repo that isn't a Project gives one
// result that opens on GitHub, and no lookup. A type word before any of
// these ("PR 21", "spec o/web#10") keeps that type only (#256), looked-up
// items included; a board item of another type still counts as found, so
// "PR 10" for Spec #10 asks GitHub nothing.
//
// Any other query finds current's open Tickets whose title holds every
// word of it, in any order and any case. An empty query finds nothing.
func Resolve(query, current string, sources []Source) []Result {
	query = strings.TrimSpace(query)
	out := []Result{}
	if query == "" {
		return out
	}
	q, isRef := parseRef(query)
	if !isRef {
		words := strings.Fields(strings.ToLower(query))
		for _, s := range sources {
			if s.Project == current {
				out = append(out, ticketsBy(s, func(t tracker.Ticket) bool {
					return hasAll(strings.ToLower(t.Title), words)
				})...)
			}
		}
		return out
	}

	ordered := slices.Clone(sources)
	slices.SortStableFunc(ordered, func(a, b Source) int {
		if (a.Project == current) != (b.Project == current) {
			if a.Project == current {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
	if q.repo != "" {
		i := slices.IndexFunc(ordered, func(s Source) bool { return strings.EqualFold(s.Repo, q.repo) })
		if i < 0 {
			return []Result{q.onGitHub()}
		}
		ordered = ordered[i : i+1]
	}
	found := make([][]Result, len(ordered))
	var wg sync.WaitGroup
	for i, s := range ordered {
		found[i] = byNumber(s, q.number, q.repo != "")
		// The type word is applied after: on GitHub one number is one item,
		// so a board hit of another type leaves nothing to look up.
		if len(found[i]) == 0 && s.Lookup != nil {
			wg.Go(func() { found[i] = lookedUp(s, q.number) })
		}
	}
	wg.Wait()
	for _, rs := range found {
		for _, r := range rs {
			if q.kind == "" || r.Kind == q.kind {
				out = append(out, r)
			}
		}
	}
	return out
}

// ref is a query that names an item by number.
type ref struct {
	number string // without "#" and leading zeros
	kind   Kind   // the type word's, or "" for any
	repo   string // "owner/repo" it names, or "" for every Project
	// link is a pasted link's issue or PR page, without its tab, and
	// linkKind which of the two its path says.
	link     string
	linkKind Kind
}

// typeWords are the words that keep one type of result, in lower case.
var typeWords = map[string]Kind{"spec": KindSpec, "ticket": KindTicket, "pr": KindPR, "issue": KindIssue}

// parseRef reads "42", "#42", "owner/repo#42" or a github.com link to issue
// or PR 42, each with an optional type word before it. ok is false for
// anything else, words included.
func parseRef(query string) (ref, bool) {
	if f := strings.Fields(query); len(f) == 2 {
		k, isType := typeWords[strings.ToLower(f[0])]
		q, ok := parseRef(f[1])
		q.kind = k
		return q, isType && ok
	}
	if n, isNumber := asNumber(query); isNumber {
		return ref{number: n}, true
	}
	if strings.Contains(query, "://") {
		return parseLink(query)
	}
	repo, n, found := strings.Cut(query, "#")
	g, isRepo := gitremote.ParseGitHub("https://github.com/" + repo)
	n, isNumber := asNumber(n)
	if !found || !isRepo || !isNumber || strings.Count(repo, "/") != 1 {
		return ref{}, false
	}
	return ref{number: n, repo: g.NameWithOwner()}, true
}

// parseLink reads a github.com link to an issue or a PR, with or without a
// tab ("/files") or a comment ("#issuecomment-1") after the number.
func parseLink(link string) (ref, bool) {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") ||
		strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") != "github.com" {
		return ref{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || (parts[2] != "issues" && parts[2] != "pull") || strings.HasPrefix(parts[3], "#") {
		return ref{}, false
	}
	g, isRepo := gitremote.ParseGitHub("https://github.com/" + parts[0] + "/" + parts[1])
	n, isNumber := asNumber(parts[3])
	if !isRepo || !isNumber {
		return ref{}, false
	}
	q := ref{number: n, repo: g.NameWithOwner(), link: g.URL() + "/" + parts[2] + "/" + n, linkKind: KindIssue}
	if parts[2] == "pull" {
		q.linkKind = KindPR
	}
	return q, true
}

// onGitHub is the one result for q when its repo isn't a Project: its
// page on GitHub. fh has no data on the repo, so owner/repo#N is an Issue
// unless a type word or a /pull/ link says PR; GitHub redirects
// /issues/N to the PR when N is one.
func (q ref) onGitHub() Result {
	return Result{
		Kind: cmp.Or(q.kind, q.linkKind, KindIssue), ProjectName: q.repo, Number: q.number, Title: q.repo,
		Detail: "not one of your Projects",
		Opens:  Opens{Place: PlaceGitHub, URL: cmp.Or(q.link, "https://github.com/"+q.repo+"/issues/"+q.number)},
	}
}

// lookedUp is what s's Lookup finds numbered n. It opens on GitHub: fh
// shows only what its board has.
func lookedUp(s Source, n string) []Result {
	f, ok, err := s.Lookup(n)
	if err != nil || !ok {
		return []Result{}
	}
	kind := KindIssue
	if f.PR {
		kind = KindPR
	}
	return []Result{{
		Kind: kind, Project: s.Project, ProjectName: s.Name, Number: n, Title: f.Title, State: f.State,
		Opens: Opens{Place: PlaceGitHub, URL: f.URL},
	}}
}

// Cache keeps Lookup answers, "none" included, for a while, so a second
// query for a number asks GitHub nothing. A failed lookup isn't kept.
type Cache struct {
	ttl time.Duration
	mu  sync.Mutex
	m   map[[2]string]cached
}

type cached struct {
	f    Found
	ok   bool
	when time.Time
}

// NewCache keeps each answer for ttl.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{ttl: ttl, m: map[[2]string]cached{}}
}

// Wrap is l, project's Lookup, answering from c while an answer is fresh.
func (c *Cache) Wrap(project string, l Lookup) Lookup {
	return func(n string) (Found, bool, error) {
		key := [2]string{project, n}
		c.mu.Lock()
		e, hit := c.m[key]
		c.mu.Unlock()
		if hit && time.Since(e.when) < c.ttl {
			return e.f, e.ok, nil
		}
		f, ok, err := l(n)
		if err != nil {
			return f, ok, err
		}
		c.mu.Lock()
		// Drop what has gone stale, so the map holds a few minutes of
		// queries, not every number ever typed.
		for k, v := range c.m {
			if time.Since(v.when) >= c.ttl {
				delete(c.m, k)
			}
		}
		c.m[key] = cached{f: f, ok: ok, when: time.Now()}
		c.mu.Unlock()
		return f, ok, nil
	}
}

// byNumber is s's items numbered n: the open ones, then the closed and
// merged ones, each part in board order (Specs, then Tickets, then PRs,
// then Untriaged issues). With onlyGitHub, a local board's Tickets are
// left out: their numbers aren't GitHub's.
func byNumber(s Source, n string, onlyGitHub bool) []Result {
	is := func(number string) bool {
		m, ok := asNumber(number)
		return ok && m == n
	}
	out := []Result{}
	if s.Board.Tracker == tracker.KindGitHub {
		// Only a GitHub Spec has a number; a local one is a folder.
		for _, f := range s.Board.Features {
			if is(f.ID) {
				r := Result{
					Kind: KindSpec, Project: s.Project, ProjectName: s.Name, Number: f.ID, Title: f.Title,
					Opens: Opens{Place: PlaceFeature, Feature: f.ID},
				}
				if f.Closed {
					r.State = StateShipped
				}
				out = append(out, r)
			}
		}
	}
	if !onlyGitHub || s.Board.Tracker == tracker.KindGitHub {
		out = append(out, tickets(s, func(t tracker.Ticket) bool { return is(t.Number) })...)
	}
	for _, p := range s.PRs {
		if !is(strconv.Itoa(p.Number)) {
			continue
		}
		r := Result{
			Kind: KindPR, Project: s.Project, ProjectName: s.Name, Number: strconv.Itoa(p.Number), Title: p.Title,
			Opens: Opens{Place: PlaceGitHub, URL: p.URL},
		}
		switch p.Record().State {
		case tracker.PRMerged:
			r.State = StateMerged
		case tracker.PRClosed:
			r.State = StateClosed
		}
		if f, t, ok := ticketOf(s.Board, p.Number); ok {
			r.Detail = "#" + t.Number + " " + t.Title
			r.Opens = Opens{Place: PlaceTicket, Feature: f.ID, Ticket: t.Number}
		}
		out = append(out, r)
	}
	for _, u := range s.Board.Untriaged {
		if u.Number != "" && is(u.Number) {
			out = append(out, Result{
				Kind: KindIssue, Project: s.Project, ProjectName: s.Name, Number: u.Number, Title: u.Title,
				Detail: "Needs triage",
				Opens:  Opens{Place: PlaceTriage, Ref: u.Ref},
			})
		}
	}
	slices.SortStableFunc(out, func(a, b Result) int {
		return cmp.Compare(dimmed(a), dimmed(b))
	})
	return out
}

// dimmed is 1 for a closed result, so it sorts after the open ones.
func dimmed(r Result) int {
	if r.State != "" {
		return 1
	}
	return 0
}

// ticketsBy is s's open Tickets of open Specs that match, in board order.
func ticketsBy(s Source, match func(tracker.Ticket) bool) []Result {
	return slices.DeleteFunc(tickets(s, match), func(r Result) bool { return r.State != "" })
}

// tickets is s's Tickets that match, in board order, open or not. A Ticket
// of a closed Spec counts as shipped, even while it is still open.
func tickets(s Source, match func(tracker.Ticket) bool) []Result {
	out := []Result{}
	for _, f := range s.Board.Features {
		for _, t := range f.Tickets {
			if !match(t) {
				continue
			}
			r := Result{
				Kind: KindTicket, Project: s.Project, ProjectName: s.Name, Number: t.Number, Title: t.Title,
				Detail: f.Title,
				Opens:  Opens{Place: PlaceTicket, Feature: f.ID, Ticket: t.Number},
			}
			switch {
			case t.Closed:
				r.State = StateClosed
			case f.Closed:
				r.State = StateShipped
			}
			out = append(out, r)
		}
	}
	return out
}

// ticketOf is the Ticket PR n belongs to, if the board placed it: an open
// Ticket of an open Spec first, else the first other one (an old PR of a
// closed Ticket).
func ticketOf(b tracker.Board, n int) (tracker.Feature, tracker.Ticket, bool) {
	var first struct {
		f  tracker.Feature
		t  tracker.Ticket
		ok bool
	}
	for _, f := range b.Features {
		for _, t := range f.Tickets {
			if !slices.ContainsFunc(t.PRs, func(p tracker.PRRecord) bool { return p.Number == n }) {
				continue
			}
			if !f.Closed && !t.Closed {
				return f, t, true
			}
			if !first.ok {
				first.f, first.t, first.ok = f, t, true
			}
		}
	}
	return first.f, first.t, first.ok
}

// asNumber reads "42", "#42" or "042" as "42"; ok is false for anything
// that isn't all digits after an optional "#".
func asNumber(s string) (n string, ok bool) {
	s = strings.TrimPrefix(s, "#")
	if s == "" {
		return "", false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		s = "0"
	}
	return s, true
}

func hasAll(text string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}
