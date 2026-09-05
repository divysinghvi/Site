# Repo, toolchain, build/dev loop, type safety, frontend delivery, CI/deploy

## Cross-section notes

| # | Note | Affects |
|---|------|---------|
| R1 | **Go version.** CONVENTIONS baseline says Go 1.24. The pinned dependencies do not build there: `go.opentelemetry.io/otel v1.46.0` declares `go 1.26.0`; `client_golang v1.24.1` and `modernc.org/sqlite v1.58.0` declare `go 1.25.0`. `api/go.mod` therefore says `go 1.26.0` / `toolchain go1.26.8`. The local Go 1.24.7 still works: `GOTOOLCHAIN=auto` (the default) downloads and runs go1.26.8 on first use. Brief requires "Go 1.23+", satisfied. | API, deploy, CI |
| R2 | **Client IP.** `chi/v5 v5.3.2` marks `middleware.RealIP` deprecated ("vulnerable to IP spoofing"). CONVENTIONS #10 is implemented with `middleware.ClientIPFromXFF(TRUSTED_PROXIES...)` and read through `middleware.GetClientIP(ctx)`; `r.RemoteAddr` is never mutated. | API |
| R3 | **One extra subcommand, `divy ping`** (`GET <url>`, exit 0 on 2xx). Needed because the runtime image is distroless (no shell, no curl) and Docker `HEALTHCHECK`, compose `depends_on: condition: service_healthy`, and the Makefile wait loops all need a probe. | API, deploy |
| R4 | **Schema files.** In addition to the per-content schemas named in the Content section (`schema/spans.schema.json`, …), `divy schemagen` writes `schema/api.schema.json` (every API response envelope) and `schema/index.schema.json` (the union of all `$defs`). TypeScript is generated from `index.schema.json` only, into one file. | Content, API |
| R5 | **URL state** adopts the Content section's formats verbatim: dashboard `/dashboard#range=7d&layout=<base64url JSON>` (only overridden panels), span deep link `/#trace?span=<span id>`. | Content, frontend |
| R6 | **Build-time env** for the web: `API_BASE` (where the prerenderer fetches) and `PUBLIC_SITE_ORIGIN` (absolute `og:url`/`og:image`/canonical). SvelteKit reads the repo-root `.env` (`kit.env.dir: '..'`, `privatePrefix: 'API_'`, `publicPrefix: 'PUBLIC_'`), so one `.env.example` serves Go, web and compose; the web build sees only `API_*` and `PUBLIC_*`. | Deploy |
| R7 | **Endpoints the frontend needs beyond the brief's list** (the API contract table is authoritative): `GET /loki/api/v1/labels`, `GET /loki/api/v1/label/{name}/values` (already proposed by the Content section), `GET /api/v1/label/__name__/values` (PromQL autocomplete; may be derived from `/api/v1/series`), and `GET /api/uptime/heartbeats?days=90` returning hourly buckets per target (`{target, buckets:[{ts, up_ratio, avg_latency_ms, samples}]}`) — a 90-day × 5-minute raw series is 25,920 points per target, too many for `/api/v1/query_range` and pointless to ship to a phone. | API |
| R8 | **Absolute URLs from the API** (`og_image`, the curl line in "View query", `robots.txt`) use `DIVY_PUBLIC_ORIGIN`. | API |
| R9 | **Postmortem bodies** arrive as sanitized HTML from `/api/content/postmortems/{id}` (Content §C.5.4); the web ships no markdown renderer. | Content |

## R.1 Repository layout

