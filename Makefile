# make build: build the UI into web/dist, then a binary at bin/fh that embeds it.
# make dev:   Vite dev server on 127.0.0.1:5173 proxied to a Go server on
#             DEV_PORT, with its own state folder so it never touches a running fh.
#             scripts/devwatch rebuilds and restarts fh (--dev) on Go changes;
#             open tabs reload by themselves.
# make lint:  Go and UI linters, including the UI typecheck (ADR 0007). Versions
#             are pinned in tools/go.mod (Go) and web/package.json (UI).
# make test:  Go tests with the race detector (the API tests in internal/app),
#             then the 10-Session load test without it.
# make e2e:   Playwright smoke tests in Chromium against a fresh bin/fh. The
#             first run downloads Chromium; CI sets PLAYWRIGHT_INSTALL_FLAGS=--with-deps
#             to also install its system libraries on Linux.
# make check: everything CI runs. Run it before opening a PR.
# make smoke: the release smoke test against the real Claude Code, Copilot CLI
#             and OpenCode (scripts/smoke). Needs them installed and logged in,
#             spends real tokens, never runs in CI (ADR 0007). Run before tagging.

DEV_PORT ?= 4816
DEV_DIR  ?= $(CURDIR)/.dev
# Lint tools are built once per tools/go.mod + tools/go.sum into a folder named
# by their hash, so CI can cache them under that key: a go.sum-only change (most
# Dependabot PRs) reuses them instead of building golangci-lint from source
# (about 2 min). A folder name, not file times, decides freshness, because a
# cache restore and a fresh checkout leave times that make can't compare.
TOOLS_HASH := $(shell cat tools/go.mod tools/go.sum | shasum -a 256 | cut -c1-12)
TOOLS      := bin/tools/$(TOOLS_HASH)
PLAYWRIGHT_INSTALL_FLAGS ?=

.PHONY: ui build dev lint test e2e check smoke web-deps install

web/node_modules: web/package-lock.json
	cd web && npm ci
	touch web/node_modules

web-deps: web/node_modules

# ui builds the UI into web/dist, which fh embeds. GoReleaser runs it too.
ui: web-deps
	cd web && npm run build
	touch web/dist/.gitkeep  # vite empties dist/; keep the placeholder go:embed needs

build: ui
	go build -o bin/fh ./cmd/fh

# install copies fh onto PATH, to run it from source.
PREFIX ?= $(HOME)/.local/bin
install: build
	mkdir -p "$(PREFIX)"
	install -m 0755 bin/fh "$(PREFIX)/fh"
	@case ":$$PATH:" in *":$(PREFIX):"*) echo "installed $(PREFIX)/fh" ;; *) echo "installed $(PREFIX)/fh; $(PREFIX) is not on your PATH, add it to run fh" ;; esac

dev: web-deps
	@trap 'kill 0' EXIT; \
	(cd web && FH_PORT=$(DEV_PORT) npm run dev) & \
	go run ./scripts/devwatch --port $(DEV_PORT) --dir $(DEV_DIR) --ui-url http://127.0.0.1:5173

$(TOOLS)/golangci-lint $(TOOLS)/govulncheck:
	go build -modfile=tools/go.mod -o $(TOOLS)/ \
		github.com/golangci/golangci-lint/v2/cmd/golangci-lint \
		golang.org/x/vuln/cmd/govulncheck

lint: web-deps $(TOOLS)/golangci-lint $(TOOLS)/govulncheck
	$(TOOLS)/golangci-lint run ./...
	$(TOOLS)/golangci-lint fmt --diff ./...
	$(TOOLS)/govulncheck ./...
	cd web && npm run typecheck && npm run lint && npm run format:check

test:
	go test -race ./...
	# The load test is built only without -race (see internal/app/load_test.go).
	go test -count=1 -v -run TestTenStreamingSessions ./internal/app/

e2e: build
	go build -o bin/fakeagent ./internal/acp/fakeagent  # the Agent the e2e Sessions talk to
	cd web && npx playwright install $(PLAYWRIGHT_INSTALL_FLAGS) chromium
	cd web && npm run e2e

check: lint test e2e

smoke:
	go run ./scripts/smoke
