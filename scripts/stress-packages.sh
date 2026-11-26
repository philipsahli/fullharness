#!/bin/sh
# Prints the Go packages make stress runs: every package with tests that
# depends on a package changed against origin/main, committed or not. The
# API tests in internal/app start all of fh, so a change to internal/session
# also stresses them; that is where timing bugs show (#153). A change
# outside internal/ prints ./... . STRESS_BASE changes the base (default
# origin/main).
set -eu
changed=$( (git diff --name-only "${STRESS_BASE:-origin/main}"...HEAD; git diff --name-only HEAD; git ls-files --others --exclude-standard) | sort -u)
if [ -z "$changed" ]; then
	exit 0
fi
if echo "$changed" | grep -qvE '^(internal/|web/|docs/|.*\.md$)'; then
	echo ./...
	exit 0
fi
dirs=$(echo "$changed" | grep -E '^internal/.*\.go$' | xargs -n1 dirname 2>/dev/null | sort -u)
[ -z "$dirs" ] && exit 0
mod=$(go list -m)
pkgs=$(for d in $dirs; do echo "$mod/$d"; done)
go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./... | sed '/^$/d' | while read -r p; do
	deps=$(go list -test -deps "$p")
	for c in $pkgs; do
		if echo "$deps" | grep -qxF "$c"; then
			echo "$p"
			break
		fi
	done
done
