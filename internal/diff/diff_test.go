package diff_test

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/philipsahli/fullharness/internal/diff"
)

func TestLines(t *testing.T) {
	for _, tc := range []struct {
		name     string
		old, new string
		want     diff.File
	}{
		{
			name: "new file is all added",
			old:  "",
			new:  "a\nb\n",
			want: diff.File{Path: "f", Added: 2, Hunks: []diff.Hunk{{
				OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 2,
				Lines: []diff.Line{{Op: "+", Text: "a"}, {Op: "+", Text: "b"}},
			}}},
		},
		{
			name: "deleted file is all removed",
			old:  "a\nb\n",
			new:  "",
			want: diff.File{Path: "f", Removed: 2, Hunks: []diff.Hunk{{
				OldStart: 1, OldLines: 2, NewStart: 0, NewLines: 0,
				Lines: []diff.Line{{Op: "-", Text: "a"}, {Op: "-", Text: "b"}},
			}}},
		},
		{
			name: "pure insert keeps 3 lines of context",
			old:  "a\nb\nc\nd\ne\nf\ng\nh\n",
			new:  "a\nb\nc\nd\nX\ne\nf\ng\nh\n",
			want: diff.File{Path: "f", Added: 1, Hunks: []diff.Hunk{{
				OldStart: 2, OldLines: 6, NewStart: 2, NewLines: 7,
				Lines: []diff.Line{ctx("b"), ctx("c"), ctx("d"), {Op: "+", Text: "X"}, ctx("e"), ctx("f"), ctx("g")},
			}}},
		},
		{
			name: "pure delete",
			old:  "a\nb\nc\nd\ne\nf\ng\nh\n",
			new:  "a\nb\nc\nd\nf\ng\nh\n",
			want: diff.File{Path: "f", Removed: 1, Hunks: []diff.Hunk{{
				OldStart: 2, OldLines: 7, NewStart: 2, NewLines: 6,
				Lines: []diff.Line{ctx("b"), ctx("c"), ctx("d"), {Op: "-", Text: "e"}, ctx("f"), ctx("g"), ctx("h")},
			}}},
		},
		{
			name: "change in the middle",
			old:  "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n",
			new:  "1\n2\n3\n4\nfive\n6\n7\n8\n9\n10\n",
			want: diff.File{Path: "f", Added: 1, Removed: 1, Hunks: []diff.Hunk{{
				OldStart: 2, OldLines: 7, NewStart: 2, NewLines: 7,
				Lines: []diff.Line{ctx("2"), ctx("3"), ctx("4"), {Op: "-", Text: "5"}, {Op: "+", Text: "five"}, ctx("6"), ctx("7"), ctx("8")},
			}}},
		},
		{
			name: "changes far apart make two hunks",
			old:  "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n",
			new:  "one\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\ntwelve\n",
			want: diff.File{Path: "f", Added: 2, Removed: 2, Hunks: []diff.Hunk{
				{
					OldStart: 1, OldLines: 4, NewStart: 1, NewLines: 4,
					Lines: []diff.Line{{Op: "-", Text: "1"}, {Op: "+", Text: "one"}, ctx("2"), ctx("3"), ctx("4")},
				},
				{
					OldStart: 9, OldLines: 4, NewStart: 9, NewLines: 4,
					Lines: []diff.Line{ctx("9"), ctx("10"), ctx("11"), {Op: "-", Text: "12"}, {Op: "+", Text: "twelve"}},
				},
			}},
		},
		{
			name: "changes 6 lines apart share a hunk",
			old:  "1\n2\n3\n4\n5\n6\n7\n8\n",
			new:  "one\n2\n3\n4\n5\n6\n7\neight\n",
			want: diff.File{Path: "f", Added: 2, Removed: 2, Hunks: []diff.Hunk{{
				OldStart: 1, OldLines: 8, NewStart: 1, NewLines: 8,
				Lines: []diff.Line{
					{Op: "-", Text: "1"}, {Op: "+", Text: "one"}, ctx("2"), ctx("3"), ctx("4"), ctx("5"), ctx("6"), ctx("7"),
					{Op: "-", Text: "8"}, {Op: "+", Text: "eight"},
				},
			}}},
		},
		{
			name: "CRLF and LF are equal",
			old:  "a\r\nb\r\n",
			new:  "a\nb\n",
			want: diff.File{Path: "f", Hunks: []diff.Hunk{}},
		},
		{
			name: "whitespace-only change shows",
			old:  "a\nb\n",
			new:  "a\nb \n",
			want: diff.File{Path: "f", Added: 1, Removed: 1, Hunks: []diff.Hunk{{
				OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 2,
				Lines: []diff.Line{ctx("a"), {Op: "-", Text: "b"}, {Op: "+", Text: "b "}},
			}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := diff.Lines("f", tc.old, tc.new)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func ctx(text string) diff.Line { return diff.Line{Op: " ", Text: text} }

func TestLinesCutsAt300ChangedLines(t *testing.T) {
	var b strings.Builder
	for i := range 400 {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	got := diff.Lines("f", "", b.String())

	if !got.Truncated {
		t.Error("Truncated = false, want true")
	}
	if got.Added != 400 || got.Removed != 0 {
		t.Errorf("Added, Removed = %d, %d, want the full counts 400, 0", got.Added, got.Removed)
	}
	if len(got.Hunks) != 1 || len(got.Hunks[0].Lines) != 300 || got.Hunks[0].NewLines != 300 {
		t.Fatalf("hunks = %+v, want one hunk of 300 lines", got.Hunks)
	}
	if last := got.Hunks[0].Lines[299].Text; last != "line 299" {
		t.Errorf("last shown line = %q, want %q", last, "line 299")
	}
}

func TestLinesUnder300IsNotCut(t *testing.T) {
	got := diff.Lines("f", "", strings.Repeat("x\n", 300))
	if got.Truncated || len(got.Hunks[0].Lines) != 300 {
		t.Errorf("Truncated = %v with %d lines, want false with 300", got.Truncated, len(got.Hunks[0].Lines))
	}
}

// A rewrite too large for the Myers search still gives a correct diff.
func TestLinesRewritingABigFile(t *testing.T) {
	var a, b strings.Builder
	for i := range 1200 {
		fmt.Fprintf(&a, "old %d\n", i)
		fmt.Fprintf(&b, "new %d\n", i)
	}
	got := diff.Lines("f", a.String(), b.String())
	if got.Added != 1200 || got.Removed != 1200 || !got.Truncated {
		t.Errorf("Added, Removed, Truncated = %d, %d, %v, want 1200, 1200, true", got.Added, got.Removed, got.Truncated)
	}
}

// Applying the hunks to the old text gives the new text, the hunk headers
// match their lines, and no shorter diff exists, for random edits of a small alphabet (so
// lines repeat, as in real code).
func TestLinesHunksApplyAndAreMinimal(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	gen := func() []string {
		lines := make([]string, r.IntN(30))
		for i := range lines {
			lines[i] = string(rune('a' + r.IntN(4)))
		}
		return lines
	}
	for range 2000 {
		a, b := gen(), gen()
		f := diff.Lines("f", join(a), join(b))
		if got := apply(t, a, f); !slices.Equal(got, b) {
			t.Fatalf("old %q\nnew %q\nhunks %+v\napplied %q", a, b, f.Hunks, got)
		}
		if want := len(a) + len(b) - 2*lcs(a, b); f.Added+f.Removed != want {
			t.Fatalf("old %q\nnew %q: %d changed lines, want the minimum %d", a, b, f.Added+f.Removed, want)
		}
	}
}

// lcs is the length of the longest common subsequence, by dynamic
// programming: a minimal diff changes len(a)+len(b)-2*lcs lines.
func lcs(a, b []string) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	return dp[0][0]
}

func join(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func apply(t *testing.T, old []string, f diff.File) []string {
	t.Helper()
	var out []string
	next := 0 // index of the next old line not yet copied
	added, removed := 0, 0
	for _, h := range f.Hunks {
		start := h.OldStart - 1
		if h.OldLines == 0 {
			start = h.OldStart // the hunk goes after line OldStart
		}
		out = append(out, old[next:start]...)
		next = start
		oldN, newN := 0, 0
		for _, l := range h.Lines {
			switch l.Op {
			case " ":
				if old[next] != l.Text {
					t.Fatalf("context %q at old line %d is %q", l.Text, next+1, old[next])
				}
				out = append(out, l.Text)
				next++
				oldN++
				newN++
			case "-":
				if old[next] != l.Text {
					t.Fatalf("removed %q at old line %d is %q", l.Text, next+1, old[next])
				}
				next++
				oldN++
				removed++
			case "+":
				out = append(out, l.Text)
				newN++
				added++
			}
		}
		if oldN != h.OldLines || newN != h.NewLines {
			t.Fatalf("hunk %+v counts %d old, %d new lines", h, oldN, newN)
		}
	}
	if added != f.Added || removed != f.Removed {
		t.Fatalf("hunks add %d, remove %d; File says %d, %d", added, removed, f.Added, f.Removed)
	}
	return append(out, old[next:]...)
}
