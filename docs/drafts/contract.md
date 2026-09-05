# API contract: master endpoint table, response shapes, cross-cutting rules

This section is authoritative for **paths, methods, parameters, status codes, headers, cache classes and rate-limit classes**. JSON bodies are defined once, in the section that owns them; §K.2 names every shape and points at the owning example. Where two sections disagreed, "Inconsistencies found" (end of this section) records the quotes and the resolution; the tables below already apply every resolution.

## Cross-section notes

| # | Note | Affects |
|---|------|---------|
| K-X1 | **CORS runs before the response cache.** A cache entry holds only `Content-Type`, `Cache-Control`, `ETag` and the body; every per-request header (`X-Divy-Trace-Id`, `X-Divy-Trace-Sampled`, `Access-Control-*`, `Vary`, `X-Cache`) is set by middleware outside the cache. Chain: `recover → otelhttp → traceHeader → clientIP → rateLimit → cors → cache → handler`. | Storage §S.7 "Order", LogQL §L.5.4 |
| K-X2 | **Cache generation.** `store.Generation()` is bumped only by writes to `series`, `samples` and `probe_results` (collector jobs). `otel_spans` and `collector_runs` writes never bump it; otherwise the 1 s span-export batches would invalidate the 15 s query cache on every request. | Storage §S.1.5, §S.6 |
| K-X3 | **Uniform method rules.** `HEAD` is accepted wherever `GET` is (chi `middleware.GetHead`; net/http drops the body). `OPTIONS` on any `/api/*`, `/loki/*`, `/metrics`, `/healthz`, `/readyz` path is the CORS preflight (§K.3.3) and returns 204. Any other unsupported method → 405 with `Allow` in the family's error envelope (§K.3.4). POST bodies are `application/x-www-form-urlencoded`, capped at 1 MiB (`http.MaxBytesReader`) → 413 in the family envelope. | PromQL §P.7.1, LogQL §L.2, Repo §R.6.3 |
| K-X4 | Loki-family routes accept `POST` (form body) as well as `GET`, exactly as Loki registers them (`Methods("GET", "POST")` for query_range, query, labels, label values, series, index/stats; facts-contract F2). Grafana's Loki plugin sends `GET` (F4); the POST form costs nothing because `r.ParseForm` merges body and query string. | LogQL §L.2.2 |
| K-X5 | **Unknown paths under an API prefix never fall through to the static handler.** `/api/v1/*` → Prometheus 404 envelope; `/loki/*` → Loki text 404; any other `/api/*` → `{"error":"not found"}` 404. Only non-API paths get `404.html`. | Repo §R.6.3 |
| K-X6 | Cache classes (§K.3.2) replace the per-section `Cache-Control` prose; `/api/v1/rules`, `metadata`, `status/buildinfo`, `alerts`, `query_exemplars` are class C60, not the 15 s query class. | PromQL §P.7.1, Storage §S.6 |
| K-X7 | Two more `-ldflags -X` values, `version.Branch` and `version.BuildUser` (Makefile: `git rev-parse --abbrev-ref HEAD` / `$(USER)`; CI: `main` / `ci`; Dockerfile: build args), so both buildinfo endpoints are filled without guessing. Both buildinfo `version` fields carry `version.Version` verbatim (`v0.1.0`). | Repo §R.3.2, §R.4; LogQL §L.2.2 |
| K-X8 | `/metrics` sends `Cache-Control: no-store` (promhttp sets none); Prometheus scrapers ignore it, browsers and Caddy must not cache a scrape. | PromQL §P.9 |
| K-X9 | `X-Divy-Trace-Sampled: 0\|1` (LogQL L-X5) is part of the every-response header set (§K.3.1). | Storage, PromQL, Repo |

## K.1 Master endpoint table

Column key. **Params**: `name:type=default`, `(req)` = required, `[]` = repeatable; types `str`, `int`, `bool`, `ts` = Prometheus timestamp (§K.3.6), `lts` = Loki timestamp, `dur` = Prometheus duration or float seconds, `godur` = Go duration (`1s`, `500ms`), `sel` = selector, `us` = integer microseconds, `json-obj` = JSON object. **Body**: POST body (`form` = `application/x-www-form-urlencoded`, merged with the query string). **Shape**: §K.2 name → owning example. **Cache** / **RL**: classes from §K.3.2 / §K.3.5. Every row can also return 429 (class `ip`/`global`), 405, 413 (POST) and 500 as §K.3.4 describes; the Status column lists endpoint-specific codes only.

### K.1.1 Prometheus HTTP API — `/api/v1/*` (`Content-Type: application/json`, Prometheus envelope)

| Method(s) | Path | Params | Body | Shape (owner) | Status | Cache | RL | Notes |
|---|---|---|---|---|---|---|---|---|
| GET, POST | `/api/v1/query` | `query:str (req)`; `time:ts=now`; `timeout:dur=QUERY_TIMEOUT`; `limit:int=0`; `lookback_delta:dur=QUERY_LOOKBACK_DELTA`; `stats` ignored | form | `PromQueryResult` (PromQL §P.7.3) | 200; 400 `bad_data`; 422 `execution`; 503 `timeout` | Q15 | ip | Grafana health check sends `query=1%2B1&time=4` |
| GET, POST | `/api/v1/query_range` | `query:str (req)`; `start:ts (req)`; `end:ts (req, ≥ start)`; `step:dur (req, > 0; (end−start)/step ≤ 11000)`; `timeout`; `limit`; `lookback_delta`; `stats` ignored | form | `PromQueryResult` (`resultType` always `matrix`) (§P.7.3) | 200; 400; 422; 503 | Q15 | ip | range-vector / string expressions → 400 (§P.10.3 H10) |
| GET, POST | `/api/v1/series` | `match[]:sel (req, ≥ 1)`; `start:ts=−∞`; `end:ts=+∞`; `limit:int=0` | form | `PromSeriesResult` (§P.7.3) | 200; 400 | Q15 | ip | live series always listed |
| GET, POST | `/api/v1/labels` | `start:ts`; `end:ts`; `match[]:sel`; `limit:int=0` | form | `PromLabelsResult` (§P.7.3) | 200; 400 | Q15 | ip | |
| GET | `/api/v1/label/{name}/values` | path `name` must match `[a-zA-Z_][a-zA-Z0-9_]*`; `start`; `end`; `match[]`; `limit` | — | `PromLabelsResult` (§P.7.3) | 200; 400 `invalid label name: "1bad"`; 405 (`Allow: GET, HEAD, OPTIONS`) | Q15 | ip | `__name__` values = metric names (PromQL autocomplete, Repo R7) |
| GET | `/api/v1/metadata` | `limit:int=−1`; `limit_per_metric:int`; `metric:str` | — | `PromMetadataResult` (§P.7.3) | 200 | C60 | ip | catalogue + live series only |
| GET | `/api/v1/status/buildinfo` | — | — | `PromBuildInfoResult` (§P.7.3) | 200 | C60 | ip | no `features` key ⇒ Grafana classifies the source as Prometheus |
| GET | `/api/v1/rules` | `type:alert\|record`; `rule_name[]`; `rule_group[]`; `file[]`; `exclude_alerts`, `match[]`, `group_limit`, `group_next_token` accepted and ignored | — | `PromRulesResult` (Content §C.7) | 200; 400 (unknown `type`) | C60 | ip | GET only, as Prometheus registers it (K-I6) |
| GET | `/api/v1/alerts` | — | — | `PromAlertsResult` (§P.7.3) | 200 | C60 | ip | always `{"alerts":[]}` |
| GET, POST | `/api/v1/query_exemplars` | `query:str (req, must parse)`; `start:ts`; `end:ts` (Grafana sends millisecond epochs; accepted, never rejected) | form | `PromExemplarsResult` = `[]` (§P.7.3) | 200; 400 | C60 | ip | |
| OPTIONS | `/api/v1/*` | — | — | — | 204 | NS | ip | preflight §K.3.3 |
| any other | `/api/v1/*` | — | — | `PromError` | 404 `not_found` `path not found`; wrong method 405 `bad_data` `method not allowed` + `Allow` | NS | ip | trailing slash is not stripped: `/api/v1/query/` → 404 |

