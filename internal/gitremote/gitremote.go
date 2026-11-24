// Package gitremote reads a git remote URL as a GitHub repo, so fh can link
// to it. It is pure logic: the caller runs git (see worktree.Origin).
package gitremote

import (
	"net/url"
	"regexp"
	"strings"
)

// GitHub is a repo on github.com.
type GitHub struct {
	Owner string
	Repo  string
}

// URL is the repo's page.
func (g GitHub) URL() string {
	return "https://github.com/" + g.Owner + "/" + g.Repo
}

// BranchURL is a branch's page. Each path segment is escaped: git allows
// '#', '%' and '?' in branch names, which would otherwise end the path.
func (g GitHub) BranchURL(branch string) string {
	parts := strings.Split(branch, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return g.URL() + "/tree/" + strings.Join(parts, "/")
}

// GitHub's own rules: an owner is letters, digits and single hyphens; a repo
// name is letters, digits, '.', '-' and '_'. Checking them also keeps
// anything odd in a remote URL out of the links fh builds from it.
var (
	ownerRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)
	repoRE  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// ParseGitHub returns the GitHub repo a remote URL points at. It accepts the
// forms `git clone` takes for github.com, with or without .git:
//
//	git@github.com:owner/repo.git
//	ssh://git@github.com/owner/repo.git
//	https://github.com/owner/repo
//
// ok is false for another host (GitLab, a self-hosted server), a local path,
// or a path that is not exactly owner/repo.
func ParseGitHub(remote string) (g GitHub, ok bool) {
	remote = strings.TrimSpace(remote)
	var host, path string
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return GitHub{}, false
		}
		switch u.Scheme {
		case "https", "http", "ssh", "git", "git+ssh", "ssh+git":
		default:
			return GitHub{}, false
		}
		host, path = u.Hostname(), u.Path
	} else {
		// scp-like syntax, [user@]host:path. A colon after a slash is a
		// local path, as git itself reads it.
		before, after, found := strings.Cut(remote, ":")
		if !found || strings.Contains(before, "/") {
			return GitHub{}, false
		}
		if i := strings.LastIndex(before, "@"); i >= 0 {
			before = before[i+1:]
		}
		host, path = before, after
	}
	if !strings.EqualFold(host, "github.com") {
		return GitHub{}, false
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	owner, repo, found := strings.Cut(path, "/")
	if !found || !ownerRE.MatchString(owner) || !repoRE.MatchString(repo) || repo == "." || repo == ".." {
		return GitHub{}, false
	}
	return GitHub{Owner: owner, Repo: repo}, true
}
