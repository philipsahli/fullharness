package switcher_test

import (
	"reflect"
	"testing"

	"github.com/philipsahli/fullharness/internal/switcher"
	"github.com/philipsahli/fullharness/internal/tracker"
)

// shop is a local-markdown Project: its Specs have no number, and each
// Spec numbers its Tickets from 01.
func shop() switcher.Source {
	return switcher.Source{Project: "/repos/shop", Name: "shop", Repo: "o/shop", Board: tracker.Board{
		Tracker: tracker.KindLocal,
		Features: []tracker.Feature{
			{ID: "login", Title: "Login", Tickets: []tracker.Ticket{
				{Number: "01", Title: "Login form"},
				{Number: "02", Title: "Session cookie"},
				{Number: "03", Title: "Logout button", Closed: true},
			}},
			{ID: "search", Title: "Search", Tickets: []tracker.Ticket{
				{Number: "01", Title: "Search box"},
				{Number: "02", Title: "Form for advanced search"},
			}},
			{ID: "old", Title: "Shipped", Closed: true, Tickets: []tracker.Ticket{
				{Number: "04", Title: "Old form"},
			}},
		},
	}}
}

// web is a GitHub Project: Specs, Tickets, PRs and issues share one
// number space.
func web() switcher.Source {
	return switcher.Source{Project: "/repos/web", Name: "web", Repo: "o/web", Board: tracker.Board{
		Tracker: tracker.KindGitHub,
		Features: []tracker.Feature{
			{ID: "10", Title: "Billing", URL: "https://github.com/o/web/issues/10", Tickets: []tracker.Ticket{
				{Number: "11", Title: "Invoice page", PRs: []tracker.PRRecord{{Number: 21, State: tracker.PROpen}}},
				{Number: "12", Title: "Card form", Closed: true},
			}},
			{ID: "30", Title: "Old Spec", Closed: true},
		},
		Untriaged: []tracker.Untriaged{
			{Number: "200", Ref: "#200", Title: "Drift alert fires twice", URL: "https://github.com/o/web/issues/200"},
		},
	}, PRs: []tracker.PR{
		{Number: 21, Title: "Invoice page", State: "OPEN", URL: "https://github.com/o/web/pull/21"},
		{Number: 22, Title: "Bump deps", State: "OPEN", URL: "https://github.com/o/web/pull/22"},
		{Number: 23, Title: "Old fix", State: "MERGED", URL: "https://github.com/o/web/pull/23"},
	}}
}

// api is a second GitHub Project that also has a #200.
func api() switcher.Source {
	return switcher.Source{Project: "/repos/api", Name: "api", Repo: "o/api", Board: tracker.Board{
		Tracker: tracker.KindGitHub,
		Features: []tracker.Feature{
			{ID: "200", Title: "Rate limits", Tickets: []tracker.Ticket{}},
		},
	}}
}

// key is a result as "<project name> <kind> <number>", enough to check
// what was found and in which order.
func keys(rs []switcher.Result) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.ProjectName+" "+string(r.Kind)+" "+r.Number)
	}
	return out
}

