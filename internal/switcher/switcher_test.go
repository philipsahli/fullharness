package switcher_test

import (
	"reflect"
	"testing"

	"github.com/philipsahli/fullharness/internal/switcher"
	"github.com/philipsahli/fullharness/internal/tracker"
)

func board() tracker.Board {
	return tracker.Board{Features: []tracker.Feature{
		{ID: "login", Title: "Login", Tickets: []tracker.Ticket{
			{Number: "01", Title: "Login form"},
			{Number: "02", Title: "Session cookie"},
			{Number: "03", Title: "Logout button", Closed: true},
		}},
		{ID: "42", Title: "Search", Tickets: []tracker.Ticket{
			{Number: "43", Title: "Search box"},
			{Number: "44", Title: "Form for advanced search"},
		}},
		{ID: "old", Title: "Shipped", Closed: true, Tickets: []tracker.Ticket{
			{Number: "45", Title: "Old form"},
		}},
	}}
}

func numbers(ts []switcher.Ticket) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, t.Number)
	}
	return out
}

func TestTickets(t *testing.T) {
	for _, c := range []struct {
		name, query string
		want        []string
	}{
		{"a number", "43", []string{"43"}},
		{"a number with #", "#44", []string{"44"}},
		{"leading zeros don't count", "1", []string{"01"}},
		{"leading zeros in the query", "#002", []string{"02"}},
		{"a number no Ticket has", "99", []string{}},
		{"a number is not a word search", "4", []string{}},
		{"words in any order, any case", "FORM login", []string{"01"}},
		{"a word in several titles, board order", "form", []string{"01", "44"}},
		{"words match parts of words", "sear adv", []string{"44"}},
		{"a closed Ticket is left out", "logout", []string{}},
		{"a closed Ticket's number is left out", "3", []string{}},
		{"the Tickets of a closed Spec are left out", "45", []string{}},
		{"an empty query lists none", "  ", []string{}},
		{"a lone # is a word, not a number", "#", []string{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := numbers(switcher.Tickets(c.query, board())); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Tickets(%q) = %v, want %v", c.query, got, c.want)
			}
		})
	}
}

func TestATicketCarriesItsFeature(t *testing.T) {
	got := switcher.Tickets("#43", board())
	want := []switcher.Ticket{{Number: "43", Title: "Search box", Feature: "42", FeatureTitle: "Search"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tickets(#43) = %+v, want %+v", got, want)
	}
}
