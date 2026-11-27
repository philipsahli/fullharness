// Package switcher works out what the Switcher (⌘K, GLOSSARY.md) lists for
// a query besides Sessions. Today that is the open Tickets of one Project's
// board, by number or by words in their title (#195). It reads only a board
// fh already has, so typing never calls GitHub. Spec #201 grows it into the
// resolver for every kind of item across all Projects (#254).
package switcher

import (
	"strings"

	"github.com/philipsahli/fullharness/internal/tracker"
)

// Ticket is one open Ticket the query matched, with the Feature the board
// opens to show its row.
type Ticket struct {
	Number       string `json:"number"`
	Title        string `json:"title"`
	Feature      string `json:"feature"` // the Feature's id on the board
	FeatureTitle string `json:"featureTitle"`
}

// Tickets lists board's open Tickets that query matches, in board order.
//
// A query that is a number, with or without "#", matches the Ticket with
// that number; leading zeros don't count, so "1" finds local Ticket "01".
// Any other query matches a Ticket whose title holds every word of it, in
// any order and any case. An empty query matches nothing: the Switcher then
// lists Sessions only. Closed Tickets and the Tickets of a closed Spec are
// left out.
func Tickets(query string, board tracker.Board) []Ticket {
	query = strings.TrimSpace(query)
	if query == "" {
		return []Ticket{}
	}
	number, isNumber := asNumber(query)
	words := strings.Fields(strings.ToLower(query))
	out := []Ticket{}
	for _, f := range board.Features {
		if f.Closed {
			continue
		}
		for _, t := range f.Tickets {
			if t.Closed {
				continue
			}
			var hit bool
			if isNumber {
				n, ok := asNumber(t.Number)
				hit = ok && n == number
			} else {
				hit = hasAll(strings.ToLower(t.Title), words)
			}
			if hit {
				out = append(out, Ticket{Number: t.Number, Title: t.Title, Feature: f.ID, FeatureTitle: f.Title})
			}
		}
	}
	return out
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
