# API

Every endpoint of the `divy` binary as built. One process serves all of it (on Vercel: one function). Base URL = `SITE_ORIGIN`.

Every response — including static files, 304, 404, 405, 429 and 500 — carries `X-Divy-Trace-Id` (32 hex) and `X-Divy-Trace-Sampled` (`1`|`0`). A sampled id resolves at `/api/traces/{id}` as soon as the response is complete (the root span is written synchronously; child spans are flushed before it). An inbound W3C `traceparent` is joined (the header then shows the caller's trace id); the sampling decision is always the server's.

Cache classes (`Cache-Control`): **Q15** `public, max-age=15, s-maxage=15` · **C60** `public, max-age=60, s-maxage=60` · **NS** `no-store` · **HTML** `public, max-age=0, s-maxage=60, stale-while-revalidate=300` · **A3600** `public, max-age=3600, s-maxage=3600` · **IMM** `public, max-age=31536000, immutable`. Errors are always `no-store`.

## Aux

| Endpoint | Returns | Cache |
|---|---|---|
| `GET /` | `Accept: text/plain` (curl) → the ASCII career trace; otherwise the site (`index.html`) or, without an embedded site, a JSON hint (404) | HTML / NS |
| `GET /ascii` | the ASCII trace, `text/plain` | C60 |
| `GET /healthz` | `{"status":"ok","open_to":["backend intern","infra","sre"],"tz":"Asia/Kolkata"}` | NS |
| `GET /readyz` | `{"status":"ok"\|"degraded"\|"shutting_down", "checks": {db: {ok, storage: file\|libsql\|ephemeral}, collectors: {…}}}`; 503 when not ready | NS |
| `GET /robots.txt` | includes `# Observability for humans: /metrics` and the curl hints | A3600 |
| `GET /favicon.svg` | live sparkline of the last 7 days of commits | Q15 |
| `GET /favicon.ico` | 404 pointing at `/favicon.svg` (`max-age=86400`) | — |
| `GET /metrics` | Prometheus text exposition (passes `promtool check metrics`): catalogue of stored series (staleness cut-off `max(3×interval, 15m)`), live series (`divy_uptime_seconds`, `divy_build_info`, `divy_open_to_work`, `divy_experience_years`), `divy_http_*`, `divy_collector_*`, `divy_otel_*`, Go/process metrics | NS |
| `GET /_app/*`, `/fonts/*` | immutable assets | IMM |

## Content (`/api/content/*`, JSON, C60)

`services`, `spans` (the tree, TODO fallbacks resolved), `logs` (`application/x-ndjson`, ordered), `postmortems`, `postmortems/{id}` (sanitized HTML, TOC, `og_image`, `span_url`), `panels`, `alerts`, `uptime` (targets as configured), `manual-metrics` (values with `source: manual` and `last_updated`), `profile`, `todos` (every `TODO(divy)` with file, line, column, path, context). Unknown id → 404 `{"error":"…"}`.

## Uptime (JSON, Q15)

| Endpoint | Notes |
|---|---|
| `GET /api/uptime` | targets with current status, latency, 90-day daily `up_ratio`, incidents (≥ 2 consecutive failed probes), per-target `note` |
| `GET /api/uptime/heartbeats?days=90&bucket=1d\|1h` | heartbeat cells per target |

## Traces (Jaeger JSON shape)

| Endpoint | Notes | Cache |
|---|---|---|
| `GET /api/traces/career` (alias of the fixed career trace id) | the career trace from `content/spans.yaml`: `{"data":[{traceID, spans:[{spanID, operationName, references, startTime, duration, tags, logs, processID}], processes}], "total":0, "limit":0, "offset":0, "errors":null}` | Q15 |
| `GET /api/traces/{32 hex}` | a self-trace from `otel_spans`; 400 for a malformed id, 404 when unknown or expired (24 h / last 20 k spans) | NS |
| `GET /api/traces?service=&operation=&tags=&minDuration=&maxDuration=&start=&end=&limit=` | search (`service` required; `start`/`end` in microseconds, default the last hour) | NS |
| `GET /api/services` | content services + `divy-api` | C60 |
| `GET /api/services/{service}/operations`, `GET /api/operations?service=` | span ids of a content service; recorded operations of `divy-api` | C60 |

Self-trace spans: `HTTP <METHOD> <route>` (server; `http.method`, `http.route`, `http.status_code`, `url.path`, `url.query` on API routes, `http.response.body.size`, `divy.cache`, `divy.ratelimited`), `sqlite.select` (client; `db.system`, `db.statement` summarized, `divy.rows`, `divy.samples`), `outbound <METHOD> <host>` (client; `server.address`, `http.response.status_code`), `collector.<name>` (a new root per run, linked to the triggering request; `divy.collector`, `divy.items`, `divy.ok`, `divy.result`). Never stored: client IPs, user agents, full URLs, headers.

## Prometheus HTTP API (`/api/v1/*`)

Envelope `{"status":"success","data":…}` / `{"status":"error","errorType":"bad_data|not_found|unavailable|internal|execution","error":"…"}`. Unknown path → 404 in the envelope; wrong method → 405 with `Allow`. `GET` and `POST` (form body) where Prometheus accepts both.

| Endpoint | Params | Cache |
|---|---|---|
| `query` | `query`, `time`, `timeout`, `lookback_delta` | Q15 |
| `query_range` | `query`, `start`, `end`, `step`, `timeout`, `lookback_delta` | Q15 |
| `series` | `match[]` (repeatable), `start`, `end` | Q15 |
| `labels`, `label/{name}/values` | `match[]`, `start`, `end` | Q15 |
| `metadata` | `metric`, `limit` | C60 |
| `status/buildinfo` | — (`version`, `revision`, `branch`, `buildUser`, `buildDate`, `goVersion`) | C60 |
| `rules`, `alerts` | `content/alerts.yaml` as Prometheus reports rules; alert state is evaluated client-side | C60 |
| `query_exemplars` | always an empty result | Q15 |

Weak `ETag` + `If-None-Match` → 304 on the query endpoints. The supported language is [promql-subset.md](promql-subset.md); default lookback 26 h (`QUERY_LOOKBACK_DELTA`).

## Loki HTTP API (`/loki/api/v1/*`)

Same success envelope; errors are `text/plain` (Loki style). Unknown path → 404 text; wrong method → 405 with `Allow`.

| Endpoint | Params | Cache |
|---|---|---|
| `query_range` | `query`, `start`, `end`, `limit` (≤ 5000 for log queries), `direction`, `step` | Q15 |
| `query` | `query`, `time`, `limit`, `direction` | Q15 |
| `labels`, `label/{name}/values` | `start`, `end`, `query` | C60 |
| `series` | `match[]`, `start`, `end` | C60 |
| `index/stats`, `index/volume` | `query`, `start`, `end` | C60 |
| `status/buildinfo` | — | C60 |

The supported language is [logql-subset.md](logql-subset.md). The weak ETag hashes `resultType` + `result` (not `stats`).

## OG images (PNG, A3600)

`GET /og/default.png`, `GET /og/postmortems/{id}.png`, `GET /og/{id}.png` (alias). 1200×630, rendered server-side from the postmortem's title, severity and summary; unknown id → 404.

## Collect

`GET|POST /api/collect` with `Authorization: Bearer $DIVY_COLLECT_TOKEN` (or `$CRON_SECRET`). 401 otherwise. Runs one bounded round (`DIVY_COLLECT_BUDGET`, default 8 s; `?budget=` can only narrow it) of the collectors that are due; `?force=1` runs all. Response: `{"collectors":[{name, ok, items, duration_ms, error}], "budget_ms", "truncated"}`. Never cached, never rate-limited by the cache, always traced (one linked root span per collector run).

## Middleware order and headers

`recover → trace (X-Divy-Trace-Id) → security headers → request context → HTTP metrics → request log → GetHead → client IP → rate limit → CORS → response cache → routes`.

| Concern | Behaviour | Knobs |
|---|---|---|
| Client IP | the TCP peer; `X-Real-IP` / right-most untrusted `X-Forwarded-For` only from `TRUSTED_PROXIES` or when `TRUST_PROXY_HEADERS` is on (default on Vercel) | `TRUSTED_PROXIES`, `TRUST_PROXY_HEADERS` |
| Rate limit | token bucket per client IP; `/healthz`, `/readyz`, `/metrics` share one global bucket; `/_app/*`, `/fonts/*` free. 429 in the family envelope with `Retry-After`. Per instance on Vercel | `RATE_LIMIT_RPS` (20), `RATE_LIMIT_BURST` (100), `RATE_LIMIT_GLOBAL_RPS` (50), `RATE_LIMIT_GLOBAL_BURST` (200) |
| CORS | exact-origin allow-list, no credentials; preflight 204 on the API paths; exposes `X-Divy-Trace-Id, X-Divy-Trace-Sampled, ETag, X-Cache, Retry-After` | `CORS_ORIGINS` |
| Response cache | in-memory LRU for `/api/*`, `/loki/*`, `/ascii`, `/`: Q15 entries expire in 15 s and with the store generation, C60 in 60 s and with the content hash; every entry is dropped after a collector run. `X-Cache: HIT|MISS`, weak `ETag`, 304 | `RESPONSE_CACHE` |
| Security headers | `X-Content-Type-Options: nosniff`, `Referrer-Policy`, `X-Frame-Options: DENY`, `Permissions-Policy`; HSTS is Vercel's | — |
| Request log | method, route pattern, status, bytes, duration, request id (= trace id) — never IPs, user agents or query values | `DIVY_LOG_LEVEL`, `DIVY_LOG_FORMAT` |
