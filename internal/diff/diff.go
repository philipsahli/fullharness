// Package diff computes line diffs of a file's old and new text, as unified
// hunks with 3 lines of context, for tool calls that edit a file.
package diff

import "strings"

// contextLines is how many unchanged lines a hunk shows around a change.
const contextLines = 3

// MaxChanged is how many added and removed lines a File shows at most. A
// longer diff is cut there and Truncated is set; Added and Removed still
// count every changed line.
const MaxChanged = 300

// maxEdits bounds the Myers search, whose memory grows with the square of the
// number of edits. Past it, the lines between the common prefix and suffix
// show as all removed, then all added: a correct diff, not a minimal one.
const maxEdits = 1000

// File is the diff of one file.
type File struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Hunks   []Hunk `json:"hunks"`
	// Truncated is set when the hunks stop after MaxChanged changed lines.
	Truncated bool `json:"truncated,omitempty"`
}

// Hunk is a run of changes with its context. Starts are 1-based line
// numbers; a start is 0 when its side has no lines, as in unified diffs.
type Hunk struct {
	OldStart int    `json:"oldStart"`
	OldLines int    `json:"oldLines"`
	NewStart int    `json:"newStart"`
	NewLines int    `json:"newLines"`
	Lines    []Line `json:"lines"`
}

// Line is one line of a hunk. Op is " " (unchanged), "-" or "+".
type Line struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// Lines diffs oldText against newText. Empty old text means a new file and
// empty new text a deleted one. "\r\n" and "\n" count as the same line end,
// but any other whitespace change shows.
func Lines(path, oldText, newText string) File {
	a, b := split(oldText), split(newText)
	ops := script(a, b)
	f := File{Path: path}
	for _, o := range ops {
		switch o.op {
		case '+':
			f.Added++
		case '-':
			f.Removed++
		}
	}
	f.Hunks, f.Truncated = hunks(ops)
	return f
}

func split(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// edit is one line of the edit script, with its 0-based line numbers in the
// old and new text (the next line's number on the side it is missing from).
type edit struct {
	op   byte // ' ', '-' or '+'
	text string
	a, b int
}

// script returns the edit script that turns a into b.
func script(a, b []string) []edit {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var ops []edit
	for i := 0; i < pre; i++ {
		ops = append(ops, edit{' ', a[i], i, i})
	}
	ops = append(ops, myers(a[pre:len(a)-suf], b[pre:len(b)-suf], pre)...)
	for i := 0; i < suf; i++ {
		ia, ib := len(a)-suf+i, len(b)-suf+i
		ops = append(ops, edit{' ', a[ia], ia, ib})
	}
	return ops
}

// myers is Myers' O(ND) diff ("An O(ND) Difference Algorithm and Its
// Variations", 1986). It keeps every round's frontier to walk the path
// back, so it gives up after maxEdits rounds. off is added to line numbers.
func myers(a, b []string, off int) []edit {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return replaceAll(a, b, off)
	}
	top := n + m
	// v[top+k] is the furthest x reached on diagonal k = x - y.
	v := make([]int, 2*top+2)
	var trace [][]int
	for d := 0; d <= min(top, maxEdits); d++ {
		// Round d reads diagonals -d-1..d+1; keep just those.
		lo := max(top-d-1, 0)
		trace = append(trace, append([]int(nil), v[lo:top+d+2]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[top+k-1] < v[top+k+1]) {
				x = v[top+k+1] // down: insert b[y]
			} else {
				x = v[top+k-1] + 1 // right: delete a[x]
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[top+k] = x
			if x >= n && y >= m {
				return backtrack(a, b, trace, top, off)
			}
		}
	}
	return replaceAll(a, b, off)
}

// backtrack walks from (n, m) back to (0, 0). trace[d] is the frontier from
// before round d, starting at diagonal -d-1 (or -top when d = top).
func backtrack(a, b []string, trace [][]int, top, off int) []edit {
	x, y := len(a), len(b)
	var rev []edit
	for d := len(trace) - 1; d > 0; d-- {
		lo := max(top-d-1, 0) - top
		at := func(k int) int { return trace[d][k-lo] }
		k := x - y
		pk := k - 1
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			pk = k + 1
		}
		px := at(pk)
		py := px - pk
		for x > px && y > py {
			x--
			y--
			rev = append(rev, edit{' ', a[x], x + off, y + off})
		}
		if x == px {
			y--
			rev = append(rev, edit{'+', b[y], x + off, y + off})
		} else {
			x--
			rev = append(rev, edit{'-', a[x], x + off, y + off})
		}
	}
	for x > 0 {
		x--
		y--
		rev = append(rev, edit{' ', a[x], x + off, y + off})
	}
	ops := make([]edit, len(rev))
	for i, o := range rev {
		ops[len(rev)-1-i] = o
	}
	return ops
}

func replaceAll(a, b []string, off int) []edit {
	var ops []edit
	for i, l := range a {
		ops = append(ops, edit{'-', l, i + off, off})
	}
	for j, l := range b {
		ops = append(ops, edit{'+', l, len(a) + off, j + off})
	}
	return ops
}

// hunks groups the edit script into hunks with contextLines of context around each
// change, stopping after MaxChanged changed lines.
func hunks(ops []edit) ([]Hunk, bool) {
	out := []Hunk{}
	changed := 0
	for i := 0; i < len(ops); {
		if ops[i].op == ' ' {
			i++
			continue
		}
		if changed == MaxChanged {
			return out, true // a hunk here would hold only context
		}
		// A hunk starts contextLines before this change and runs until a gap
		// of more than 2*contextLines unchanged lines.
		start := max(0, i-contextLines)
		end := i
		cut := false
		for end < len(ops) {
			if ops[end].op != ' ' {
				if changed == MaxChanged {
					cut = true
					break
				}
				changed++
				end++
				continue
			}
			gap := end
			for gap < len(ops) && ops[gap].op == ' ' {
				gap++
			}
			if gap == len(ops) || gap-end > 2*contextLines {
				end = min(gap, end+contextLines)
				break
			}
			end = gap
		}
		out = append(out, hunk(ops[start:end]))
		if cut {
			return out, true
		}
		i = end
	}
	return out, false
}

func hunk(ops []edit) Hunk {
	h := Hunk{OldStart: ops[0].a + 1, NewStart: ops[0].b + 1}
	for _, o := range ops {
		h.Lines = append(h.Lines, Line{Op: string(o.op), Text: o.text})
		if o.op != '+' {
			h.OldLines++
		}
		if o.op != '-' {
			h.NewLines++
		}
	}
	// A side with no lines starts at the line before, as in unified diffs.
	if h.OldLines == 0 {
		h.OldStart--
	}
	if h.NewLines == 0 {
		h.NewStart--
	}
	return h
}
