package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ghTimeout bounds one gh call, so a hung gh can't hold a board forever.
const ghTimeout = 30 * time.Second

// Reasons a gh read fails, each with what the user does about it.
var (
	ErrNoGh    = errors.New("`gh` not found; install the GitHub CLI: brew install gh")
	ErrGhLogin = errors.New("`gh` is not logged in: run `gh auth login`")
)

// issuesQuery pages through every issue (gh api --paginate fills
// $endCursor). 50 per page keeps GitHub's node limit with the nested
// connections.
const issuesQuery = `query($owner: String!, $repo: String!, $endCursor: String) {
  repository(owner: $owner, name: $repo) {
    issues(first: 50, after: $endCursor, states: [OPEN, CLOSED]) {
      nodes {
        number title state body
        labels(first: 20) { nodes { name } }
        parent { number }
        subIssues(first: 50) { nodes { number } }
        blockedBy(first: 20) { nodes { number state } }
      }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

// CheckGh returns ErrNoGh or ErrGhLogin when gh can't read GitHub.
func CheckGh(repo string) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return ErrNoGh
	}
	// Any auth status failure reads as "not logged in": it is the usual
	// cause, and the note's command is what fixes it.
	if _, err := gh(repo, "auth", "status"); err != nil {
		return ErrGhLogin
	}
	return nil
}

// Note is what the board says for a failed gh read: the error itself when
// it says what to do, else that GitHub couldn't be read and why.
func Note(err error) string {
	if errors.Is(err, ErrNoGh) || errors.Is(err, ErrGhLogin) {
		return err.Error()
	}
	return "Couldn't read GitHub: " + err.Error()
}

// FetchIssues reads every issue of the repo's GitHub repository through
// the user's gh login; gh finds the repository from the git remote.
func FetchIssues(repo string) ([]Issue, error) {
	out, err := gh(repo, "api", "graphql", "--paginate", "--slurp",
		"-F", "owner={owner}", "-F", "repo={repo}", "-f", "query="+issuesQuery)
	if err != nil {
		return nil, err
	}
	var pages []struct {
		Data struct {
			Repository struct {
				Issues struct {
					Nodes []Issue `json:"nodes"`
				} `json:"issues"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &pages); err != nil {
		return nil, fmt.Errorf("reading gh's issues: %w", err)
	}
	var issues []Issue
	for _, p := range pages {
		issues = append(issues, p.Data.Repository.Issues.Nodes...)
	}
	return issues, nil
}

// prLimit caps the PR read at the most recent PRs: enough for every open
// one, while a long-closed Ticket's old Merged PR may drop off (#127).
const prLimit = "500"

// FetchPRs reads the repository's PRs in every state, the prLimit most
// recent, with what a PRRecord needs.
func FetchPRs(repo string) ([]PR, error) {
	out, err := gh(repo, "pr", "list", "--state", "all", "--limit", prLimit,
		"--json", "number,headRefName,body,state,isDraft,url,statusCheckRollup")
	if err != nil {
		return nil, err
	}
	var prs []PR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("reading gh's PRs: %w", err)
	}
	return prs, nil
}

func gh(repo string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = repo
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("gh %s: %s", args[0], msg)
		}
		return nil, fmt.Errorf("gh %s: %w", args[0], err)
	}
	return out, nil
}

// HasGitHubRemote reports whether one of the repo's remotes is on GitHub.
func HasGitHubRemote(repo string) bool {
	out, err := exec.Command("git", "-C", repo, "remote", "-v").Output()
	return err == nil && strings.Contains(string(out), "github.com")
}
