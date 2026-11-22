package tracker

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ReadLocal reads Features from .scratch/<feature>/spec.md and
// .scratch/<feature>/issues/<NN>-<slug>.md, as Matt's skills write them.
// aheadBranches are the Project's branches with commits ahead of its base.
func ReadLocal(repo string, aheadBranches []string) []Feature {
	features := []Feature{}
	dirs, err := os.ReadDir(filepath.Join(repo, ".scratch"))
	if err != nil {
		return features // no .scratch: no Features
	}

	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(repo, ".scratch", d.Name())
		spec, err := os.ReadFile(filepath.Join(dir, "spec.md"))
		if err != nil {
			continue // a folder without spec.md isn't a Feature (e.g. a Wayfinder map)
		}
		f := Feature{ID: d.Name(), Title: heading(string(spec), d.Name()), Closed: isClosed(status(string(spec)))}
		files, _ := filepath.Glob(filepath.Join(dir, "issues", "*.md")) // a bad pattern is impossible here
		for _, file := range files {
			b, err := os.ReadFile(file)
			if err != nil {
				continue // unreadable file: skip it rather than fail the board
			}
			t := readLocalTicket(filepath.Base(file), string(b))
			t.BranchAhead = slices.ContainsFunc(aheadBranches, func(br string) bool { return localBranchFor(br, filepath.Base(file)) })
			f.Tickets = append(f.Tickets, t)
		}
		slices.SortFunc(f.Tickets, func(a, b Ticket) int { return strings.Compare(a.Number, b.Number) })
		features = append(features, f)
	}
	Derive(features)
	return features
}

var (
	fileNumber   = regexp.MustCompile(`^(\d+)-`)
	fileSlug     = regexp.MustCompile(`^(\d+)-(.+)\.md$`)
	blockerNumbs = regexp.MustCompile(`\b\d{1,3}\b`)
	titleNumber  = regexp.MustCompile(`^\d+:\s*`)
)

func readLocalTicket(file, text string) Ticket {
	t := Ticket{Blockers: []Blocker{}}
	if m := fileNumber.FindStringSubmatch(file); m != nil {
		t.Number = m[1]
	}
	t.Title = titleNumber.ReplaceAllString(heading(text, file), "")
	t.Label = status(text)
	t.Closed = isClosed(t.Label)
	for _, n := range blockerNumbs.FindAllString(field(text, "Blocked by"), -1) {
		// Keep the width of the Ticket's own numbers ("01"), so they match.
		num, _ := strconv.Atoi(n)
		t.Blockers = append(t.Blockers, Blocker{Number: pad(num, len(t.Number))})
	}
	return t
}

// localBranchFor reports whether branch works on the Ticket in file
// (<NN>-<slug>.md). Local numbers restart in every feature folder, so the
// branch must name the Ticket too: <N>-<slug>…, e.g. 1-csv or 1-csv-export
// for 01-csv.md.
func localBranchFor(branch, file string) bool {
	m := fileSlug.FindStringSubmatch(file)
	if m == nil {
		return false
	}
	want, _ := strconv.Atoi(m[1])
	n, ok := numberOf(branch)
	_, rest, _ := strings.Cut(branch, "-")
	return ok && n == want && strings.HasPrefix(rest, m[2])
}

// field returns the value of a `**Name:** value` or `Name: value` line.
func field(text, name string) string {
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		for _, prefix := range []string{"**" + name + ":**", name + ":"} {
			if rest, ok := strings.CutPrefix(l, prefix); ok {
				return strings.TrimSpace(rest)
			}
		}
	}
	return ""
}

func status(text string) string { return strings.ToLower(field(text, "Status")) }

// isClosed: the triage roles are open; done or closed means closed (#11).
func isClosed(status string) bool { return status == "done" || status == "closed" }

// heading returns the first "# " heading, or fallback.
func heading(text, fallback string) string {
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return fallback
}

func pad(n, width int) string {
	s := strconv.Itoa(n)
	for len(s) < width {
		s = "0" + s
	}
	return s
}