### K.1.2 Loki HTTP API — `/loki/api/v1/*` (`application/json` on success; `text/plain; charset=utf-8` on error)

| Method(s) | Path | Params | Body | Shape (owner) | Status | Cache | RL | Notes |
|---|---|---|---|---|---|---|---|---|
| GET, POST | `/loki/api/v1/query_range` | `query:str (req)`; `start:lts=root span start`; `end:lts=now`; `since:dur`; `limit:int=100 (1..5000)`; `direction:forward\|backward=backward`; `step:dur=max(⌊(end−start)/250⌋, 1) s`; `interval` ignored | form | `LokiQueryRangeResult` (`streams` or `matrix`) (LogQL §L.2.2) | 200; 400; 504 | Q15 | ip | window `start ≤ ts < end`; `limit` ignored for metric queries |
| GET, POST | `/loki/api/v1/query` | `query:str (req)`; `time:lts=now`; `limit:int=100`; `direction=backward` | form | `LokiQueryResult` (`streams` or `vector`) (§L.2.2, §L.2.3) | 200; 400; 504 | Q15 | ip | Grafana Save & test: `vector(1)+vector(1)` |
| GET, POST | `/loki/api/v1/labels` | `start:lts`; `end:lts`; `since:dur`; `query:sel` | form | `LokiLabelsResult` (§L.2.2) | 200; 400 | Q15 | ip | |
| GET, POST | `/loki/api/v1/label/{name}/values` | `start`; `end`; `since`; `query:sel` | form | `LokiLabelsResult` (§L.2.2) | 200; 400 | Q15 | ip | unknown label → `"data":[]` |
| GET, POST | `/loki/api/v1/series` | `match[]:sel (req, ≥ 1)`; `start`; `end`; `since` | form | `LokiSeriesResult` (§L.2.2) | 200; 400 | Q15 | ip | |
| GET, POST | `/loki/api/v1/index/stats` | `query:sel (req)`; `start`; `end` | form | `LokiIndexStats` (§L.2.2) | 200; 400 | Q15 | ip | no envelope (Loki shape) |
| GET | `/loki/api/v1/index/volume` | `query:sel (req)`; `start`; `end`; `limit:int=100`; `targetLabels:str`; `aggregateBy:series\|labels=series` | — | `LokiVolumeResult` (§L.2.2) | 200; 400 | Q15 | ip | |
| GET | `/loki/api/v1/status/buildinfo` | — | — | `LokiBuildInfo` (§L.2.2) | 200 | C60 | ip | |
| any | `/loki/api/v1/tail`, `/detected_labels`, `/detected_fields`, `/detected_field/*`, `/patterns`, `/index/volume_range`, `POST /loki/api/v1/push`, any other `/loki/*` | — | — | text | 404 `not supported by divy.dev; see /loki/api/v1/status/buildinfo` | NS | ip | Grafana degrades gracefully (§L.2.5) |

### K.1.3 Traces — Jaeger JSON (`application/json`; errors `{"error":"…"}`)

| Method(s) | Path | Params | Body | Shape (owner) | Status | Cache | RL | Notes |
|---|---|---|---|---|---|---|---|---|
| GET | `/api/traces/{id}` | path `id` = `career` \| the career trace id `9f3a0703b53d5b0aae2fb3bdacea0ff6` \| any 32-hex OTel trace id | — | `JaegerTraceResponse` (Content §C.3.5 for the career trace; LogQL §L.4.3 for self-traces) | 200; 400 `invalid trace id "x": want "career" or 32 hex characters`; 404 `trace not found (self-traces are sampled and kept 24h; the career trace is /api/traces/career)` | career: Q15; OTel id: NS | ip | zero rows → `ForceFlush` (2 s) and one retry before 404 |
| GET | `/api/traces` | `service:str (req)`; `operation:str`; `tags:json-obj of strings`; `minDuration:godur`; `maxDuration:godur`; `limit:int=20 (≤ 100)`; `start:us`; `end:us` (default last 1 h for `divy-api`; ignored for career services); `lookback` ignored | — | `JaegerTraceResponse` (`total` = matches) (§L.4.1) | 200; 400 `parameter 'service' is required`; 400 `invalid tags: want a JSON object of string values` | Q15 | ip | Grafana Jaeger search form |
| GET | `/api/services` | — | — | `JaegerStringsResponse` (§L.4.1) | 200 | C60 | ip | Grafana Jaeger Save & test |
| GET | `/api/services/{service}/operations` | — | — | `JaegerStringsResponse` (§L.4.1) | 200; 404 `service not found` | C60 | ip | `divy-api` → distinct span names of the last 24 h |
| GET | `/api/operations` | `service:str (req)` | — | `JaegerOperationsResponse` (§L.4.1) | 200; 400 | C60 | ip | |

### K.1.4 Content and uptime (`application/json` unless noted; errors `{"error":"…"}`)

| Method(s) | Path | Params | Body | Shape (owner) | Status | Cache | RL | Notes |
|---|---|---|---|---|---|---|---|---|
| GET | `/api/content/services` | — | — | `ContentServices` (LogQL §L.6.8) | 200 | C60 | ip | file order |
| GET | `/api/content/spans` | — | — | `ContentSpans` = `spans.yaml` as JSON (§L.6.8) | 200 | C60 | ip | raw date strings; resolution happens only in `/api/traces/career` |
| GET | `/api/content/logs` | — | — | `application/x-ndjson`, the file verbatim (§L.6.8) | 200 | C60 | ip | |
| GET | `/api/content/postmortems` | — | — | `ContentPostmortemList` (Content §C.5.4) | 200 | C60 | ip | sorted by id |
| GET | `/api/content/postmortems/{id}` | path `id` `^INC-[0-9]{3}$` | — | `ContentPostmortem` (§C.5.4) | 200; 404 `postmortem not found` | C60 | ip | `html` sanitized; `toc` carries heading ids |
| GET | `/api/content/postmortems/{id}.md` | same | — | `text/markdown; charset=utf-8`, the raw file (§C.5.4) | 200; 404 | C60 | ip | |
| GET | `/api/content/panels` | — | — | `ContentPanels` = `panels.yaml` as JSON (§L.6.8) | 200 | C60 | ip | |
| GET | `/api/content/alerts` | — | — | `ContentAlerts` = `alerts.yaml` as JSON (§L.6.8) | 200 | C60 | ip | the Prometheus shape lives at `/api/v1/rules` |
| GET | `/api/content/uptime` | — | — | `ContentUptime` (§L.6.8) | 200 | C60 | ip | `self-api.url` = effective `UPTIME_SELF_URL`; `configured` computed |
| GET | `/api/content/profile` | — | — | `ContentProfile` (§L.6.8; pod fields per Content §C.3.7) | 200 | C60 | ip | `pods[].restarts`, `age_s` computed per request |
| GET | `/api/content/todos` | — | — | `ContentTodos` (Content §C.10.3) | 200 | C60 | ip | |
| GET | `/api/uptime/heartbeats` | `days:int=90 (1..90)`; `bucket:1d\|1h=1d` (`1h` requires `days ≤ 7`) | — | `UptimeHeartbeats` (Storage §S.4.3) | 200; 400 `days must be between 1 and 90` / `bucket=1h requires days<=7` | C60 | ip | |
| GET | `/api/uptime` | — | — | `UptimeHeartbeats`, byte-identical to `/api/uptime/heartbeats?days=90&bucket=1d` (§L.6.8) | 200 | C60 | ip | alias |
| any other | `/api/*` | — | — | `PlainError` | 404 `not found`; 405 `method not allowed` + `Allow` | NS | ip | K-X5 |

