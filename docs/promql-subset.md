# PromQL subset, Prometheus HTTP API and `/metrics`

The Go API speaks a subset of PromQL through the Prometheus HTTP API so that a real Grafana can add the site as a Prometheus data source. Everything below is what `internal/promql` and `internal/server/promapi.go` implement; the tests in `internal/promql/*_test.go` and `internal/server/promapi_test.go` pin every row.

Reference behaviour: Prometheus 3.14 (`github.com/prometheus/prometheus v0.314.0`) for grammar, error messages, `rate()` extrapolation and float semantics; the Grafana 12 Prometheus data source for the request shapes.

## 1. Supported constructs

| Area | Supported | Not supported (parse error, §4) |
|---|---|---|
| Selectors | instant selector with `=`, `!=`, `=~`, `!~`; `__name__` matcher; range selector `[d]` with a Prometheus duration or a number of seconds | `offset`, `@`, subqueries `[r:s]`, quoted UTF-8 names `{"a.b"}`, duration expressions `[5m * 2]`, `anchored`/`smoothed` |
| Literals | float (decimal, exponent, hex, `Inf`, `NaN`, `_` separators), string (`"…"`, `'…'`, `` `…` ``; instant query only) | — |
| Operators | unary `+ -`; arithmetic `+ - * / % ^`; comparison `== != > < >= <=` with optional `bool`; scalar⊕scalar, vector⊕scalar, vector⊕vector one-to-one | `and`, `or`, `unless`, `atan2`, `on`, `ignoring`, `group_left`, `group_right`, `fill*`, `</`, `>/` |
| Aggregations | `sum avg min max count` with `by`/`without` before or after the argument | `topk bottomk quantile stddev stdvar group count_values limitk limit_ratio` |
| Functions | `rate increase irate delta`, `sum_over_time avg_over_time min_over_time max_over_time count_over_time last_over_time`, `abs ceil floor round clamp_min clamp_max`, `time vector scalar` | every other Prometheus function |
| Queries | instant (`/api/v1/query`) and range (`/api/v1/query_range`) evaluation, `lookback_delta`, `timeout`, `limit` | `stats` output (accepted, ignored), native histograms, exemplars |

Why beyond the brief's "selectors, label matchers, `rate()`, `sum()`, `increase()`, `[range]`":

| Construct | Needed by |
|---|---|
| comparisons (`==`, `>`), `bool` | the three alert expressions (`divy_open_to_work == 1`, `sum(increase(…[7d])) > 20`, `lfx_applications{status="pending"} > 0`), the `stars-by-repo` panel (`github_stars > 0`) |
| scalar arithmetic (`1+1`) | Grafana's data-source health check (`query=1+1&time=4`) |
| `* 86400` on a vector | the `pypi-downloads` panel (`rate(…[2d]) * 86400`) |
| `avg min max count`, `*_over_time`, `abs ceil floor round clamp_*`, `time vector scalar`, `irate delta` | none — cheap once the evaluator exists; kept so Explore in Grafana feels like Prometheus |
| `/api/v1/label/{name}/values`, `metadata`, `status/buildinfo`, `rules`, `alerts`, `query_exemplars` | Grafana: metrics browser, autocomplete, source-type detection, alert rule listing, data-source load |

## 2. Grammar

```
expr        = binary ;
binary      = unary { binop [ "bool" ] unary } ;          (* precedence table below *)
unary       = ( "+" | "-" ) unary | primary [ range ] ;
primary     = NUMBER | STRING | "(" expr ")" | aggregation | call | selector ;
aggregation = aggr_op grouping "(" expr ")" | aggr_op "(" expr ")" [ grouping ] ;
aggr_op     = "sum" | "avg" | "min" | "max" | "count" ;
grouping    = ( "by" | "without" ) "(" [ label_list ] ")" ;
label_list  = IDENTIFIER { "," IDENTIFIER } [ "," ] ;
call        = IDENTIFIER "(" [ expr { "," expr } ] ")" ;
selector    = metric_name [ matchers ] | matchers ;
metric_name = IDENTIFIER | METRIC_IDENTIFIER | aggr_op | keyword ;   (* sum{} is {__name__="sum"} *)
matchers    = "{" [ matcher { "," matcher } [ "," ] ] "}" ;
matcher     = IDENTIFIER ( "=" | "!=" | "=~" | "!~" ) STRING ;
range       = "[" ( DURATION | NUMBER ) "]" ;             (* NUMBER = seconds; must be > 0 *)
binop       = "^" | "*" | "/" | "%" | "+" | "-" | "==" | "!=" | ">" | "<" | ">=" | "<=" ;
```

