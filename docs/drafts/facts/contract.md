# facts-contract.md — verified facts for the API contract section

Verified 2026-09-05. Each row was checked in this session against the named source (raw.githubusercontent.com mirrors of the official repositories, or local computation). Facts relied on from the other `facts-*.md` files are cited by id at the end rather than repeated.

| # | Fact | Source |
|---|------|--------|
| F1 | The trace/span ids used by Content §C.3.4, §C.3.5 and LogQL §L.3.4, §L.4.2 are correct: `sha256("divy.career")[0:16]` = `9f3a0703b53d5b0aae2fb3bdacea0ff6` (span id = its first 8 bytes `9f3a0703b53d5b0a`); `sha256("edu.btech-ece")[0:8]` = `4e76e10ea3071d79`; `gradr.inc-002` → `ef53e50f70cc9d38`; `gradr.product-engineer` → `da42f4e70b8baf7c`; `gradr.observability` → `87e27ab96901a7d8`. | local `printf '%s' <id> \| sha256sum` |
| F2 | Loki registers `GET` **and** `POST` for `/loki/api/v1/query_range`, `/query`, `/label`, `/labels`, `/label/{name}/values`, `/series` and `/index/stats` (`router.Path(constants.PathLoki…).Methods("GET", "POST")`, modules.go lines 663–682). | https://raw.githubusercontent.com/grafana/loki/main/pkg/loki/modules.go |
| F3 | Prometheus v0.314.0 registers `r.Get("/alerts", …)` and `r.Get("/rules", …)` only (api.go lines 494–495); `/query`, `/query_range`, `/query_exemplars`, `/labels`, `/series` are `GET`+`POST`; `/label/:name/values`, `/metadata`, `/status/buildinfo` are `GET` only; `r.Options("/*path", …)` answers `OPTIONS` on every path (lines 441–473). | https://raw.githubusercontent.com/prometheus/prometheus/v0.314.0/web/api/v1/api.go |
| F4 | Grafana's Loki data source backend builds `GET` requests for `/loki/api/v1/query_range` and `/loki/api/v1/query` (`http.NewRequestWithContext(ctx, "GET", …)`, api.go lines 81–94 and 259). | https://raw.githubusercontent.com/grafana/grafana-loki-datasource/main/pkg/loki/api.go |
| F5 | promhttp (client_golang v1.24.1) with `MaxRequestsInFlight > 0` answers excess requests with `503` and the body `Limit of concurrent requests reached (%d), try again later.` (http.go lines 252–291; option doc lines 543–546). | https://raw.githubusercontent.com/prometheus/client_golang/v1.24.1/prometheus/promhttp/http.go |
| F6 | The Fetch standard defines every CORS header used in §K.3.3: `Access-Control-Allow-Origin`, `-Allow-Methods`, `-Allow-Headers`, `-Max-Age`, `-Expose-Headers`, `-Allow-Credentials`, and the preflight request headers `Access-Control-Request-Method`, `-Request-Headers`. | https://raw.githubusercontent.com/whatwg/fetch/main/fetch.bs |
| F7 | chi v5.3.2 `middleware.GetHead` "automatically route[s] undefined HEAD requests to GET handlers" (look-ahead routing context, then falls through to the GET route). | https://raw.githubusercontent.com/go-chi/chi/v5.3.2/middleware/get_head.go |

Relied on from the other facts files (not re-fetched): Prometheus `errorType`/status mapping, parameter parsing and range checks (facts-promql "Prometheus HTTP API" rows 3–6, 19); Grafana request methods and metadata endpoints (facts-promql "Grafana Prometheus data source" rows 3, 8–10); Loki timestamp parsing, limits and plain-text errors (facts-logql L12–L16); Jaeger envelope and the Grafana Jaeger client URLs (facts-logql J3, J5–J6); chi `ClientIPFromXFF` (facts-repo §4); promlint and exposition `Content-Type` (facts-promql "Exposition format and promlint"); SQLite behaviour (facts-storage S3–S7); the per-day GitHub commit series (facts-storage G4).

## UNVERIFIED

- Loki's method list for `/loki/api/v1/index/volume` and `/loki/api/v1/status/buildinfo` (outside the grep window); the contract keeps them `GET`-only, which is what Grafana uses.
- Go `net/http` dropping the body for `HEAD` responses and `http.MaxBytesReader` producing a 413-worthy error: standard-library behaviour relied on from memory, not re-read this session.
- Whether Loki itself emits `Vary` or CORS headers: irrelevant to compatibility (Grafana proxies server-side), not checked.
- Whether a browser needs `If-None-Match` listed in `Access-Control-Allow-Headers` for a cross-origin conditional fetch; the contract lists it explicitly, which is safe either way.
