package app_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// roadmap is the Roadmap as GET /api/roadmap returns it.
type roadmap struct {
	Projects []struct {
		Path    string `json:"path"`
		Name    string `json:"name"`
		Tracker string `json:"tracker"`
		Note    string `json:"note"`
	} `json:"projects"`
	Features []struct {
		Project string `json:"project"`
		ID      string `json:"id"`
		Title   string `json:"title"`
		Ref     string `json:"ref"`
		Stage   string `json:"stage"`
		Roadmap string `json:"roadmap"`
		NoGroup bool   `json:"noGroup"`
	} `json:"features"`
}

func (f *fh) roadmap(c *http.Client) roadmap {
	f.t.Helper()
	resp, err := c.Get(f.origin() + "/api/roadmap")
	if err != nil {
		f.t.Fatal(err)
	}
	body := readBody(f.t, resp)
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("GET roadmap: status %d: %s", resp.StatusCode, body)
	}
	var r roadmap
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		f.t.Fatal(err)
	}
	return r
}

// groups maps "project-name/feature-id" to its Roadmap group.
func (r roadmap) groups() map[string]string {
	name := map[string]string{}
	for _, p := range r.Projects {
		name[p.Path] = p.Name
	}
	g := map[string]string{}
	for _, f := range r.Features {
		g[name[f.Project]+"/"+f.ID] = f.Roadmap
	}
	return g
}

func TestTheRoadmapListsTheOpenFeaturesOfEveryProjectInTheirGroups(t *testing.T) {
	t.Parallel()
	gh := githubRepo(t) // named shop
	fakeGh(t, []ghIssue{
		{Number: 10, Title: "Login", State: "OPEN", Labels: []string{"ready-for-agent", "roadmap:now"}, SubIssues: []int{11}},
		{Number: 11, Title: "Form", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 10},
		{Number: 20, Title: "Search", State: "OPEN", Labels: []string{"roadmap:next"}, SubIssues: []int{21}},
		{Number: 21, Title: "Index", State: "OPEN", Labels: []string{"ready-for-agent"}, Parent: 20},
		// No roadmap:* label: Later.
		{Number: 30, Title: "Export", State: "OPEN", Labels: []string{"ready-for-agent"}},
		// Shipped: not on the Roadmap, whatever its label (#197).
		{Number: 40, Title: "Billing", State: "CLOSED", Labels: []string{"roadmap:now"}, SubIssues: []int{41}},
		{Number: 41, Title: "Invoice", State: "CLOSED", Parent: 40},
	}, nil, true)

	local := realPath(t, newRepo(t, t.TempDir(), "notes"))
	scratch(t, local, "onboarding/spec.md", "# Onboarding\n\n**Roadmap:** Now\n")
	scratch(t, local, "themes/spec.md", "# Themes\n\nRoadmap: next\n")
	scratch(t, local, "tags/spec.md", "# Tags\n")
	scratch(t, local, "done/spec.md", "# Done thing\n\n**Status:** done\n**Roadmap:** now\n")

	// No tracker at all: not on the Roadmap, and no chip.
	plain := realPath(t, newRepo(t, t.TempDir(), "plain"))

	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	for _, r := range []string{gh, local, plain} {
		f.addProject(c, r)
	}

	r := f.roadmap(c)

	want := map[string]string{
		"shop/10":          "now",
		"shop/20":          "next",
		"shop/30":          "later",
		"notes/onboarding": "now",
		"notes/themes":     "next",
		"notes/tags":       "later",
	}
	got := r.groups()
	if asJSON(got) != asJSON(want) {
		t.Errorf("groups = %s\nwant %s", asJSON(got), asJSON(want))
	}
	var chips []string
	for _, p := range r.Projects {
		chips = append(chips, p.Name+":"+p.Tracker)
	}
	if asJSON(chips) != `["shop:github","notes:local"]` {
		t.Errorf("projects = %v, want shop (github) and notes (local), in the order they were added", chips)
	}
	for _, ft := range r.Features {
		// Only a Spec without a group says so on its card.
		if wantNoGroup := ft.ID == "30" || ft.ID == "tags"; ft.NoGroup != wantNoGroup {
			t.Errorf("%s noGroup = %v, want %v", ft.ID, ft.NoGroup, wantNoGroup)
		}
		if ft.ID == "10" && (ft.Title != "Login" || ft.Ref != "#10" || ft.Stage != "tickets") {
			t.Errorf("Login = %+v, want its title, ref #10 and Stage tickets", ft)
		}
	}
}

func TestARoadmapWithNoProjectsIsEmptyLists(t *testing.T) {
	t.Parallel()
	f := startFh(t, t.TempDir())

	r := f.roadmap(f.authorisedClient())

	if r.Projects == nil || r.Features == nil || len(r.Projects)+len(r.Features) != 0 {
		t.Errorf("roadmap = %+v, want two empty lists", r)
	}
}

func TestARoadmapProjectWhoseTrackerCantBeReadKeepsItsChipWithANote(t *testing.T) {
	t.Parallel()
	gh := githubRepo(t)
	fakeGh(t, nil, nil, false) // gh is logged out
	f := startFh(t, t.TempDir())
	c := f.authorisedClient()
	f.addProject(c, gh)

	r := f.roadmap(c)

	if len(r.Projects) != 1 || r.Projects[0].Note == "" {
		t.Fatalf("projects = %+v, want shop with a note saying why it has no Features", r.Projects)
	}
	if len(r.Features) != 0 {
		t.Errorf("features = %+v, want none", r.Features)
	}
}
