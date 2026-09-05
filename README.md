# divy.dev

A working observability stack whose only monitored service is a person. Metrics, logs, traces, uptime, alerts and postmortems about Divy's career — served by one Go binary that speaks the real protocols, so the same URL is a Prometheus data source, a Loki data source and a Jaeger-shaped trace API.

Live: https://websites-alpha-indol.vercel.app

Nothing is faked. A number that has no source yet is a `TODO(divy)` in `content/`, a manual metric is labelled `source: manual` with its last-updated date, and an empty panel names the source that is missing.

## Run it locally

```sh
docker compose up --build        # http://localhost:8080 — API + embedded site + collectors, SQLite in the `data` volume
```

or from source (Go 1.24, Node 22):

```sh
make setup                        # .env from .env.example, go modules, web deps
make dev                          # API on :8080 with --collect, Vite on :5173 (proxies /api, /metrics, /loki, …)
```

`make help` lists every target (lint, test, validate, gen, promtool-check, web-build, docker…). Set `DIVY_GITHUB_TOKEN` in `.env` for the GitHub series; without it the GitHub collector is reported as skipped and those panels stay empty.

## Add it as a Prometheus and Loki data source in your Grafana

1. Connections → Data sources → Add new data source → **Prometheus**. URL: `https://websites-alpha-indol.vercel.app`. Leave server access (proxy) mode; no auth. Save & test — Grafana calls `/api/v1/status/buildinfo` and `query=1+1`.
2. Add another → **Loki**. URL: `https://websites-alpha-indol.vercel.app`. Save & test — Grafana calls `/loki/api/v1/labels`.
3. Explore: `rate(github_commits_total[7d]) * 86400`, `{service="gradr"} |= "promoted"`, `sum by (org) (increase(github_merged_prs_total[365d]))`. The supported subsets are exact: [docs/promql-subset.md](docs/promql-subset.md), [docs/logql-subset.md](docs/logql-subset.md).

Grafana in browser access mode needs `CORS_ORIGINS=https://your-grafana` on the server.

## curl

```sh
B=https://websites-alpha-indol.vercel.app
curl -s $B/metrics | promtool check metrics                 # a real exposition; passes
curl -s $B/healthz                                          # {"status":"ok","open_to":["backend intern","infra","sre"],"tz":"Asia/Kolkata"}
curl -s -H 'Accept: text/plain' $B/                         # the career as an ASCII trace waterfall
curl -s $B/robots.txt                                       # "# Observability for humans: /metrics"
ID=$(curl -sI $B/healthz | awk 'tolower($1)=="x-divy-trace-id:"{print $2}' | tr -d '\r')
curl -s $B/api/traces/$ID                                   # the request you just made, as its own span tree
curl -s "$B/api/v1/query?query=divy_uptime_seconds"
curl -s "$B/loki/api/v1/query_range?query=%7Bservice%3D%22gradr%22%7D"
```

Every response — HTML, assets, API, 304, 404, 429 — carries `X-Divy-Trace-Id`; paste it into the trace viewer at `/trace/<id>` or fetch `/api/traces/<id>`. Traces are sampled (`OTEL_SAMPLE_RPS`, 100/s by default) and kept 24 h.

## Deploy on Vercel

1. Import the repository. Framework Preset **Go**, Root Directory = repository root. `vercel.json` sets the build command (`deploy/vercel-build.sh`: build the API, prerender the SvelteKit app against it, build the final binary with the site embedded) and a daily `/api/collect` cron.
2. Storage → Marketplace → **Turso Cloud**: the integration injects `TURSO_DATABASE_URL` and `TURSO_AUTH_TOKEN`. Without them the function keeps its samples in `/tmp` and loses them with the instance (`/readyz` says `storage: ephemeral`).
3. Environment variables: `SITE_ORIGIN=https://websites-alpha-indol.vercel.app`, `DIVY_COLLECT_TOKEN` (`openssl rand -hex 32`), `CRON_SECRET` (Vercel Cron sends it as the bearer token), `DIVY_GITHUB_TOKEN` (classic PAT, `repo` + `read:user`, or `public_repo` for public data only). Everything else in `.env.example` has a safe default.
4. Repository secrets `SITE_ORIGIN` and `DIVY_COLLECT_TOKEN`: `.github/workflows/collect.yml` calls `POST $SITE_ORIGIN/api/collect` every 5 minutes (probes, GitHub, PyPI, manual, retention — each collector only when it is due). Without the secrets the workflow skips.
5. Push. Deploys go through Vercel's Git integration; CI (`.github/workflows/ci.yml`) lints, tests, validates content, checks generated files, runs `promtool check metrics` against a live binary, builds the site the way Vercel does and builds the Docker image.

