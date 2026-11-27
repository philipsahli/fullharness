package tracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The writes fh runs itself (ADR 0008), one function each. Every write
// takes the Project's repo and a Ref as the board serves it ("#42" on
// GitHub, ".scratch/<feature>/…md" in local markdown), so a caller needs no
// knowledge of which tracker the Project has.

// ErrBadRef: the ref names no Spec or Ticket fh may write in this Project.
var ErrBadRef = errors.New("not a Spec or Ticket of this Project")

// ErrNoWrites: the Project's tracker is one fh can't write (none or
// unsupported).
var ErrNoWrites = errors.New("fh can't write to this Project's Issue tracker")

// MarkReady labels the Spec or Ticket ref ready-for-agent. On GitHub it
// adds the label and removes needs-triage and needs-info; in local
// markdown it sets the file's Status line. A gh failure comes back with
// gh's own message.
func MarkReady(ctx context.Context, repo, ref string) error {
	switch kind, _ := Detect(repo); kind {
	case KindGitHub:
		n, ok := issueNumber(ref)
		if !ok {
			return fmt.Errorf("%w: %q", ErrBadRef, ref)
		}
		_, err := gh(ctx, repo, "issue", "edit", n, "--add-label", ReadyLabel,
			"--remove-label", "needs-triage", "--remove-label", "needs-info")
		return err
	case KindLocal:
		path, ok := localItem(repo, ref)
		if !ok {
			return fmt.Errorf("%w: %q", ErrBadRef, ref)
		}
		return setLine(path, "Status", ReadyLabel)
	default:
		return ErrNoWrites
	}
}

// ErrBadGroup: the group is not a Roadmap group fh knows.
var ErrBadGroup = errors.New("not a Roadmap group: send now, next or later")

// roadmapGroups are the Roadmap groups (ADR 0005), with the colour of
// their label: the Autopilot status colours of running, idle and off.
var roadmapGroups = map[string]string{RoadmapNow: "6a2fd6", "next": "0b6e85", "later": "5f6b7a"}

// SetRoadmap moves the Spec ref to the Roadmap group to (ADR 0005). On
// GitHub it adds roadmap:<to> and removes roadmap:<from>, the group the
// board last read for it (empty for none); a label the repo lacks is
// created the first time and the edit run again. In local markdown it
// sets the spec.md's Roadmap line, and from isn't needed. A gh failure
// comes back with gh's own message.
func SetRoadmap(ctx context.Context, repo, ref, from, to string) error {
	if _, ok := roadmapGroups[to]; !ok {
		return fmt.Errorf("%w: %q", ErrBadGroup, to)
	}
	switch kind, _ := Detect(repo); kind {
	case KindGitHub:
		n, ok := issueNumber(ref)
		if !ok {
			return fmt.Errorf("%w: %q", ErrBadRef, ref)
		}
		label := "roadmap:" + to
		args := []string{"issue", "edit", n, "--add-label", label}
		if from != "" && from != to {
			args = append(args, "--remove-label", "roadmap:"+from)
		}
		_, err := gh(ctx, repo, args...)
		// gh names a label the repo lacks "'<name>' not found".
		if err != nil && strings.Contains(err.Error(), "'"+label+"' not found") {
			// --force: another fh, or a person, may have made it meanwhile.
			if _, cerr := gh(ctx, repo, "label", "create", label, "--color", roadmapGroups[to],
				"--description", "Roadmap group, set by fullharness (ADR 0005)", "--force"); cerr != nil {
				return cerr
			}
			_, err = gh(ctx, repo, args...)
		}
		return err
	case KindLocal:
		path, ok := localItem(repo, ref)
		if !ok || filepath.Base(path) != "spec.md" {
			return fmt.Errorf("%w: %q", ErrBadRef, ref)
		}
		return setLine(path, "Roadmap", to)
	default:
		return ErrNoWrites
	}
}

var issueRef = regexp.MustCompile(`^#?([1-9]\d*)$`)

func issueNumber(ref string) (string, bool) {
	m := issueRef.FindStringSubmatch(ref)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// localRef is a Spec or Ticket file as ReadLocal names it.
var localRef = regexp.MustCompile(`^\.scratch/([^/]+)/(spec\.md|issues/[^/]+\.md)$`)

// localItem is the file ref names under repo, if ref is a Spec or Ticket
// file that exists: a ref can't leave .scratch, and a link isn't followed.
func localItem(repo, ref string) (string, bool) {
	m := localRef.FindStringSubmatch(ref)
	// The pattern keeps every part to one path segment; only the feature
	// folder could still be "." or "..".
	if m == nil || m[1] == "." || m[1] == ".." {
		return "", false
	}
	path := filepath.Join(repo, filepath.FromSlash(ref))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	// A linked folder on the way could still lead out of .scratch.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	root, err := filepath.EvalSymlinks(filepath.Join(repo, ".scratch"))
	if err != nil || !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", false
	}
	return path, true
}

// setLine rewrites the file's first name line, such as Status or Roadmap
// (the one ReadLocal reads), to value, keeping its bold or plain form and
// every other byte. A file without one gets a bold line after its heading.
// The file is replaced in one rename, so a reader never sees half of it.
func setLine(path, name, value string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	done := false
	for i, line := range lines {
		text := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(text)]
		end := "" // a CRLF file keeps its \r
		if strings.HasSuffix(text, "\r") {
			end = "\r"
		}
		for _, prefix := range []string{"**" + name + ":**", name + ":"} {
			if strings.HasPrefix(text, prefix) {
				lines[i] = indent + prefix + " " + value + end
				done = true
				break
			}
		}
		if done {
			break
		}
	}
	if !done {
		at := 0 // no heading: the line goes first
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "# ") {
				at = i + 1
				break
			}
		}
		add := []string{"**" + name + ":** " + value, ""}
		if at > 0 {
			add = []string{"", "**" + name + ":** " + value}
		}
		lines = append(lines[:at], append(add, lines[at:]...)...)
	}
	return replaceFile(path, []byte(strings.Join(lines, "\n")))
}

// replaceFile writes data to a temporary file beside path, with path's
// mode, and renames it over path.
func replaceFile(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".fh-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after the rename; on failure, the leftover
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() // the write error is the one to report
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