func TestResolve(t *testing.T) {
	all := []switcher.Source{web(), shop(), api()}
	for _, c := range []struct {
		name, query, current string
		want                 []string
	}{
		// By number, across every Project.
		{"a Spec", "10", "/repos/web", []string{"web spec 10"}},
		{"a Ticket", "11", "/repos/web", []string{"web ticket 11"}},
		{"an open PR", "22", "/repos/web", []string{"web pr 22"}},
		{"a plain issue", "200", "/repos/web", []string{"web issue 200", "api spec 200"}},
		{"with #", "#200", "/repos/web", []string{"web issue 200", "api spec 200"}},
		{"the current Project first", "200", "/repos/api", []string{"api spec 200", "web issue 200"}},
		{"other Projects by name", "200", "/repos/shop", []string{"api spec 200", "web issue 200"}},
		{"local Tickets of every Spec", "1", "/repos/shop", []string{"shop ticket 01", "shop ticket 01"}},
		{"leading zeros don't count", "#002", "/repos/web", []string{"shop ticket 02", "shop ticket 02"}},
		{"an unknown number", "999", "/repos/web", []string{}},
		{"a closed Ticket is left out (#255)", "12", "/repos/web", []string{}},
		{"a closed Spec is left out (#255)", "30", "/repos/web", []string{}},
		{"a merged PR is left out (#255)", "23", "/repos/web", []string{}},
		{"a Ticket of a closed Spec is left out", "4", "/repos/shop", []string{}},
		// By words: the current Project's open Tickets only (#195).
		{"words in any order, any case", "FORM login", "/repos/shop", []string{"shop ticket 01"}},
		{"a word in several titles, board order", "form", "/repos/shop", []string{"shop ticket 01", "shop ticket 02"}},
		{"words match parts of words", "sear adv", "/repos/shop", []string{"shop ticket 02"}},
		{"words find no other Project's Tickets", "invoice", "/repos/shop", []string{}},
		{"a closed Ticket's words are left out", "logout", "/repos/shop", []string{}},
		{"an empty query lists none", "  ", "/repos/shop", []string{}},
		{"a lone # is a word, not a number", "#", "/repos/shop", []string{}},
		// A type word before the number keeps that type only (#256).
		{"PR and a number", "PR 21", "/repos/web", []string{"web pr 21"}},
		{"Spec and a number", "Spec 200", "/repos/web", []string{"api spec 200"}},
		{"Issue and a number", "issue #200", "/repos/web", []string{"web issue 200"}},
		{"Ticket and a number", "TICKET 1", "/repos/shop", []string{"shop ticket 01", "shop ticket 01"}},
		{"a type with no item of it", "PR 200", "/repos/web", []string{}},
		{"a type word before words is a word", "ticket form", "/repos/shop", []string{}},
		// owner/repo#N: that Project's item alone (#256).
		{"owner/repo#N", "o/api#200", "/repos/web", []string{"api spec 200"}},
		{"owner/repo in any case", "O/Web#200", "/repos/api", []string{"web issue 200"}},
		{"owner/repo#N of a PR", "o/web#22", "/repos/web", []string{"web pr 22"}},
		{"owner/repo#N finds no local Ticket", "o/shop#1", "/repos/shop", []string{}},
		{"owner/repo#N with no open item", "o/web#999", "/repos/web", []string{}},
		{"a type word and owner/repo#N", "Spec o/web#200", "/repos/web", []string{}},
		{"a repo that isn't a Project", "other/repo#5", "/repos/web", []string{"other/repo issue 5"}},
		{"a type word and a repo that isn't a Project", "PR other/repo#5", "/repos/web", []string{"other/repo pr 5"}},
		{"owner/repo with no number is words", "o/web#", "/repos/web", []string{}},
		// A pasted github.com link to an issue or a PR: as owner/repo#N (#256).
		{"a PR link", "https://github.com/o/web/pull/21", "/repos/api", []string{"web pr 21"}},
		{"a PR link to a tab", "https://github.com/o/web/pull/21/files", "/repos/web", []string{"web pr 21"}},
		{"an issue link to a comment", "https://github.com/o/web/issues/200#issuecomment-7", "/repos/web", []string{"web issue 200"}},
		{"a link with www", "http://www.github.com/o/api/issues/200", "/repos/web", []string{"api spec 200"}},
		{"a PR link to a repo that isn't a Project", "https://github.com/other/repo/pull/5", "/repos/web", []string{"other/repo pr 5"}},
		{"an issue link to a repo that isn't a Project", "https://github.com/other/repo/issues/5", "/repos/web", []string{"other/repo issue 5"}},
		{"a link to a commit", "https://github.com/o/web/commit/200", "/repos/web", []string{}},
		{"a link to a branch", "https://github.com/o/web/tree/main", "/repos/web", []string{}},
		{"a link to a repo", "https://github.com/o/web", "/repos/web", []string{}},
		{"a link to a discussion", "https://github.com/other/repo/discussions/5", "/repos/web", []string{}},
		{"a link with no number", "https://github.com/other/repo/pull/new", "/repos/web", []string{}},
		{"a link to another host", "https://gitlab.com/o/web/issues/200", "/repos/web", []string{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := keys(switcher.Resolve(c.query, c.current, all)); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Resolve(%q, %s) = %v, want %v", c.query, c.current, got, c.want)
			}
		})
	}
}

