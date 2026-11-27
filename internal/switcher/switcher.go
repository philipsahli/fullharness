// Package switcher works out what the Switcher (⌘K, GLOSSARY.md) lists for
// a query besides Sessions (Spec #201). A number finds the open Spec,
// Ticket, PR or issue with that number in every Project (#254); words find
// the current Project's open Tickets by title (#195). It reads only board
// data fh already has, so typing never calls GitHub.
package switcher

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

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
	Opens  Opens  `json:"opens"`
}

// Source is one Project's data: its board and its PRs, as fh has them
// cached. A board that couldn't be read is an empty board.
type Source struct {
	Project string
	Name    string
	Board   tracker.Board
	PRs     []tracker.PR
}

// Resolve lists what query finds, current being the Project the board
// shows.
//
// A number, with or without "#", finds the open Specs, Tickets, PRs and
// Untriaged issues with that number in every Project: current's first,
// then the others by name, each in board order. Leading zeros don't count,
// so "1" finds local Ticket "01". Closed and merged items are left out.
//
// Any other query finds current's open Tickets whose title holds every
// word of it, in any order and any case. An empty query finds nothing.
func Resolve(query, current string, sources []Source) []Result {
	query = strings.TrimSpace(query)
	out := []Result{}
	if query == "" {
		return out
	}
	number, isNumber := asNumber(query)
	if !isNumber {
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
	for _, s := range ordered {
		out = append(out, byNumber(s, number)...)
	}
	return out
}

// byNumber is s's open items numbered n, in board order: Specs, then
// Tickets, then PRs, then Untriaged issues.
func byNumber(s Source, n string) []Result {
	is := func(number string) bool {
		m, ok := asNumber(number)
		return ok && m == n
	}
	out := []Result{}
	if s.Board.Tracker == tracker.KindGitHub {
		// Only a GitHub Spec has a number; a local one is a folder.
		for _, f := range s.Board.Features {
			if !f.Closed && is(f.ID) {
				out = append(out, Result{
					Kind: KindSpec, Project: s.Project, ProjectName: s.Name, Number: f.ID, Title: f.Title,
					Opens: Opens{Place: PlaceFeature, Feature: f.ID},
				})
			}
		}
	}
	out = append(out, ticketsBy(s, func(t tracker.Ticket) bool { return is(t.Number) })...)
	for _, p := range s.PRs {
		if !p.Unmerged() || !is(strconv.Itoa(p.Number)) {
			continue
		}
		r := Result{
			Kind: KindPR, Project: s.Project, ProjectName: s.Name, Number: strconv.Itoa(p.Number), Title: p.Title,
			Opens: Opens{Place: PlaceGitHub, URL: p.URL},
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
	return out
}

// ticketsBy is s's open Tickets of open Specs that match, in board order.
func ticketsBy(s Source, match func(tracker.Ticket) bool) []Result {
	out := []Result{}
	for _, f := range s.Board.Features {
		if f.Closed {
			continue
		}
		for _, t := range f.Tickets {
			if !t.Closed && match(t) {
				out = append(out, Result{
					Kind: KindTicket, Project: s.Project, ProjectName: s.Name, Number: t.Number, Title: t.Title,
					Detail: f.Title,
					Opens:  Opens{Place: PlaceTicket, Feature: f.ID, Ticket: t.Number},
				})
			}
		}
	}
	return out
}

// ticketOf is the open Ticket PR n belongs to, if the board placed it.
func ticketOf(b tracker.Board, n int) (tracker.Feature, tracker.Ticket, bool) {
	for _, f := range b.Features {
		if f.Closed {
			continue
		}
		for _, t := range f.Tickets {
			if t.Closed {
				continue
			}
			for _, p := range t.PRs {
				if p.Number == n {
					return f, t, true
				}
			}
		}
	}
	return tracker.Feature{}, tracker.Ticket{}, false
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