```
.
├── README.md                     One-command local run, one-command deploy, "add divy.dev as a Prometheus data source" note
├── LICENSE                       TODO(divy): choose a license (MIT suggested; affects nothing else)
├── Makefile                      Every dev/CI/deploy entry point (§R.4); the only file CI and humans call directly
├── .env.example                  All env vars with safe local defaults (§R.3.3); copied to .env (git-ignored)
├── .gitignore                    .env, data/, bin/, web/build, web/node_modules, api/internal/web/dist, .playwright, .lighthouseci
├── .dockerignore                 same list + .git, docs/
├── .editorconfig                 tabs for Go, 2-space for YAML/TS/Svelte, LF
├── .github/
│   ├── dependabot.yml            weekly: gomod (api/), npm (web/), github-actions, docker (deploy/)
│   └── workflows/
│       ├── ci.yml                PR + main: api, web, build-e2e jobs (§R.8.1)
│       └── release.yml           tag v*: build+push image to GHCR, deploy to Hetzner (§R.8.2)
├── api/                          Go module `divy.dev/api` — the single binary `divy`
│   ├── go.mod / go.sum           go 1.26.0, toolchain go1.26.8
│   ├── .golangci.yml             golangci-lint v2 config (errcheck, govet, staticcheck, revive, gosec, misspell)
│   ├── cmd/divy/
│   │   ├── main.go               subcommand dispatch (stdlib flag; no CLI framework), version ldflags
│   │   ├── serve.go              `divy serve [--collect]`
│   │   ├── collect.go            `divy collect [--once] [--only …]`
│   │   ├── validate.go           `divy validate [--strict] [--json] [--todos] [dir]`
│   │   ├── schemagen.go          `divy schemagen [--out dir] [--check]`
│   │   ├── migrate.go            `divy migrate [--status] [--to N]`
│   │   ├── export_ascii.go       `divy export-ascii [--width 80]`
│   │   └── ping.go               `divy ping [--url]` (healthcheck probe)
│   ├── internal/
│   │   ├── config/               env + flag parsing into one `Config` struct; `.env` is NOT read by the binary (the shell/compose does that)
│   │   ├── version/              `Version`, `Commit`, `Date` vars set by `-ldflags -X`; feeds `divy_build_info`
│   │   ├── model/                Go structs = source of truth for content files and API envelopes; `SchemaRoots` registry (§R.5)
│   │   ├── schema/               `Generate()` (invopop reflector) and `Validator` (santhosh-tekuri) used by schemagen, validate and serve
│   │   ├── content/              loads content/ (YAML, NDJSON, Markdown+frontmatter), runs the Content-section rules, exposes the in-memory content model, TODO inventory, goldmark rendering
│   │   ├── store/                SQLite (modernc) open/pragmas, single writer, migrations (`migrations/0001_init.sql` … embedded), series/samples/probe/otel/collector_runs queries, retention
│   │   ├── promql/               lexer, parser, AST, evaluator for the supported subset; table-driven tests
│   │   ├── logql/                stream selector + line filters + `| json`; table-driven tests
│   │   ├── collector/            scheduler + github/, pypi/, uptime/ collectors, retention job, cumulative backfill
│   │   ├── trace/                career trace builder (fixed ids), OTel SDK setup, SQLite SpanExporter, Jaeger JSON shaping
│   │   ├── metrics/              client_golang registry, custom collectors that expose the latest stored sample of every series, HTTP metrics
│   │   ├── server/               chi router, middleware (client IP, rate limit, cache, ETag, CORS, trace header), handlers per endpoint family, static handler (§R.6.3), content negotiation on `/`
│   │   ├── ascii/                ASCII waterfall renderer (also used by export-ascii)
│   │   ├── og/                   OG PNG renderer (gg + embedded TTFs; §R.6.6)
│   │   └── web/                  `web.go` (`//go:embed all:dist`), `web_noweb.go` (build tag `noweb`), `dist/` = copy of web/build (git-ignored)
│   └── testdata/                 golden files: exposition output, Jaeger JSON, PromQL/LogQL cases
├── collector/
│   └── README.md                 "The collector is not a separate module; it is api/internal/collector and runs inside `divy serve --collect`. See docs/PLAN.md."
├── content/                      The only place prose lives (Content section)
│   ├── spans.yaml · logs.ndjson · panels.yaml · alerts.yaml · uptime.yaml · manual_metrics.yaml · profile.yaml
│   ├── postmortems/INC-001.md … INC-004.md
│   └── README.md
├── schema/                       GENERATED by `divy schemagen`, committed, CI fails on drift
│   ├── spans.schema.json · logs.schema.json · postmortem.schema.json · panels.schema.json · alerts.schema.json
│   ├── uptime.schema.json · manual_metrics.schema.json · profile.schema.json
│   ├── api.schema.json           all API response/error envelopes
│   └── index.schema.json         union of every $defs above → the single TS file
├── web/                          SvelteKit 2 + Svelte 5 (runes) + TypeScript + Tailwind v4
│   ├── package.json / package-lock.json   scripts: dev, build, preview, check, lint, format, test, e2e, gen:types, lighthouse
│   ├── .nvmrc                    `24`
│   ├── svelte.config.js          adapter-static (pages/assets `build`, fallback `spa.html`, strict), env dir/prefixes, alias
│   ├── vite.config.ts            sveltekit() + tailwindcss(); `server.proxy` table (§R.6.4); `server.port 5173`, `strictPort`
│   ├── tsconfig.json · eslint.config.js · .prettierrc · .prettierignore (types.gen.ts)
│   ├── playwright.config.ts      e2e against a running binary (`E2E_BASE_URL`, default http://127.0.0.1:18080)
│   ├── lighthouserc.json         LHCI assertions (§R.9.6)
│   ├── scripts/build-with-api.mjs  starts the noweb binary, waits for /readyz, runs `vite build`, stops it (§R.6.2)
│   ├── src/
│   │   ├── app.html              lang, viewport, theme bootstrap script, font preloads, favicon.svg link
│   │   ├── app.css               `@import "tailwindcss"`, @font-face, theme tokens (Grafana-11-inspired palette, light theme, 2017 theme), reduced-motion rules
│   │   ├── error.html            SvelteKit's last-resort error page (static)
│   │   ├── hooks.client.ts       report unhandled errors to console with X-Divy-Trace-Id if present
│   │   ├── lib/
│   │   │   ├── api/client.ts     `createApi({fetch, base})` typed client; ApiError with errorType + traceId
│   │   │   ├── api/types.gen.ts  GENERATED by json2ts from schema/index.schema.json — never edited
│   │   │   ├── api/prom.ts       query/query_range helpers, sample-pair narrowing, legendFormat templating, step calculation
│   │   │   ├── api/loki.ts       LogQL helpers, autocomplete sources
│   │   │   ├── server/api.ts     server-only: `serverApi(fetch)` bound to `$env/static/private` API_BASE
│   │   │   ├── state/timerange.svelte.ts · layout.svelte.ts · theme.svelte.ts · alerts.svelte.ts · motion.svelte.ts · console.svelte.ts
│   │   │   ├── charts/uplot.ts   uPlot options factory (theme colors, crosshair, legend toggle, stacking transform, resize)
│   │   │   ├── charts/sparkline.ts  inline SVG sparkline path builder (stat panels; favicon preview)
│   │   │   ├── grid/gridstack.ts gridstack init/save/load glue for Svelte-rendered panels
│   │   │   ├── hash.ts           encode/decode `#range=…&layout=…` and `#trace?span=…`
│   │   │   ├── keyboard.ts       global shortcut registry (j/k/Enter/Esc, `/`, `?`, sequences: `promql`, Konami)
│   │   │   ├── format.ts         durations, numbers, Grafana unit ids, relative time
│   │   │   └── components/
│   │   │       ├── ui/           Panel.svelte (header, kebab, resize handle, Explore), Drawer.svelte, Toast.svelte, Kbd.svelte, Tabs.svelte, Skeleton.svelte, ThemeToggle.svelte
│   │   │       ├── trace/        Waterfall.svelte, SpanRow.svelte, Minimap.svelte, SpanDrawer.svelte, VerticalTimeline.svelte
│   │   │       ├── panels/       TimeseriesPanel.svelte, StatPanel.svelte, GaugePanel.svelte, BarGaugePanel.svelte, QueryInspector.svelte, TimeRangePicker.svelte
│   │   │       ├── logs/         QueryBar.svelte (LogQL autocomplete), LevelChips.svelte, LogLine.svelte, LiveTail.svelte
│   │   │       ├── uptime/       TargetRow.svelte, HeartbeatBar.svelte, IncidentList.svelte
│   │   │       ├── postmortem/   Toc.svelte, SeverityBadge.svelte, Body.svelte
│   │   │       ├── alerts/       RuleTable.svelte, AlertToasts.svelte, SilenceButton.svelte
│   │   │       └── console/      PromqlConsole.svelte (floating), kubectl.ts (pods table from profile)
│   │   └── routes/               (§R.7.1) `+layout.svelte` (data-free chrome), `+layout.ts` (prerender, trailingSlash), `(site)/` group = every prerendered route + its `+layout.server.ts`, `trace/[id]/` outside the group (fallback-served, no server loads in its chain), `404/`, `sitemap.xml/`
│   ├── static/
│   │   ├── fonts/                Inter and JetBrains Mono woff2 (latin subset, variable), version in filename
│   │   ├── apple-touch-icon.png · og-default.png (fallback only; live OG comes from the API)
│   │   └── .well-known/          (empty placeholder; security.txt TODO(divy): contact address)
│   └── tests/e2e/                Playwright: trace keyboard nav, dashboard hash round-trip, logs query, easter eggs, axe scan per route
├── deploy/
│   ├── Dockerfile                4-stage build (§R.6.2)
│   ├── docker-compose.yml        services api + caddy; volumes divy-data, caddy-data, caddy-config
│   ├── Caddyfile                 `{$DIVY_DOMAIN}` site: encode, security headers, reverse_proxy api:8080
│   ├── divy.service              systemd unit running `docker compose up -d` from /opt/divy
│   ├── deploy.sh                 idempotent Hetzner deploy: sync files, pull tag, up -d, wait readyz, prune
│   └── README.md                 server bootstrap checklist (Phase 5)
└── docs/
    ├── PLAN.md                   this document
    ├── api.md                    API contract (Phase 1 keeps it in sync with handlers)
    ├── promql-subset.md · logql-subset.md   the exact supported grammar (Phase 1)
    └── runbook.md                operations: rotate token, restore DB, roll back image (Phase 5)
```

## R.2 Toolchain (pinned)

| Component | Version | Where pinned | Note |
|---|---|---|---|
| Go | 1.26.8 (module `go 1.26.0`) | `api/go.mod` `toolchain` line; `golang:1.26.8-bookworm` in Dockerfile; CI `go-version-file: api/go.mod` | see R1; local 1.24.7 auto-downloads |
| Node | 24 LTS in CI/Docker (`node:24-bookworm-slim`); local ≥ 22.12 | `web/.nvmrc`, `engines.node ">=22.12"` | Vite 8 requires `^20.19 \|\| >=22.12`; local 22.22 is fine; 22 is Maintenance LTS until 2027-04-30 |
| npm | 10.x (bundled) | lockfile v3 | `npm ci` everywhere |
| @sveltejs/kit | 2.70.3 | web/package.json | |
| svelte | 5.57.0 | web/package.json | runes only; no legacy `$:` |
| @sveltejs/adapter-static | 3.0.10 | web/package.json | |
| @sveltejs/vite-plugin-svelte | 7.3.0 | web/package.json | |
| vite | 8.2.2 | web/package.json | |
| tailwindcss / @tailwindcss/vite | 4.3.3 / 4.3.3 | web/package.json | CSS-first config in app.css; no tailwind.config.js |
| typescript | 6.0.3 | web/package.json | not 7.x: svelte-check 4.7.6 peers `^5 \|\| ^6`, typescript-eslint 8.69.0 peers `<6.1.0` |
| svelte-check | 4.7.6 | web/package.json | `npm run check` |
| eslint / eslint-plugin-svelte / typescript-eslint | 10.10.0 / 3.23.0 / 8.69.0 | web/package.json | flat config |
| prettier / prettier-plugin-svelte | 3.9.6 / 4.1.1 | web/package.json | |
| vitest | 5.0.0 | web/package.json | unit tests for hash.ts, format.ts, prom.ts, alert engine |
| @playwright/test / @axe-core/playwright | 1.63.0 / 4.13.0 | web/package.json | e2e + a11y |
| @lhci/cli | 0.15.1 | web/package.json | Lighthouse CI |
| json-schema-to-typescript | 16.0.0 | web/package.json (devDependency) | `json2ts`; actively maintained (commits Sep 2026) |
| uplot | 1.6.32 | web/package.json | 51.1 kB min / 22.0 kB gzip + 1.9 kB CSS |
| gridstack | 13.2.0 | web/package.json | 88.1 kB min / 23.8 kB gzip + 5.5 kB CSS; dashboard route only |
| github.com/go-chi/chi/v5 | v5.3.2 | api/go.mod | |
| modernc.org/sqlite | v1.58.0 | api/go.mod | CGo-free; SQLite 3.53.4; driver name `sqlite` |
| go.opentelemetry.io/otel, /sdk, /trace | v1.46.0 | api/go.mod | |
| go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp | v0.71.0 | api/go.mod | |
| github.com/prometheus/client_golang | v1.24.1 | api/go.mod | |
| golang.org/x/time | v0.15.0 | api/go.mod | `rate.NewLimiter` |
| gopkg.in/yaml.v3 | v3.0.1 | api/go.mod | `Decoder.KnownFields(true)` |
| github.com/invopop/jsonschema | v0.14.0 | api/go.mod | emits draft 2020-12 |
| github.com/santhosh-tekuri/jsonschema/v6 | v6.0.3 | api/go.mod | validates 2020-12 |
| github.com/fogleman/gg + golang.org/x/image | v1.3.0 + v0.45.0 | api/go.mod | pure Go PNG/text rendering |
| promtool | github.com/prometheus/prometheus v0.314.0 | Makefile (`go run …/cmd/promtool@v0.314.0`) | not installed locally; `go run` caches it |
| golangci-lint | v2.13.2 | Makefile/CI | |
| Docker Engine / Compose | 29 / v2 | local | BuildKit cache mounts in Dockerfile |
| Runtime base image | `gcr.io/distroless/static-debian13:nonroot` | deploy/Dockerfile | ca-certificates + tzdata included, no shell |
| Edge | `caddy:2` | deploy/docker-compose.yml | Phase 5 pins the exact 2.x tag |
| GitHub Actions | checkout@v7, setup-go@v7, setup-node@v7, docker/setup-buildx-action@v4, docker/login-action@v4, docker/metadata-action@v6, docker/build-push-action@v7 | .github/workflows | |

`api/go.mod` header:

```
module divy.dev/api

go 1.26.0

toolchain go1.26.8
```

## R.3 The `divy` binary

### R.3.1 Subcommands and flags

Every flag has an env twin (§R.3.2); precedence is flag > env > default. `--help` on every subcommand. Exit codes: 0 ok, 1 error, 2 usage.

| Subcommand | Flags | What it does |
|---|---|---|
| `divy serve` | `--addr` (DIVY_ADDR), `--db` (DIVY_DB), `--content` (DIVY_CONTENT_DIR), `--collect` (bool, default false), `--public-origin` | Validates content (fails fast, same output as `validate`), opens SQLite (WAL, busy_timeout 5000, one writer goroutine), runs pending migrations, starts OTel provider + SQLite exporter, starts HTTP server; with `--collect` also starts the collector scheduler in-process. Graceful shutdown on SIGINT/SIGTERM (10 s). |
| `divy collect` | `--once`, `--only github,pypi,uptime,retention` (default all), `--db`, `--content` | Runs collectors without the HTTP server. `--once` runs each selected collector one time and exits 0/1. Used by `make collect-once` and for debugging a token. |
| `divy validate` | `--strict`, `--json`, `--todos`, `[dir]` (default DIVY_CONTENT_DIR) | Schema pass + Content-section rules (§C.10). Exit 1 on errors. |
| `divy schemagen` | `--out` (default `./schema`), `--check` | Writes `schema/*.schema.json` from `model.SchemaRoots` (§R.5). `--check` writes nothing and exits 1 if the files on disk differ (used by CI as a second, Go-only drift guard). |
| `divy migrate` | `--db`, `--status`, `--to N` | Applies embedded SQL migrations (`schema_migrations` table). `--status` prints applied/pending. `serve` runs migrations automatically; this exists for ops and rollback rehearsal. |
| `divy export-ascii` | `--content`, `--width` (default 80), `--no-color` | Prints the ASCII waterfall to stdout — the same renderer used by `Accept: text/plain` on `/`. |
| `divy ping` | `--url` (default `http://127.0.0.1:8080/readyz`), `--timeout` (3s) | GET; exit 0 on 2xx, 1 otherwise. Docker HEALTHCHECK and Makefile wait loops. |

Global flags on every subcommand: `--log-level` (LOG_LEVEL), `--log-format text|json` (LOG_FORMAT), `--version`.

### R.3.2 Environment variables

| Variable | Default | Required | Used by |
|---|---|---|---|
| `DIVY_ADDR` | `:8080` | no | serve |
| `DIVY_DB` | `./data/divy.db` (image: `/data/divy.db`) | no | serve, collect, migrate |
| `DIVY_CONTENT_DIR` | `./content` (image: `/content`) | no | serve, collect, validate, export-ascii |
| `DIVY_PUBLIC_ORIGIN` | `http://localhost:8080` (`.env.example`: `http://localhost:5173`; prod: `https://divy.dev`) | no | serve — absolute URLs it emits (`og_image`, curl lines, robots.txt) |
| `GITHUB_TOKEN` | — | **yes for the GitHub collector**; empty ⇒ collector disabled with one warning and `divy_collector_runs_total{collector="github",result="skipped"}` | collect |
| `GITHUB_LOGIN` | `divysinghvi` | no | collect |
| `GITHUB_PRIVATE_ORGS` | `gradr` | no | collect |
| `PYPI_PACKAGES` | `codemind-ci` | no | collect |
| `UPTIME_SELF_URL` | `https://divy.dev/readyz` (compose: `http://api:8080/readyz`) | no | collect (Content §C.8) |
| `COLLECT_GITHUB_INTERVAL` | `15m` | no | collect |
| `COLLECT_PYPI_INTERVAL` | `60m` | no | collect |
| `COLLECT_UPTIME_INTERVAL` | `5m` | no | collect |
| `COLLECT_RETENTION_INTERVAL` | `60m` | no | collect |
| `PROBE_TIMEOUT` | `10s` | no | collect (upper bound for `uptime.yaml` timeouts) |
| `TRUSTED_PROXIES` | empty (compose: the compose network CIDR) | no | serve — `ClientIPFromXFF` prefixes |
| `CORS_ORIGINS` | empty (`.env.example`: `http://localhost:5173`) | no | serve |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | `20` / `100` | no | serve |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `json` (`.env.example`: `text`) | no | all |
| `OTEL_SERVICE_NAME` | `divy-api` | no | serve (resource attribute) |
| `API_BASE` | `http://127.0.0.1:8080` | web build/dev only | SvelteKit server loads during prerender (`$env/static/private`) |
| `PUBLIC_SITE_ORIGIN` | `http://localhost:5173` (prod build: `https://divy.dev`) | web build | `<link rel=canonical>`, `og:url`, `og:image` (`$env/static/public`) |
| `DIVY_DOMAIN` | `divy.dev` | deploy | Caddyfile `{$DIVY_DOMAIN}` |
| `DIVY_IMAGE` | `ghcr.io/divysinghvi/site:latest` | deploy | docker-compose.yml |

Build-time values that are **not** env: `VERSION`, `COMMIT`, `DATE` are `-ldflags -X divy.dev/api/internal/version.<Name>=…` (Makefile computes them from `git describe --tags --always --dirty`).

### R.3.3 `.env.example` (complete)

```
# divy.dev — copy to .env (git-ignored). Every value below is a safe local default.
# The Go binary never reads this file; `make dev`, docker compose and deploy.sh export it.

# ---- API: divy serve ----
DIVY_ADDR=:8080
DIVY_DB=./data/divy.db
DIVY_CONTENT_DIR=./content
# Origin as the browser sees the site (dev: the Vite server, which proxies /og etc. to the API)
DIVY_PUBLIC_ORIGIN=http://localhost:5173
LOG_LEVEL=info
LOG_FORMAT=text
OTEL_SERVICE_NAME=divy-api
# CIDRs allowed to set X-Forwarded-For (empty = trust nobody; compose sets the caddy network)
TRUSTED_PROXIES=
CORS_ORIGINS=http://localhost:5173
RATE_LIMIT_RPS=20
RATE_LIMIT_BURST=100

# ---- Collector: divy serve --collect / divy collect ----
# Fine-grained PAT with read-only access to public data. Empty = GitHub collector disabled.
GITHUB_TOKEN=
GITHUB_LOGIN=divysinghvi
GITHUB_PRIVATE_ORGS=gradr
PYPI_PACKAGES=codemind-ci
UPTIME_SELF_URL=http://localhost:8080/readyz
COLLECT_GITHUB_INTERVAL=15m
COLLECT_PYPI_INTERVAL=60m
COLLECT_UPTIME_INTERVAL=5m
COLLECT_RETENTION_INTERVAL=60m
PROBE_TIMEOUT=10s

# ---- Web (SvelteKit reads only API_* and PUBLIC_* from this file) ----
# Where +page.server.ts loads fetch during `vite dev` and prerender
API_BASE=http://127.0.0.1:8080
# Absolute origin baked into canonical/og tags at build time
PUBLIC_SITE_ORIGIN=http://localhost:5173

# ---- Deploy: docker compose, Caddy, deploy.sh ----
DIVY_DOMAIN=divy.dev
DIVY_IMAGE=ghcr.io/divysinghvi/site:latest
```

## R.4 Makefile

Variables: `VERSION ?= $(shell git describe --tags --always --dirty)`, `COMMIT ?= $(shell git rev-parse --short HEAD)`, `DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)`, `LDFLAGS = -s -w -X divy.dev/api/internal/version.Version=$(VERSION) -X …Commit=$(COMMIT) -X …Date=$(DATE)`, `IMAGE ?= ghcr.io/divysinghvi/site`, `PROMTOOL = go run github.com/prometheus/prometheus/cmd/promtool@v0.314.0`. Recipes run with `SHELL := bash` and `.ONESHELL`; env comes from `set -a; source .env; set +a` where noted.

| Target | Runs | Notes |
|---|---|---|
| `help` | prints this table from `##` comments | default target |
| `setup` | `cp -n .env.example .env`; `cd api && go mod download`; `cd web && npm ci` | first-run |
| `dev` | `$(MAKE) -j2 dev-api dev-web` | §R.4.1 |
| `dev-api` | `source .env; cd api && go run -tags noweb ./cmd/divy serve --collect` | :8080 |
| `dev-web` | `cd web && npm run dev` (= `vite dev --host 127.0.0.1 --port 5173 --strictPort`) | :5173 |
| `gen` | `cd api && go run ./cmd/divy schemagen --out ../schema` then `cd web && npm run gen:types` | regenerates schema/ and types.gen.ts |
| `gen-check` | `$(MAKE) gen && git diff --exit-code -- schema web/src/lib/api/types.gen.ts` | CI drift guard |
| `validate` | `cd api && go run -tags noweb ./cmd/divy validate --strict ../content` | content rules |
| `todos` | `… validate --todos ../content` | lists TODO(divy) |
| `test` | `test-api test-web` | |
| `test-api` | `cd api && go test -tags noweb -race -count=1 ./...` | includes PromQL/LogQL tables, exposition golden, migration up/down |
| `test-web` | `cd web && npm run check && npm test` | svelte-check + vitest |
| `lint` | `lint-api lint-web` | |
| `lint-api` | `cd api && go vet -tags noweb ./... && golangci-lint run` | |
| `lint-web` | `cd web && npm run lint` (= `prettier --check . && eslint .`) | |
| `promtool-check` | build-noweb; `bin/divy-noweb serve --addr 127.0.0.1:18080 --db $$TMP/promtool.db & `; wait with `bin/divy-noweb ping --url http://127.0.0.1:18080/readyz`; `curl -s :18080/metrics \| $(PROMTOOL) check metrics --extended`; `$(PROMTOOL) check rules content/alerts.yaml`; kill | exit 1 on any lint |
| `build-noweb` | `cd api && CGO_ENABLED=0 go build -tags noweb -trimpath -ldflags "$(LDFLAGS)" -o ../bin/divy-noweb ./cmd/divy` | content source for prerender |
| `web` | build-noweb; `cd web && API_BIN=../bin/divy-noweb DIVY_CONTENT_DIR=$(CURDIR)/content DIVY_DB=$(CURDIR)/bin/build.db DIVY_PUBLIC_ORIGIN=$${PUBLIC_SITE_ORIGIN:-http://localhost:8080} node scripts/build-with-api.mjs` | produces web/build; `PUBLIC_SITE_ORIGIN` comes from the shell (CI/Docker set it to `https://divy.dev`) |
| `build` | web; `rm -rf api/internal/web/dist && cp -r web/build api/internal/web/dist`; `cd api && CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o ../bin/divy ./cmd/divy` | the production binary |
| `run` | `source .env; bin/divy serve --collect` | serves the embedded site on :8080 |
| `docker` | `docker build -f deploy/Dockerfile --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .` | context = repo root |
| `up` / `down` | `docker compose --project-directory . -f deploy/docker-compose.yml up -d --build` / `down` | local compose, reads root .env |
| `e2e` | build; start `bin/divy` on :18080 with a temp DB; `cd web && npx playwright test`; kill | Playwright + axe |
| `lighthouse` | same server; `cd web && npx lhci autorun` | asserts §R.9.6 |
| `migrate` | `source .env; cd api && go run -tags noweb ./cmd/divy migrate --status` | ops |
| `collect-once` | `source .env; cd api && go run -tags noweb ./cmd/divy collect --once` | token check |
| `ascii` | `cd api && go run -tags noweb ./cmd/divy export-ascii --content ../content` | |
| `deploy` | `deploy/deploy.sh $(VERSION)` | Phase 5; needs DEPLOY_HOST in the shell |
| `clean` | `rm -rf bin web/build api/internal/web/dist web/.svelte-kit` | |

### R.4.1 `make dev` exact behaviour

1. Preconditions (checked, with a one-line fix message): `.env` exists (`make setup`), `web/node_modules` exists, Go ≥ 1.24 on PATH (the 1.26.8 toolchain downloads automatically on first `go run`).
2. `make -j2` starts two foreground jobs; Ctrl-C stops both.
   - `dev-api`: `go run -tags noweb ./cmd/divy serve --collect` on `:8080` with the root `.env` exported. `noweb` ⇒ `GET /` with a browser `Accept` returns `404 {"error":"web assets not embedded (built with -tags noweb); use the Vite dev server on :5173"}`; `curl -H 'Accept: text/plain' localhost:8080/` still returns the ASCII trace. Collectors run on their cadences (GitHub only if `GITHUB_TOKEN` is set). Content edits need a restart of this job (content is loaded once and validated at startup).
   - `dev-web`: Vite on `http://127.0.0.1:5173` with HMR. `+page.server.ts` loads run inside Vite and fetch `API_BASE` (= the Go server). Browser-side code fetches relative URLs, which Vite proxies (table below).
3. Vite proxy table (`web/vite.config.ts`, `server.proxy`; every entry `{ target: 'http://127.0.0.1:8080', changeOrigin: false }`, no path rewrite):

| Prefix | Backed by |
|---|---|
| `/api` | Prometheus API subset, traces, content, uptime heartbeats |
| `/metrics` | exposition |
| `/loki` | Loki subset |
| `/healthz`, `/readyz` | health |
| `/favicon.svg` | live sparkline favicon |
| `/og` | OG PNGs |
| `/robots.txt` | robots with the easter-egg comment |

4. Verify: `curl -s localhost:5173/healthz` returns the healthz JSON through the proxy; `open http://localhost:5173/`.

## R.5 Type-safety pipeline (Go structs → JSON Schema → TypeScript → validation)

### R.5.1 Source of truth: `api/internal/model`

All content-file shapes and all API envelopes are Go structs in one package (so generated `$defs` names cannot collide). Registry:

```go
// model/roots.go
var SchemaRoots = []Root{
	{Name: "spans", Type: SpansFile{}},              // content/spans.yaml
	{Name: "logs", Type: LogLine{}},                 // one NDJSON line
	{Name: "postmortem", Type: PostmortemFrontmatter{}},
	{Name: "panels", Type: PanelsFile{}},
	{Name: "alerts", Type: AlertsFile{}},            // Prometheus rule-file shape
	{Name: "uptime", Type: UptimeFile{}},
	{Name: "manual_metrics", Type: ManualMetricsFile{}},
	{Name: "profile", Type: Profile{}},
	{Name: "api", Type: APIRoots{}},                 // struct whose fields are every response/error envelope
}
```

`APIRoots` is a struct with one field per envelope so a single reflection walk collects them: `PromQueryResult`, `PromRangeResult`, `PromSeriesResult`, `PromLabelsResult`, `PromLabelValuesResult`, `PromRulesResult`, `PromError`, `LokiQueryRangeResult`, `LokiLabelsResult`, `LokiError`, `JaegerTraceResponse`, `Healthz`, `Readyz`, `ContentServices`, `ContentPanels`, `ContentPostmortemList`, `ContentPostmortem`, `ContentProfile`, `ContentUptime`, `ContentTodos`, `UptimeHeartbeats`, `PlainError`. Exact fields are defined in the API section; the mechanism is identical for all.

Struct-tag rules (invopop/jsonschema):

| Rule | Mechanism |
|---|---|
| Required ⇔ no `omitempty` | reflector default (`RequiredFromJSONSchemaTags` off) |
| Enum | a named Go string type with a `JSONSchema()` method returning `{"type":"string","enum":[…]}` → becomes its own `$defs` entry → a named TS union |
| Constraints | `jsonschema:"minimum=0,maximum=23"`, `minLength=1`, `pattern=…`, `format=date-time` — enforced by Go validation; **dropped by json2ts** (not expressible in TS) |
| Descriptions | Go doc comments via `AddGoComments("divy.dev/api", "./internal/model")` → `description` → TS doc comments |
| No extra keys | reflector default (`additionalProperties: false`), so unknown YAML keys fail validation and TS gets no index signature |
| Free-form maps | `map[string]string` (Loki stream labels, Prom `metric`) → `additionalProperties: {type: string}` → `{ [k: string]: string }` |
| Either/or | `jsonschema:"oneof_required=a"` groups; json2ts treats `oneOf` as `anyOf` (union) |

Concrete example (three types from `panels.yaml`):

```go
// GridPos places a panel on the 24-column dashboard grid.
type GridPos struct {
	X int `json:"x" jsonschema:"minimum=0,maximum=23"`
	Y int `json:"y" jsonschema:"minimum=0"`
	W int `json:"w" jsonschema:"minimum=1,maximum=24"`
	H int `json:"h" jsonschema:"minimum=2"`
}

// Target is one PromQL query of a panel.
type Target struct {
	RefID        string `json:"refId" jsonschema:"pattern=^[A-Z]$"`
	Expr         string `json:"expr" jsonschema:"minLength=1"`
	LegendFormat string `json:"legendFormat,omitempty"`
	Instant      bool   `json:"instant,omitempty"`
	Hide         bool   `json:"hide,omitempty"`
}

// PanelType is the visualisation of a panel.
type PanelType string

func (PanelType) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Enum: []any{"timeseries", "stat", "gauge", "bargauge"}}
}
```

### R.5.2 `divy schemagen` → `schema/*.schema.json`

Reflector: `jsonschema.Reflector{Anonymous: true, ExpandedStruct: false}` + `AddGoComments`. For each root: set `$id` to `https://divy.dev/schema/<name>.schema.json`, `$schema` to `https://json-schema.org/draft/2020-12/schema`, root `$ref` → `#/$defs/<RootType>`; marshal with `json.MarshalIndent(v, "", "  ")` + `\n` (Go sorts map keys ⇒ byte-stable output). Then `schema/index.schema.json` = `{ "$schema", "$id": ".../index.schema.json", "$defs": <merge of every root's $defs> }`; a duplicate `$defs` key with a different body is a schemagen error. Output of the example above (fragment of `panels.schema.json`):

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://divy.dev/schema/panels.schema.json",
  "$ref": "#/$defs/PanelsFile",
  "$defs": {
    "GridPos": {
      "description": "GridPos places a panel on the 24-column dashboard grid.",
      "type": "object",
      "additionalProperties": false,
      "required": ["x", "y", "w", "h"],
      "properties": {
        "x": {"type": "integer", "minimum": 0, "maximum": 23},
        "y": {"type": "integer", "minimum": 0},
        "w": {"type": "integer", "minimum": 1, "maximum": 24},
        "h": {"type": "integer", "minimum": 2}
      }
    },
    "PanelType": {"type": "string", "enum": ["timeseries", "stat", "gauge", "bargauge"]},
    "Target": {
      "description": "Target is one PromQL query of a panel.",
      "type": "object",
      "additionalProperties": false,
      "required": ["refId", "expr"],
      "properties": {
        "refId": {"type": "string", "pattern": "^[A-Z]$"},
        "expr": {"type": "string", "minLength": 1},
        "legendFormat": {"type": "string"},
        "instant": {"type": "boolean"},
        "hide": {"type": "boolean"}
      }
    }
  }
}
```

Generated files (all committed): `schema/spans.schema.json`, `logs.schema.json`, `postmortem.schema.json`, `panels.schema.json`, `alerts.schema.json`, `uptime.schema.json`, `manual_metrics.schema.json`, `profile.schema.json`, `api.schema.json`, `index.schema.json`.

### R.5.3 TypeScript generation

`web/package.json`:

```json
"gen:types": "json2ts --unreachableDefinitions -i ../schema/index.schema.json -o src/lib/api/types.gen.ts"
```

`--unreachableDefinitions` emits every `$defs` entry as an exported type even though the index has no root. Output for the example:

```ts
/* eslint-disable */
/**
 * This file was automatically generated by json-schema-to-typescript.
 * DO NOT MODIFY IT BY HAND. Instead, modify the source JSONSchema file,
 * and run json-schema-to-typescript to regenerate this file.
 */

/** GridPos places a panel on the 24-column dashboard grid. */
export interface GridPos { x: number; y: number; w: number; h: number; }
export type PanelType = "timeseries" | "stat" | "gauge" | "bargauge";
/** Target is one PromQL query of a panel. */
export interface Target { refId: string; expr: string; legendFormat?: string; instant?: boolean; hide?: boolean; }
```

Rules: `web/src/lib/api/types.gen.ts` is listed in `.prettierignore` and the eslint `ignores` (formatting must not create drift). Prometheus sample pairs (`[unix_seconds, "value"]`) are declared in Go with a custom `JSONSchema()` returning `{"type":"array","prefixItems":[{"type":"number"},{"type":"string"}],"minItems":2,"maxItems":2}`; if json2ts emits `unknown[]` for it, `prom.ts` narrows to `[number, string]` in exactly one function (`toSamplePair`) — nothing else in the app casts. No other hand-written type mirrors a Go struct.

### R.5.4 Drift check (CI, every PR)

```
make gen && git diff --exit-code -- schema web/src/lib/api/types.gen.ts
```

plus `divy schemagen --check` inside the `api` job (catches a schema edit without a regenerated TS file even when Node is unavailable). Both run on the pinned versions from the lockfile/go.sum, so output is reproducible.

### R.5.5 Content validation uses the same schemas

`divy validate` and `divy serve` do **not** read `schema/` from disk: `schema.Generate()` builds the identical schema objects in memory from the same structs, compiles them with santhosh-tekuri (`jsonschema.UnmarshalJSON` → `Compiler.AddResource` → `Compile`, `AssertFormat()` on), and validates each content document (YAML → `any` via yaml.v3 → `json.Marshal` → `UnmarshalJSON`, so numbers/strings have JSON types; NDJSON line by line). After the schema pass, the document is decoded into the Go struct with `yaml.Decoder.KnownFields(true)` and the Content-section rules run (§C.10). Consequently: the committed `schema/` files are for TypeScript, editors and humans; the drift check guarantees they equal what the binary enforces. Failure output is the format in §C.10.2; `serve` prints it and exits 1 before binding the port.

## R.6 Frontend delivery: adapter-static + full prerender + embed.FS

Decision (recommended; open question #1): every page is prerendered at build time against the real API, the output directory is embedded in the Go binary, and production is one `api` container behind Caddy. Live data is fetched by the browser from the same origin after hydration.

### R.6.1 SvelteKit configuration

```js
// web/svelte.config.js
import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
export default {
	preprocess: vitePreprocess(),
	kit: {
		adapter: adapter({ pages: 'build', assets: 'build', fallback: 'spa.html', precompress: false, strict: true }),
		env: { dir: '..', publicPrefix: 'PUBLIC_', privatePrefix: 'API_' },
		prerender: { concurrency: 4, handleHttpError: 'fail', handleMissingId: 'fail' }
	}
};
```

```ts
// web/src/routes/+layout.ts
export const prerender = true;      // every route unless it opts out
export const trailingSlash = 'never'; // build/logs.html, build/postmortems/INC-001.html, build/index.html
```

| Mechanism | How it is used |
|---|---|
| Build-time data | `+page.server.ts` / `+layout.server.ts` `load` with `fetch(API_BASE + path)` (`$lib/server/api.ts`). Server loads run only at build (prerender) and in `vite dev`; their result is serialized into the HTML and never re-run on hydration. Client-side navigation reads the prerendered `<route>/__data.json`. |
| Dynamic routes | `postmortems/[id]/+page.server.ts` exports `entries = async () => (await api.content.postmortems()).items.map(p => ({ id: p.id }))`. |
| Not prerendered | `trace/[id]` (`export const prerender = false` in `trace/[id]/+page.ts`; the id space is open — any request's `X-Divy-Trace-Id`). Served by the `spa.html` fallback; the page fetches `/api/traces/{id}` in an effect. A fallback-served route must have **no** `+page.server.ts`/`+layout.server.ts` anywhere in its chain (the client would request a `__data.json` that was never prerendered), so all server loads live in the `(site)` layout group and `trace/[id]/` sits outside it; the root `+layout.svelte` renders chrome from static route metadata only. |
| Query strings / hash | Never read in `load` (forbidden during prerender). `/explore?…`, `/logs?q=…`, `#range=…&layout=…`, `#trace?span=…` are read in `$effect` from `page.url` (`$app/state`) after hydration and written with `replaceState` from `$app/navigation`. |
| Live refresh | Components call the browser API client in `$effect`/timers (§R.7.2). Prerendered HTML is a truthful snapshot at build time with a visible "as of <build time>" label on live panels until the first live response replaces it. |
| 404 | `src/routes/404/+page.svelte` prerenders to `build/404.html`; Go serves it with status 404. |
| Sitemap | `src/routes/sitemap.xml/+server.ts` (`prerender = true`) → `build/sitemap.xml`, lists the routes table; uses `PUBLIC_SITE_ORIGIN`. |
| `building` guard | none needed; no module-level side effects at import time. |

### R.6.2 Build cycle

```
              content/ + schema in-memory              web/ sources
                     │                                      │
   [1] api-noweb ────┴──► go build -tags noweb ──► /out/divy-noweb
                                                       │ runs in stage 2 as the content source
   [2] web-build  ── node:24 ── npm ci ── scripts/build-with-api.mjs:
                        spawn /divy-noweb serve --addr 127.0.0.1:8080 --db /tmp/build.db (no --collect)
                        wait: /divy-noweb ping … /readyz   (content validated at startup → invalid content fails the build here)
                        API_BASE=http://127.0.0.1:8080 PUBLIC_SITE_ORIGIN=$SITE_ORIGIN vite build   ──► web/build/
                        SIGTERM the API
   [3] api-final  ── COPY --from=web-build web/build → api/internal/web/dist ── go build (no tag) ──► /out/divy  (embeds dist)
   [4] runtime    ── gcr.io/distroless/static-debian13:nonroot ── COPY /out/divy /divy ; COPY content /content ── HEALTHCHECK /divy ping
```

`deploy/Dockerfile` (context = repo root):

```dockerfile
# syntax=docker/dockerfile:1
ARG GO_IMAGE=golang:1.26.8-bookworm
ARG NODE_IMAGE=node:24-bookworm-slim
ARG SITE_ORIGIN=https://divy.dev

FROM ${GO_IMAGE} AS api-noweb
WORKDIR /src/api
COPY api/go.mod api/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api/ ./
ARG VERSION=dev
ARG COMMIT=none
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -tags noweb -trimpath -ldflags "-s -w -X divy.dev/api/internal/version.Version=${VERSION} -X divy.dev/api/internal/version.Commit=${COMMIT}" -o /out/divy-noweb ./cmd/divy

FROM ${NODE_IMAGE} AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
COPY content/ /content/
COPY --from=api-noweb /out/divy-noweb /divy-noweb
ARG SITE_ORIGIN
ENV API_BIN=/divy-noweb DIVY_CONTENT_DIR=/content DIVY_DB=/tmp/build.db DIVY_PUBLIC_ORIGIN=${SITE_ORIGIN} PUBLIC_SITE_ORIGIN=${SITE_ORIGIN}
RUN node scripts/build-with-api.mjs            # → /src/web/build

FROM ${GO_IMAGE} AS api-final
WORKDIR /src/api
COPY api/ ./
COPY --from=web-build /src/web/build ./internal/web/dist
ARG VERSION=dev
ARG COMMIT=none
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X divy.dev/api/internal/version.Version=${VERSION} -X divy.dev/api/internal/version.Commit=${COMMIT}" -o /out/divy ./cmd/divy

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=api-final /out/divy /divy
COPY content/ /content/
ENV DIVY_ADDR=:8080 DIVY_DB=/data/divy.db DIVY_CONTENT_DIR=/content LOG_FORMAT=json
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/divy", "ping"]
ENTRYPOINT ["/divy"]
CMD ["serve", "--collect"]
```

`web/scripts/build-with-api.mjs`: if `API_BASE` is set in the process environment (a value that only exists in `.env` does not count — Vite gives real environment variables precedence over `.env` files, and the script sets `API_BASE` explicitly for the child `vite build`), just runs `vite build` (developer already has an API up); otherwise spawns `$API_BIN serve --addr 127.0.0.1:8080` with `--collect` off, polls `/readyz` for up to 30 s via `fetch`, sets `API_BASE`, runs `vite build`, then kills the child and propagates the build's exit code. The same script serves `make web` and the Dockerfile, so local and CI builds are identical.

`api/internal/web/web.go`:

```go
//go:build !noweb

package web

import ("embed"; "io/fs")

//go:embed all:dist
var dist embed.FS // "all:" is required: SvelteKit's output directory is named _app and embed skips _-prefixed names by default

func FS() (fs.FS, bool) { sub, err := fs.Sub(dist, "dist"); return sub, err == nil }
```

`web_noweb.go` (`//go:build noweb`) returns `nil, false`. Building without the tag and without `dist/` fails at compile time (embed patterns must match) — the intended signal that `make web` was skipped.

### R.6.3 Go static handler (`api/internal/server/static.go`)

Mounted last in the chi router (after every API route), inside the same middleware chain (trace header, request metrics). `/_app/immutable/*` and `/fonts/*` are exempt from the per-IP token bucket.

| Request path | Resolution (first match wins) | Status / headers |
|---|---|---|
| `/` with `Accept` preferring `text/plain` | `ascii.Render(content)` | 200 `text/plain; charset=utf-8`, `Vary: Accept`, `Cache-Control: public, max-age=60` |
| `/` | `index.html` | 200 `text/html`, `Cache-Control: no-cache`, strong `ETag` |
| exact file exists (`/_app/immutable/…`, `/fonts/…`, `/logs/__data.json`, `/sitemap.xml`, `/apple-touch-icon.png`) | that file via `http.ServeFileFS` | `/_app/immutable/*` and `/fonts/*`: `Cache-Control: public, max-age=31536000, immutable`; `__data.json`: `no-cache` + ETag; other: `public, max-age=3600` |
| `<path>.html` exists | that page (`/logs` → `logs.html`, `/postmortems/INC-001` → `postmortems/INC-001.html`) | 200, `no-cache` + ETag |
| path ends with `/` and `<path>.html` exists | 308 redirect to the slash-less path | matches `trailingSlash: 'never'` |
| `/trace/…` | `spa.html` | 200, `no-cache` (client router renders `/trace/[id]`) |
| anything else | `404.html` | 404 |

ETags are sha256 prefixes computed once at startup by walking the embedded FS (a few hundred files). Compression is Caddy's job (`encode zstd gzip`); the binary sends identity bodies. `Accept` parsing for `/`: media ranges with `q>0`; text wins when `text/plain` is listed and either `text/html` is absent or has a lower `q` (`curl -H 'Accept: text/plain'` ⇒ text; browsers and bare `curl` (`*/*`) ⇒ HTML). `HEAD` mirrors `GET`.

### R.6.4 Dev proxy

The table in §R.4.1 step 3 is the complete list. Only paths the Go server owns are proxied; everything else is SvelteKit. Because `/` is served by Vite in dev, the ASCII negotiation is tested against `:8080` directly and by the Playwright suite against the built binary.

### R.6.5 Content negotiation, headers and caching summary

| Concern | Where |
|---|---|
| `Accept: text/plain` on `/` | Go (§R.6.3) |
| `X-Divy-Trace-Id` on every response (static included) | Go middleware (API section) |
| Security headers (HSTS, CSP, X-Content-Type-Options, Referrer-Policy, Permissions-Policy) | Caddy (Phase 5) |
| Compression | Caddy `encode zstd gzip` |
| TLS | Caddy automatic HTTPS for `{$DIVY_DOMAIN}` |
| Rate limiting | Go (API section); static assets exempt |
| CSP compatibility | prerendered pages carry inline data/hydration `<script>` blocks; Phase 5 chooses between hashing them in Caddy's `Content-Security-Policy` and SvelteKit's `kit.csp` option (Phase 5 verifies the option's behaviour with adapter-static before relying on it) |

### R.6.6 OG images (server-side)

`api/internal/og`: `//go:embed fonts/Inter-Bold.ttf fonts/JetBrainsMono-Regular.ttf` (static TTF instances from the OFL releases; same families as the site) → `opentype.Parse(bytes)` once at startup → `opentype.NewFace(font, &opentype.FaceOptions{Size, DPI: 72})` per render (Font methods are safe for concurrent use with separate buffers; `font.Face` is not, so a face is never shared) → `gg.NewContext(1200, 630)`, `SetFontFace`, `DrawRoundedRectangle`, `DrawStringWrapped`, `EncodePNG`. Pure Go: no cgo, no fontconfig, no system fonts, so it runs unchanged in the distroless image. Inputs come from the Content section (§C.5.4). Routes: `GET /og/postmortems/{id}.png`, `GET /og/default.png`; responses cached in memory keyed by `(id, content hash)`, `Cache-Control: public, max-age=86400`, ETag. Pages emit `<meta property="og:image" content="{PUBLIC_SITE_ORIGIN}/og/postmortems/INC-001.png">` and `og:image:width/height`.

### R.6.7 If `adapter-node` instead (≤10 lines)

1. `web/svelte.config.js`: `@sveltejs/adapter-node@5.5.7`; drop `fallback`; keep `prerender = true` only on content pages (`/postmortems/*`, `/contact`), set `prerender = false` elsewhere so panels SSR with fresh data on every request.
2. `$lib/server/api.ts` reads `API_BASE` from `$env/dynamic/private` (runtime), value `http://api:8080` in compose.
3. Dockerfile: stages 3–4 become a second image `web` (`node:24-bookworm-slim`, `npm ci --omit dev`, `node build`, `PORT=3000`, `ORIGIN=https://divy.dev`); the Go image is built with `-tags noweb` permanently and `api/internal/web/` is deleted.
4. Caddyfile routes `/api/* /metrics /loki/* /healthz /readyz /favicon.svg /og/* /robots.txt` → `api:8080`, everything else → `web:3000`; the `Accept: text/plain` negotiation on `/` moves to Caddy (`@ascii header Accept text/plain*` → `api:8080`).
5. compose gains a `web` service; release.yml builds and pushes two images; deploy.sh pulls both.
6. Costs: HTML responses no longer carry the request's own `X-Divy-Trace-Id`; Caddy has no built-in rate limiter, so the web container is unprotected unless a Caddy plugin is added; Lighthouse depends on Node render time instead of static files; one more container to patch.

## R.7 Frontend plan summary

### R.7.1 Routes

| Route | Files (`web/src/routes/…`) | Prerender | Build-time data (server load) | Live data (browser) | `<title>` |
|---|---|---|---|---|---|
| `/` | `(site)/+page.svelte`, `+page.server.ts` | yes | `/api/traces/career`, `/api/content/services`, `/api/content/postmortems` (span → INC links) | none for the trace (static per deploy); alert toasts from the shared engine; stat strip via `/api/v1/query` | `divy.career — a career, traced` |
| `/dashboard` | `(site)/dashboard/+page.svelte`, `+page.server.ts` | yes | `/api/content/panels` | per panel `/api/v1/query_range` or `/api/v1/query`, every `dashboard.refresh` (60 s) while visible | `Metrics` |
| `/explore` | `(site)/explore/+page.svelte` | yes (empty shell) | — | `/api/v1/query`, `/api/v1/query_range`, `/api/v1/labels`, `/api/v1/series`, `/loki/api/v1/*`; params `?ds=prom\|loki&expr=…&range=…` read in `$effect` | `Explore` |
| `/logs` | `(site)/logs/+page.svelte`, `+page.server.ts` | yes | `/loki/api/v1/labels`, `/loki/api/v1/label/service/values`, initial `/loki/api/v1/query_range` (`{service=~".+"}`, limit 100, range all) | `/loki/api/v1/query_range` on every query change; live tail replays the result set (no server streaming) | `Logs` |
| `/uptime` | `(site)/uptime/+page.svelte`, `+page.server.ts` | yes | `/api/content/uptime` | `/api/uptime/heartbeats?days=90`, `/api/v1/query` for `probe_success`, `probe_duration_seconds`, `probe_http_status_code`; refresh 60 s | `Uptime` |
| `/postmortems` | `(site)/postmortems/+page.svelte`, `+page.server.ts` | yes | `/api/content/postmortems` | — | `Postmortems` |
| `/postmortems/[id]` | `(site)/postmortems/[id]/+page.svelte`, `+page.server.ts` (`entries`) | yes, one page per id | `/api/content/postmortems/{id}` (html, toc, span_url, og_image) | — | `INC-001 — <title>` |
| `/alerts` | `(site)/alerts/+page.svelte`, `+page.server.ts` | yes | `/api/v1/rules` | engine state (pending/firing/inactive per rule), silence list | `Alerts` |
| `/contact` | `(site)/contact/+page.svelte`, `+page.server.ts` | yes | `/api/content/profile` | `/healthz` rendered as the live curl output | `Runbook: contact` |
| `/trace/[id]` | `trace/[id]/+page.svelte`, `+page.ts` (`prerender = false`) — outside `(site)`, no server load in its chain | no — `spa.html` | — | `/api/traces/{id}` (career or an OTel request trace); profile/rules fetched by the browser client because the `(site)` layout data is absent here | `Trace <id>` |
| `/404` | `(site)/404/+page.svelte` | yes → `404.html` | — | — | `Not found` |
| `/sitemap.xml` | `sitemap.xml/+server.ts` | yes | — | — | — |
| root layout | `+layout.svelte`, `+layout.ts` (prerender, trailingSlash) | — | none (route metadata is a static module) | — | — |
| site layout | `(site)/+layout.server.ts`, `(site)/+layout.svelte` | — | `/api/content/profile` (name, `open_to_work` badge, escalation link), `/api/v1/rules` (engine boots without waiting) | — | — |

Cross-cutting UI in the root `+layout.svelte`: top bar (route tabs, theme toggle, `?` shortcuts), alert toast stack (`aria-live="polite"`), the floating PromQL console, skip-link, footer with the `curl divy.dev/metrics` hint. The time-range picker is rendered by the pages that use it (dashboard, explore, logs).

### R.7.2 State and data flow

| Piece | File | Behaviour |
|---|---|---|
| API client | `lib/api/client.ts` | `createApi({ fetch, base })` → typed methods returning `types.gen.ts` shapes: `query`, `queryRange`, `series`, `labels`, `labelValues`, `rules`, `lokiQueryRange`, `lokiLabels`, `lokiLabelValues`, `trace`, `heartbeats`, `content.{services,panels,postmortems,postmortem,profile,uptime,todos}`, `healthz`. GET only, `Accept: application/json`. Non-2xx → `ApiError { status, errorType, message, traceId }` parsed from the Prometheus / Loki / plain envelopes; `traceId` = `X-Divy-Trace-Id`. |
| Server client | `lib/server/api.ts` | `serverApi(fetch)` = `createApi({ fetch, base: env.API_BASE })` with `$env/static/private`; importable only from `+*.server.ts`. |
| Browser client | `lib/api/client.ts` export `api` | `createApi({ fetch: (i, o) => globalThis.fetch(i, o), base: '' })` — same origin, so proxied in dev and embedded in prod. |
| Time range | `lib/state/timerange.svelte.ts` | `$state` preset ∈ `24h\|7d\|30d\|1y\|all`; `$derived` `from`, `to` (`to = now`, refreshed on each tick), `step` = range ÷ 300 rounded up to `1m\|5m\|30m\|2h\|1d`; `all` starts at the root span's resolved start (from layout data). Persisted in the hash (`range=`) on `/dashboard` and `/explore`; default from `dashboard.time.default`. |
| Layout | `lib/state/layout.svelte.ts` | `overrides: Record<id, {x,y,w,h}>` for panels that differ from `panels.yaml`; `load()` from `#layout=<base64url JSON>`, `save()` on gridstack `change`; `reset()`. Hash writes use `replaceState` (no history entries). Shareable: opening the URL restores the layout for anyone. |
| Hash codec | `lib/hash.ts` | `#range=7d&layout=eyJ…` and `#trace?span=<id>`; unit-tested round trips; unknown keys preserved. |
| Alert engine | `lib/state/alerts.svelte.ts` | rules from `/api/v1/rules`; every 15 s evaluates each `expr` via `/api/v1/query`; state machine inactive → pending (result non-empty) → firing after `for`; silences kept in `sessionStorage` (`{alertname, until}`); firing transitions push a toast; the same store powers `/alerts` and the `/` strip. Pauses when `document.hidden`. |
| Theme | `lib/state/theme.svelte.ts` | `dark\|light\|grafana2017`; `localStorage`; `<html data-theme>` set by an inline script in `app.html` before first paint; Konami sequence toggles `grafana2017`. |
| Motion | `lib/state/motion.svelte.ts` | `reduced` from `matchMedia('(prefers-reduced-motion: reduce)')`; draw-in animations, typewriter cadence and gridstack `animate` read it. |
| Console | `lib/state/console.svelte.ts` | open/close, history; `promql` key sequence outside inputs opens it; `kubectl get pods` answered from `profile.pods` (Content §C.9.2). |
| Panel refresh | `dashboard/+page.svelte` | one interval per page, visibility-aware; range change aborts in-flight requests (`AbortController`); requests deduplicated by URL across panels; results cached in a `Map` keyed by `(expr, from, to, step)` for back-navigation. |
| "View query" / "Explore" | `components/panels/QueryInspector.svelte` | shows the exact PromQL, the resolved `curl "$DIVY_PUBLIC_ORIGIN/api/v1/query_range?query=…&start=…&end=…&step=…"` with copy button, and a link to `/explore?ds=prom&expr=…&range=…`. |
| Keyboard | `lib/keyboard.ts` | registry with scopes; trace: `j/k` move focus, `Enter` opens drawer, `Esc` closes; logs/explore: `/` focuses the query bar; `?` opens the shortcut sheet; sequences `promql`, Konami. |
| Span linking | `components/trace/*` | `#trace?span=<id>` opens the drawer; log lines and postmortems link there; the drawer shows tags, events, duration, and back-links (postmortems, logs filtered by `span`). |

### R.7.3 Libraries and why

| Need | Choice | Why (measured/verified) |
|---|---|---|
| Timeseries panels (crosshair, legend toggle, stacking) | **uPlot 1.6.32** | 51.1 kB min / 22.0 kB gzip, canvas, built-in cursor/crosshair + legend series toggling, the library Grafana uses. Stacking is deliberately not built in; the app ships a 40-line cumulative-sum transform plus `bands` (the pattern in uPlot's own `demos/stacked-series.html`/`stack.js`), re-stacking on legend toggle. Svelte 5 glue: `bind:this` container, `$effect` creating `new uPlot(opts, data, el)`, `setData` on data change, `ResizeObserver` → `setSize`, teardown `destroy()`; effects never run during prerender. Rejected: hand-rolled SVG — a crosshair + legend + stacking + 4 panels × 300 points × 60 s refresh is where SVG DOM churn and code volume (≈ 600 lines) lose to a 22 kB library. |
| Trace waterfall, minimap, heartbeat bars, sparklines | hand-rolled **SVG** in Svelte | prerenders into the HTML (LCP element, indexable text), scales to 390 px with CSS, no library models a Jaeger waterfall. |
| Drag/resize dashboard grid | **gridstack 13.2.0** | 88.1 kB min / 23.8 kB gzip + 5.5 kB CSS, zero deps, TypeScript types, native touch drag since v6, `columnOpts` breakpoints (1 column ≤ 640 px), `save()/load()` for the hash, `change` event. Svelte 5 glue: panels are Svelte-rendered `.grid-stack-item` children with `gs-*` attributes; `GridStack.init({ column: 24, cellHeight: 30, margin: 8, float: false, animate: !reduced }, el)` in `$effect`, `makeWidget(child)` per panel, `removeWidget` on destroy. Loaded only on `/dashboard` (route-level code splitting). Rejected: custom grid — the drag/resize part is small but collision resolution and compaction are the bulk of gridstack, not worth re-deriving. |
| Styling | Tailwind v4 via `@tailwindcss/vite` | utility classes + `@theme` tokens for the palette; light/dark/2017 themes are CSS variables switched by `data-theme`. |
| Icons | inline SVG sprite (~20 icons) | no icon package. |
| Markdown | none client-side | HTML from the API (R9). |
| Fonts | self-hosted Inter (v4.1) + JetBrains Mono (v2.304), OFL | §R.9.1 |
| Tests | vitest (units), Playwright + axe (e2e/a11y), LHCI | §R.9.6 |

## R.8 CI and deploy shape

### R.8.1 `.github/workflows/ci.yml` — `pull_request` and `push: branches: [main]`

`concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }`, `permissions: contents: read`.

| Job | Steps |
|---|---|
| `api` | checkout@v7 → setup-go@v7 (`go-version-file: api/go.mod`, `cache-dependency-path: api/go.sum`) → `make lint-api` → `make test-api` → `make validate` → `go run ./cmd/divy schemagen --check` → `make promtool-check` |
| `web` | checkout@v7 → setup-node@v7 (`node-version-file: web/.nvmrc`, `cache: npm`, `cache-dependency-path: web/package-lock.json`) → `npm ci` → `npm run gen:types && git diff --exit-code -- src/lib/api/types.gen.ts` → `make lint-web` → `make test-web` |
| `build-e2e` (needs api, web) | checkout → setup-go + setup-node → `make build` (runs the prerender against the noweb binary) → start `bin/divy` on :18080 → `npx playwright install --with-deps chromium` → `make e2e` → `make lighthouse` (assertions §R.9.6; `continue-on-error: true` until Phase 4 exit, then blocking) → docker/setup-buildx-action@v4 → docker/build-push-action@v7 with `push: false`, `cache-from: type=gha`, `cache-to: type=gha,mode=max` (validates the Dockerfile on every PR) |

### R.8.2 `.github/workflows/release.yml` — `push: tags: ['v*']`

`permissions: { contents: read, packages: write }`, `concurrency: { group: release, cancel-in-progress: false }`.

| Job | Steps |
|---|---|
| `image` | checkout@v7 → setup-buildx-action@v4 → login-action@v4 (`registry: ghcr.io`, `username: ${{ github.actor }}`, `password: ${{ secrets.GITHUB_TOKEN }}`) → metadata-action@v6 (`images: ghcr.io/divysinghvi/site`, `tags: type=semver,pattern={{version}}` + `type=sha` + `latest`) → build-push-action@v7 (`context: .`, `file: deploy/Dockerfile`, `push: true`, `platforms: linux/amd64`, `build-args: VERSION=${{ github.ref_name }}, COMMIT=${{ github.sha }}, SITE_ORIGIN=https://divy.dev`, `cache-from/to: type=gha`) |
| `deploy` (needs image; `environment: production`) | load SSH key from `secrets.DEPLOY_SSH_KEY` + `secrets.DEPLOY_KNOWN_HOSTS` → `scp deploy/{docker-compose.yml,Caddyfile,divy.service,deploy.sh} $DEPLOY_USER@$DEPLOY_HOST:/opt/divy/` → `ssh … '/opt/divy/deploy.sh ${{ github.ref_name }}'` → `curl -fsS https://divy.dev/readyz` |

Secrets: `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_SSH_KEY`, `DEPLOY_KNOWN_HOSTS`. The server's `/opt/divy/.env` (with `GITHUB_TOKEN`, `DIVY_DOMAIN`, `TRUSTED_PROXIES`) is created once by hand and never passes through CI.

### R.8.3 Deploy shape (Phase 5 details)

One Hetzner VM (Ubuntu, Docker Engine + compose plugin) with `/opt/divy/{docker-compose.yml,Caddyfile,.env,deploy.sh}` and the named volumes `divy-data` (SQLite), `caddy-data`, `caddy-config`. Compose runs two services: `api` (`${DIVY_IMAGE}`, `env_file: .env`, `DIVY_PUBLIC_ORIGIN=https://${DIVY_DOMAIN}`, `UPTIME_SELF_URL=http://api:8080/readyz`, `TRUSTED_PROXIES` = the compose network CIDR, healthcheck `/divy ping`, `restart: unless-stopped`) and `caddy` (`caddy:2`, ports 80/443 + 443/udp, `depends_on: api: condition: service_healthy`); the Caddyfile is the one site block `{$DIVY_DOMAIN} { encode zstd gzip; header { …security headers… }; reverse_proxy api:8080 }` — Caddy terminates TLS with automatic certificates, compresses, sets `X-Forwarded-For/Proto/Host`, and forwards everything (static pages and API alike) to the binary. `deploy/divy.service` is a oneshot systemd unit (`RemainAfterExit=yes`, `ExecStart=docker compose up -d`, `ExecStop=docker compose down`, `WorkingDirectory=/opt/divy`, `WantedBy=multi-user.target`) so the stack returns after a reboot. `deploy/deploy.sh <tag>` is idempotent: installs Docker if absent, writes `DIVY_IMAGE=ghcr.io/divysinghvi/site:<tag>` into `.env`, `docker compose pull`, `docker compose up -d --remove-orphans`, waits for `docker inspect --format '{{.State.Health.Status}}'` to read `healthy` (60 s), then `docker image prune -f`; a failed wait prints the last 100 log lines and exits 1 (the previous container keeps running because compose only replaces it after the new one starts). Rollback = `deploy.sh <previous tag>`. Locally the same compose file runs with `make up` (`--project-directory .` so it reads the root `.env`; the `api` service exposes 8080 and Caddy serves `localhost` on 80 with an internal certificate).

## R.9 Lighthouse / accessibility / mobile guardrails (Phase 3 and 4 acceptance)

Scores are asserted on the built binary (no dev server) for `/`, `/dashboard`, `/logs`, `/uptime`, `/postmortems`, `/postmortems/INC-001`, `/alerts`, `/contact` — mobile preset, ≥ 95 in all four categories. Performance weights that matter: TBT 30 %, LCP 25 %, CLS 25 %.

### R.9.1 Fonts

- [ ] Self-hosted only: `web/static/fonts/Inter-4.1-latin.var.woff2` and `JetBrainsMono-2.304-latin.var.woff2` (variable, latin subset; version in the filename so `immutable` caching is safe). Budget ≤ 120 kB for both.
- [ ] `app.html` preloads both: `<link rel="preload" href="/fonts/…woff2" as="font" type="font/woff2" crossorigin>` — `crossorigin` is mandatory even same-origin.
- [ ] `@font-face { font-display: swap }` for both; if CLS from the swap exceeds 0.02 on `/`, add metric-matched fallbacks (`size-adjust`, `ascent-override`, `descent-override`) for `Inter` → Arial and `JetBrains Mono` → Menlo/Consolas.
- [ ] No third-party font CSS, no Google Fonts.

### R.9.2 Performance

- [ ] Every route's above-the-fold content is in the prerendered HTML (trace SVG, panel frames with reserved heights, first 100 log lines, target rows). Live data replaces, never inserts, so CLS stays < 0.1.
- [ ] Panel bodies have fixed heights from `gridPos.h × 30 px + margins`; charts mount into a box that already has its final size (no layout shift when uPlot appears).
- [ ] JS budget (gzip): shared runtime + `/` ≤ 90 kB; `/dashboard` ≤ 150 kB (includes uPlot + gridstack, which load only there); no other route above 110 kB. Checked by `vite build` output in CI logs and a Playwright test summing transferred script bytes.
- [ ] No render-blocking third-party requests; no requests to other origins at all.
- [ ] `_app/immutable/*` and `/fonts/*` cached `immutable`; HTML/`__data.json` revalidated with ETag.
- [ ] Charts are created after hydration in `$effect`; live tail and draw-in animations use `requestAnimationFrame`, never per-frame layout reads; `content-visibility: auto` on below-the-fold panels.
- [ ] Images: only `apple-touch-icon.png`; OG images are referenced by meta tags, never loaded by the page.

### R.9.3 Motion

- [ ] Every animation lives behind `@media (prefers-reduced-motion: no-preference)` in CSS, and JS animations check `motion.reduced` (typewriter tail → instant, trace bars → static, gridstack `animate: false`).
- [ ] 60 fps: animate only `transform`/`opacity`; the trace draw-in uses `stroke-dashoffset` on SVG paths or `transform: scaleX` on rects.

### R.9.4 Keyboard and screen readers

- [ ] Skip link to `<main>`; visible `:focus-visible` ring on every interactive element (2 px, palette blue).
- [ ] Trace: rows are `role="listbox"`/`role="option"` with roving `tabindex`; `j/k/↑/↓` move, `Enter` opens the drawer (`role="dialog"`, focus trapped, `Esc` closes and restores focus). Minimap handles are `<input type="range">` pairs.
- [ ] Panels: header kebab is `<button aria-haspopup="menu">`; menu items include "Move up/down/left/right" and "Resize +/−" so drag-and-drop has a keyboard equivalent; "Explore" and "View query" are real links/buttons.
- [ ] Toasts: container `role="status" aria-live="polite"`; "Silence" is a button with an accessible name including the alert name.
- [ ] Logs: query bar is a `combobox` with `aria-expanded`/`aria-activedescendant` for autocomplete; level chips are `aria-pressed` toggle buttons; expanded JSON uses `<details>`.
- [ ] Sticky TOC on postmortems is `<nav aria-label="Contents">`; on mobile it collapses to `<details>`.
- [ ] Every route sets `<title>` and `<meta name="description">`; `<html lang="en">`; heading order h1 → h2 → h3 without gaps; all icons `aria-hidden` with text labels.
- [ ] Contrast: all text ≥ 4.5:1 against panel/background in all three themes (axe `color-contrast` in the Playwright suite fails the build); muted text uses a token that passes, not opacity.
- [ ] Targets ≥ 24 × 24 CSS px (WCAG 2.2 SC 2.5.8; Lighthouse `target-size`), primary nav and chips ≥ 40 px on touch.
- [ ] `meta-viewport` present without `user-scalable=no`.

### R.9.5 390 px layout rules

- [ ] No horizontal page scroll at 390 px: any wide element (log line JSON, curl snippets, timeline tables) scrolls inside its own `overflow-x: auto` box.
- [ ] Trace: below 640 px the waterfall becomes the vertical timeline (same DFS order, indent by depth, durations as text); the minimap hides; the drawer becomes a full-height bottom sheet.
- [ ] Dashboard: gridstack `columnOpts: { breakpoints: [{ w: 640, c: 1 }] }` → one column, drag disabled below 640 px (reorder via the kebab menu); time-range picker becomes a `<select>`.
- [ ] Logs: chips wrap; each line shows `ts level service msg` on two lines; live tail button is fixed at the bottom with safe-area padding.
- [ ] Uptime: 90-day heartbeat bar renders 90 daily cells at 390 px (hourly cells from 1024 px); latency as text under the name.
- [ ] Typography: 16 px base, 14 px mono for data, never below 12 px; line length ≤ 75 ch for postmortem prose.
- [ ] Tested in Playwright with `viewport: { width: 390, height: 844 }` for every route plus the trace keyboard flow.

### R.9.6 Enforcement

| Check | Tool | Gate |
|---|---|---|
| Lighthouse ≥ 95 ×4 on 8 URLs, mobile | `@lhci/cli` `autorun` with `web/lighthouserc.json` assertions (`categories:performance/accessibility/best-practices/seo ≥ 0.95`, 3 runs, median) | blocking from Phase 4 exit |
| axe violations = 0 (WCAG 2.2 AA tags) | `@axe-core/playwright` per route, both themes | blocking from Phase 3 |
| Keyboard flows | Playwright specs (`trace-keyboard.spec.ts`, `dashboard-hash.spec.ts`, `logs-query.spec.ts`, `easter-eggs.spec.ts`) | blocking from Phase 3/4 |
| Bundle budget | `vite build` sizes + Playwright transfer-size assertion | blocking from Phase 3 |
| Reduced motion | Playwright with `reducedMotion: 'reduce'` asserting no running animations (`getAnimations()` empty) | blocking from Phase 4 |

## R.10 What each phase owes this section

| Phase | Deliverables from this section |
|---|---|
| 1 | `api/` skeleton with all subcommands, `.env.example`, Makefile (all targets except `e2e`, `lighthouse`, `deploy`), `schemagen`, `validate`, `noweb` build tag, static handler with `/` negotiation, `deploy/Dockerfile` stages 1 and 4 buildable, `ci.yml` `api` job |
| 2 | `schema/` regenerated and committed for the final content structs; `make validate --strict` green; `web` job's `gen:types` produces the first `types.gen.ts` |
| 3 | `web/` scaffold (`npx sv create web --template minimal --types ts --no-add-ons`, then tailwindcss/prettier/eslint/vitest/playwright add-ons), routes `/`, `/dashboard`, `/trace/[id]`, `/404`, `build-with-api.mjs`, `make web`/`make build` producing an embedded binary, Playwright + axe baseline, LHCI job (non-blocking) |
| 4 | remaining routes, theming, grid, easter eggs, `spa.html` fallback path, sitemap, all §R.9 boxes ticked, LHCI blocking |
| 5 | `release.yml`, compose, Caddyfile, systemd unit, deploy.sh, README one-liners, `docs/runbook.md` |