### K.1.5 Auxiliary, easter-egg and static

| Method(s) | Path | Params | Body | Shape (owner) | Status | Cache | RL | Notes |
|---|---|---|---|---|---|---|---|---|
| GET | `/metrics` | header `Accept` (text by default; protobuf when asked); `Accept-Encoding: gzip` honoured by promhttp | — | Prometheus text exposition `text/plain; version=0.0.4; charset=utf-8; escaping=underscores` (PromQL §P.9) | 200; 503 text `Limit of concurrent requests reached (8), try again later.` | NS | global | no sample timestamps; stale series hidden (§K.3.7) |
| GET | `/healthz` | — | — | `Healthz` (LogQL §L.6.5) | 200 | NS | global | liveness; exact body fixed by Content §C.9.2 |
| GET | `/readyz` | — | — | `Readyz` (§L.6.6) | 200; 503 (`status` = `unavailable` \| `shutting_down`) | NS | global | Docker `HEALTHCHECK`, compose, `deploy.sh`, the `self-api` probe, the prerender wait |
| GET, HEAD | `/` | `format=ascii`; header `Accept` (§L.6.1 rule) | — | text: ASCII waterfall `text/plain; charset=utf-8` (§L.6.2); otherwise `index.html` `text/html; charset=utf-8` (Repo §R.6.3) | 200; with `-tags noweb` the HTML branch is 404 `{"error":"web assets not embedded (built with -tags noweb); use the Vite dev server on :5173"}` | text: C60 + `Vary: Accept`; HTML: NC | ip | |
| GET, HEAD | `/ascii` | `width:int=80` (clamped to 60..200) | — | ASCII text (§L.6.1–L.6.2) | 200 | C60 | ip | same renderer as `divy export-ascii` |
| GET, HEAD | `/favicon.svg` | — | — | `image/svg+xml` (§L.6.3) | 200 | A3600 (ETag = sha256 of the body; cache keyed on the 7 daily counts) | ip | live sparkline; no-data fallback body |
| GET, HEAD | `/favicon.ico` | — | — | `image/x-icon`, static `web/static/favicon.ico` (§L.6.3) | 200 | A86400 | ip | not live |
| GET, HEAD | `/og/postmortems/{id}.png`, `/og/default.png` | path `id` `^INC-[0-9]{3}$` | — | `image/png` 1200×630 (§L.6.7, Repo §R.6.6) | 200; 404 `{"error":"postmortem not found"}` | A86400 (ETag; in-memory PNG keyed `(id, content hash)`) | ip | |
| GET, HEAD | `/robots.txt` | — | — | `text/plain; charset=utf-8`, exact body §L.6.4 | 200 | A3600 | ip | `Sitemap:` line uses `DIVY_PUBLIC_ORIGIN` |
| GET, HEAD | `/sitemap.xml` | — | — | `application/xml`, prerendered static (Repo §R.6.1) | 200 | A3600 | ip | |
| GET, HEAD | `/_app/immutable/*`, `/fonts/*` | — | — | embedded static (Repo §R.6.3) | 200; 404 | IMM | none | hashed / versioned file names |
| GET, HEAD | `/<route>` (when `<route>.html` exists), `/<route>/__data.json`, `/trace/*` (→ `spa.html`) | — | — | `text/html` / `application/json` (Repo §R.6.3) | 200; 308 to the slash-less path when the path ends in `/` | NC (strong ETag) | ip | the client router renders `/trace/[id]` |
| GET, HEAD | other embedded files (`/apple-touch-icon.png`, `/.well-known/*`, …) | — | — | by extension | 200 | A3600 | ip | |
| any | anything else | — | — | `404.html` `text/html` | 404 | NC | ip | never for `/api/*`, `/loki/*` (K-X5) |

## K.2 Response-shape catalogue

`Go type` = the field of `model.APIRoots` (Repo §R.5.1) that generates the TypeScript type of the same name. Examples are copied from the owning section; long bodies are referenced, not duplicated.

### K.2.1 Prometheus family

