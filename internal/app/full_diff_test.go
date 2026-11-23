package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/philipsahli/fullharness/internal/diff"
)

// fullDiffs fetches the uncut diffs of a tool card or permission request, as
// Show full diff does. It returns the status and, on 200, the diffs.
func (f *fh) fullDiffs(c *http.Client, session, item string) (int, []diff.File) {
	f.t.Helper()
	resp, err := c.Get(f.origin() + "/api/sessions/" + session + "/diffs/" + item)
	if err != nil {
		f.t.Fatal(err)
	}
	body := readBody(f.t, resp)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	var files []diff.File
	if err := json.Unmarshal([]byte(body), &files); err != nil {
		f.t.Fatalf("diffs %s: %v", body, err)
	}
	return resp.StatusCode, files
}

// numbered returns n lines "line 1" to "line n", each with a newline.
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

// changedLines counts a diff's added and removed lines in its hunks.
func changedLines(f diff.File) int {
	n := 0
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Op != " " {
				n++
			}
		}
	}
	return n
}

func TestShowFullDiffGetsTheLinesPastTheCut(t *testing.T) {
	f := startFhWithAgent(t, script{"default": []any{
		map[string]any{"tool": map[string]any{"id": "t1", "title": "Write big.txt", "kind": "edit", "status": "completed",
			"path": "big.txt", "diffNew": numbered(400)}},
		map[string]any{"permission": map[string]any{"id": "t2", "title": "Edit big.txt", "kind": "edit",
			"path": "big.txt", "diffOld": numbered(400), "diffNew": "line 1\n"}},
	}})
	c, _, id, req := askAndWait(t, f)

	var tool map[string]any
	for _, it := range f.transcript(c, id) {
		if it["type"] == "tool" {
			tool = it["tool"].(map[string]any)
		}
	}
	if tool == nil {
		t.Fatal("no tool card")
	}
	items := map[string]struct {
		id      string
		shown   map[string]any
		changed int
	}{
		"tool card":          {tool["id"].(string), tool, 400}, // a new file of 400 lines
		"permission request": {req["id"].(string), req, 399},   // 399 of 400 lines removed
	}
	for name, it := range items {
		if cut := diffs(t, it.shown); len(cut) != 1 || !cut[0].Truncated || changedLines(cut[0]) != diff.MaxChanged {
			t.Fatalf("%s: shown diffs = %+v, want one cut at %d lines", name, cut, diff.MaxChanged)
		}
		status, full := f.fullDiffs(c, id, it.id)
		if status != http.StatusOK {
			t.Fatalf("%s: GET full diffs: status %d, want 200", name, status)
		}
		if len(full) != 1 || full[0].Truncated || full[0].Path != "big.txt" || changedLines(full[0]) != it.changed {
			t.Errorf("%s: full diffs = %d files, truncated %v, %d changed lines; want 1, false, %d", name,
				len(full), len(full) == 1 && full[0].Truncated, sumChanged(full), it.changed)
		}
	}
}

func sumChanged(files []diff.File) int {
	n := 0
	for _, f := range files {
		n += changedLines(f)
	}
	return n
}

func TestFullDiffOfAnUnknownItemIsNotFound(t *testing.T) {
	f := startFhWithAgent(t, askScript(nil))
	c, _, id, _ := askAndWait(t, f)

	if status, _ := f.fullDiffs(c, id, "nope"); status != http.StatusNotFound {
		t.Errorf("unknown item: status %d, want 404", status)
	}
	if status, _ := f.fullDiffs(c, "nope", "t1"); status != http.StatusNotFound {
		t.Errorf("unknown Session: status %d, want 404", status)
	}
}
