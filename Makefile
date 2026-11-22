# make build: build the UI into web/dist, then a binary at bin/fh that embeds it.
# make dev:   Vite dev server on 127.0.0.1:5173 proxied to a Go server on
#             DEV_PORT, with its own state folder so it never touches a running fh.
# make lint:  Go and UI linters, including the UI typecheck (ADR 0007). Versions
#             are pinned in tools/go.mod (Go) and web/package.json (UI).
# make test:  Go tests with the race detector (the API tests in internal/app).
# make check: everything CI runs. Run it before opening a PR.

DEV_PORT ?= 4816
DEV_DIR  ?= $(CURDIR)/.dev
GOTOOL   := go tool -modfile=tools/go.mod

.PHONY: build dev lint test check web-deps

web/node_modules: web/package-lock.json
	cd web && npm ci
	touch web/node_modules

web-deps: web/node_modules

build: web-deps
	cd web && npm run build
	touch web/dist/.gitkeep  # vite empties dist/; keep the placeholder go:embed needs
	go build -o bin/fh ./cmd/fh

dev: web-deps
	@trap 'kill 0' EXIT; \
	(cd web && FH_PORT=$(DEV_PORT) npm run dev) & \
	go run ./cmd/fh --port $(DEV_PORT) --dir $(DEV_DIR) --ui-url http://127.0.0.1:5173

lint: web-deps
	$(GOTOOL) golangci-lint run ./...
	$(GOTOOL) golangci-lint fmt --diff ./...
	$(GOTOOL) govulncheck ./...
	cd web && npm run typecheck && npm run lint && npm run format:check

test:
	go test -race ./...

check: lint test
