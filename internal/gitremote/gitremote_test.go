package gitremote_test

import (
	"testing"

	"github.com/philipsahli/fullharness/internal/gitremote"
)

func TestParseGitHub(t *testing.T) {
	shop := gitremote.GitHub{Owner: "someone", Repo: "shop"}
	for _, tc := range []struct {
		remote string
		want   gitremote.GitHub
		ok     bool
	}{
		{"git@github.com:someone/shop.git", shop, true},
		{"git@github.com:someone/shop", shop, true},
		{"https://github.com/someone/shop.git", shop, true},
		{"https://github.com/someone/shop", shop, true},
		{"https://github.com/someone/shop/", shop, true},
		{"ssh://git@github.com/someone/shop.git", shop, true},
		{"ssh://git@github.com:22/someone/shop.git", shop, true},
		{"https://token@github.com/someone/shop.git", shop, true},
		{"  git@github.com:someone/shop.git\n", shop, true}, // as git prints it
		{"git@GitHub.com:someone/shop.git", shop, true},
		{"git@github.com:some-one/my.repo_2.git", gitremote.GitHub{Owner: "some-one", Repo: "my.repo_2"}, true},

		{"git@gitlab.com:someone/shop.git", gitremote.GitHub{}, false},
		{"https://gitlab.com/someone/shop.git", gitremote.GitHub{}, false},
		{"https://github.example.com/someone/shop.git", gitremote.GitHub{}, false},
		{"https://notgithub.com/someone/shop", gitremote.GitHub{}, false},
		{"/srv/git/shop.git", gitremote.GitHub{}, false},
		{"./a:b/c", gitremote.GitHub{}, false},
		{"file:///github.com/someone/shop.git", gitremote.GitHub{}, false},
		{"https://github.com/someone", gitremote.GitHub{}, false},
		{"https://github.com/someone/shop/tree/main", gitremote.GitHub{}, false},
		{"https://github.com/some%22one/shop", gitremote.GitHub{}, false},
		{"https://github.com/someone/..", gitremote.GitHub{}, false},
		{"", gitremote.GitHub{}, false},
	} {
		got, ok := gitremote.ParseGitHub(tc.remote)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseGitHub(%q) = %v, %v; want %v, %v", tc.remote, got, ok, tc.want, tc.ok)
		}
	}
}

func TestGitHubURL(t *testing.T) {
	if got := (gitremote.GitHub{Owner: "someone", Repo: "shop"}).URL(); got != "https://github.com/someone/shop" {
		t.Errorf("URL() = %q", got)
	}
}

func TestGitHubBranchURL(t *testing.T) {
	shop := gitremote.GitHub{Owner: "someone", Repo: "shop"}
	for branch, want := range map[string]string{
		"main":         "https://github.com/someone/shop/tree/main",
		"feature/x":    "https://github.com/someone/shop/tree/feature/x",
		"fix#12/a%b?c": "https://github.com/someone/shop/tree/fix%2312/a%25b%3Fc",
	} {
		if got := shop.BranchURL(branch); got != want {
			t.Errorf("BranchURL(%q) = %q, want %q", branch, got, want)
		}
	}
}
