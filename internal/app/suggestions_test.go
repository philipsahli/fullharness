package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
)

func (f *fh) suggestions(c *http.Client) []string {
	f.t.Helper()
	resp, err := c.Get(f.origin() + "/api/projects/suggestions")
	if err != nil {
		f.t.Fatal(err)
	}
	body := readBody(f.t, resp)
	if resp.StatusCode != http.StatusOK {
		f.t.Fatalf("suggestions: status %d: %s", resp.StatusCode, body)
	}
	var list []struct{ Path, Name string }
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		f.t.Fatalf("decode %q: %v", body, err)
	}
	paths := []string{}
	for _, s := range list {
		paths = append(paths, s.Path)
	}
	slices.Sort(paths)
	return paths
}

func TestScanFolderSuggestsReposUpToTwoLevelsDown(t *testing.T) {
	dir := t.TempDir()
	// Not resolved: on macOS t.TempDir sits behind the /var symlink, like a
	// scan_folder a user writes through a symlinked path.
	scan := t.TempDir()
	shop := realPath(t, newRepo(t, scan, "shop"))                                     // level 1
	billing := realPath(t, newRepo(t, filepath.Join(scan, "acme"), "billing"))        // level 2
	newRepo(t, filepath.Join(scan, "deep", "er"), "tooFar")                           // level 3
	added := realPath(t, newRepo(t, scan, "added"))                                   // already a Project
	git(t, shop, "worktree", "add", "-q", "-b", "wt", filepath.Join(scan, "shop-wt")) // .git file
	writeFile(t, filepath.Join(scan, "notes.txt"), "not a folder\n")
	writeConfig(t, dir, fmt.Sprintf("scan_folder = %q\n", scan))
	f := startFh(t, dir)
	c := f.authorisedClient()
	f.addProject(c, added)

	got := f.suggestions(c)

	want := []string{billing, shop}
	if !slices.Equal(got, want) {
		t.Errorf("suggestions %v, want %v", got, want)
	}
}

func TestNoScanFolderMeansNoSuggestions(t *testing.T) {
	f := startFh(t, t.TempDir())

	if got := f.suggestions(f.authorisedClient()); len(got) != 0 {
		t.Errorf("suggestions %v, want none", got)
	}
}
