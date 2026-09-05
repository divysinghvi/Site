# Vercel adaptation (delta spec — HIGHEST precedence, overrides every other draft where they differ)

Hosting changed from "Hetzner + Caddy + divy.dev" to **Vercel, no custom domain** (site URL = `https://<project>.vercel.app`; configurable via `SITE_ORIGIN`). Verified against the `@vercel/go` v10 builder source (vercel/vercel `packages/go`, `packages/frameworks`, `packages/fs-detectors`) and Turso/Vercel docs on 2026-09-05.

## Facts (verified)
| Fact | Consequence |
|---|---|
| Vercel "Go" framework preset = **standalone server mode**: detects root `go.mod` + one of `main.go`, `cmd/api/main.go`, `cmd/server/main.go`; builds `go build ./cmd/api` with `GOOS=linux CGO_ENABLED=0`; runs the binary as ONE function; the app must listen on `$PORT`; routing = `handle: filesystem` then `/(.*)` → the function | The whole chi server deploys unchanged. Everything (HTML, assets, API) is served by Go, so `X-Divy-Trace-Id`, `/` Accept negotiation and all easter eggs work exactly as specified. |
| The preset sets `ignoreRuntimes: ['@vercel/go']` | Files under `api/` are NOT compiled as separate functions — but we still avoid a top-level `api/` directory to prevent confusion with Vercel's functions convention. |
| `findGoModPath` only looks at the project root | `go.mod` MUST be at the repo root. |
| `buildCommand` (vercel.json or dashboard) replaces the default build; the builder then looks for the binary at `$VERCEL_OUTPUT_FILE` (or the newest binary produced) | Two-pass build works: build once → run it → prerender the web app against it → build the final binary with the embedded web output into `$VERCEL_OUTPUT_FILE`. |
| Per-function options read from `vercel.json` `functions["cmd/api/main.go"]` (`maxDuration`, `memory`) | Set `maxDuration: 60` (Hobby ceiling without Fluid; Fluid allows 300). Every request must finish well under 10 s to be safe on any plan. |
| Filesystem is ephemeral in functions | Time series need an external DB: **Turso (libSQL = SQLite dialect)**. Vercel Marketplace "Turso Cloud" injects `TURSO_DATABASE_URL` + `TURSO_AUTH_TOKEN`. Free tier: 5 GB, 500 M row reads / 10 M row writes per month. |
| Go client: `github.com/tursodatabase/libsql-client-go/libsql` (pure Go `database/sql` driver, `libsql://host?authToken=…`) | Store gets ONE driver switch: `file:` → `modernc.org/sqlite`; `libsql://`/`https://` → libsql. Same DDL (SQLite dialect), pragmas only for file mode. |
| Vercel Cron on Hobby: max once per day, hour-level precision, UTC | 5-minute probes and 15-minute GitHub collection are triggered by a **GitHub Actions schedule** calling the collect endpoint; a daily Vercel cron is the fallback. |
| HTTP → HTTPS is a 308 on Vercel | README shows `curl https://…`. |
| Vercel build image has Node 22 + the Go toolchain chosen from `go.mod` | `buildCommand` may run `npm ci && npm run build` in `web/`. |

## Layout (final — replaces the brief's `api/` + `collector/` dirs)
```
/                       go.mod (module divy.dev), go.sum, Makefile, .env.example, vercel.json, README.md
cmd/api/main.go         the `divy` CLI; with no subcommand (how Vercel runs it) it behaves as `serve` using env
internal/{model,content,store,server,promql,logql,collector,trace,metrics,ascii,og,schemagen,web}
internal/web/dist/      SvelteKit build output (adapter-static `pages`/`assets` = ../internal/web/dist), embedded via //go:embed all:dist; only dist/.gitkeep is committed
web/                    SvelteKit 2 + Svelte 5 + Tailwind v4 (adapter-static, full prerender, fallback 200.html)
content/  schema/  docs/  deploy/{Dockerfile,docker-compose.yml,README.md}  .github/workflows/{ci.yml,collect.yml}
```
No `noweb` build tag: if the embedded dist has no `index.html`, `/` (browser Accept) returns the JSON hint; `curl -H 'Accept: text/plain'` still returns the ASCII trace.

