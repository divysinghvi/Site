# LogQL subset and Loki HTTP API

The Go API speaks a subset of LogQL through the Loki HTTP API so that a real Grafana can add the site as a Loki data source and Explore the career log. Everything below is what `internal/logql` and `internal/server/handlers_loki.go` implement; `internal/logql/parser_test.go`, `internal/logql/eval_test.go` and `internal/server/handlers_loki_test.go` pin every row.

Reference behaviour: Loki 3.x (`pkg/logql`, `pkg/loghttp`) for grammar, error texts, `| json` extraction and timestamp parsing; the Grafana 12 Loki data source (`grafana-loki-datasource`) for the request shapes. The data is `content/logs.ndjson` (≤ 100 lines, loaded at startup, immutable per process): there is no ingestion, no chunks, no tail.

## 1. Supported constructs

| Area | Supported | Not supported (parse error, §4) |
|---|---|---|
| Stream selector | `{a="x", b!="y", c=~"re", d!~"re"}`; at least one matcher must not match the empty string | `{}` and selectors that match everything |
| Line filters | `\|= "text"`, `!= "text"`, `\|~ "re"`, `!~ "re"` (RE2, unanchored), in any number and order | `\|= ip("…")`, `or` between filters, `!= ` without a string |
| Parsers | `\| json` (no arguments) | `logfmt`, `pattern`, `regexp`, `unpack`, `json` with expressions |
| Label filters | `name = "v"`, `!=`, `=~`, `!~` (strings); `name == 65`, `!=`, `>`, `>=`, `<`, `<=` against a number, a duration (`5m`, `1h30m`) or a bytes literal (`512KB`, `3MiB`); `and` / `,` / `or` / parentheses; `__error__=""` | `ip()` filters |
| Other stages | — | `line_format`, `label_format`, `unwrap`, `drop`, `keep`, `decolorize` |
| Metric queries | `count_over_time(<log query>[R])`, `rate(<log query>[R])`; `sum`, `count`, `min`, `max`, `avg` with `by (…)` / `without (…)` before or after the argument | every other range function and aggregation, nested aggregations, `offset`, binary operators between metric queries |
| Scalars | `vector(N)` combined with `+ - * /` and parentheses (Grafana's health check) | any other operand |
| Endpoints | `query_range`, `query`, `labels`, `label/{name}/values`, `series`, `index/stats`, `index/volume`, `status/buildinfo` | `tail`, `push`, `detected_*`, `patterns`, `index/volume_range` (404, see §3.5) |

Why beyond the brief's "stream selectors, line filters `|=`, `!=`, `|~`, and `| json`":

| Construct | Needed by |
|---|---|
| `!~` line filter, label filters after `\| json` | the logs page (level chips rewrite the selector; "show the incident lines of INC-002" is `\| json \| incident="INC-002"`), the drawer's reverse link (`\| json \| span="gradr.inc-002"`) |
| `count_over_time` / `rate` + `sum by (…)` | Grafana Explore's log-volume histogram (`sum by (level, detected_level) (count_over_time(<query>[<auto>]))`) and the logs page's own histogram |
| `vector(1)+vector(1)` | Grafana's Loki data source **Save & test** |
| `labels`, `label/{name}/values`, `series`, `index/stats`, `index/volume`, `status/buildinfo` | Grafana's label browser, autocomplete, query stats hint, volume panel and version detection; the logs page's autocomplete |
| duration / bytes literals in numeric filters | none — cheap once numeric filters exist; kept so Loki examples paste unchanged |

## 2. Grammar

```
query          = log_query | metric_query | scalar_expr ;
log_query      = selector { stage } ;
selector       = "{" matcher { "," matcher } "}" ;
matcher        = IDENT ( "=" | "!=" | "=~" | "!~" ) STRING ;
stage          = line_filter | "|" "json" | "|" label_filter ;
line_filter    = ( "|=" | "!=" | "|~" | "!~" ) STRING ;
label_filter   = lf_or ;
lf_or          = lf_and { "or" lf_and } ;
lf_and         = lf_atom { ( "and" | "," ) lf_atom } ;
lf_atom        = "(" lf_or ")" | string_filter | number_filter ;
string_filter  = IDENT ( "=" | "!=" | "=~" | "!~" ) STRING ;
number_filter  = IDENT ( "==" | "!=" | ">" | ">=" | "<" | "<=" ) ( NUMBER | DURATION | BYTES ) ;
metric_query   = range_agg | aggregation ;
range_agg      = ( "count_over_time" | "rate" ) "(" selector { stage } "[" DURATION "]" { stage } ")" ;
aggregation    = agg_op [ grouping ] "(" range_agg ")" [ grouping ] ;
agg_op         = "sum" | "count" | "min" | "max" | "avg" ;
grouping       = ( "by" | "without" ) "(" [ IDENT { "," IDENT } ] ")" ;
scalar_expr    = scalar_term { ( "+" | "-" ) scalar_term } ;
scalar_term    = scalar_atom { ( "*" | "/" ) scalar_atom } ;
scalar_atom    = "vector" "(" NUMBER ")" | "(" scalar_expr ")" ;
```

| Token | Lexeme | Notes |
|---|---|---|
| `IDENT` | `[a-zA-Z_][a-zA-Z0-9_]*` | label names, function names, keywords; the keywords `json and or by without count_over_time rate sum count min max avg vector` cannot be label names (`reserved word "by" cannot be a label name`) |
| `STRING` | `"…"` with Go escapes or `` `…` `` raw | unterminated → `literal not terminated`; `'…'` → `syntax error: unexpected '` |
| `NUMBER` | `[0-9]+(\.[0-9]+)?` | no sign |
| `DURATION` | `NUMBER unit` repeated with units `ms s m h d w y` in descending order (`1h30m`, `7d`, `1y` = 365 d) | Prometheus `model.ParseDuration` rules; `1.5h` → `unknown unit "." in duration "1.5h"` |
| `BYTES` | `NUMBER unit` with `B KB KiB MB MiB GB GiB TB TiB PB PiB EB EiB` (case-insensitive; `KB` = 1000, `KiB` = 1024) | `go-humanize` rules |
| operators | `{ } ( ) [ ] , \| \|= \|~ = != =~ !~ == > >= < <= + - * /` | longest match wins (`!~` before `!=`, `\|=` before `\|`) |

Disambiguation: `!=` starts a line filter when a `STRING` follows, and is a label-filter operator only inside a `| …` stage; `!=` inside a label filter is a string comparison when the right side is a `STRING` and numeric otherwise; `>` `<` `>=` `<=` `==` followed by a string → `numeric comparison needs a number, duration or bytes literal, got string`. Whitespace (including newlines) separates tokens. Parse errors read `parse error at line L, col C: <message>` with a 1-based column on the raw query; the empty-selector rule and an empty query use Loki's position-less form `parse error : <message>`.

Canonical form: `Query.String()` prints `{service="gradr"} |= "sentry" | json | level="warn"`, `sum by (level) (count_over_time({service="gradr"} | json [1y]))`, `vector(2)`.

## 3. Semantics

### 3.1 Streams and selectors

| Rule | Detail |
|---|---|
| Streams | one per distinct label set of `content/logs.ndjson`: `service` (always), `level` (always), `component` (only when the line has one). Today: 10 services, 4 levels, 15 components. |
| Entry timestamp | the line's `ts`; `TODO(divy)` falls back to the linked span's resolved start, then to the root span start (`2023-01-01T00:00:00Z`); `+ line index` nanoseconds makes every timestamp unique and keeps file order (`internal/content`). |
| Matching | every matcher must accept the stream; a label the stream lacks compares as `""` (`{component=""}` = streams without a component, `{component!="x"}` also matches them). `=~` / `!~` are RE2 and anchored `^(?s:…)$`. |
| Non-empty rule | at least one matcher must reject `""` (`=` with a non-empty value, `!= ""`, `=~` not matching `""`, `!~` matching `""`); else 400 `queries require at least one regexp or equality matcher that does not have an empty-compatible value. For instance, app=~".*" does not meet this requirement, but app=~".+" will` (Loki's text; `{}` fails the same way). |
| Window | log entries with `start ≤ ts < end`; metric windows `(t − R, t]`. |

### 3.2 Pipeline

Stages run per entry in query order; an entry survives only if every stage keeps it. The label set starts as the stream labels and grows through stages; the response groups entries by their **final** label set, so `{service="gradr"} | json` returns one stream per distinct extracted set.

| Stage | Semantics |
|---|---|
| `\|= "t"` / `!= "t"` | `strings.Contains` on the **raw line** (case-sensitive; `\|= ""` passes everything). |
| `\|~ "re"` / `!~ "re"` | `regexp.MatchString` on the raw line, unanchored; `(?i)` for case-insensitive. A bad regex is a parse error with Go's message (`error parsing regexp: missing closing ]: …`). Line filters after `\| json` still see the raw line, never the extracted labels. |
| `\| json` | parses the raw line: top-level strings, numbers (literal text: `65`, `1.50`) and booleans (`true`/`false`) become labels; nested objects are flattened with `_` (`{"a":{"b":1}}` → `a_b="1"`); arrays and `null` are skipped (Loki's no-argument parser rule); keys are sanitized (every byte outside `[a-zA-Z0-9_]` → `_`, a leading digit gets `_` prefixed); `__error__` / `__error_details__` keys are ignored; a second `\| json` is idempotent. Invalid JSON (or a non-object) keeps the entry and adds `__error__="JSONParserErr"`, `__error_details__="<decoder message>"`. |
| collision with a stream label | **divergence from Loki** (which always renames the extracted key to `<key>_extracted` when the stream already has `<key>`, even for equal values): an extracted key whose value equals the stream label's value is skipped; a different value is emitted as `<key>_extracted`. Reason: the stream labels are copied from the line, so Loki's rule would add `service_extracted`, `level_extracted` and `component_extracted` to every parsed line. |
| `\| name = "v"` etc. | string compare on the current label set; a missing label is `""`; regexes anchored. |
| `\| name > 60` | value via `strconv.ParseFloat`; a **missing label never matches** (dropped, no error — Loki's rule); an unparsable value keeps the entry and adds `__error__="LabelFilterErr"`, `__error_details__="strconv.ParseFloat: parsing \"INC-001\": invalid syntax"`; an entry that already carries `__error__` passes numeric filters unchanged (only string filters can drop it — Loki's rule, which is why `\| __error__=""` works). |
| `\| name > 5m` | both sides in seconds: the label is parsed as a Prometheus duration (`1h30m`, `2d`), then a Go duration (`1.5s`, `300ms`), then a plain number of seconds; failure → `LabelFilterErr` with `not a valid duration string: "x"`. |
| `\| name > 3MiB` | both sides in bytes (`go-humanize`: `512KB`, `3 MiB`, `42`). |
| `a and b`, `a, b`, `a or b` | `and` binds tighter than `or`; parentheses allowed; short-circuit left to right. |

### 3.3 Metric queries

| Form | Output |
|---|---|
| `count_over_time(q[R])` | at each step `t`: entries passing the pipeline with `t − R < ts ≤ t`, counted per final label set; a series has a point at `t` only if its count > 0. |
| `rate(q[R])` | `count_over_time / R.Seconds()`. |
| `sum \| count \| min \| max \| avg [by (l…) \| without (l…)] (…)` | Prometheus-style aggregation of the series at each step: `by` keeps only the listed labels the series has (an unknown name is simply absent, so Grafana's `sum by (level, detected_level)` groups by `level`), `without` drops the listed ones, no grouping → one series `{}`; `by ()` is legal. |
| Steps | `t = start + k·step`, `k ≥ 0`, `t ≤ end`; `(end − start)/step > 11000` → 400 `too many steps (N > 11000); increase step`. |
| Caps | more than 1000 output series → 400 `query produced too many series (N > 1000); add a by() clause`; 5 s wall time → 504. |
| Output | matrix on `query_range`, vector on `query`; timestamps are JSON numbers in seconds (millisecond precision, fraction only when needed: `1774915200`, `1788606883.701`); values are strings from `strconv.FormatFloat(v, 'f', -1, 64)` — `"3"`, `"1.3333333333333333"`, `"0.00000038580246913580245"` (= 1/2592000, what Loki prints too), `"+Inf"`. |
| Scalars | `vector(1)+vector(1)` is constant-folded at parse time: `/query` → `{"metric":{},"value":[<time>,"2"]}`, `/query_range` → one series `{}` with a point per step. Division by zero → `"+Inf"`. |

## 4. Unsupported constructs and their errors

All HTTP 400, `text/plain; charset=utf-8`, body = the message (Loki's error shape, no JSON envelope).

| Input | Error |
|---|---|
| `{}` , `{service=~".*"}`, `{level!="debug"}` | `parse error : queries require at least one regexp or equality matcher …` (§3.1) |
| `\| logfmt`, `\| pattern "…"`, `\| regexp "…"`, `\| unpack` | `parse error at line 1, col N: unsupported parser "logfmt" (supported: json)` |
| `\| line_format "…"`, `\| label_format a=b`, `\| unwrap x`, `\| drop x`, `\| keep x`, `\| decolorize` | `unsupported stage "line_format"` |
| `\| json a="b"` | `json parser takes no arguments` |
| `[5m] offset 1h` | `offset modifier is not supported` |
| `bytes_rate`, `bytes_over_time`, `absent_over_time`, `sum_over_time`, `avg_over_time`, `min_over_time`, `max_over_time`, `quantile_over_time`, `first_over_time`, `last_over_time`, `stddev_over_time`, `stdvar_over_time` | `unsupported function "bytes_rate" (supported: count_over_time, rate)` |
| `topk`, `bottomk`, `stddev`, `stdvar`, `sort`, `sort_desc`, `approx_topk` | `unsupported aggregation "topk" (supported: sum, count, min, max, avg)` |
| `sum(sum(…))` | `syntax error: unexpected IDENTIFIER "sum", expecting count_over_time or rate` |
| `<metric> / 2`, `2 * <metric>`, `<metric> + vector(1)` | `binary operators are only supported between vector() literals` |
| `sum by (a) (…) by (b)` | `duplicate grouping` |
| `{a="x"} json` (missing pipe) | `syntax error: unexpected IDENTIFIER "json", expecting \|, \|=, !=, \|~, !~ or end of query` |
| `{a="x", }` | `syntax error: unexpected }` |
| `{service="gradr"` | `syntax error: unexpected $end, expecting }` |
| `'single quoted'` | `syntax error: unexpected '` |
| `\| json \| by="x"` | `reserved word "by" cannot be a label name` |
| `\| json \| containers > "x"` | `numeric comparison needs a number, duration or bytes literal, got string` |
| `foo({…}[1d])` | `syntax error: unexpected IDENTIFIER "foo"` |
| empty query | `parse error : syntax error: unexpected $end` |

## 5. Loki HTTP API

Every endpoint accepts `GET` and, where Loki does, `POST` with an `application/x-www-form-urlencoded` body merged with the query string (a JSON body → 400 `invalid parameter: body must be application/x-www-form-urlencoded`; > 1 MiB → 413). `HEAD` mirrors `GET`. Success: `application/json`, `Cache-Control: public, max-age=15, s-maxage=15` (`status/buildinfo`: `max-age=60`), weak `ETag` + 304 on `If-None-Match` (for query responses the ETag covers `resultType` + `result`, not the timing stats). Errors: `text/plain; charset=utf-8`, `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`. `X-Loki-Response-Encoding-Flags` (Grafana sends `categorize-labels`) is ignored: `values` entries always have two elements and no `encodingFlags` key is emitted — the pre-Loki-3.0 shape every Grafana version accepts.

### 5.1 Parameters

| Parameter | Accepted forms | Rule |
|---|---|---|
| `start`, `end`, `time` | contains `.` → float seconds (fraction rounded to ms); integer with ≤ 10 digits → Unix **seconds**; integer with > 10 digits → Unix **nanoseconds**; else RFC 3339 / RFC 3339-nano (Loki's `parseTimestamp`) | invalid → 400 `invalid parameter "start": cannot parse "x" as nanoseconds, float seconds or RFC3339`; `end < start` → 400 `end must be after start` |
| defaults | `end` = now, `time` = now, `start` = the root span's resolved start (`2023-01-01T00:00:00Z`) — not Loki's "1 hour ago": every stored line is historical | Grafana always sends explicit bounds |
| `since` | Prometheus duration (`5m`, `30d`, `1y`) | `start = min(end, now) − since` when `start` is absent; `1.5h` → 400 `invalid parameter "since": unknown unit "." in duration "1.5h"` |
| `limit` | integer | absent → 100; `≤ 0` → 400 `limit must be a positive value`; not a number → 400 `invalid parameter "limit": strconv.Atoi: …`; log queries with `> 5000` → 400 `max entries limit per query exceeded, limit > max_entries_limit_per_query (6000 > 5000)`; ignored by metric queries |
| `direction` | `backward` (default) \| `forward` | else 400 `invalid direction "x": want forward or backward` |
| `step` | float seconds or Prometheus duration — Grafana sends integer milliseconds with a unit (`step=15000ms`) | default `max(⌊(end − start) / 250⌋, 1)` s (Loki's formula); `≤ 0` → 400 `zero or negative query resolution step widths are not accepted. Try a positive integer` |
| `interval` | any | accepted and ignored |
| `query` | non-empty after trimming | absent/blank → 400 `parse error : syntax error: unexpected $end` |
| `match[]` (also `match`) | repeatable selector | none → 400 `at least one match[] selector is required` |

### 5.2 Endpoints

| Endpoint | Params | Success body |
|---|---|---|
| `GET\|POST /loki/api/v1/query_range` | `query`, `start`, `end`, `since`, `limit`, `direction`, `step`, `interval` | `resultType` `streams` (log query) or `matrix` (metric / scalar query) |
| `GET\|POST /loki/api/v1/query` | `query`, `time`, `limit`, `direction` | `streams` (log query: every entry with `ts ≤ time`, newest first unless `direction=forward`, capped by `limit`) or `vector` |
| `GET\|POST /loki/api/v1/labels` | `start`, `end`, `since`, `query` (selector, optional) | `{"status":"success","data":["component","level","service"]}` — label names of the streams with ≥ 1 entry in the window |
| `GET\|POST /loki/api/v1/label/{name}/values` | same | `{"status":"success","data":["community","divy","edu",…]}` sorted; unknown label → `[]` |
| `GET\|POST /loki/api/v1/series` | `match[]` (repeat), `start`, `end`, `since` | `{"status":"success","data":[{"component":"caddy","level":"debug","service":"gradr"},…]}` — union of the streams matching any selector, sorted by label string |
| `GET\|POST /loki/api/v1/index/stats` | `query` (selector), `start`, `end` | `{"streams":15,"chunks":15,"entries":24,"bytes":4984}` — no envelope; `chunks` = `streams`, `bytes` = raw line bytes in the window |
| `GET /loki/api/v1/index/volume` | `query` (selector), `start`, `end`, `limit` (100), `targetLabels` (comma list), `aggregateBy` (`series` \| `labels`) | `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"level":"info"},"value":[<end>,"12341"]},…]}}` — bytes per stream (`series`, restricted to `targetLabels` when given) or per label name/value pair (`labels`), top `limit` by bytes |
| `GET /loki/api/v1/status/buildinfo` | — | `{"version":"v0.1.0","revision":"3f2a9c1","branch":"main","buildUser":"ci","buildDate":"2026-09-05T00:00:00Z","goVersion":"go1.24.7"}` — from `internal/version`, no envelope |
| anything else under `/loki/` (`tail`, `push`, `detected_labels`, `detected_fields`, `detected_field/*`, `patterns`, `index/volume_range`, a trailing slash) | — | **404** text `not supported by divy.dev; see /loki/api/v1/status/buildinfo`; wrong method → 405 text `method not allowed` + `Allow` |

Streams response (`GET /loki/api/v1/query_range?query={service="gradr"} |= "sentry" | json | level="warn"&limit=1`, real content):

```json
{"status":"success","data":{"resultType":"streams","result":[
 {"stream":{"component":"sentry","duplicate_issues":"1000+","incident":"INC-004","level":"warn","msg":"unbounded fingerprinting created 1000+ duplicate issues","resolved":"true","service":"gradr","span":"gradr.inc-004","ts":"TODO(divy)"},
  "values":[["1772323200000000048","{\"ts\":\"TODO(divy)\",\"level\":\"warn\",\"service\":\"gradr\",\"component\":\"sentry\",\"span\":\"gradr.inc-004\",\"msg\":\"unbounded fingerprinting created 1000+ duplicate issues\",\"incident\":\"INC-004\",\"duplicate_issues\":\"1000+\",\"resolved\":true}"]]}],
 "stats":{"ingester":{"compressedBytes":0,"decompressedBytes":0,"decompressedLines":0,"headChunkBytes":0,"headChunkLines":0,"totalBatches":0,"totalChunksMatched":0,"totalDuplicates":0,"totalLinesSent":0,"totalReached":0},
          "store":{"compressedBytes":0,"decompressedBytes":0,"decompressedLines":0,"chunksDownloadTime":0,"totalChunksRef":15,"totalChunksDownloaded":0,"totalDuplicates":0},
          "summary":{"bytesProcessedPerSecond":16228290,"execTime":0.000041,"linesProcessedPerSecond":78145,"queueTime":0,"totalBytesProcessed":4984,"totalLinesProcessed":24,"totalEntriesReturned":1}}}}
```

Rules: surviving entries are sorted by `(ts, label string)` — descending `ts` for `backward`, ascending for `forward` — the first `limit` are kept, then grouped by final label set; streams are sorted by label string; `values[i][0]` is the decimal nanosecond timestamp as a **string**, `values[i][1]` the raw line verbatim. `stats` has exactly the three Loki sections: `store.totalChunksRef` = streams selected, `summary.totalLinesProcessed` / `totalBytesProcessed` = entries scanned in those streams and their bytes, `totalEntriesReturned`, `execTime` in seconds; everything else is 0.

Matrix response (`sum by (level) (count_over_time({service="gradr"}[1y]))`, `start=2025-01-01T00:00:00Z`, `end=2026-09-05T00:00:00Z`, `step=1y`):

```json
{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"level":"info"},"values":[[1767225600,"2"]]}],"stats":{…}}}
```

(the step at `2025-01-01` has no point: the window `(2024-01-01, 2025-01-01]` holds no gradr line.)

Vector response (Grafana's health check, `GET /loki/api/v1/query?direction=backward&query=vector(1)%2Bvector(1)&time=4000000000`):

```json
{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[4000000000,"2"]}],"stats":{…}}}
```

### 5.3 Errors and status codes

| Case | Status | Body |
|---|---|---|
| parse error | 400 | `parse error at line 1, col 21: unsupported parser "logfmt" (supported: json)` |
| empty-compatible selector | 400 | Loki's verbatim message (§3.1) |
| bad parameter | 400 | `invalid parameter "start": …`, `end must be after start`, `limit must be a positive value`, … |
| limit above 5000 | 400 | `max entries limit per query exceeded, limit > max_entries_limit_per_query (6000 > 5000)` |
| too many steps / series | 400 | §3.3 |
| unknown endpoint under `/loki/` | 404 | `not supported by divy.dev; see /loki/api/v1/status/buildinfo` |
| wrong method | 405 + `Allow` | `method not allowed` |
| form body too large | 413 | `request body too large` |
| evaluation timeout (5 s) | 504 | `query timed out after 5s` |
| client disconnect | 499 | (empty) |
| panic | 500 | `internal error: <request id>` (the recoverer's JSON envelope; the OTel lane replaces the id with the trace id) |

## 6. Grafana

### 6.1 Add the site as a Loki data source

1. Grafana → **Connections → Data sources → Add new data source → Loki**.
2. **URL** = the site origin (`SITE_ORIGIN`, e.g. `https://<project>.vercel.app`; locally `http://localhost:8080`). No authentication, no custom headers.
3. **Save & test** → "Data source successfully connected." Grafana sends `GET /loki/api/v1/query?direction=backward&query=vector(1)%2Bvector(1)&time=4000000000` and requires one frame with two fields, one row and the value `2` (§5.2's vector example).

What Grafana does afterwards and what it gets:

| Grafana action | Requests | Outcome |
|---|---|---|
| Explore, log query, time range T | `GET /loki/api/v1/query_range?query=…&start=<ns>&end=<ns>&limit=1000&direction=backward&step=<ms>ms`; log-volume histogram `sum by (level, detected_level) (count_over_time(<query>[<auto>]))` without `limit` | streams + matrix (`detected_level` absent → grouped by `level`) |
| Label browser / autocomplete | `/labels?start&end`, `/label/{name}/values?start&end&query`, `/series?match[]` | supported |
| Query stats hint, volume panel | `/index/stats?query&start&end`, `/index/volume` | supported |
| Detected fields, patterns, `volume_range` | `/detected_fields`, `/detected_field/*/values`, `/patterns`, `/index/volume_range` | 404 — the plugin catches it, the feature is absent, the query still runs |
| Live tail | WebSocket `/loki/api/v1/tail` | 404 — Live mode shows an error; normal queries are unaffected |
| Derived field on `span` | regex `"span":"([a-z0-9.-]+)"`, URL `<SITE_ORIGIN>/trace/career?span=${__value.raw}` | a link per line to the career trace drawer |

### 6.2 Explore examples (real content)

| Query | What it shows |
|---|---|
| `{service="gradr"}` | the 24 Gradr lines, newest first |
| `{service="gradr"} \|= "sentry" \| json \| level="warn"` | the three Sentry incident warnings (INC-002 memory exhaustion, INC-003 email relay, INC-004 fingerprinting), each with its extracted `incident`, `span`, `resolved` labels |
| `{service="gradr"} \| json \| incident=~"INC-.*" \| level="error"` | the four incident-start lines (`level="error"`) — one per postmortem |
| `{service="gradr"} \| json \| span="gradr.inc-002"` | every line of one incident span: error, warn and the "resolved" info line (the trace drawer's reverse link) |
| `{service=~"euro-tech\|ef-polymer"} \|~ "(?i)shipped\|deployed"` | what shipped at Euro Technologies and EF Polymer |
| `{service=~".+"} \| json \| active_users >= 5000 or global_rank < 100` | Savely passing 5,000 users and the WorldQuant IQC rank 98 (numeric filters on extracted fields) |
| `{level="debug"}` | the fun details (first asm routine, LeetCode streak, BRAIN alphas, …) |
| `{service=~".+"} \| json \| __error__=""` | every line — none fails to parse; the idiom is here for Loki habit |
| `sum by (service) (count_over_time({service=~".+"}[10y]))` (instant) | lines per service: gradr 24, oss 28, ef-polymer 9, … |
| `sum by (level) (count_over_time({service=~".+"}[1y]))` (range, step `1y`) | lines per level per year |
| `count_over_time({service="gradr"} \| json \| incident=~"INC-.*" [1y])` | one series per incident label set |

The site's own logs page issues the same requests (`/logs?q=…`), so any query that works in Grafana works there and vice versa.

## 7. Tests

| File | Rows | Covers |
|---|---|---|
| `internal/logql/parser_test.go` | 63 parser rows (`TestParse`) + shape checks | every construct of §1, every error of §4 with exact position, canonical-form round trip |
| `internal/logql/eval_test.go` | 17 log rows, 2 JSON tables, 14 metric/aggregation rows, guards, labels/values/series/stats/volume, JSON marshalling | §3 on the 11 fixture lines of `internal/content/testdata/valid/logs.ndjson` plus synthetic lines |
| `internal/server/handlers_loki_test.go` | 4 tests, 13 error rows, 5 timestamp forms | §5: shapes, headers, ETag/304, HEAD, Grafana's exact requests (`time=4000000000`, `step=15000ms`, no `limit`), 404/405/413 |
