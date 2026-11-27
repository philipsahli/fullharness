package switcher_test

import (
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

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
				{Number: "03", Title: "Search results"},
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
				{Number: "12", Title: "Card form", Closed: true, PRs: []tracker.PRRecord{{Number: 23, State: tracker.PRMerged}}},
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
		{Number: 24, Title: "Abandoned", State: "CLOSED", URL: "https://github.com/o/web/pull/24"},
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

// key is a result as "<project name> <kind> <number>", and its state when
// it is closed, shipped or merged: enough to check what was found and in
// which order.
func keys(rs []switcher.Result) []string {
	out := []string{}
	for _, r := range rs {
		k := r.ProjectName + " " + string(r.Kind) + " " + r.Number
		if r.State != "" {
			k += " " + string(r.State)
		}
		out = append(out, k)
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
		// Closed and merged items from the board, dimmed (#255).
		{"a closed Ticket", "12", "/repos/web", []string{"web ticket 12 closed"}},
		{"a shipped Spec", "30", "/repos/web", []string{"web spec 30 shipped"}},
		{"a merged PR", "23", "/repos/web", []string{"web pr 23 merged"}},
		{"a closed PR", "24", "/repos/web", []string{"web pr 24 closed"}},
		{"an open Ticket of a shipped Spec", "4", "/repos/shop", []string{"shop ticket 04 shipped"}},
		{"open items before closed ones", "3", "/repos/shop", []string{"shop ticket 03", "shop ticket 03 closed"}},
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
		{"a shipped Spec opens its Feature page", "30", switcher.Result{
			Kind: switcher.KindSpec, Project: "/repos/web", ProjectName: "web", Number: "30", Title: "Old Spec",
			State: switcher.StateShipped,
			Opens: switcher.Opens{Place: switcher.PlaceFeature, Feature: "30"},
		}},
		{"a closed Ticket opens its Feature with its row", "12", switcher.Result{
			Kind: switcher.KindTicket, Project: "/repos/web", ProjectName: "web", Number: "12", Title: "Card form",
			Detail: "Billing", State: switcher.StateClosed,
			Opens: switcher.Opens{Place: switcher.PlaceTicket, Feature: "10", Ticket: "12"},
		}},
		{"a merged PR of a closed Ticket opens the Ticket's row", "23", switcher.Result{
			Kind: switcher.KindPR, Project: "/repos/web", ProjectName: "web", Number: "23", Title: "Old fix",
			Detail: "#12 Card form", State: switcher.StateMerged,
			Opens: switcher.Opens{Place: switcher.PlaceTicket, Feature: "10", Ticket: "12"},
		}},
		{"a closed PR fh can't place opens on GitHub", "24", switcher.Result{
			Kind: switcher.KindPR, Project: "/repos/web", ProjectName: "web", Number: "24", Title: "Abandoned",
			State: switcher.StateClosed,
			Opens: switcher.Opens{Place: switcher.PlaceGitHub, URL: "https://github.com/o/web/pull/24"},
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

// lookup is a fake GitHub lookup for one Project: it answers from found
// (a number not in it isn't on GitHub) or fails with err, and counts its
// calls.
type lookup struct {
	found map[string]switcher.Found
	err   error
	calls atomic.Int32
}

func (l *lookup) ask(n string) (switcher.Found, bool, error) {
	l.calls.Add(1)
	if l.err != nil {
		return switcher.Found{}, false, l.err
	}
	f, ok := l.found[n]
	return f, ok, nil
}

func withLookup(s switcher.Source, l *lookup) switcher.Source {
	s.Lookup = l.ask
	return s
}

// webOnGitHub is web with items its board doesn't have: a closed issue, a
// merged PR older than the PRs fh reads, and an open issue the board
// leaves out (a wayfinder issue).
func webOnGitHub() *lookup {
	return &lookup{found: map[string]switcher.Found{
		"77": {Title: "Board read fails behind a proxy", State: switcher.StateClosed, URL: "https://github.com/o/web/issues/77"},
		"5":  {PR: true, Title: "First commit", State: switcher.StateMerged, URL: "https://github.com/o/web/pull/5"},
		"8":  {Title: "wayfinder: map", URL: "https://github.com/o/web/issues/8"},
	}}
}

func TestALookupFindsWhatTheBoardHasNot(t *testing.T) {
	l := webOnGitHub()
	all := []switcher.Source{withLookup(web(), l), shop()}
	for _, c := range []struct {
		name, query string
		want        []switcher.Result
	}{
		{"a closed issue, dimmed, opens on GitHub", "#77", []switcher.Result{{
			Kind: switcher.KindIssue, Project: "/repos/web", ProjectName: "web", Number: "77",
			Title: "Board read fails behind a proxy", State: switcher.StateClosed,
			Opens: switcher.Opens{Place: switcher.PlaceGitHub, URL: "https://github.com/o/web/issues/77"},
		}}},
		{"a merged PR, dimmed, opens on GitHub", "5", []switcher.Result{{
			Kind: switcher.KindPR, Project: "/repos/web", ProjectName: "web", Number: "5",
			Title: "First commit", State: switcher.StateMerged,
			Opens: switcher.Opens{Place: switcher.PlaceGitHub, URL: "https://github.com/o/web/pull/5"},
		}}},
		{"an open issue off the board opens on GitHub", "8", []switcher.Result{{
			Kind: switcher.KindIssue, Project: "/repos/web", ProjectName: "web", Number: "8", Title: "wayfinder: map",
			Opens: switcher.Opens{Place: switcher.PlaceGitHub, URL: "https://github.com/o/web/issues/8"},
		}}},
		{"a number GitHub doesn't have", "999", []switcher.Result{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := switcher.Resolve(c.query, "/repos/web", all); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Resolve(%q) = %+v\nwant %+v", c.query, got, c.want)
			}
		})
	}
}

func TestALookupRunsOnlyWhenTheBoardHasNothingNumberedSo(t *testing.T) {
	l := webOnGitHub()
	all := []switcher.Source{withLookup(web(), l)}
	// On GitHub one number is one item, so a board hit leaves nothing to
	// look up; words never look anything up.
	for _, q := range []string{"10", "12", "23", "200", "invoice", ""} {
		switcher.Resolve(q, "/repos/web", all)
	}
	if n := l.calls.Load(); n != 0 {
		t.Errorf("lookups = %d, want 0", n)
	}
}

func TestASecondQueryForTheSameNumberAsksGitHubOnce(t *testing.T) {
	cache := switcher.NewCache(5 * time.Minute)
	l, other := webOnGitHub(), &lookup{}
	sources := func() []switcher.Source {
		w, a := web(), api()
		w.Lookup = cache.Wrap(w.Project, l.ask)
		a.Lookup = cache.Wrap(a.Project, other.ask)
		return []switcher.Source{w, a}
	}
	for range 3 {
		if got, want := keys(switcher.Resolve("77", "/repos/web", sources())), []string{"web issue 77 closed"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	// "Not on GitHub" is an answer too, so api's is kept as well.
	if a, b := l.calls.Load(), other.calls.Load(); a != 1 || b != 1 {
		t.Errorf("lookups: web %d, api %d; want 1 each", a, b)
	}
	switcher.Resolve("#5", "/repos/web", sources())
	if n := l.calls.Load(); n != 2 {
		t.Errorf("web lookups after another number = %d, want 2", n)
	}
}

func TestAFailedLookupIsAskedAgain(t *testing.T) {
	cache := switcher.NewCache(5 * time.Minute)
	l := &lookup{err: errors.New("gh api: HTTP 502")}
	w := web()
	w.Lookup = cache.Wrap(w.Project, l.ask)
	switcher.Resolve("77", "/repos/web", []switcher.Source{w})
	switcher.Resolve("77", "/repos/web", []switcher.Source{w})
	if n := l.calls.Load(); n != 2 {
		t.Errorf("lookups = %d, want 2: a failure is no answer to keep", n)
	}
}

func TestAFailingLookupKeepsTheOpenResults(t *testing.T) {
	failing := &lookup{err: errors.New("gh api: connection refused")}
	docs := withLookup(switcher.Source{Project: "/repos/docs", Name: "docs", Board: tracker.Board{Tracker: tracker.KindGitHub}}, failing)
	got := keys(switcher.Resolve("200", "/repos/docs", []switcher.Source{docs, web(), api()}))
	if want := []string{"api spec 200", "web issue 200"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if n := failing.calls.Load(); n != 1 {
		t.Errorf("lookups = %d, want 1", n)
	}
}

// A type word, owner/repo#N and a pasted link (#256) work on closed and
// looked-up items as on open ones (#255).
func TestTypeWordsAndReposApplyToClosedAndLookedUpItems(t *testing.T) {
	for _, c := range []struct {
		name, query, current string
		want                 []string
		webCalls, apiCalls   int32
	}{
		{"a type word keeps a looked-up PR", "PR 5", "/repos/web", []string{"web pr 5 merged"}, 1, 1},
		{"a type word drops a looked-up PR", "Issue 5", "/repos/web", []string{}, 1, 1},
		{"a type word keeps a looked-up issue", "issue #77", "/repos/web", []string{"web issue 77 closed"}, 1, 1},
		{"a type word on closed board items", "PR 23", "/repos/web", []string{"web pr 23 merged"}, 0, 1},
		{"a type word on a closed Ticket", "Ticket 12", "/repos/api", []string{"web ticket 12 closed"}, 0, 1},
		{"no lookup when the board has the number as another type", "PR 10", "/repos/web", []string{}, 0, 1},
		{"owner/repo#N looks up that repo only", "o/web#5", "/repos/api", []string{"web pr 5 merged"}, 1, 0},
		{"owner/repo#N with a type word", "PR o/web#5", "/repos/api", []string{"web pr 5 merged"}, 1, 0},
		{"owner/repo#N of a shipped Spec", "o/web#30", "/repos/web", []string{"web spec 30 shipped"}, 0, 0},
		{"a pasted link to a merged PR", "https://github.com/o/web/pull/5/files", "/repos/api", []string{"web pr 5 merged"}, 1, 0},
		{"a pasted link to a closed issue", "https://github.com/o/web/issues/77", "/repos/web", []string{"web issue 77 closed"}, 1, 0},
		{"a repo that isn't a Project: one GitHub row, no lookup", "PR other/repo#5", "/repos/web", []string{"other/repo pr 5"}, 0, 0},
		{"a local Project has no lookup", "o/shop#5", "/repos/shop", []string{}, 0, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			webL, apiL := webOnGitHub(), &lookup{}
			all := []switcher.Source{withLookup(web(), webL), withLookup(api(), apiL), shop()}
			if got := keys(switcher.Resolve(c.query, c.current, all)); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Resolve(%q) = %v, want %v", c.query, got, c.want)
			}
			if w, a := webL.calls.Load(), apiL.calls.Load(); w != c.webCalls || a != c.apiCalls {
				t.Errorf("lookups: web %d, api %d; want %d, %d", w, a, c.webCalls, c.apiCalls)
			}
		})
	}
}