func TestEachResultSaysWhereItOpens(t *testing.T) {
	all := []switcher.Source{web(), shop()}
	for _, c := range []struct {
		name, query string
		want        switcher.Result
	}{
		{"a Spec opens its Feature page", "10", switcher.Result{
			Kind: switcher.KindSpec, Project: "/repos/web", ProjectName: "web", Number: "10", Title: "Billing",
			Opens: switcher.Opens{Place: switcher.PlaceFeature, Feature: "10"},
		}},
		{"a Ticket opens its Feature with its row", "11", switcher.Result{
			Kind: switcher.KindTicket, Project: "/repos/web", ProjectName: "web", Number: "11", Title: "Invoice page",
			Detail: "Billing",
			Opens:  switcher.Opens{Place: switcher.PlaceTicket, Feature: "10", Ticket: "11"},
		}},
		{"an open PR opens its Ticket's row", "21", switcher.Result{
			Kind: switcher.KindPR, Project: "/repos/web", ProjectName: "web", Number: "21", Title: "Invoice page",
			Detail: "#11 Invoice page",
			Opens:  switcher.Opens{Place: switcher.PlaceTicket, Feature: "10", Ticket: "11"},
		}},
		{"a PR fh can't place opens on GitHub", "22", switcher.Result{
			Kind: switcher.KindPR, Project: "/repos/web", ProjectName: "web", Number: "22", Title: "Bump deps",
			Opens: switcher.Opens{Place: switcher.PlaceGitHub, URL: "https://github.com/o/web/pull/22"},
		}},
		{"a plain issue opens its Needs triage row", "200", switcher.Result{
			Kind: switcher.KindIssue, Project: "/repos/web", ProjectName: "web", Number: "200", Title: "Drift alert fires twice",
			Detail: "Needs triage",
			Opens:  switcher.Opens{Place: switcher.PlaceTriage, Ref: "#200"},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := switcher.Resolve(c.query, "/repos/web", all)
			if len(got) != 1 || !reflect.DeepEqual(got[0], c.want) {
				t.Errorf("Resolve(%q) = %+v\nwant [%+v]", c.query, got, c.want)
			}
		})
	}
}

func TestAnItemOfARepoThatIsntAProjectOpensOnGitHub(t *testing.T) {
	all := []switcher.Source{web(), shop()}
	for _, c := range []struct {
		name, query string
		want        switcher.Result
	}{
		{"owner/repo#N opens as an issue; GitHub redirects a PR", "other/repo#5", switcher.Result{
			Kind: switcher.KindIssue, ProjectName: "other/repo", Number: "5", Title: "other/repo",
			Detail: "not one of your Projects",
			Opens:  switcher.Opens{Place: switcher.PlaceGitHub, URL: "https://github.com/other/repo/issues/5"},
		}},
		{"a PR link opens the PR", "https://github.com/other/repo/pull/005/files", switcher.Result{
			Kind: switcher.KindPR, ProjectName: "other/repo", Number: "5", Title: "other/repo",
			Detail: "not one of your Projects",
			Opens:  switcher.Opens{Place: switcher.PlaceGitHub, URL: "https://github.com/other/repo/pull/5"},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := switcher.Resolve(c.query, "/repos/web", all)
			if len(got) != 1 || !reflect.DeepEqual(got[0], c.want) {
				t.Errorf("Resolve(%q) = %+v\nwant [%+v]", c.query, got, c.want)
			}
		})
	}
}

func TestAWordMatchCarriesItsFeature(t *testing.T) {
	got := switcher.Resolve("cookie", "/repos/shop", []switcher.Source{shop()})
	want := []switcher.Result{{
		Kind: switcher.KindTicket, Project: "/repos/shop", ProjectName: "shop", Number: "02", Title: "Session cookie",
		Detail: "Login",
		Opens:  switcher.Opens{Place: switcher.PlaceTicket, Feature: "login", Ticket: "02"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve(cookie) = %+v\nwant %+v", got, want)
	}
}

func TestAProjectWithoutABoardListsNothing(t *testing.T) {
	// A board that couldn't be read is an empty board: its Project adds no
	// results, the others still do.
	empty := switcher.Source{Project: "/repos/x", Name: "x", Board: tracker.Board{Tracker: tracker.KindNone}}
	got := keys(switcher.Resolve("200", "/repos/x", []switcher.Source{empty, api()}))
	if want := []string{"api spec 200"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