Rate limiting on Vercel is per function instance (`RATE_LIMIT_RPS`, default 20/s per client); the platform's DDoS protection is the outer layer. Details: [docs/deploy.md](docs/deploy.md).

## Fill the TODO(divy) markers

Every fact not supplied in the brief is a `TODO(divy)` in `content/` (dates, PR links, numbers). Nothing is invented.

```sh
make validate                                   # schema + cross-file rules, warnings fail (--strict)
make todos                                      # = go run ./cmd/api validate --list-todos: file:line:col, path, context
curl -s $B/api/content/todos                    # the same inventory over HTTP
```

Edit the YAML/NDJSON/Markdown, run `make validate`, push. The site, `/metrics`, the Loki streams and the ASCII trace all read the same files.

## Repository layout

```
cmd/api/            the `divy` CLI: serve (default, what Vercel runs), collect, validate, schemagen, migrate, export-ascii, ping, version
internal/
  server/           chi router: Prometheus + Loki + Jaeger-shaped APIs, content, uptime, OG images, /metrics, static site, middlewares
  promql/ logql/    the query engines (documented subsets, table-driven tests)
  collector/        github (GraphQL), pypi, uptime probes, manual metrics, retention; runner, scheduler, /api/collect rounds
  store/            SQLite (modernc, file:) or Turso (libsql://) — same DDL, embedded migrations
  trace/            OpenTelemetry self-tracing: sampler, span processor, exporter to otel_spans, HTTP + collector wrappers
  metrics/          client_golang registry behind /metrics (catalogue of stored series + live series)
  content/ model/   loader, validation, JSON Schema source of truth (Go structs → schema/ → web/src/lib/api/types.gen.ts)
  og/ ascii/        server-side OG PNGs, the ASCII waterfall
  web/dist/         SvelteKit build output, embedded (only .gitkeep is committed)
content/            spans.yaml, logs.ndjson, postmortems/, panels.yaml, alerts.yaml, uptime.yaml, manual_metrics.yaml, profile.yaml — the only place prose lives
web/                SvelteKit 2 + Svelte 5 + Tailwind v4, adapter-static, full prerender
schema/             generated JSON Schemas (make gen / make gen-check)
deploy/             Dockerfile (multi-stage → distroless), docker-compose.yml, vercel-build.sh
.github/workflows/  ci.yml, collect.yml
docs/               see below
```

## Docs

- [docs/api.md](docs/api.md) — every endpoint, envelope, cache class and header
- [docs/promql-subset.md](docs/promql-subset.md) — exactly which PromQL is supported
- [docs/logql-subset.md](docs/logql-subset.md) — exactly which LogQL is supported
- [docs/collectors.md](docs/collectors.md) — GitHub/PyPI/uptime/manual/retention, the uptime API, the favicon sparkline
- [docs/deploy.md](docs/deploy.md) — Vercel, Docker, CI, environment
- [docs/DEVIATIONS.md](docs/DEVIATIONS.md) — every place the build departs from the plan, and why
- [docs/brief.md](docs/brief.md) — the original brief

## Honesty notes

- Manual metrics (`savely_active_users`, `lfx_applications`) come from `content/manual_metrics.yaml` and are labelled with `source: manual` and their `last_updated` date wherever they are shown.
- The site's own uptime target is probed from the same process: a full outage shows as a gap in the heartbeat bar, not as red. The uptime page says so.
- GitHub private-org activity (`gradr`) is counted, never named. Without `DIVY_GITHUB_TOKEN` the `github_*` and `oss_prs_open` series are absent, not zero.
- Self-traces are sampled and pruned (24 h / last 20 k spans); Prometheus scrapes of `/metrics` are never recorded.
- `/api/collect` collector failures are reported in the JSON summary and on `/readyz`; the GitHub Actions run stays green so the Actions tab is not a status page.