| Level | Operators | Associativity | Note |
|---|---|---|---|
| 1 | `^` | right | `2 ^ 3 ^ 2` = 512 |
| 2 | unary `+`, `-` | prefix | binds tighter than `*`, looser than `^`: `-2 ^ 2` = −4, `-x * 2` = `(-x) * 2` |
| 3 | `*`, `/`, `%` | left | |
| 4 | `+`, `-` | left | |
| 5 | `==`, `!=`, `>`, `<`, `>=`, `<=` (optionally `bool`) | left | |

Keywords are case-insensitive (`SUM(x)` = `sum(x)`); function names are case-sensitive (`Rate` is unknown). `#` starts a comment. Durations: `y w d h m s ms` in strictly descending order, each at most once (`1h30m`, `90s`, `7d`), rendered canonically by the printer (`[7d]` → `[1w]`, `[90s]` → `[1m30s]`).

Static checks (Prometheus' messages): `binary expression must contain only scalar and instant vector types`, `comparisons between scalars must use BOOL modifier`, `bool modifier can only be used on comparison operators`, `vector selector must contain at least one non-empty matcher`, `metric name must not be set twice: "a" or "b"`, Go's `error parsing regexp: …`, `no arguments for aggregate expression provided`, `wrong number of arguments for aggregate expression provided, expected 1, got 2`, `expected type instant vector in aggregation expression, got range vector`, `expected 1 argument(s) in call to "rate", got 2`, `expected at most 2 argument(s) in call to "round", got 3`, `expected type range vector in call to function "rate", got instant vector`, `ranges only allowed for vector selectors`, `unary expression only allowed on expressions of type scalar or instant vector, got "range vector"`, `duration must be greater than 0`, `trailing commas not allowed in function call args`.

## 3. Semantics

### Selectors and lookback

| Construct | Semantics |
|---|---|
| `name`, `name{m…}`, `{m…}` | every series whose label set satisfies all matchers; `name` is sugar for `{__name__="name"}`. `=~` / `!~` are RE2 (Go `regexp`), fully anchored (`^(?s:…)$`). A label the series lacks matches as `""`, so `{repo=""}` matches series without `repo`. |
| value at `t` | per series, the newest sample with `t_s ≤ t` and `t_s > t − lookback`; series without one are omitted; the result timestamp is `t`. |
| lookback | per request `lookback_delta` (duration or float seconds), else `QUERY_LOOKBACK_DELTA` (**default 26h**: stored counters are one sample per UTC day, so any lookback below 24 h + collector cadence leaves raw selectors empty between day boundaries; a dead collector shows on `/metrics` and in `/readyz`, not through the query lookback). |
| `sel[d]` | range vector: all samples with `t − d < t_s ≤ t` (left-open) in ascending order; series with no sample in the window are omitted; lookback does not apply. A `[1d]` window over daily samples therefore holds one sample and `rate`/`increase` return nothing — use `[2d]` or more. |
| live series | `divy_uptime_seconds`, `divy_build_info`, `divy_open_to_work`, `divy_experience_years` are functions of `t`, never stored: an instant selector sees one sample at every evaluation timestamp, a range selector one sample at `t`. |

### Operators

All float arithmetic is IEEE 754 through Go: `/` gives `+Inf`, `-Inf`, `NaN`; `%` is `math.Mod`; `^` is `math.Pow`. Comparisons on NaN are false except `!=`.

| Form | Result | `__name__` |
|---|---|---|
| `s ⊕ s` | scalar | — |
| `s cmp bool s` | scalar 0/1 (`s cmp s` without `bool` is a parse error) | — |
| `v ⊕ s`, `s ⊕ v` | vector, op applied per sample (`s − v` computes `s − value`) | dropped |
| `v cmp s`, `s cmp v` | filter: samples for which the comparison holds, value unchanged | kept |
| `v cmp bool s` | every sample, value 1/0 | dropped |
| `v ⊕ v` | one-to-one on the label set without `__name__`; unmatched samples dropped | dropped |
| `v cmp v` | one-to-one filter keeping the left sample | kept |
| `v cmp bool v` | one-to-one, value 1/0 | dropped |

Execution errors (HTTP 422, `errorType: execution`), exactly as Prometheus raises them:

- a signature repeated on the right side: `found duplicate series for the match group {target="pypi"} on the right hand-side of the operation: [{__name__="probe_success", target="pypi"}, {__name__="probe_duration_seconds", target="pypi"}];many-to-many matching not allowed: matching labels must be unique on one side` (Prometheus checks the right side first; the later series is printed first);
- a signature repeated on the left side only: `multiple matches for labels: many-to-one matching must be explicit (group_left/group_right)`.

If either side is empty the result is empty and no error is raised.

### Aggregations

`op [by|without (l…)] (v)` or `op(v) [by|without (…)]`. Output labels: none → `{}`; `by` → only the listed labels the series has (`by (__name__)` keeps the name); `without` → all labels except the listed ones and `__name__`. `sum` and `avg` use Prometheus' Kahan/Neumaier summation; `min`/`max` skip NaN unless every value is NaN; `count` counts samples. Groups with zero inputs never appear (`sum(nonexistent)` is empty).

### Functions

| Signature | Semantics |
|---|---|
| `rate(r)`, `increase(r)`, `delta(r)` | Prometheus' `extrapolatedRate`, float operations in the same order: series with < 2 samples give nothing; `Δ = last − first` (+ `prev` at every counter reset for `rate`/`increase`); extrapolate to the window edges unless the gap exceeds 1.1 × the average sample spacing (then by half a spacing); counters are clamped at their extrapolated zero point; `rate` divides by the range in seconds. Over daily samples: `increase(github_commits_total[7d])` at a day boundary sees 7 samples spanning 6 days and reports the 6-day increase extrapolated by 7⁄6 (a zero first sample removes the left extrapolation); `rate(x[2d]) * 86400` over two daily samples 1 d apart is the day's increase, rendered as e.g. `29.999999999999996` because `Δ × factor` is multiplied in float64 exactly as Prometheus does. |
| `irate(r)` | last two samples: `(b − a)` (or `b` on a reset) ÷ their spacing in seconds; nothing if they share a timestamp. |
| `sum_over_time`, `avg_over_time`, `min_over_time`, `max_over_time`, `count_over_time` | per series over the window; NaN propagates through sums, `min`/`max` skip it. |
| `last_over_time(r)` | newest sample in the window; **keeps `__name__`** (as Prometheus). |
| `abs`, `ceil`, `floor` | per sample. |
| `round(v[, to_nearest=1])` | `floor(x/to_nearest + 0.5) × to_nearest` via the inverse (ties up): `round(3.75, 0.5)` = 4. |
| `clamp_min(v, min)`, `clamp_max(v, max)` | `max(min, x)` / `min(max, x)`. |
| `time()` | the evaluation timestamp in seconds. |
| `vector(s)` | one sample `{}`. |
| `scalar(v)` | the single sample's value; NaN for 0 or ≥ 2 samples. |

Every function drops `__name__` except `last_over_time`.

### Evaluation model

| Topic | Rule |
|---|---|
| Instant query | `resultType` = `vector`, `scalar`, `matrix` (bare range selector) or `string`. |
| Range query | expression must be scalar or instant vector (else 400 `invalid parameter "query": invalid expression type "range vector" for range query, must be Scalar or instant Vector`); timestamps `start, start+step, … ≤ end`, no alignment; every step is a full instant evaluation; a scalar becomes one series `{}`. |
| Sample fetch | one SQL read per selector per query over `[start − max(lookback, range), end]`, then per-step windows in memory. |
| Guards | `(end − start)/step > 11000` → 400; more than `QUERY_MAX_SAMPLES` (2 000 000) samples loaded → 422 `query processing would load too many samples into memory in query execution`; wall time above `min(timeout, QUERY_TIMEOUT=30s)` → 503 `query timed out in query execution`; at most `QUERY_MAX_CONCURRENCY` (20) evaluations at once, waiting counts against the timeout; a client disconnect cancels the evaluation (499). |
| Ordering | vectors and matrices sorted by label set; points by time. |
| Output | timestamps as float seconds with a 3-digit fraction only when needed (`1788602400`, `1788602400.500`); values as strings in shortest form, exponent form outside `[1e-6, 1e21)`, `NaN`, `+Inf`, `-Inf` spelled out; empty results are `[]`, never `null`. |

## 4. Unsupported constructs and their errors

Every parse error reads `<line>:<col>: parse error: <message>` (`unknown position: parse error: no expression found in input` for an empty query) and reaches the API as `invalid parameter "query": …` with HTTP 400.

| Construct | Example | Message (position = the offending token) |
|---|---|---|
| `offset` | `x offset 1d` | `offset modifier is not supported` |
| `@` | `x @ 1609746000`, `x @ start()` | `@ modifier is not supported` |
| subquery | `x[1h:5m]` | `subqueries are not supported` (at the colon) |
| duration expression | `x[5m * 2]` | `unexpected <op:*> in range selector, expected "]"` |
| set operators | `a and b`, `a or b`, `a unless b` | `set operator "and" is not supported` |
| `atan2` | `a atan2 b` | `binary operator "atan2" is not supported` |
| matching modifiers | `a + on(x) b`, `a / ignoring(x) group_left b` | `vector matching modifier "on" is not supported` (also `ignoring`, `group_left`, `group_right`) |
| other aggregations | `topk(3, x)`, `quantile(0.9, x)`, `count_values("v", x)`, `stddev(x)`, `group(x)` | `aggregation operator "topk" is not supported` |
| other Prometheus functions | `histogram_quantile(0.9, x)`, `label_join(…)`, `absent(x)`, `predict_linear(…)`, `changes(…)`, `sort(x)`, `timestamp(x)`, `day_of_week()`, `clamp(x, 0, 10)`, … | `function "histogram_quantile" is not supported` |
| unknown function | `foo(x)`, `holt_winters(…)`, `Rate(x[5m])` | `unknown function with name "foo"` |
| quoted / UTF-8 names | `{"a.b"="1"}` | `unexpected string "a.b" in label matching, expected identifier or "}"` |
| string operand | `"a" + 1` | `binary expression must contain only scalar and instant vector types` |
| range on a non-selector | `rate(x[5m])[5m]`, `(x)[5m]` | `ranges only allowed for vector selectors` |
| `anchored` / `smoothed` | `x[5m] anchored` | `unexpected identifier "anchored"` |

Generic syntax errors use Prometheus' composition `unexpected <desc>[ in <context>][, expected <what>]`, e.g. `unexpected identifier "repo" in label matching, expected "," or "}"`, `unexpected end of input inside braces`, `unclosed left parenthesis`, `unexpected right parenthesis ')'`, `unexpected <bool>`.

