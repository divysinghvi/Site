# divy.dev — every dev/CI/deploy entry point. Run `make help`.
SHELL := bash
.ONESHELL:
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE      ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BRANCH    ?= $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)
BUILDUSER ?= $(shell whoami 2>/dev/null || echo unknown)
LDFLAGS    = -s -w -X divy.dev/internal/version.Version=$(VERSION) -X divy.dev/internal/version.Commit=$(COMMIT) -X divy.dev/internal/version.Date=$(DATE) -X divy.dev/internal/version.Branch=$(BRANCH) -X divy.dev/internal/version.BuildUser=$(BUILDUSER)
GOFLAGS   ?= -trimpath
PROMTOOL  ?= $(shell command -v promtool 2>/dev/null || echo "go run github.com/prometheus/prometheus/cmd/promtool@latest")
DEV_ADDR  ?= 127.0.0.1:8080
CHECK_ADDR ?= 127.0.0.1:18081
IMAGE     ?= ghcr.io/divysinghvi/site
export CGO_ENABLED = 0

.PHONY: help setup dev api web-dev web-build web-check web-gen web-e2e web-shots gen gen-check test lint validate todos promtool-check build vercel-build docker clean ascii migrate collect-once

help: ## print this table
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[1m%-16s\033[0m %s\n", $$1, $$2}'

setup: ## first run: .env, go modules, web deps (when web/ exists)
	cp -n .env.example .env || true
	go mod download
	if [ -d web ]; then (cd web && npm ci); fi

# Load .env into the recipe environment when it exists.
define load_env
set -a; [ -f .env ] && source .env; set +a
endef

dev: ## api with --collect on :8080 + web dev server (web/ optional)
	$(load_env)
	if [ -d web ]; then $(MAKE) -j2 api web-dev; else echo "web/ not present yet: starting the API only"; $(MAKE) api; fi

api: ## run the API from source on $(DEV_ADDR) with the collector scheduler
	$(load_env)
	go run ./cmd/api serve --addr $(DEV_ADDR) --collect

## ---- web targets (the web lane edits this section) ----
WEB_SHOTS_DIR ?= web/.playwright/shots

web-dev: ## vite dev server on :5173 (proxies /api, /metrics, /loki, /healthz, /readyz, /favicon.svg, /og, /robots.txt, /ascii to the API)
	$(load_env)
	cd web && API_BASE="$${API_BASE:-http://$(DEV_ADDR)}" npm run dev

web-build: ## build the SvelteKit app into internal/web/dist (prerenders against a throwaway API on $(CHECK_ADDR))
	$(load_env)
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/divy-pass1 ./cmd/api
	tmp=$$(mktemp -d)
	origin="$${SITE_ORIGIN:-http://localhost:5173}"
	bin/divy-pass1 serve --addr $(CHECK_ADDR) --db "file:$$tmp/build.db" --content ./content --site-origin "$$origin" & pid=$$!
	trap 'kill $$pid 2>/dev/null || true' EXIT
	for i in $$(seq 1 30); do bin/divy-pass1 ping --url http://$(CHECK_ADDR)/readyz >/dev/null 2>&1 && break; sleep 1; done
	bin/divy-pass1 ping --url http://$(CHECK_ADDR)/readyz >/dev/null
	if [ ! -d web/node_modules ]; then (cd web && npm ci); fi
	cd web && API_BASE=http://$(CHECK_ADDR) PUBLIC_SITE_ORIGIN="$$origin" npm run build

web-check: ## svelte-check (0 errors, 0 warnings) + prettier --check
	cd web && npm run check && npm run lint

web-gen: ## regenerate web/src/lib/api/types.gen.ts from schema/index.schema.json
	cd web && npm run gen:types

web-e2e: ## Playwright smoke suite against the embedded site served by a fresh binary on $(CHECK_ADDR)
	$(MAKE) web-build
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/divy ./cmd/api
	tmp=$$(mktemp -d)
	bin/divy serve --addr $(CHECK_ADDR) --db "file:$$tmp/e2e.db" --content ./content --site-origin http://$(CHECK_ADDR) & pid=$$!
	trap 'kill $$pid 2>/dev/null || true' EXIT
	for i in $$(seq 1 30); do bin/divy ping --url http://$(CHECK_ADDR)/readyz >/dev/null 2>&1 && break; sleep 1; done
	cd web && E2E_BASE_URL=http://$(CHECK_ADDR) npx playwright test

web-shots: ## reference screenshots of the hero trace into $(WEB_SHOTS_DIR) (needs a running site; SHOTS_BASE_URL, default :5173)
	cd web && SHOTS_BASE_URL="$${SHOTS_BASE_URL:-http://127.0.0.1:5173}" SHOTS_DIR="$(abspath $(WEB_SHOTS_DIR))" npm run shots
## ---- end web targets ----

gen: ## regenerate schema/*.schema.json (and the TS types when web/ exists)
	go run ./cmd/api schemagen --out ./schema
	if [ -d web ] && [ -f web/package.json ]; then $(MAKE) web-gen; fi

gen-check: ## CI drift guard for generated files
	go run ./cmd/api schemagen --out ./schema --check
	if [ -d web ] && [ -f web/src/lib/api/types.gen.ts ]; then $(MAKE) web-gen; fi
	git diff --exit-code -- schema $$( [ -f web/src/lib/api/types.gen.ts ] && echo web/src/lib/api/types.gen.ts )

test: ## go test (with -race when a C compiler is available) + web unit tests when web/ exists
	if command -v gcc >/dev/null || command -v clang >/dev/null; then CGO_ENABLED=1 go test -race -count=1 ./...; else go test -count=1 ./...; fi
	if [ -d web ] && [ -f web/package.json ]; then (cd web && npm test --if-present); fi

lint: ## gofmt, go vet, golangci-lint (+ web lint when web/ exists)
	test -z "$$(gofmt -l cmd internal)" || { gofmt -l cmd internal; echo "gofmt: files need formatting"; exit 1; }
	go vet ./...
	if command -v golangci-lint >/dev/null; then golangci-lint run ./...; fi
	if [ -d web ] && [ -f web/package.json ]; then (cd web && npm run lint --if-present); fi

validate: ## validate content/ (--strict: warnings fail)
	go run ./cmd/api validate --content ./content --strict

todos: ## list every TODO(divy) in content/
	go run ./cmd/api validate --content ./content --list-todos

promtool-check: ## build bin/divy, serve it on $(CHECK_ADDR) with a temp DB, lint /metrics with promtool, lint content/alerts.yaml
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/divy ./cmd/api
	tmp=$$(mktemp -d)
	bin/divy serve --addr $(CHECK_ADDR) --db "file:$$tmp/promtool.db" --content ./content --log-level warn & pid=$$!
	trap 'kill $$pid 2>/dev/null || true; rm -rf "$$tmp"' EXIT
	for i in $$(seq 1 30); do bin/divy ping --url http://$(CHECK_ADDR)/readyz >/dev/null 2>&1 && break; sleep 1; done
	bin/divy ping --url http://$(CHECK_ADDR)/readyz >/dev/null
	curl -fsS http://$(CHECK_ADDR)/metrics | $(PROMTOOL) check metrics
	$(PROMTOOL) check rules content/alerts.yaml

build: ## build bin/divy with the embedded site (run `make web-build` first for a full site)
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/divy ./cmd/api

vercel-build: ## two-pass Vercel build: binary → prerender web → final binary at $$VERCEL_OUTPUT_FILE
	if [ -d web ] && [ -f web/package.json ]; then $(MAKE) web-build; else echo "web/ not present: building the API without an embedded site"; fi
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$${VERCEL_OUTPUT_FILE:-bin/divy}" ./cmd/api

docker: ## build the container image (deploy/Dockerfile lands in Phase 5)
	docker build -f deploy/Dockerfile --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

ascii: ## print the ASCII career trace
	go run ./cmd/api export-ascii --content ./content

migrate: ## apply pending migrations and print the status table
	$(load_env)
	go run ./cmd/api migrate

collect-once: ## run one collection round against the configured database
	$(load_env)
	go run ./cmd/api collect --once

clean: ## remove build output
	rm -rf bin web/build web/.svelte-kit internal/web/dist/* && touch internal/web/dist/.gitkeep