## Runtime behaviour changes
- **Serve**: `PORT` env wins over `DIVY_ADDR` (Vercel sets PORT). Startup: load+validate content, open store, run migrations (idempotent), install OTel, listen. Must be ready < 2 s (cold start).
- **DB**: `DIVY_DB_URL` (default `file:./data/divy.db`); if `TURSO_DATABASE_URL` is set it wins and `TURSO_AUTH_TOKEN` is appended as `authToken`. Single-writer goroutine only in file mode; in libsql mode writes go straight to the driver (Turso serialises them; all writes are idempotent upserts).
- **Collectors**: `serve --collect` keeps the in-process scheduler (local/Docker). On Vercel there is no scheduler: `POST|GET /api/collect` runs one bounded collection round: auth `Authorization: Bearer $DIVY_COLLECT_TOKEN` (also accepts `$CRON_SECRET`, which Vercel Cron sends); 401 otherwise; round budget 8 s (`DIVY_COLLECT_BUDGET`, ctx deadline); collectors run concurrently with per-collector timeouts; long backfills (merged-PR pagination) are resumable via a cursor row in `collector_state(key, value)`; response = JSON summary `{ "collectors": [{name, ok, items, duration_ms, error}], "budget_ms", "truncated": bool }`. `.github/workflows/collect.yml`: `schedule: '*/5 * * * *'` + `workflow_dispatch`, curls `$SITE_ORIGIN/api/collect` with secret `DIVY_COLLECT_TOKEN` (repo secrets `SITE_ORIGIN`, `DIVY_COLLECT_TOKEN`). `vercel.json` `crons: [{ "path": "/api/collect", "schedule": "0 3 * * *" }]` as a daily fallback.
- **OTel**: root server spans are exported synchronously at span end (SimpleSpanProcessor → store) so an `X-Divy-Trace-Id` resolves immediately; child spans (sqlite, upstream HTTP) batch behind it and are flushed before the root span is written. Retention: 24 h / last 20 k spans. Never trace `/api/collect` internals beyond one span per collector.
- **Rate limiting**: per-instance token bucket (documented as per-instance on Vercel); Vercel's platform DDoS protection is the outer layer.
- **Uptime self-probe**: target url = `$SITE_ORIGIN/readyz`; note "probed from the same function: a full outage shows as a gap, not red" rendered on the uptime page.
- **Caching**: `Cache-Control: public, max-age=0, s-maxage=<n>, stale-while-revalidate=<m>` on prerendered HTML (`s-maxage=60`), `public, max-age=31536000, immutable` on `/_app/immutable/*`, the contract table's values on API responses (Vercel's CDN honours `s-maxage`).
- **Env** (`.env.example`): `PORT`, `DIVY_ADDR`, `DIVY_DB_URL`, `TURSO_DATABASE_URL`, `TURSO_AUTH_TOKEN`, `DIVY_CONTENT_DIR`, `DIVY_GITHUB_TOKEN`, `DIVY_GITHUB_LOGIN=divysinghvi`, `DIVY_GITHUB_PRIVATE_ORGS=gradr`, `DIVY_COLLECT_TOKEN`, `CRON_SECRET`, `DIVY_COLLECT_BUDGET=8s`, `SITE_ORIGIN`, `CORS_ORIGINS`, `TRUSTED_PROXIES`, `QUERY_LOOKBACK_DELTA=26h`, `DIVY_LOG_LEVEL`, collector cadences for local mode.

## Deploy (Phase 5, replaces Caddy/systemd/Hetzner)
- `vercel.json`: `{"framework":"go"}` is set in the dashboard OR `"projectSettings":{"framework":"go"}`; `"buildCommand": "make vercel-build"` (two-pass build writing to `$VERCEL_OUTPUT_FILE`), `"functions": {"cmd/api/main.go": {"maxDuration": 60}}`, `"crons": [...]`.
- `make vercel-build`: `go build -o /tmp/divy-pass1 ./cmd/api && (/tmp/divy-pass1 serve --addr 127.0.0.1:18090 --db file:/tmp/divy-build.db --content ./content &) && wait-for /readyz && (cd web && npm ci && npm run build) && kill %1 && go build -o "${VERCEL_OUTPUT_FILE:-bin/divy}" ./cmd/api`.
- `deploy/Dockerfile` (multi-stage, distroless static, same two-pass) + `deploy/docker-compose.yml` for the brief's `docker compose up` (api on :8080 with a file DB volume, `--collect` on) + root `compose.yaml` that includes it. No Caddy, no systemd, no Hetzner script.
- CI (`.github/workflows/ci.yml`): gofmt/vet/golangci-lint, `go test ./...`, `make validate`, `make gen-check`, `make promtool-check`, `npm run check && npm run build`, `docker build`. Deploys happen through Vercel's Git integration on push (no token in CI).
- Steps for Divy (README): import the repo in Vercel (Framework Preset: Go, Root Directory: repo root), add the Turso Cloud integration (injects the two env vars), set `DIVY_GITHUB_TOKEN`, `DIVY_COLLECT_TOKEN`, `SITE_ORIGIN`, `CRON_SECRET`; add the two GitHub repo secrets for `collect.yml`.