| Shape | Go type | Endpoints | Definition / minimal example |
|---|---|---|---|
| `PromSuccess` | envelope embedded by the types below | every 2xx under `/api/v1/` | `{"status":"success","data":<data>}` plus optional `"warnings":["results truncated due to limit"]` |
| `PromQueryResult` | `PromQueryResult` (`data.resultType` ∈ `vector`, `matrix`, `scalar`, `string`) | `/api/v1/query`, `/api/v1/query_range` | vector: `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"org":"gradr"},"value":[1788602400,"3"]}]}}` · matrix: `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"package":"codemind-ci"},"values":[[1788480000,"30"],[1788566400,"60"]]}]}}` · scalar: `{"status":"success","data":{"resultType":"scalar","result":[4,"2"]}}` · string: `{"status":"success","data":{"resultType":"string","result":[1788602400,"hello"]}}` (PromQL §P.7.3). Timestamps are JSON numbers (float seconds), values JSON strings (`"NaN"`, `"+Inf"`, `"-Inf"` allowed). Empty result = `[]`, never `null`. `PromRangeResult` (Repo §R.5.1) is the same type with `resultType` fixed to `matrix`. |
| `PromSeriesResult` | `PromSeriesResult` | `/api/v1/series` | `{"status":"success","data":[{"__name__":"github_stars","repo":"codemind"},{"__name__":"probe_success","target":"pypi"}]}` |
| `PromLabelsResult` | `PromLabelsResult` (also the repo's `PromLabelValuesResult`) | `/api/v1/labels`, `/api/v1/label/{name}/values` | `{"status":"success","data":["__name__","org","repo"]}` — sorted strings |
| `PromMetadataResult` | `PromMetadataResult` | `/api/v1/metadata` | `{"status":"success","data":{"github_stars":[{"type":"gauge","help":"Current stargazer count of a repository.","unit":""}]}}` |
| `PromBuildInfoResult` | `PromBuildInfoResult` | `/api/v1/status/buildinfo` | `{"status":"success","data":{"version":"v0.1.0","revision":"3f2a9c1","branch":"main","buildUser":"ci","buildDate":"2026-09-05T09:12:44Z","goVersion":"go1.26.8"}}` — `version`, `revision`, `branch`, `buildUser`, `buildDate` from `internal/version` (K-X7); `goVersion` = `runtime.Version()` |
| `PromRulesResult` | `PromRulesResult` | `/api/v1/rules` | Content §C.7 (full document); `state` always `inactive`, `health` `unknown`, `alerts` `[]` |
| `PromAlertsResult` | `PromAlertsResult` | `/api/v1/alerts` | `{"status":"success","data":{"alerts":[]}}` |
| `PromExemplarsResult` | `PromExemplarsResult` | `/api/v1/query_exemplars` | `{"status":"success","data":[]}` |
| `PromError` | `PromError` | every non-2xx under `/api/v1/` (400, 404, 405, 413, 422, 429, 500, 503) | `{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": 1:5: parse error: unclosed left parenthesis"}`; `errorType` ∈ `bad_data`, `execution`, `timeout`, `internal`, `unavailable`, `not_found` (§K.3.4) |

### K.2.2 Loki family

| Shape | Go type | Endpoints | Definition / minimal example |
|---|---|---|---|
| `LokiQueryRangeResult` | `LokiQueryRangeResult` (`data.resultType` ∈ `streams`, `matrix`) | `/loki/api/v1/query_range` | streams: LogQL §L.2.2 first example; matrix: §L.2.2 second example. Skeleton: `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"level":"warn","service":"gradr"},"values":[["1772323200000000008","<raw line>"]]}],"stats":{"ingester":{…},"store":{…},"summary":{…}}}}`. `values[i][0]` is a **string** of nanoseconds; matrix/vector timestamps are JSON numbers (seconds) and values are strings. |
| `LokiQueryResult` | `LokiQueryResult` (`streams` or `vector`) | `/loki/api/v1/query` | vector: `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1757030400.123,"2"]}],"stats":{…}}}` (§L.2.2) |
| `LokiLabelsResult` | `LokiLabelsResult` | `/loki/api/v1/labels`, `/loki/api/v1/label/{name}/values` | `{"status":"success","data":["component","level","service"]}` |
| `LokiSeriesResult` | `LokiSeriesResult` | `/loki/api/v1/series` | `{"status":"success","data":[{"level":"info","service":"gradr"}]}` |
| `LokiIndexStats` | `LokiIndexStats` | `/loki/api/v1/index/stats` | `{"streams":3,"chunks":3,"entries":4,"bytes":612}` — **no envelope** (Loki's shape) |
| `LokiVolumeResult` | `LokiVolumeResult` | `/loki/api/v1/index/volume` | `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"level":"info","service":"gradr"},"value":[1757030400,"318"]}]}}` |
| `LokiBuildInfo` | `LokiBuildInfo` | `/loki/api/v1/status/buildinfo` | `{"version":"v0.1.0","revision":"3f2a9c1","branch":"main","buildUser":"ci","buildDate":"2026-09-05T09:12:44Z","goVersion":"go1.26.8"}` — no envelope; same values as the Prometheus buildinfo (K-X7) |
| Loki error | — (text; no Go envelope type, K-I12) | every non-2xx under `/loki/` | `text/plain; charset=utf-8` + `X-Content-Type-Options: nosniff`; body = the message, e.g. `parse error at line 1, col 21: unsupported parser "logfmt" (supported: json)` |

### K.2.3 Traces

| Shape | Go type | Endpoints | Definition / minimal example |
|---|---|---|---|
| `JaegerTraceResponse` | `JaegerTraceResponse` | `/api/traces/{id}`, `/api/traces` | `{"data":[<Trace>,…],"total":0,"limit":0,"offset":0,"errors":null}`; by id: one Trace, `total`/`limit` 0; search: `total` = `limit` = number returned. `Trace` = `{traceID, spans[], processes{}, warnings:null}`; `Span` = `{traceID, spanID, operationName, references[], startTime (µs), duration (µs), tags[], logs[], processID, warnings:null}`; `KeyValue` = `{key, type ∈ string\|bool\|int64\|float64, value}`. Career example: Content §C.3.5; self-trace example: LogQL §L.4.3. |
| `JaegerStringsResponse` | `JaegerStringsResponse` | `/api/services`, `/api/services/{service}/operations` | `{"data":["divy","divy-api","edu","ef-polymer","euro-tech","gradr","oss","project","quant"],"total":9,"limit":0,"offset":0,"errors":null}` |
| `JaegerOperationsResponse` | `JaegerOperationsResponse` | `/api/operations` | `{"data":[{"name":"gradr.inc-001","spanKind":"internal"}],"total":1,"limit":0,"offset":0,"errors":null}` |

### K.2.4 Content, uptime, health

| Shape | Go type | Endpoints | Definition / minimal example |
|---|---|---|---|
| `Healthz` | `Healthz` | `/healthz` | `{"status":"ok","open_to":["backend intern","infra","sre"],"tz":"Asia/Kolkata"}` (Content §C.9.2, LogQL §L.6.5) |
| `Readyz` | `Readyz` | `/readyz` | LogQL §L.6.6 (full example). Skeleton: `{"status":"ok"\|"unavailable"\|"shutting_down","version","commit","uptime_s","checks":{"db":{"ok","latency_ms","error"?},"content":{"ok","files","spans","log_lines","todos","loaded_at"},"collectors":{"<name>":{"ok":true\|false\|null,"last_success","age_s","stale_after_s","disabled"?}}}}` |
| `ContentServices` | `ContentServices` | `/api/content/services` | `{"services":[{"id":"divy","title":"Divy","color":"#73bf69","counts_as_experience":false}]}` |
| `ContentSpans` | `SpansFile` (the content root, reused) | `/api/content/spans` | `{"version":1,"services":[…],"trace":{"id":"divy.career",…,"children":[…]}}` — the validated file, YAML → JSON, raw date strings |
| `ContentPostmortemList` | `ContentPostmortemList` | `/api/content/postmortems` | `{"items":[{"id":"INC-001","title":"…","severity":"SEV1","date":"TODO(divy)","span":"gradr.inc-001","services":["gradr"],"duration":"TODO(divy)","status":"resolved","tags":["startup-ordering"],"summary":"…","todo_count":5,"og_image":"https://divy.dev/og/postmortems/INC-001.png"}]}` (fields per Content §C.5.4; `og_image` absolute via `DIVY_PUBLIC_ORIGIN`, Repo R8) |
| `ContentPostmortem` | `ContentPostmortem` | `/api/content/postmortems/{id}` | the list item's fields plus `"html":"<h2 id=\"summary\">Summary</h2>…"`, `"toc":[{"level":2,"id":"summary","text":"Summary"}]`, `"markdown":"---\nid: INC-001\n…"`, `"span_url":"/#trace?span=gradr.inc-001"` (Content §C.5.4) |
| `ContentPanels` | `PanelsFile` | `/api/content/panels` | `{"version":1,"dashboard":{…},"panels":[…]}` — Content §C.6.2 as JSON |
| `ContentAlerts` | `AlertsFile` | `/api/content/alerts` | `{"groups":[…]}` — Content §C.7 rule file as JSON |
| `ContentUptime` | `ContentUptime` | `/api/content/uptime` | `{"targets":[{"id":"github-profile","name":"GitHub profile","url":"https://github.com/divysinghvi","method":"HEAD","expected_status":[200],"timeout":"10s","interval":"5m","follow_redirects":true,"span":null,"configured":true}]}` (LogQL §L.6.8) |
| `ContentProfile` | `ContentProfile` | `/api/content/profile` | `profile.yaml` as JSON with `pods[]` extended by `restarts` and `age_s` (LogQL §L.6.8) |
| `ContentTodos` | `ContentTodos` | `/api/content/todos` | Content §C.10.3 (full example). Skeleton: `{"generated_at","count","by_file":{…},"items":[{"file","line","col","path","context","text"}]}` |
| `UptimeHeartbeats` | `UptimeHeartbeats` | `/api/uptime/heartbeats`, `/api/uptime` | Storage §S.4.3 (full example). Skeleton: `{"generated_at","days","bucket","targets":[{"target","name","url","span","status":"up\|down\|unconfigured\|unknown","last":{…}\|null,"uptime":{"24h","7d","30d","90d"},"buckets":[{"ts","samples","up_ratio","avg_latency_ms","max_latency_ms"}],"incidents":[{"started_at","ended_at","duration_s","probes","first_error"}]}]}` |
| `PlainError` | `PlainError` | every non-2xx JSON response outside `/api/v1/` and `/loki/` | `{"error":"postmortem not found"}` |

### K.2.5 Non-JSON bodies

| Body | Endpoints | Content-Type | Owner |
|---|---|---|---|
| Prometheus text exposition (protobuf on request) | `/metrics` | `text/plain; version=0.0.4; charset=utf-8; escaping=underscores` | PromQL §P.9 |
| ASCII waterfall | `/` (negotiated), `/ascii` | `text/plain; charset=utf-8` | LogQL §L.6.2 |
| SVG sparkline | `/favicon.svg` | `image/svg+xml` | LogQL §L.6.3 |
| PNG | `/og/*` | `image/png` | LogQL §L.6.7 |
| robots | `/robots.txt` | `text/plain; charset=utf-8` | LogQL §L.6.4 |
| Markdown | `/api/content/postmortems/{id}.md` | `text/markdown; charset=utf-8` | Content §C.5.4 |
| NDJSON | `/api/content/logs` | `application/x-ndjson` | LogQL §L.6.8 |
| HTML, JS, CSS, fonts, JSON data | embedded site | by extension (`http.ServeFileFS`) | Repo §R.6.3 |

## K.3 Cross-cutting rules

### K.3.1 Headers on every response

| Header | Value | On |
|---|---|---|
| `X-Divy-Trace-Id` | 32 lowercase hex: the request's own OTel trace id | every response the router produces, including 304, 404, 405, 413, 429, 500, static files and `/metrics` (set before any write; LogQL §L.5.4) |
| `X-Divy-Trace-Sampled` | `1` if the span was recorded (resolvable at `/api/traces/{id}`), else `0` | same |
| `Content-Type` | per §K.1; JSON is `application/json` (no charset parameter: JSON is UTF-8 by definition) | every response with a body |
| `Cache-Control` | the class value (§K.3.2) | every response; all 4xx/5xx are `no-store` |
| `ETag` | `W/"<first 16 hex of sha256(body)>"` for classes Q15/C60/A3600/A86400 (weak: the body may be re-serialized); strong `"<sha256 prefix>"` for embedded static files and HTML (computed once at startup) | cacheable 200 responses; echoed on 304 |
| `X-Cache` | `HIT` \| `MISS` | classes Q15 and C60 only |
| `Vary` | `Accept` on `/`; `Origin` on any response that carries CORS headers | as stated |
| `Retry-After` | integer seconds, minimum `1` | 429 |
| `Allow` | the route's method list, e.g. `GET, HEAD, OPTIONS` or `GET, POST, HEAD, OPTIONS` | 405 |
| `Access-Control-*` | §K.3.3 | when `Origin` is allow-listed |
| `X-Content-Type-Options: nosniff` | — | Loki-family error bodies (as Loki does); every response in production via Caddy (Repo §R.6.5) |

Not sent by the binary: security headers (HSTS, CSP, Referrer-Policy, Permissions-Policy: Caddy), `Content-Encoding` (Caddy `encode`; exception: promhttp gzips `/metrics` itself when asked), `Server`.

### K.3.2 Cache classes

| Class | `Cache-Control` | ETag | Server-side response cache | Routes |
|---|---|---|---|---|
| **Q15** | `public, max-age=15` | weak | 15 s; key = `method \n path \n canonical query \n gen` (Storage §S.6; `gen` = `store.Generation()`, K-X2). Canonical query = params sorted by name (repeated names keep order), values percent-decoded then re-encoded, **and** every parsed `time`/`start`/`end` rewritten as integer milliseconds and every `step`/`timeout`/`lookback_delta` as integer milliseconds, so `start=1788602400` and `start=2026-09-05T10:00:00Z` share one entry (PromQL §P.6). `If-None-Match` equal to the entry's ETag → 304 with `ETag`, `Cache-Control`, `X-Cache` and no body. | `/api/v1/query`, `query_range`, `series`, `labels`, `label/*/values`; `/loki/api/v1/query_range`, `query`, `labels`, `label/*/values`, `series`, `index/stats`, `index/volume`; `/api/traces/career` (and its hex alias); `/api/traces?service=` |
| **C60** | `public, max-age=60` | weak | 60 s; same key with `gen` = the content hash (constant per process) | `/api/v1/rules`, `metadata`, `status/buildinfo`, `alerts`, `query_exemplars`; `/loki/api/v1/status/buildinfo`; `/api/services*`, `/api/operations`; `/api/content/*`; `/api/uptime*`; `/` (text branch), `/ascii` |
| **NS** | `no-store` | none | none | `/healthz`, `/readyz`, `/metrics`, `/api/traces/{otel id}`, `OPTIONS`, every 4xx/5xx |
| **NC** | `no-cache` | strong | none (files are in memory) | `index.html`, every `<route>.html`, `spa.html`, `404.html`, `*/__data.json` |
| **A3600** | `public, max-age=3600` | strong (`/favicon.svg`: sha256 of the generated body) | `/favicon.svg`: 3600 s keyed on the 7 daily counts | `/favicon.svg`, `/robots.txt`, `/sitemap.xml`, other embedded files |
| **A86400** | `public, max-age=86400` | strong | `/og/*`: in-memory PNGs keyed `(id, content hash)` | `/og/*`, `/favicon.ico` |
| **IMM** | `public, max-age=31536000, immutable` | strong | none | `/_app/immutable/*`, `/fonts/*` |

Entries are stored for status 200 only, bodies ≤ 4 MiB; LRU of 2,000 entries / 32 MiB (Storage §S.6). No client cache directive bypasses the server cache.

### K.3.3 CORS

`CORS_ORIGINS` (comma list; `.env.example`: `http://localhost:5173`; production default empty = no CORS headers at all). Only exact origin matches are allowed; no credentials. The `cors` middleware runs after rate limiting and before the cache (K-X1).

| Request | Response |
|---|---|
| `OPTIONS <path>` with `Origin` ∈ allow-list and `Access-Control-Request-Method` | 204, no body: `Access-Control-Allow-Origin: <origin>`, `Access-Control-Allow-Methods: GET, POST, HEAD, OPTIONS`, `Access-Control-Allow-Headers: Accept, Content-Type, If-None-Match, X-Requested-With`, `Access-Control-Max-Age: 600`, `Vary: Origin`. Paths: `/api/*`, `/loki/*`, `/metrics`, `/healthz`, `/readyz`. |
| `OPTIONS` from a non-listed `Origin` (or none) | 204 without `Access-Control-*` headers (the browser blocks the call) |
| any other method with `Origin` ∈ allow-list | the normal response plus `Access-Control-Allow-Origin: <origin>`, `Access-Control-Expose-Headers: X-Divy-Trace-Id, X-Divy-Trace-Sampled, ETag, X-Cache, Retry-After`, `Vary: Origin` |
| any request without an allow-listed `Origin` | unchanged |

Grafana proxies server-side and needs none of this; the Vite dev server proxies too (Repo §R.4.1), so `CORS_ORIGINS` only matters for a browser page on another origin calling the API directly.

### K.3.4 Error envelopes per family

| Family (path prefix) | Body by status | `Content-Type` | Example |
|---|---|---|---|
| Prometheus `/api/v1/` | `{"status":"error","errorType":"<type>","error":"<message>"}`: 400 `bad_data`; 404 `not_found`; 405 `bad_data` (`method not allowed`); 413 `bad_data` (`request body too large`); 422 `execution`; 429 `unavailable`; 500 `internal` (`internal error: <trace id>`); 503 `timeout` | `application/json` | `{"status":"error","errorType":"unavailable","error":"rate limit exceeded: 20 req/s per client, burst 100; retry after 1s"}` |
| Loki `/loki/` | plain text = the message: 400 parse/limit/parameter errors; 404 unsupported path; 405 `method not allowed`; 413 `request body too large`; 429 rate limit; 500 `internal error; trace id <id>`; 504 `query timed out after 5s` | `text/plain; charset=utf-8` + `X-Content-Type-Options: nosniff` | `max entries limit per query exceeded, limit > max_entries_limit_per_query (6000 > 5000)` |
| Traces, content, uptime, health, aux JSON (`/api/*` other than `/api/v1/`; `/healthz`, `/readyz`, `/og/*`, `/favicon.svg`, `/ascii`, `/`) | `{"error":"<message>"}`: 400; 404 `not found` / `trace not found …` / `postmortem not found`; 405 `method not allowed`; 429 `rate limit exceeded: …`; 500 `internal error: <trace id>` | `application/json` | `{"error":"postmortem not found"}` |
| `/metrics` | promhttp: 503 text `Limit of concurrent requests reached (8), try again later.`; 429 (global cap) `{"error":"rate limit exceeded: …"}` | `text/plain` / `application/json` | |
| Static (everything else) | `404.html` with status 404; 405 → `{"error":"method not allowed"}` | `text/html` | |

Rules: 500 bodies never contain Go error text (the details go to the log line carrying the same trace id); a recovered panic sets the span status to Error (LogQL §L.5.4). Client disconnects (499) write nothing.

### K.3.5 Rate-limit classes

| Class | Limiter | Routes | On exceed |
|---|---|---|---|
| **ip** | one token bucket per client IP, `RATE_LIMIT_RPS=20`, `RATE_LIMIT_BURST=100`; IP from `middleware.ClientIPFromXFF(TRUSTED_PROXIES…)` when set, else `RemoteAddr` (Storage §S.7) | every route not listed below | 429, `Retry-After: ⌈delay⌉ ≥ 1`, `Cache-Control: no-store`, family body (§K.3.4), counted in `divy_http_requests_total{code="429"}`, span attribute `divy.ratelimited=true` |
| **global** | one bucket for all clients, 50 r/s, burst 200 | `/healthz`, `/readyz`, `/metrics` | 429 as above |
| **none** | — | `/_app/*`, `/fonts/*` (immutable assets; page loads must not spend the visitor's budget) | — |
| engine concurrency | semaphore `QUERY_MAX_CONCURRENCY=20` for PromQL; LogQL evaluates in memory without a semaphore (≤ 100 lines) | `/api/v1/query*` | waiting counts against the query timeout → 503 `timeout` |
| promhttp in-flight | `MaxRequestsInFlight: 8` | `/metrics` | 503 text |

Order in the chain: `traceHeader` before `rateLimit`, so 429s carry the trace headers; `rateLimit` before `cache`, so cached hits still consume tokens (Storage §S.7).

### K.3.6 Time-format acceptance

| Family | Parameters | Accepted input | Normalized to | Output format | Error |
|---|---|---|---|---|---|
| Prometheus | `time`, `start`, `end` | float Unix seconds (`1788602400`, `1788602400.5`; fraction rounded to ms) or RFC 3339 / RFC 3339-nano (`2026-09-05T10:00:00Z`, `…T10:00:00.123+05:30`) | integer ms | JSON number, float seconds with ms precision (`1788602400`, `1788602400.5`) | 400 `invalid parameter "start": cannot parse "x" to a valid timestamp` |
| Prometheus | `step`, `timeout`, `lookback_delta` | float seconds (`15`, `0.5`) or Prometheus duration (`15s`, `1h30m`, `1d`; units `ms s m h d w y`, descending, each at most once) | integer ms | — | 400 `invalid parameter "step": cannot parse "x" to a valid duration` |
| Prometheus | `query_exemplars` `start`/`end` | as above; Grafana's millisecond epochs parse as huge second values and are accepted | — | — | never rejected |
| Loki | `start`, `end`, `time` | contains `.` → float seconds; all digits → **nanoseconds**; else RFC 3339 / RFC 3339-nano | integer ns | streams: `values[i][0]` = decimal ns **string**; matrix/vector: JSON number seconds (with fraction) | 400 `invalid parameter "start": cannot parse "x" as nanoseconds, float seconds or RFC3339`; `end < start` → 400 `end must be after start` |
| Loki | `since`, `step`, range `[R]` | Prometheus duration; `step` also float seconds | seconds / ns | — | 400 |
| Loki | defaults | `start` = the root span's resolved start `2023-01-01T00:00:00Z`; `end`, `time` = now (LogQL L-X2) | | | |
| Jaeger | `start`, `end` | integer **microseconds** since epoch | µs | `startTime`, `duration`, `logs[].timestamp` = integer µs | 400 `invalid parameter "start": want microseconds since epoch` |
| Jaeger | `minDuration`, `maxDuration` | Go durations (`1s`, `500ms`, `1h`) | ns | — | 400 |
| Uptime | `days` | integer 1..90 | days | RFC 3339 UTC strings (`generated_at`, `ts`, `started_at`, `ended_at`, `last.ts`) | 400 |
| Content, health | — | — | — | RFC 3339 UTC strings; `age_s`, `uptime_s`, `duration_s` integer seconds; `interval`/`timeout` echo the YAML duration strings | |
| Static | `If-None-Match` only (`If-Modified-Since` ignored: embedded files carry no mtime) | | | `Last-Modified` not sent | |

Window semantics: Prometheus range vectors `(t − R, t]`, range queries inclusive `[start, end]`; Loki log entries `start ≤ ts < end`, metric windows `(t − R, t]`; Jaeger search `start ≤ startTime ≤ end`.

### K.3.7 Pagination, limits, timeouts

There is no cursor pagination anywhere; every list is bounded by a limit.

| Endpoint / concern | Limit | Default | Over the limit |
|---|---|---|---|
| `/api/v1/query`, `query_range`, `series`, `labels`, `label/*/values` `limit` | `≥ 0`; `0` = unlimited | `0` | truncation adds `"warnings":["results truncated due to limit"]` |
| `/api/v1/query_range` points | `(end − start)/step ≤ 11000` | — | 400 `exceeded maximum resolution of 11,000 points per timeseries. Try decreasing the query resolution (?step=XX)` |
| PromQL samples per query | `QUERY_MAX_SAMPLES=2000000` | | 422 `query processing would load too many samples into memory in query execution` |
| PromQL wall time | `min(timeout, QUERY_TIMEOUT=30s)` | | 503 `query timed out in query execution` |
| `/api/v1/metadata` `limit` | `−1` = all | `−1` | truncated |
| Loki `limit` | 1..5000 | `100` (Grafana sends 1000) | 400 `max entries limit per query exceeded, limit > max_entries_limit_per_query (6000 > 5000)`; `< 1` → 400 `limit must be between 1 and 5000` |
| Loki metric steps | `(end − start)/step ≤ 11000` | | 400 `too many steps (N > 11000); increase step` |
| Loki metric series | ≤ 1000 | | 400 `query produced too many series (N > 1000); add a by() clause` |
| Loki `index/volume` `limit` | | `100` | top-N by bytes |
| Loki wall time | 5 s | | 504 `query timed out after 5s` |
| Jaeger search `limit` | 1..100 | `20` | clamped to 100 |
| `/api/traces/{id}` | one trace; self-traces kept 24 h / newest 20,000 | | 404 |
| `/api/uptime/heartbeats` | `days` 1..90; `bucket=1h` ⇒ `days ≤ 7` | `90`, `1d` | 400 |
| `/ascii` `width` | 60..200 | `80` | clamped |
| `/api/content/logs` | ≤ 100 lines (validator) | | — |
| POST body | 1 MiB | | 413 |
| `/metrics` stale series | hidden when `now − latest.ts > max(3 × collector interval, 15m)` (Storage §S.2.6) | | absent from the exposition; history still queryable |
| HTTP server | `ReadHeaderTimeout 10s`, `IdleTimeout 120s`, no `WriteTimeout` (query timeouts are context-based); `MaxHeaderBytes` 1 MiB (Go default) | | 408 / connection close |

### K.3.8 Path and method rules

| Rule | Detail |
|---|---|
| Matching | chi exact match; API paths are case-sensitive and trailing slashes are not stripped (`/api/v1/query/` → family 404). Static routes: a path ending in `/` whose `.html` exists → 308 to the slash-less path (Repo §R.6.3). |
| `GET` vs `POST` | POST is form-encoded and merged with the query string (`r.ParseForm`); a JSON body is not accepted (400 in the family envelope: `invalid parameter: body must be application/x-www-form-urlencoded`). |
| `HEAD` | chi `middleware.GetHead`: same handler and headers as GET, body dropped. |
| `OPTIONS` | §K.3.3; never reaches a handler. |
| Unknown query parameters | ignored everywhere (Grafana "custom query parameters"). |
| Repeated parameters | `match[]`, `rule_name[]`, `rule_group[]`, `file[]` are unions; any other repeated name uses the first value. |
| Percent-encoding | Go `url.ParseQuery` (`+` = space); Grafana and `curl -G --data-urlencode` both comply. |
| Route names in metrics and traces | `divy_http_requests_total{route}` and span names use the chi pattern (`/api/v1/query_range`, `/api/traces/{id}`, `/*` for static), never the raw path. |

## Inconsistencies found

Resolution rule: CONVENTIONS first, then the more protocol-correct draft, citing `facts-*.md`. "Changes" names the section that must adopt the resolution in the Revise step; the tables above already apply it.

| # | Where (file · quote) | Conflict | Resolution · Changes |
|---|---|---|---|
| K-I1 | `draft-storage.md` S-X2: "the PromQL engine's default lookback delta must be **26h** (constant `promql.DefaultLookback`…)" · `draft-promql.md` P3: "Server lookback delta defaults to `2h` (`QUERY_LOOKBACK_DELTA` …)" | two defaults for the same knob | **2h, env `QUERY_LOOKBACK_DELTA`**. Storage's own §S.2.3 rule 2 (live sample at every run) and §S.2.4 (1 h heartbeat) guarantee a sample within 2 h for every healthy series, so 26 h is unnecessary and would hide a dead collector for a day; the request parameter stays the real Prometheus `lookback_delta` (facts-promql "Staleness/lookback"). · storage S-X2, §S.2.3 rule 5 ("lookback 26h" → 2h) |
| K-I2 | `draft-promql.md` P4: "`/metrics` … a stored series whose latest sample is older than the lookback delta is not exposed" · `draft-storage.md` §S.2.6: "hidden when `now − latest.ts > staleAfter(metric)`, `staleAfter = max(3 × collector interval, 15m)`" | two staleness cut-offs for the exposition | **Storage's per-collector `staleAfter`**: it tolerates one missed PyPI run (60 m cadence) where a flat 2 h cut-off would flap, and `/readyz` (`draft-logql.md` §L.6.6 `stale_after_s`) already uses it. Query lookback (K-I1) and exposition staleness are separate rules. · promql P4 and §P.9 "Handler" row ("age ≤ `QUERY_LOOKBACK_DELTA`" → `staleAfter(metric)`) |
| K-I3 | `draft-content.md` §C.6.2 `commits-weekly`: "github_commits_total counts GitHub contribution-calendar events (commits, PRs, issues, reviews)"; `draft-promql.md` §P.9 HELP: "Cumulative GitHub contributions (commits, PRs, issues, reviews) per GitHub's contribution calendar." · `draft-storage.md` S-X1: "**`github_commits_total` counts commits only** … the calendar's mixed count … is exposed as … **`github_contributions_total`**" | metric semantics and HELP text disagree; the promql HELP list also lacks the five metrics storage and logql added | **Storage** (facts-storage G4 verifies the per-day commit series). HELP: `github_commits_total` = "Cumulative commits by divysinghvi per day (GitHub contributionsCollection; private repositories counted, never named)."; add `github_contributions_total` ("Cumulative GitHub contributions per the contribution calendar (commits, issues, PRs, reviews)."), `divy_collector_run_duration_seconds` (histogram), `divy_otel_spans_total{decision}`, `divy_otel_exported_spans_total`, `divy_otel_export_errors_total` (counters). Panel `commits-weekly` title/legend/description and the `HighContributionRate` summary say "commits". · promql §P.9; content §C.6.2, §C.7 |
| K-I4 | `draft-content.md` §C.6.2 `pypi-downloads`: `expr: 'sum(increase(pypi_downloads_total{package="codemind-ci"}[1d]))'` · `draft-promql.md` P1: "`[1d]` windows over daily-backfilled counters are always empty … must change to `rate(pypi_downloads_total{package="codemind-ci"}[2d]) * 86400`" | the panel would render empty | **PromQL P1** (facts-promql "Range selector: left-open"; evaluator rows E10/E11). · content §C.6.2 (expr + legend `downloads / day (2d avg)`); parser row 68 stays as a parse-only case |
| K-I5 | `draft-storage.md` §S.6: "`GET /api/content/*`, `/api/v1/rules`, `/api/traces/career`, `/api/uptime/heartbeats`: **60 s**" · `draft-content.md` §C.3.5: "`now` evaluated per request (`Cache-Control: max-age=15`)"; `draft-logql.md` §L.4.1: "career: `public, max-age=15`" | TTL of the career trace | **15 s (class Q15)**: open spans' `duration` moves with `now`, and two sections agree. · storage §S.6 route list |
| K-I6 | `draft-promql.md` §P.8: "Data source load … `POST /api/v1/rules` (form body, empty) — on 405/400 retried as `GET`" · `facts-promql.md`: "all others (`/api/v1/label/<name>/values`, `/api/v1/metadata`, `/api/v1/rules`, `/api/v1/query_exemplars`) use GET"; Prometheus registers `r.Get("/rules", …)` only (facts-contract F3) | method of `/api/v1/rules` | **GET only**; POST → 405 with `Allow: GET, HEAD, OPTIONS` (Grafana uses GET, so nothing breaks either way). · promql §P.8 row 2 wording |
| K-I7 | `draft-storage.md` §S.7: "embedded static assets under `/_app/*`" exempt · `draft-repo.md` §R.6.3: "`/_app/immutable/*` and `/fonts/*` are exempt from the per-IP token bucket" | exemption list | **Union: `/_app/*` and `/fonts/*`** = class `none` (fonts are as immutable and as page-load-critical as `_app`). · storage §S.7 "Scope" |
| K-I8 | `draft-promql.md` §P.7.1 table: 429 example `rate limit exceeded`; §P.10.3 H14: `"error":"rate limit exceeded"` · `draft-storage.md` §S.7: `rate limit exceeded: 20 req/s per client, burst 100; retry after 1s` | 429 message text | **Storage's full message** (it says what the limit is). · promql §P.7.1 example; H14 asserts the prefix |
| K-I9 | `draft-repo.md` R7: "`GET /api/uptime/heartbeats?days=90` returning hourly buckets per target"; §R.9.5: "90-day heartbeat bar renders 90 daily cells at 390 px (hourly cells from 1024 px)" · `draft-storage.md` §S.4.3: "`bucket` ∈ `1d` (default, `days` ≤ 90) \| `1h` (`days` ≤ 7)" | hourly buckets over 90 days are not served | **Storage**: 90 d × hourly × 5 targets is 10,800 rows per page load; the desktop bar shows 90 daily cells and switches to hourly cells only in the 7-day view (`?days=7&bucket=1h`). · repo R7, §R.9.5 |
| K-I10 | `draft-storage.md` §S.7 "Order": "… → rate limit → response cache → CORS → handler"; `draft-logql.md` §L.5.4: "`… → rateLimit → cache → cors → handler`" · CORS headers depend on the request's `Origin` and must not be replayed from a cache entry (`Vary: Origin` likewise) | the order would cache `Access-Control-Allow-Origin` (or omit it on hits) | **`cors` before `cache`** (K-X1); the cache stores only `Content-Type`, `Cache-Control`, `ETag`, body. · storage §S.7 "Order", logql §L.5.4 |
| K-I11 | `draft-storage.md` §S.1.5: "`gen` … bumped after every committed write"; §S.6: "Every committed collector/exporter write bumps `gen`" · `draft-logql.md` §L.5.1: `WithBatchTimeout(1*time.Second)` (the exporter commits a batch about every second under traffic) | the query cache would be invalidated every second and never hit | **`gen` bumps only on `series`/`samples`/`probe_results` writes** (K-X2); `otel_spans` and `collector_runs` writes leave it unchanged. · storage §S.1.5, §S.6 |
| K-I12 | `draft-repo.md` §R.5.1: `APIRoots` includes "`LokiError`" · `draft-logql.md` L-X1: Loki errors are "**plain text** … No JSON envelope" (facts-logql L15) | a JSON type for a body that is text | **Drop `LokiError`** from `APIRoots`; the web `ApiError` parser treats any non-JSON body on `/loki/*` as the message (already L-X1). · repo §R.5.1 |
| K-I13 | `draft-repo.md` §R.3.1/§R.3.2/§R.3.3 (subcommands, env table, `.env.example`) · additions elsewhere: `COLLECT_MANUAL_INTERVAL`, `COLLECT_DISABLED`, `divy collect --only … manual`, `divy backup` (storage S-X4, S-X5, §S.3); `QUERY_LOOKBACK_DELTA`, `QUERY_TIMEOUT`, `QUERY_MAX_SAMPLES`, `QUERY_MAX_CONCURRENCY`, `api/tools/promql-oracle`, `make promql-oracle` (promql P7, §P.11); `OTEL_SAMPLE_RPS`, `OTEL_SAMPLE_BURST` (logql L-X6); the `GITHUB_TOKEN` comment (storage S-X7); `version.Branch`, `version.BuildUser` ldflags (K-X7) | the repo tables lack knobs the other sections rely on | **Repo adopts all** (env table, `.env.example`, subcommand table, Makefile `LDFLAGS`, §R.1 tree). · repo |
| K-I14 | `draft-repo.md` §R.6.3: "text wins when `text/plain` is listed and either `text/html` is absent or has a lower `q`" · `draft-logql.md` §L.6.1: "text iff `q_plain > 0` and (`q_html == 0` or `q_plain > q_html`); ties → HTML … `Accept: text/*` → HTML" (q taken from the most specific matching range, so `*/*` counts for HTML) | `Accept: text/plain, */*` → text (repo) vs HTML (logql) | **LogQL's rule** (fully specified; `*/*` accounted for). · repo §R.6.3 sentence |
| K-I15 | `draft-logql.md` §L.2.2 Loki buildinfo: `"version":"divy.dev v0.1.0"` · `draft-promql.md` §P.7.3 Prometheus buildinfo: `"version":"v0.1.0"`; both need `branch`/`buildUser` values the repo's `version` package (`Version`, `Commit`, `Date`) does not carry | two spellings of one version; two unsourced fields | **Both `version` = `version.Version`**; `branch`/`buildUser` from the new ldflags (K-X7). · logql §L.2.2; repo §R.3.2, §R.4 |
| K-I16 | `draft-repo.md` §R.6.6: "`//go:embed fonts/Inter-Bold.ttf fonts/JetBrainsMono-Regular.ttf`" · `draft-logql.md` §L.6.7: "embedded Inter Bold / Inter Regular / JetBrains Mono Regular TTFs" (the layout sets the summary in Inter Regular) | two vs three embedded fonts | **Three files** (`Inter-Regular.ttf` added). · repo §R.6.6 |
| K-I17 | `draft-promql.md` §P.7.1: "Response headers … `Cache-Control: public, max-age=15`" for every `/api/v1/*` · `draft-storage.md` §S.6: `/api/v1/rules` in the 60 s group | TTL of the non-query Prometheus endpoints | **Per-endpoint classes** (§K.3.2): query family Q15; `rules`, `metadata`, `status/buildinfo`, `alerts`, `query_exemplars` C60 (content-derived or constant). · promql §P.7.1 |
| K-I18 | `draft-promql.md` §P.6 "Caching": "keyed by `(path, normalized query string, lookback, method)` … Keys normalise `time`/`start`/`end` to ms and `step` to ms" · `draft-storage.md` §S.6: key = `method \n path \n canonical query \n gen` … "No time rounding" | two key definitions | **Storage's key with promql's normalization** (§K.3.2 Q15): `gen` is required for invalidation; normalizing spellings to ms is not rounding and lets equivalent requests share entries. · promql §P.6; storage §S.6 "Key" |
| K-I19 | `draft-content.md` X4 endpoint list · `draft-logql.md` L-X4 adds `/api/content/spans`, `/api/content/logs`, `/api/content/alerts`, `/api/uptime`, `/ascii`, `/api/services*`, `/api/operations`, `/api/traces?service=` | two partial lists | **Union** = §K.1.3–K.1.5. · none (both defer to this table) |
| K-I20 | `draft-repo.md` §R.4.1 Vite proxy table: `/api`, `/metrics`, `/loki`, `/healthz`, `/readyz`, `/favicon.svg`, `/og`, `/robots.txt` · `draft-logql.md` L-X4/§L.6.1: `GET /ascii` | `/ascii` is not proxied in dev | **Add `/ascii`** to the proxy table and `vite.config.ts`. · repo §R.4.1, §R.1 |
| K-I21 | `draft-content.md` §C.6.2: "View query" shows "`curl -sG 'https://divy.dev/api/v1/query_range' …`" · `draft-repo.md` R8: absolute URLs from the API "use `DIVY_PUBLIC_ORIGIN`" | hard-coded origin in the curl line | **`DIVY_PUBLIC_ORIGIN`** (repo R8; the web builds the line from `PUBLIC_SITE_ORIGIN`, the same value). · content §C.6.2 sentence |
| K-I22 | `draft-content.md` X5: 404 body "`{"error":"trace not found"}`" · `draft-logql.md` §L.4.1: `trace not found (self-traces are sampled and kept 24h; the career trace is /api/traces/career)` | message text | **LogQL's longer message** (same envelope; explains the 404). · content X5 |
| K-I23 | `draft-promql.md` §P.6 "Sample fetch": "Matchers run in Go over the in-memory series index (… reloaded when the writer adds a series and at most every 15 s)" · `draft-storage.md` §S.1.5/§S.1.6: `seriesCache` is mutated synchronously by the writer goroutine | two descriptions of one cache | **Storage** (the single writer keeps the cache current; no 15 s reload). No contract impact. · promql §P.6 sentence |
| K-I24 | `CONVENTIONS.md` #7: "`/metrics` exposes the LATEST sample of every stored series" · `draft-promql.md` P4 and `draft-storage.md` §S.2.6 both hide stale series (justified by BRIEF §7 "No fake data") | baseline wording vs both drafts | **Keep the staleness cut-off** (brief §7 outranks the baseline's phrasing; both drafts already flag the divergence): a series older than `staleAfter` is absent from the exposition while `divy_collector_last_success_timestamp_seconds` stays visible. Not an open question. · none |

## Open questions

None from this section. K-I9 can be re-opened if hourly resolution across the full 90 days is wanted on desktop; the cost is stated in its resolution.
