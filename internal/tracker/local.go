package tracker

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ReadLocal reads a local markdown board's Features from .scratch/<feature>/spec.md and
// .scratch/<feature>/issues/<NN>-<slug>.md, as Matt's skills write them.
// aheadBranches are the Project's branches with commits ahead of its base,
// prs its PRs in every state (when it has a GitHub remote and gh). Stages
// are left to Derive.
//
// A folder without spec.md isn't a Feature (a Wayfinder map, or a few
// one-off issues). Each open file in its issues/ is an Untriaged issue
// instead: its ref is the file's path relative to the Project, its Status
// its one label. They come newest first by file name; closed ones are
// skipped.
func ReadLocal(repo string, aheadBranches []string, prs []PR) Board {
	board := Board{Tracker: KindLocal, Features: []Feature{}, Untriaged: []Untriaged{}}
	dirs, err := os.ReadDir(filepath.Join(repo, ".scratch"))
	if err != nil {
		return board // no .scratch: no Features
	}

	var issueFiles []untriagedFile

	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(repo, ".scratch", d.Name())
		spec, err := os.ReadFile(filepath.Join(dir, "spec.md"))
		if err != nil {
			issueFiles = append(issueFiles, readUntriaged(d.Name(), dir)...)
			continue
		}
		// Local Specs have no number: the folder names the branch, and the
		// prompt names the file, relative to the Project.
		f := Feature{ID: d.Name(), Title: heading(string(spec), d.Name()), Closed: isClosed(status(string(spec))),
			Branch: d.Name(), Ref: filepath.ToSlash(filepath.Join(".scratch", d.Name(), "spec.md")), Tickets: []Ticket{},
			URL:      vscodeURL(filepath.Join(dir, "spec.md")),
			Roadmap:  strings.ToLower(field(string(spec), "Roadmap")),
			Approved: status(string(spec)) == ReadyLabel}
		files, _ := filepath.Glob(filepath.Join(dir, "issues", "*.md")) // a bad pattern is impossible here
		for _, file := range files {
			b, err := os.ReadFile(file)
			if err != nil {
				continue // unreadable file: skip it rather than fail the board
			}
			t := readLocalTicket(filepath.Base(file), string(b))
			// The file's own name is a branch localBranchFor matches.
			t.Branch = strings.TrimSuffix(filepath.Base(file), ".md")
			t.Ref = filepath.ToSlash(filepath.Join(".scratch", d.Name(), "issues", filepath.Base(file)))
			t.URL = vscodeURL(file)
			mine := func(br string) bool { return localBranchFor(br, filepath.Base(file)) }
			t.BranchAhead = slices.ContainsFunc(aheadBranches, mine)
			t.linkPRs(prs, func(p PR) bool { return mine(p.HeadRefName) })
			f.Tickets = append(f.Tickets, t)
		}
		slices.SortFunc(f.Tickets, func(a, b Ticket) int { return strings.Compare(a.Number, b.Number) })
		board.Features = append(board.Features, f)
	}
	// Newest first: file names start with a number that grows with each
	// issue, so the name sorts them; the folder breaks a tie.
	slices.SortFunc(issueFiles, func(a, b untriagedFile) int {
		if c := strings.Compare(b.file, a.file); c != 0 {
			return c
		}
		return strings.Compare(b.folder, a.folder)
	})
	for _, f := range issueFiles {
		board.Untriaged = append(board.Untriaged, f.issue)
	}
	return board
}

// untriagedFile is an Untriaged issue with the folder and file name it
// sorts by.
type untriagedFile struct {
	folder, file string
	issue        Untriaged
}

// readUntriaged reads the issue files of folder, a .scratch folder with no
// spec.md, as Untriaged issues: with no Spec they have nowhere else to show.
func readUntriaged(folder, dir string) []untriagedFile {
	var out []untriagedFile
	files, _ := filepath.Glob(filepath.Join(dir, "issues", "*.md")) // a bad pattern is impossible here
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			continue // unreadable file: skip it rather than fail the board
		}
		t := readLocalTicket(filepath.Base(file), string(b))
		if t.Closed {
			continue // closed one-off issues stay hidden, as on GitHub (#12)
		}
		name := filepath.Base(file)
		u := Untriaged{Ref: filepath.ToSlash(filepath.Join(".scratch", folder, "issues", name)),
			Title: t.Title, Labels: []string{}}
		if t.Label != "" {
			u.Labels = append(u.Labels, t.Label)
		}
		out = append(out, untriagedFile{folder: folder, file: name, issue: u})
	}
	return out
}

// vscodeURL opens file in VS Code (#102). It needs no remote, so a local
// board links its numbers whatever the Project's origin is. repo comes from
// git's top level, so file is absolute; url.URL percent-encodes spaces.
func vscodeURL(file string) string {
	return (&url.URL{Scheme: "vscode", Host: "file", Path: filepath.ToSlash(file)}).String()
}

var (
	fileNumber    = regexp.MustCompile(`^(\d+)-`)
	fileSlug      = regexp.MustCompile(`^(\d+)-(.+)\.md$`)
	titleNumber   = regexp.MustCompile(`^\d+:\s*`)
	parenthesised = regexp.MustCompile(`\([^)]*\)`)
	itemSeparator = regexp.MustCompile(`[,;]|\band\b`)
	leadingNumber = regexp.MustCompile(`^#?(\d{1,3})(?:\D|$)`)
)

func readLocalTicket(file, text string) Ticket {
	t := Ticket{Blockers: []Blocker{}}
	if m := fileNumber.FindStringSubmatch(file); m != nil {
		t.Number = m[1]
	}
	t.Title = titleNumber.ReplaceAllString(heading(text, file), "")
	t.Label = status(text)
	t.Closed = isClosed(t.Label)
	for _, n := range blockerNumbers(field(text, "Blocked by")) {
		t.Blockers = append(t.Blockers, Blocker{Number: n})
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
	want, _ := strconv.Atoi(m[1]) // the regex matched digits only
	n, ok := numberOf(branch)
	_, rest, _ := strings.Cut(branch, "-")
	return ok && n == want && strings.HasPrefix(rest, m[2])
}

// blockerNumbers reads a "Blocked by" line as /to-tickets writes it,
// "numbers/titles": the number starting each comma-, semicolon- or
// "and"-separated item, so "01 (OAuth 2 flow), 03" gives 01 and 03 and a
// date or a title gives nothing. Each number appears once.
func blockerNumbers(line string) []string {
	line = parenthesised.ReplaceAllString(line, "")
	var out []string
	for _, item := range itemSeparator.Split(line, -1) {
		m := leadingNumber.FindStringSubmatch(strings.TrimSpace(item))
		if m != nil && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
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