## 5. Prometheus HTTP API

| Method | Path | Parameters | `data` | Cache-Control |
|---|---|---|---|---|
| GET, POST | `/api/v1/query` | `query` (required); `time` (default now); `timeout`; `limit`; `lookback_delta`; `stats` ignored | `{"resultType","result"}` | `public, max-age=15` |
| GET, POST | `/api/v1/query_range` | `query`, `start`, `end` (≥ start), `step` (> 0, `(end−start)/step ≤ 11000`) required; `timeout`; `limit`; `lookback_delta` | `{"resultType":"matrix","result":[…]}` | 15 s |
| GET, POST | `/api/v1/series` | `match[]` (≥ 1); `start`, `end` (default unbounded); `limit` | array of label sets incl. `__name__`, sorted; stored series with a sample in `[start, end]`, live series always | 15 s |
| GET, POST | `/api/v1/labels` | `start`, `end`, `match[]`, `limit` | sorted label names | 15 s |
| GET | `/api/v1/label/{name}/values` | `{name}` must be non-empty valid UTF-8 (Prometheus 3 UTF-8 validation: `1bad` is legal and yields `[]`; `%FF` → 400 `invalid label name: "\xff"`); `start`, `end`, `match[]`, `limit` | sorted distinct values | 15 s |
| GET | `/api/v1/metadata` | `limit` (default −1 = all); `limit_per_metric`; `metric` | `{"<family>":[{"type","help","unit":""}]}` for the stored and live catalogue families (not `go_*`, `process_*`, `divy_http_*`, `divy_collector_*`) | 60 s |
| GET | `/api/v1/status/buildinfo` | — | `{"version","revision","branch","buildUser","buildDate","goVersion"}` | 60 s |
| GET | `/api/v1/rules` | `type` (`alert`\|`record`); `rule_name[]`, `rule_group[]`, `file[]`; `match[]`, `exclude_alerts`, `group_limit`, `group_next_token` accepted | `content/alerts.yaml` in Prometheus shape: `state` `inactive`, `health` `unknown`, `alerts` `[]`, `query` in canonical form, `file` `content/alerts.yaml`; groups left empty by a filter are omitted | 60 s |
| GET | `/api/v1/alerts` | — | `{"alerts":[]}` (rules are evaluated in the browser) | 60 s |
| GET, POST | `/api/v1/query_exemplars` | `query` (must parse), `start`, `end` (Grafana's millisecond epochs are accepted) | `[]` | 60 s |
| any other | `/api/v1/*` | | 404 `{"status":"error","errorType":"not_found","error":"path not found"}`; wrong method → 405 `method not allowed` with `Allow` | no-store |

Common rules:

- `POST` bodies are `application/x-www-form-urlencoded` (merged with the query string, 1 MiB cap → 413); a JSON body is rejected with 400 `invalid parameter: body must be application/x-www-form-urlencoded`. `HEAD` mirrors `GET`.
- Timestamps: Unix seconds as a float (fraction rounded to ms) or RFC 3339 / RFC 3339-nano. `query_range` errors: `invalid parameter "start": cannot parse "x" to a valid timestamp`; the optional `time`/`start`/`end` of the other endpoints: `invalid parameter "time": invalid time value for 'time': cannot parse "x" to a valid timestamp` (Prometheus 3 wraps them that way).
- Durations (`step`, `timeout`, `lookback_delta`): float seconds or a Prometheus duration; `invalid parameter "step": cannot parse "abc" to a valid duration`; a bad `lookback_delta` is `error parsing lookback delta duration: cannot parse "x" to a valid duration`.
- `limit` ≥ 0 (`0` = unlimited); truncation adds `"warnings":["results truncated due to limit"]`.
- `match[]` repeats; a set with no non-empty matcher is rejected (`match[] must contain at least one non-empty matcher`, prefixed with `invalid parameter "match[]": ` on `/series` only, as Prometheus does).
- Every 2xx body carries a weak `ETag`; a matching `If-None-Match` answers 304.
- Errors: `{"status":"error","errorType":"<type>","error":"<message>"}` with 400 `bad_data`, 404 `not_found`, 405 `bad_data`, 413 `bad_data`, 422 `execution`, 499 `canceled`, 500 `internal`, 503 `timeout`.

Environment knobs: `QUERY_LOOKBACK_DELTA=26h`, `QUERY_TIMEOUT=30s`, `QUERY_MAX_SAMPLES=2000000`, `QUERY_MAX_CONCURRENCY=20`.

## 6. `/metrics`

`GET /metrics` is a client_golang exposition (`promhttp`, `text/plain; version=0.0.4; charset=utf-8` by default, protobuf when a scraper asks, gzip when accepted, at most 8 scrapes in flight, `Cache-Control: no-store`). It carries:

- `go_*` and `process_*` from the standard collectors;
- `divy_http_requests_total{route,method,code}` and `divy_http_request_duration_seconds{route,method}` (route = chi pattern, never the raw path), `divy_collector_runs_total{collector,result}`, `divy_collector_run_duration_seconds{collector}`;
- the newest stored sample of every catalogue series whose age is at most `max(3 × collector cadence, 15m)` — a series the collector has not confirmed for three cycles disappears instead of being re-exposed as current (`divy_collector_last_success_timestamp_seconds{collector}`, read from `collector_runs`, stays visible);
- the live series evaluated at scrape time.

No sample timestamps are emitted. Series outside the catalogue (`internal/metrics/catalogue.go`) are queryable through the API but never exposed. `github_stars_total` from the brief is exposed as **`github_stars`** because promlint rejects the `_total` suffix on a gauge; `make promtool-check` runs `promtool check metrics` against a live server and `promtool check rules` against `content/alerts.yaml`, and `internal/metrics` runs the same promlint rules in `go test`.

## 7. Grafana

1. Connections → Data sources → Add → **Prometheus**.
2. URL: the site origin (`SITE_ORIGIN`, e.g. `https://<project>.vercel.app`). No authentication.
3. HTTP method: **POST** (the default) or **GET** — both work for `query`, `query_range`, `series` and `labels`.
4. Leave "Prometheus type" and "version" unset; keep "Use series endpoint" off.
5. Save & test: Grafana sends `POST /api/v1/query` with `query=1+1&time=4` and expects `{"status":"success","data":{"resultType":"scalar","result":[4,"2"]}}`, then `GET /api/v1/status/buildinfo`. The message "Successfully queried the Prometheus API." confirms the data source.

Grafana never sends `lookback_delta`, so its panels see the 26 h default: a raw counter selector renders as a daily step function at any step. `$__rate_interval` expands client-side; with daily samples set Min step to `1d` (or use `[2d]` and longer windows) so `rate()` sees two samples. Explore, the metrics browser and the query builder use `/api/v1/label/__name__/values`, `/api/v1/labels`, `/api/v1/metadata` and `/api/v1/series`; the alerting UI reads `/api/v1/rules` and `/api/v1/alerts`. Every error reaches Grafana as JSON, so a rejected construct shows as `bad_data: invalid parameter "query": …`.

A copyable pair of requests:

```bash
curl -sG "$SITE_ORIGIN/api/v1/query" --data-urlencode 'query=sum by (org) (github_merged_prs_total)'
curl -s "$SITE_ORIGIN/api/v1/query_range" --data-urlencode 'query=sum(increase(github_commits_total[7d]))' --data-urlencode "start=$(date -u -d '-30 days' +%s)" --data-urlencode "end=$(date -u +%s)" --data-urlencode 'step=1d'
```
