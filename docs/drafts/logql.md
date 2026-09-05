# LogQL, Loki API, traces, OTel self-tracing, easter-egg endpoints

## Cross-section notes

| # | Note | Affects |
|---|------|---------|
| L-X1 | **Loki error shape (CONVENTIONS #14 "Loki's")** = what real Loki does: **plain text**, `Content-Type: text/plain; charset=utf-8`, `X-Content-Type-Options: nosniff`, body = the message, status 400/404/429/500. The web `ApiError` parser (repo §R.7.2) must treat a non-JSON body on `/loki/*` as the message. Storage S-X8's 429 on `/loki/*` is therefore the text `rate limit exceeded: …`. | Repo, storage |
| L-X2 | **Default time range on `/loki/*`** when `start`/`end`/`since` are absent: `start` = root span's resolved start (`2023-01-01T00:00:00Z`), `end` = now — not Loki's "1 hour ago" / "6 hours ago". Every stored line is historical, so Loki's defaults would return nothing to a bare `curl`. Grafana always sends explicit bounds and is unaffected. | API |
| L-X3 | `| json` label collision rule diverges from Loki: an extracted key whose value equals the stream label's value is skipped; a different value is emitted as `<key>_extracted` (§L.1.6). | Content (labels are copied from the line, so collisions are always equal-valued) |
| L-X4 | Adds to Content X4's endpoint list: `GET /api/content/spans`, `GET /api/content/logs`, `GET /api/content/alerts` (raw content mirrors, §L.6.8), `GET /api/uptime` = alias of storage §S.4.3 `/api/uptime/heartbeats` with its defaults (`days=90&bucket=1d`), `GET /ascii` (§L.6.1), `GET /api/services`, `GET /api/services/{service}/operations`, `GET /api/operations`, `GET /api/traces?service=` (§L.4). | API contract |
| L-X5 | Adds response header **`X-Divy-Trace-Sampled: 0|1`** next to `X-Divy-Trace-Id` so a `curl` user can see why a trace id 404s (§L.5.6). | API |
| L-X6 | Adds env `OTEL_SAMPLE_RPS` (default `100`) and `OTEL_SAMPLE_BURST` (default `200`) to repo §R.3.2 / `.env.example`, and metrics `divy_otel_spans_total{decision}`, `divy_otel_exported_spans_total`, `divy_otel_export_errors_total` to storage §S.5. | Repo, storage |
| L-X7 | OG route follows repo §R.6.6 (`/og/postmortems/{id}.png`, `/og/default.png`), not `/og/{id}.png`. Adds `web/static/favicon.ico` (static fallback icon) to the repo static-file list. | Repo |
| L-X8 | The trace page `/trace/[id]` must accept `?span=<divy.id | Jaeger spanID>` and open that span's drawer; `/trace/career?span=gradr.inc-002` is a second valid deep link besides R5's `/#trace?span=…`. Grafana derived-field links use it (§L.3.4). | Frontend |
| L-X9 | `/metrics`, `/healthz`, `/readyz` and static assets **are** traced (one root span each) so `X-Divy-Trace-Id` resolves on every response, as the brief requires. Baseline volume ≈ 3.2k spans/day from health checks and self-probes, well inside storage's 24 h / 20k cap. | Storage (`otel_spans` volume) |
| L-X10 | `otel_spans.status_code` stores **OTLP numbering** (0 UNSET, 1 OK, 2 ERROR) as the DDL comment says; the exporter maps Go `codes.Ok (2) → 1`, `codes.Error (1) → 2`, `codes.Unset (0) → 0`. | Storage |

Package: `api/internal/logql/` (`lexer.go`, `ast.go`, `parser.go`, `json.go`, `eval.go`, `*_test.go`), `api/internal/trace/` (`career.go`, `otel.go`, `exporter.go`, `jaeger.go`, `sampler.go`), `api/internal/ascii/`, `api/internal/og/`, handlers in `api/internal/server/` (`loki.go`, `traces.go`, `aux.go`, `content.go`). Docs copy of §L.1–L.2 goes to `docs/logql-subset.md` (repo §R.1).

---

## L.1 LogQL subset

### L.1.1 Tokens

Longest match wins (`!~` before `!=` before `!`; `|=`, `|~` before `|`; `=~`, `==` before `=`; `>=` before `>`; `<=` before `<`). Whitespace separates tokens and is otherwise ignored. Tokenizer errors are parse errors at the offending column.

| Token | Lexeme | Notes |
|-------|--------|-------|
| `LBRACE` `RBRACE` | `{` `}` | stream selector |
| `LPAREN` `RPAREN` | `(` `)` | grouping, function calls |
| `LBRACKET` `RBRACKET` | `[` `]` | range |
| `COMMA` | `,` | matcher separator; also `and` inside label filters |
| `PIPE` | `\|` | stage separator |
| `EQ` `NEQ` `RE` `NRE` | `=` `!=` `=~` `!~` | matcher / string label filter; `!=` is also a line filter when it starts a stage without an identifier |
| `PIPE_EXACT` `PIPE_MATCH` | `\|=` `\|~` | line filters |
| `CMP_EQ` `GT` `GTE` `LT` `LTE` | `==` `>` `>=` `<` `<=` | numeric label filters |
| `PLUS` `MINUS` `MUL` `DIV` | `+` `-` `*` `/` | only between `vector(N)` literals (§L.1.9) |
| `IDENT` | `[a-zA-Z_][a-zA-Z0-9_]*` | label names, function names, keywords (`json and or by without count_over_time rate sum count min max avg vector`); keywords are recognised by the parser in context and cannot be used as label names |
| `STRING` | `"…"` with Go escapes (`strconv.Unquote`) or `` `…` `` raw | unterminated → `literal not terminated` |
| `NUMBER` | `[0-9]+(\.[0-9]+)?` | |
| `DURATION` | `NUMBER (ms\|s\|m\|h\|d\|w\|y)` repeated, e.g. `1h30m`, `7d` | `d` = 24 h, `w` = 7 d, `y` = 365 d (Prometheus `model.ParseDuration` rules); no unit mixing with fractions |

### L.1.2 Grammar (EBNF)

```ebnf
query          = metric_query | log_query | scalar_expr ;

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
number_filter  = IDENT ( "==" | "!=" | ">" | ">=" | "<" | "<=" ) ( NUMBER | DURATION ) ;

metric_query   = range_agg | aggregation ;
range_agg      = ( "count_over_time" | "rate" ) "(" log_query "[" DURATION "]" ")" ;
aggregation    = agg_op [ grouping ] "(" range_agg ")" [ grouping ] ;
agg_op         = "sum" | "count" | "min" | "max" | "avg" ;
grouping       = ( "by" | "without" ) "(" [ IDENT { "," IDENT } ] ")" ;

scalar_expr    = scalar_term { ( "+" | "-" ) scalar_term } ;
scalar_term    = scalar_atom { ( "*" | "/" ) scalar_atom } ;
scalar_atom    = "vector" "(" NUMBER ")" | "(" scalar_expr ")" ;
```

Disambiguation rules the parser applies:

| Situation | Rule |
|-----------|------|
| `!=` at the start of a stage | line filter if followed by `STRING`; if preceded by `\|` and followed by `IDENT` it is inside a label filter |
| `number_filter` vs `string_filter` with `!=` | decided by the right-hand token: `STRING` → string filter, `NUMBER`/`DURATION` → numeric |
| `>` `<` … followed by `STRING` | parse error `numeric comparison needs a number or duration, got string` |
| Two groupings (`sum by (a) (…) by (b)`) | parse error `duplicate grouping` |
| Empty `by ()` | legal: one series with `{}` |

AST (Go):

```go
type Query interface{ isQuery() }
type LogQuery   struct { Selector []Matcher; Stages []Stage }              // log_query
type MetricQuery struct { Agg *Aggregation; Range RangeAgg }               // Agg nil = bare range_agg
type ScalarQuery struct { Value float64 }                                  // constant-folded scalar_expr

type Matcher struct { Name string; Op MatchOp; Value string; re *regexp.Regexp } // re anchored ^(?:v)$ for =~ !~
type Stage interface{ isStage() }
type LineFilter struct { Op LineOp; Text string; re *regexp.Regexp }      // |= != |~ !~ ; re unanchored
type JSONParser struct{}
type LabelFilter struct { Expr LFExpr }
type LFExpr interface{ isLF() }
type LFOr  struct{ L, R LFExpr }
type LFAnd struct{ L, R LFExpr }
type LFString struct { Name string; Op MatchOp; Value string; re *regexp.Regexp }
type LFNumber struct { Name string; Op CmpOp; Value float64; IsDuration bool } // duration literal → seconds
type RangeAgg struct { Fn string; Log LogQuery; Range time.Duration }     // count_over_time | rate
type Aggregation struct { Op string; Without bool; Labels []string }      // Labels nil = no grouping
```

Parse errors are `*ParseError{Line, Col, Msg}` printed as `parse error at line 1, col N: <msg>` (Loki's format); the column is 1-based on the raw query string. Regexes are compiled at parse time (RE2 via Go `regexp`); a compile failure is a parse error `error parsing regexp: <go message>`.

### L.1.3 Stream selector semantics

- Stream label set (fixed by ingestion, §L.3.1): `service` (always), `level` (always), `component` (only when the line has one).
- A matcher on a label the stream lacks compares against the empty string (Prometheus rule): `{component!="dev-proxy"}` matches streams without `component`; `{component=""}` matches exactly those.
- `=~`/`!~` regexes are anchored to the whole value: `{service=~"gradr|euro-tech"}` compiles as `^(?:gradr|euro-tech)$`.
- **Non-empty rule** (Loki, verbatim error): at least one matcher must not match the empty string — `=` with a non-empty value, `=~` whose regex does not match `""`, `!= ""`, or `!~` whose regex matches `""`. Otherwise 400 `queries require at least one regexp or equality matcher that does not have an empty-compatible value. For instance, app=~".*" does not meet this requirement, but app=~".+" will`. `{}` fails the same way.
- Selected streams = every stream whose label set satisfies all matchers. No selector, no query (a bare `|= "x"` is a parse error).

### L.1.4 Line filters

Applied in order to the **raw line text** (the verbatim NDJSON object). An entry survives a filter chain only if every filter passes. `|=` = `strings.Contains` (case-sensitive; `|= ""` passes everything), `!=` = not contains, `|~` = `re.MatchString` (unanchored; use `(?i)` for case-insensitive), `!~` = not matched. Filters before `| json` and after it behave identically (they always see the raw line, never extracted labels).

### L.1.5 Label set of a line

Each entry carries a label set that starts as its stream labels and grows through stages. Label filters (§L.1.7) read this set. The response groups entries by their **final** label set (§L.2.2), which is why `{service="gradr"} | json` returns one stream per distinct extracted set, exactly as Loki does.

### L.1.6 `| json`

| Rule | Detail |
|------|--------|
| Input | the raw line, parsed with `encoding/json` into an ordered token walk (numbers kept as their literal text via `json.Number`) |
| Top-level scalars | `"k": "v"` → label `k="v"`; numbers → literal text (`65`, `1.5`); booleans → `true`/`false`; `null` → skipped |
| Nested objects | flattened with `_`: `{"a":{"b":1}}` → `a_b="1"` (any depth) |
| Arrays | skipped entirely (Loki: "Arrays are skipped"), including arrays inside nested objects |
| Key sanitization | every byte outside `[a-zA-Z0-9_]` → `_`; a leading digit gets `_` prefixed; a key that becomes empty is skipped. Content validation already forbids such keys in `content/logs.ndjson`, so this only matters for the synthetic test lines |
| Collision with a stream label | identical value → skipped (no duplicate); different value → emitted as `<key>_extracted` (L-X3) |
| Reserved names | keys named `__error__`, `__error_details__` in the line are ignored |
| Invalid JSON | labels `__error__="JSONParserErr"` and `__error_details__="<go error text>"` are added; the entry is **kept** (Loki rule). Cannot happen for validated content; handled for robustness |
| Second `\| json` | idempotent (same result) |
| Extraction expressions (`\| json a="b[0]"`) | unsupported → parse error `json parser takes no arguments` |

### L.1.7 Label filters (after `|`)

| Form | Semantics |
|------|-----------|
| `name = "v"`, `name != "v"`, `name =~ "re"`, `name !~ "re"` | string compare against the current label set; a missing label is `""`; regexes anchored like matchers |
| `name == 65`, `name != 65`, `name > 60`, `name >= 65`, `name < 100`, `name <= 12` | value parsed with `strconv.ParseFloat`; on failure the entry is **kept** and gets `__error__="LabelFilterErr"`, `__error_details__="strconv.ParseFloat: parsing \"INC-001\": invalid syntax"` (Loki: "the log line is not filtered and an `__error__` label is added"); a **missing label never matches** (entry dropped, no error) — this is our rule |
| `name > 5m` (duration literal) | both sides in seconds: the label value is parsed as a Prometheus duration (`1h30m`, `45s`, `2d`); a plain number is treated as seconds; failure → `LabelFilterErr` as above |
| `a and b`, `a, b` | both must hold |
| `a or b` | either; `and` binds tighter than `or`; parentheses allowed |
| `__error__=""` | the Loki idiom to drop errored entries works because `__error__` is an ordinary label of the entry |
| Bytes literals (`1KB`, `3MiB`) | unsupported → parse error `bytes literals are not supported` |

Label filters may appear without a preceding `| json` (they then see stream labels only): `{service="gradr"} | level="warn"` is valid.

### L.1.8 Metric queries

| Form | Output |
|------|--------|
| `count_over_time(<log_query>[R])` | at each step `t`: for every entry passing the log pipeline with `t − R < ts ≤ t`, count per **final label set**; a series has a point at `t` only if its count > 0 |
| `rate(<log_query>[R])` | `count_over_time / R.Seconds()` |
| `sum \| count \| min \| max \| avg [by (l…) \| without (l…)] (range_agg)` | Prometheus aggregation over the range_agg's series at each step; `by` keeps only the listed labels (unknown names are simply absent — `sum by (level, detected_level)` groups by `level`), `without` drops the listed ones plus nothing else; no grouping → one series `{}` |
| Steps | `t = start + k·step`, `k ≥ 0`, `t ≤ end`; `(end − start) / step > 11000` → 400 `too many steps (N > 11000); increase step` |
| Value formatting | `strconv.FormatFloat(v, 'f', -1, 64)` (`"3"`, `"0.0000003858024691358025"`) |
| Result | `matrix` on `/query_range`, `vector` on `/query` (single evaluation at `time`) |
| Series cap | > 1000 series in a result → 400 `query produced too many series (N > 1000); add a by() clause` |

### L.1.9 Scalar expressions (for Grafana's health check only)

`vector(N)` literals combined with `+ - * /` and parentheses are constant-folded at parse time and evaluate to a single vector sample `{"metric":{},"value":[<time>,"<N>"]}` on `/query`, or a matrix with one point per step on `/query_range`. Any other operand (a metric query, a bare number) is a parse error `binary operators are only supported between vector() literals`. Division by zero → `+Inf` → serialised `"+Inf"`.

### L.1.10 Unsupported (exact error text; all HTTP 400, plain text)

| Input | Error (`parse error at line 1, col N: …`) |
|-------|-------------------------------------------|
| `\| logfmt`, `\| pattern "…"`, `\| regexp "…"`, `\| unpack` | `unsupported parser "logfmt" (supported: json)` |
| `\| line_format "…"`, `\| label_format a=b`, `\| unwrap x`, `\| drop x`, `\| keep x`, `\| decolorize` | `unsupported stage "line_format"` |
| `[5m] offset 1h` | `offset modifier is not supported` |
| `bytes_rate`, `bytes_over_time`, `absent_over_time`, `sum_over_time`, `avg_over_time`, `min_over_time`, `max_over_time`, `quantile_over_time`, `first_over_time`, `last_over_time`, `stddev_over_time`, `stdvar_over_time` | `unsupported function "bytes_rate" (supported: count_over_time, rate)` |
| `topk`, `bottomk`, `stddev`, `stdvar`, `sort`, `sort_desc` | `unsupported aggregation "topk" (supported: sum, count, min, max, avg)` |
| `<metric> + <metric>`, `<metric> > 3`, `2 * <metric>` | `binary operators are only supported between vector() literals` |
| `\| json a="b"` | `json parser takes no arguments` |
| `'single quoted'` | `syntax error: unexpected '` |
| `{a="x"} json` (missing pipe) | `syntax error: unexpected IDENTIFIER "json", expecting |, |=, !=, |~, !~ or end of query` |
| `{a="x", }` | `syntax error: unexpected }` |

---

## L.2 Loki HTTP API (this server)

All routes `GET` (plus `POST` on `/series`), JSON `application/json` on success, plain text on error (L-X1). Every response: `X-Divy-Trace-Id`, `X-Divy-Trace-Sampled`, `Cache-Control: public, max-age=15`, `ETag`, `X-Cache` (storage §S.6), CORS per CONVENTIONS #15. Rate limited per IP (storage §S.7).

### L.2.1 Parameter parsing (shared)

| Parameter | Accepted forms | Rule |
|-----------|----------------|------|
| `start`, `end`, `time` | contains `.` → float seconds; all digits → **nanoseconds** since epoch; else RFC 3339 / RFC 3339-nano (Loki's `parseTimestamp`) | invalid → 400 `invalid parameter "start": cannot parse "x" as nanoseconds, float seconds or RFC3339`; `end < start` → 400 `end must be after start` |
| `since` | Prometheus duration (`5m`, `2h`, `30d`) | `start = min(end, now) − since` when `start` absent |
| defaults | `start` = `2023-01-01T00:00:00Z` (root span start), `end` = now, `time` = now (L-X2) | |
| `limit` | integer 1…5000 | absent → 100; `> 5000` → 400 `max entries limit per query exceeded, limit > max_entries_limit_per_query (6000 > 5000)`; `< 1` → 400 `limit must be between 1 and 5000` |
| `direction` | `forward` \| `backward` (default) | else 400 `invalid direction "x": want forward or backward` |
| `step` | float seconds or duration | default `max(floor((end−start)/250 s), 1)` s (Loki's formula); `≤ 0` → 400 |
| `interval` | any | accepted and ignored |
| `query` | non-empty after trimming | absent/empty → 400 `parse error : syntax error: unexpected $end` |
| `match[]` | repeatable selector | none → 400 `at least one match[] selector is required` |

Time window semantics everywhere: **`start ≤ ts < end`** for log entries (Loki's convention); metric windows are `(t − R, t]`.

### L.2.2 Endpoint table

| Endpoint | Params | Success body | Notes |
|----------|--------|--------------|-------|
| `GET /loki/api/v1/query_range` | `query`, `start`, `end`, `since`, `limit`, `direction`, `step`, `interval` | streams (log query) or matrix (metric / scalar query) | `limit` ignored for metric queries |
| `GET /loki/api/v1/query` | `query`, `time`, `limit`, `direction` | streams (log query: entries with `ts ≤ time`, newest first, `limit`) or vector | |
| `GET /loki/api/v1/labels` | `start`, `end`, `since`, `query` (selector; optional) | `{"status":"success","data":["component","level","service"]}` | label names of streams with ≥ 1 entry in the window (and matching `query`) |
| `GET /loki/api/v1/label/{name}/values` | same | `{"status":"success","data":["gradr","euro-tech",…]}` sorted | unknown label → `"data":[]` (200) |
| `GET\|POST /loki/api/v1/series` | `match[]` (repeat), `start`, `end`, `since`; POST = form-encoded | `{"status":"success","data":[{"level":"info","service":"gradr"},…]}` | union of streams matching any selector, sorted by label string |
| `GET /loki/api/v1/index/stats` | `query` (selector), `start`, `end` | `{"streams":3,"chunks":3,"entries":4,"bytes":612}` | `chunks` = `streams`; `bytes` = sum of raw line bytes in the window |
| `GET /loki/api/v1/index/volume` | `query`, `start`, `end`, `limit` (100), `targetLabels`, `aggregateBy` (`series`\|`labels`) | `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"level":"info","service":"gradr"},"value":[1757030400,"318"]}]}}` | bytes per stream (`series`) or per label value of `targetLabels` (`labels`); top `limit` by bytes |
| `GET /loki/api/v1/status/buildinfo` | — | `{"version":"divy.dev v0.1.0","revision":"3f2a9c1","branch":"main","buildUser":"ci","buildDate":"2026-09-05T00:00:00Z","goVersion":"go1.26.8"}` | from `internal/version` |
| `GET /loki/api/v1/tail`, `/detected_labels`, `/detected_fields`, `/detected_field/*`, `/patterns`, `/index/volume_range`, `POST /loki/api/v1/push` | — | **404** text `not supported by divy.dev; see /loki/api/v1/status/buildinfo` | Grafana's LanguageProvider swallows `detected_*` failures; Live tail shows an error only in Live mode |

Streams response (`query_range`, `{service="gradr"} | json | resolved="true"`, backward):

```json
{"status":"success","data":{"resultType":"streams","result":[
 {"stream":{"component":"dev-proxy","containers":"65","incident":"INC-002","level":"warn","msg":"cascading memory exhaustion: sentry containers saturating swap","resolved":"true","service":"gradr","span":"gradr.inc-002","ts":"TODO(divy)"},
  "values":[["1772323200000000008","{\"ts\":\"TODO(divy)\",\"level\":\"warn\",\"service\":\"gradr\",\"component\":\"dev-proxy\",\"span\":\"gradr.inc-002\",\"msg\":\"cascading memory exhaustion: sentry containers saturating swap\",\"incident\":\"INC-002\",\"containers\":65,\"resolved\":true}"]]},
 {"stream":{"component":"secrets-sidecar","incident":"INC-001","level":"warn","msg":"post-reboot race: secrets sidecar wrote .env after app containers started; Supabase-backed service down","resolved":"true","service":"gradr","span":"gradr.inc-001","ts":"TODO(divy)"},
  "values":[["1772323200000000007","{\"ts\":\"TODO(divy)\",\"level\":\"warn\",\"service\":\"gradr\",\"component\":\"secrets-sidecar\",\"span\":\"gradr.inc-001\",\"msg\":\"post-reboot race: secrets sidecar wrote .env after app containers started; Supabase-backed service down\",\"incident\":\"INC-001\",\"resolved\":true}"]]}],
 "stats":{"ingester":{"compressedBytes":0,"decompressedBytes":0,"decompressedLines":0,"headChunkBytes":0,"headChunkLines":0,"totalBatches":0,"totalChunksMatched":0,"totalDuplicates":0,"totalLinesSent":0,"totalReached":0},
          "store":{"compressedBytes":0,"decompressedBytes":0,"decompressedLines":0,"chunksDownloadTime":0,"totalChunksRef":3,"totalChunksDownloaded":0,"totalDuplicates":0},
          "summary":{"bytesProcessedPerSecond":8160000,"execTime":0.000075,"linesProcessedPerSecond":53333,"queueTime":0,"totalBytesProcessed":612,"totalLinesProcessed":4}}}}
```

Rules behind it:

| Rule | Detail |
|------|--------|
| Ordering | all surviving entries are sorted by `(ts, stream label string)`; `backward` = descending ts, `forward` = ascending; then the first `limit` are kept; then grouped by final label set; streams sorted by label string ascending; `values` within a stream keep the direction order |
| `values[i][0]` | decimal nanoseconds as a string |
| `values[i][1]` | the raw line verbatim (Content §C.4.2) |
| `stream` | the entry's final label set (stream labels + extracted), keys sorted |
| `stats` | exactly the three documented sections; `summary.totalLinesProcessed` = entries scanned in selected streams, `totalBytesProcessed` = their bytes, `execTime` seconds, `store.totalChunksRef` = streams selected, everything else 0 |

Matrix response (`sum by (level) (count_over_time({service="gradr"}[30d]))`, `start=2026-03-01T00:00:00Z`, `end=2026-03-31T00:00:00Z`, `step=2592000`):

```json
{"status":"success","data":{"resultType":"matrix","result":[
 {"metric":{"level":"info"},"values":[[1774915200,"1"]]},
 {"metric":{"level":"warn"},"values":[[1774915200,"2"]]}],
 "stats":{"ingester":{…zeros…},"store":{…,"totalChunksRef":3,…},"summary":{…,"totalLinesProcessed":4}}}}
```

(the step at `2026-03-01` has no point: the window `(2026-01-30, 2026-03-01]` contains no entry.)

Vector response (`/query?query=vector(1)%2Bvector(1)`): `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1757030400.123,"2"]}],"stats":{…}}}`.

### L.2.3 Instant log query rule (our definition)

`/query` with a log query returns entries with `ts ≤ time` (all history by default), newest first, capped by `limit`, as `streams`. Grafana's Explore "instant" toggle sends this.

### L.2.4 Errors and status codes

| Case | Status | Body (text/plain) |
|------|--------|-------------------|
| parse error | 400 | `parse error at line 1, col 21: unsupported parser "logfmt" (supported: json)` |
| empty-compatible selector | 400 | Loki's verbatim message (§L.1.3) |
| bad parameter | 400 | `invalid parameter "start": …` |
| limit exceeded | 400 | `max entries limit per query exceeded, limit > max_entries_limit_per_query (6000 > 5000)` |
| unknown endpoint under `/loki/` | 404 | `not supported by divy.dev; see /loki/api/v1/status/buildinfo` |
| rate limited | 429 + `Retry-After` | `rate limit exceeded: 20 req/s per client, burst 100; retry after 1s` |
| evaluation timeout (5 s per request) | 504 | `query timed out after 5s` |
| panic | 500 | `internal error; trace id <X-Divy-Trace-Id>` |

### L.2.5 Grafana flow (verified against the standalone plugin, Grafana ≥ 12.3)

| Grafana action | Requests to this server | Outcome |
|----------------|-------------------------|---------|
| Data source → **Save & test** | backend health check: instant query `vector(1)+vector(1)` (`step=1s`) → `/loki/api/v1/query` | supported (§L.1.9) → "Data source successfully connected." |
| Explore, log query, time range T | `/loki/api/v1/query_range?query=…&start=<ns>&end=<ns>&limit=1000&direction=backward&step=…`; supplementary volume `sum by (level, detected_level) (count_over_time(<expr>[<auto>]))` | streams + matrix; `detected_level` absent → grouping by `level` |
| Label browser / autocomplete | `/labels?start&end`, `/label/{name}/values?start&end&query`, `/series?match[]` | supported |
| Query stats hint | `/index/stats?query&start&end` | supported |
| Detected fields, patterns, volume_range | `/detected_fields`, `/detected_field/*/values`, `/patterns`, `/index/volume_range` | 404; plugin catches → feature absent, query still runs |
| Live tail | WebSocket `/loki/api/v1/tail` | 404 → Live mode errors; normal queries unaffected |
| Derived field on `span` | `regex: "span":"([a-z0-9.-]+)"`, URL `https://divy.dev/trace/career?span=${__value.raw}` | link per line (§L.3.4) |

README snippet: "Add `https://divy.dev` as a Loki data source in Grafana ≥ 12.3 (Save & test passes). Query `{service="gradr"} | json`."

---

## L.3 Log ingestion

### L.3.1 `content/logs.ndjson` → in-memory streams (at `divy serve` startup, after validation)

| Step | Rule |
|------|------|
| Read | line by line, `\r?\n` terminated; empty lines skipped; `line_index` = 0-based index among non-empty lines |
| Raw text | the line bytes minus the terminator, stored verbatim (this is what `values[][1]` and line filters see) |
| Stream key | canonical label string `{component="…", level="…", service="…"}` (keys sorted; `component` only when present); parsed from the line's `service`, `level`, `component` fields |
| Ordering timestamp | Content §C.4.1: RFC 3339 `ts` → itself; `TODO(divy)` with `span` → that span's resolved start (fallbacks included); without `span` → `2023-01-01T00:00:00Z`; then **`ns += line_index`** so every entry has a unique nanosecond timestamp and file order is the tiebreak |
| Sort | entries within a stream ascending by ns; a global ascending slice is kept too (`allEntries`) for merged scans |
| Duplicates | impossible after `+ line_index`; two byte-identical lines are two entries 1 ns apart |
| Size | ≤ 100 lines (validator) — everything lives in memory; SQLite is not involved; no runtime mutation (content is immutable per process) |
| Precision | `precision` stays inside the line; the UI reads it from the JSON, the API never interprets it |

Example: the 11 sample lines of §C.4.3 become 8 streams: `{level="info",service="edu"}` (1), `{level="info",service="ef-polymer"}` (2), `{level="info",service="euro-tech"}` (2), `{level="info",service="gradr"}` (2), `{component="secrets-sidecar",level="warn",service="gradr"}` (1, ns `1772323200000000007`), `{component="dev-proxy",level="warn",service="gradr"}` (1, ns `…008`), `{level="debug",service="oss"}` (1, `1672531200000000009`), `{level="debug",service="quant"}` (1, `1672531200000000010`).

### L.3.2 Evaluation pipeline (`logql.Eval`)

`select streams by matchers → for each entry in window: line filters in order → | json → label filters (in order) → collect (labels, ns, raw)`. Stages are applied per entry in query order; a `LineFilter` after `| json` still reads the raw line. Complexity is O(entries × stages); with ≤ 100 lines every query is sub-millisecond, which is why the 15 s response cache (storage §S.6) is enough.

### L.3.3 Tail / replay

**Client-side only.** There is no `/loki/api/v1/tail` and no server push. The logs page's live-tail toggle replays the entries of the current `query_range` result in `forward` order with a typewriter cadence (repo §R.7.2 / §R.9.3) and stops at the last entry. The API contract is unchanged by the toggle.

### L.3.4 Linking a log line to its span

| Where | Link |
|-------|------|
| Log line field `span: "gradr.inc-002"` (kept in the line, not a stream label) | frontend renders `/#trace?span=gradr.inc-002` (repo R5) and, on `/trace/*` pages, `/trace/career?span=gradr.inc-002` (L-X8) |
| Jaeger span id for that span | `hex(sha256("gradr.inc-002")[0:8])` = `ef53e50f70cc9d38` — shown in the expanded-JSON view; `/trace/career?span=ef53e50f70cc9d38` is accepted too |
| Reverse link (drawer → logs) | `/logs?q={service="gradr"} | json | span="gradr.inc-002"` |
| Grafana | derived field (§L.2.5) |

---

## L.4 Traces

### L.4.1 Endpoints

| Endpoint | Params | Success | Errors (`{"error":"…"}`) | Cache |
|----------|--------|---------|--------------------------|-------|
| `GET /api/traces/{id}` | `id` = `career` \| `9f3a0703b53d5b0aae2fb3bdacea0ff6` \| any 32-hex OTel trace id | `{"data":[Trace],"total":0,"limit":0,"offset":0,"errors":null}` | 400 `invalid trace id "x": want "career" or 32 hex characters`; 404 `trace not found (self-traces are sampled and kept 24h; the career trace is /api/traces/career)` | career: `public, max-age=15`; OTel ids: `no-store` |
| `GET /api/services` | — | `{"data":["divy","divy-api","edu","ef-polymer","euro-tech","gradr","oss","project","quant"],"total":9,"limit":0,"offset":0,"errors":null}` | — | `max-age=60` |
| `GET /api/services/{service}/operations` | — | `{"data":["gradr.inc-001","gradr.inc-002","gradr.inc-003","gradr.inc-004","gradr.intern","gradr.observability","gradr.product-engineer","gradr.product-features"],"total":8,…}`; for `divy-api`: distinct span names of the last 24 h | 404 `service not found` | `max-age=60` |
| `GET /api/operations?service=x` | `service` | `{"data":[{"name":"gradr.inc-001","spanKind":"internal"},…],…}` (`spanKind` from the `span.kind` tag; career spans are `internal`) | 400 `parameter 'service' is required` | `max-age=60` |
| `GET /api/traces` (search) | `service` (required), `operation`, `tags` (JSON object), `minDuration`, `maxDuration` (Go durations), `limit` (default 20, max 100), `start`, `end` (µs since epoch; default last 1 h for `divy-api`, ignored for career services), `lookback` (ignored) | `{"data":[Trace,…],"total":N,"limit":N,"offset":0,"errors":null}` | 400 `parameter 'service' is required`; 400 `invalid tags: want a JSON object of string values` | `max-age=15` |

Search rules: career services (`divy`, `edu`, …) → the single career trace is returned iff at least one of its spans has that service and (if given) that `operationName`, every `tags` pair matches a tag of that span (string compare of the Jaeger value), and the span's duration is within `[minDuration, maxDuration]`; `divy-api` → `SELECT DISTINCT trace_id FROM otel_spans WHERE service='divy-api' [AND name=?] AND start_unix_nano BETWEEN ? AND ? ORDER BY start_unix_nano DESC LIMIT ?`, then each trace loaded fully and filtered by tags/duration in Go. Services = every `spans.yaml` service owning ≥ 1 span (`community` has none → excluded) plus `divy-api`.

Lookup of an OTel id: `SELECT … FROM otel_spans WHERE trace_id = ? ORDER BY start_unix_nano`; on zero rows the handler calls `TracerProvider.ForceFlush(ctx)` (2 s budget) and retries once — the request that produced the header ended milliseconds ago and may still sit in the batch processor — then 404.

### L.4.2 Career trace → Jaeger JSON

The mapping is fixed in Content §C.3.5 (ids, `processes`, tags `divy.*`, `logs` from events, `references`, µs times) and is not repeated here. Example with three spans (root, `gradr.product-engineer`, `gradr.inc-002`) as served on 2026-09-05T00:00:00Z:

```json
{"data":[{"traceID":"9f3a0703b53d5b0aae2fb3bdacea0ff6","spans":[
 {"traceID":"9f3a0703b53d5b0aae2fb3bdacea0ff6","spanID":"9f3a0703b53d5b0a","operationName":"divy.career","references":[],
  "startTime":1672531200000000,"duration":115862400000000,
  "tags":[{"key":"divy.id","type":"string","value":"divy.career"},{"key":"divy.title","type":"string","value":"Divy — career"},{"key":"divy.start","type":"string","value":"2023"},{"key":"divy.start_precision","type":"string","value":"year"},{"key":"divy.end_precision","type":"string","value":"open"},{"key":"divy.open","type":"bool","value":true},{"key":"role","type":"string","value":"student + part-time product engineer"},{"key":"location","type":"string","value":"Rajasthan, India"},{"key":"divy.depth","type":"int64","value":0},{"key":"divy.todo_count","type":"int64","value":0}],
  "logs":[],"processID":"p-divy","warnings":null},
 {"traceID":"9f3a0703b53d5b0aae2fb3bdacea0ff6","spanID":"da42f4e70b8baf7c","operationName":"gradr.product-engineer",
  "references":[{"refType":"CHILD_OF","traceID":"9f3a0703b53d5b0aae2fb3bdacea0ff6","spanID":"9f3a0703b53d5b0a"}],
  "startTime":1772323200000000,"duration":16070400000000,
  "tags":[{"key":"divy.id","type":"string","value":"gradr.product-engineer"},{"key":"divy.open","type":"bool","value":true},{"key":"divy.start","type":"string","value":"2026-03"},{"key":"divy.start_precision","type":"string","value":"month"},{"key":"divy.end_precision","type":"string","value":"open"},{"key":"role","type":"string","value":"Product Engineer (part-time)"},{"key":"location","type":"string","value":"TODO(divy)"},{"key":"divy.depth","type":"int64","value":1},{"key":"divy.todo_count","type":"int64","value":0}],
  "logs":[{"timestamp":1772323200000000,"fields":[{"key":"event","type":"string","value":"promoted to Product Engineer"},{"key":"from","type":"string","value":"intern"}]}],
  "processID":"p-gradr","warnings":null},
 {"traceID":"9f3a0703b53d5b0aae2fb3bdacea0ff6","spanID":"ef53e50f70cc9d38","operationName":"gradr.inc-002",
  "references":[{"refType":"CHILD_OF","traceID":"9f3a0703b53d5b0aae2fb3bdacea0ff6","spanID":"87e27ab96901a7d8"}],
  "startTime":1772323200000000,"duration":16070400000000,
  "tags":[{"key":"divy.id","type":"string","value":"gradr.inc-002"},{"key":"divy.title","type":"string","value":"INC-002 — cascading memory exhaustion on the proxy host"},{"key":"divy.start","type":"string","value":"TODO(divy)"},{"key":"divy.start_precision","type":"string","value":"todo"},{"key":"divy.end","type":"string","value":"TODO(divy)"},{"key":"divy.end_precision","type":"string","value":"todo"},{"key":"otel.status_code","type":"string","value":"ERROR"},{"key":"error","type":"bool","value":true},{"key":"component","type":"string","value":"dev-proxy"},{"key":"divy.links","type":"string","value":"[{\"kind\":\"postmortem\",\"ref\":\"INC-002\"}]"},{"key":"divy.postmortems","type":"string","value":"INC-002"},{"key":"divy.depth","type":"int64","value":3},{"key":"divy.todo_count","type":"int64","value":0}],
  "logs":[],"processID":"p-gradr","warnings":null}],
 "processes":{"p-divy":{"serviceName":"divy","tags":[{"key":"divy.title","type":"string","value":"Divy"},{"key":"divy.color","type":"string","value":"#73bf69"}]},
              "p-gradr":{"serviceName":"gradr","tags":[{"key":"divy.title","type":"string","value":"Gradr"},{"key":"divy.color","type":"string","value":"#5794f2"},{"key":"divy.counts_as_experience","type":"bool","value":true}]}},
 "warnings":null}],"total":0,"limit":0,"offset":0,"errors":null}
```

(`gradr.inc-002`'s TODO dates fall back to its parent chain: `gradr.observability` (start TODO, open) → `gradr.product-engineer` (`2026-03`, open) — hence the same interval and `*_precision = "todo"`.)

### L.4.3 Self-trace → Jaeger JSON (`otel_spans` rows → `ui.Trace`)

| Row / SDK value | Jaeger field | Rule |
|-----------------|--------------|------|
| `trace_id`, `span_id` | `traceID`, `spanID` (and `spans[].traceID`) | hex as stored |
| `parent_span_id` | `references: [{"refType":"CHILD_OF","traceID","spanID"}]`; NULL → `[]` | `parentSpanID` omitted |
| `name` | `operationName` | |
| `service` | `processID: "p1"`; `processes.p1 = {serviceName: service, tags: resource attrs except service.name}` | one process per distinct service in the trace (`p1`, `p2`, …) |
| `start_unix_nano` | `startTime = start_unix_nano / 1000` (integer division) | µs |
| `end_unix_nano − start_unix_nano` | `duration` | µs, `/ 1000` |
| `attributes` JSON | `tags[]` | JSON string → `string`, bool → `bool`, integral number → `int64`, other number → `float64`, array → JSON text as `string` (exporter already stringified slices) |
| `status_code` 1 / 2 | tags `otel.status_code = "OK"` / `"ERROR"`; `error = true` (bool) when 2; `otel.status_description = status_msg` when non-empty; 0 → none | OTel→Jaeger rule |
| `attributes["span.kind"]` | tag `span.kind` (`server`/`internal`/`client`) | written by the exporter |
| `attributes["otel.scope.name"]`, `["otel.scope.version"]` | tags of the same name | written by the exporter |
| `events` JSON `[{time_unix_nano, name, attributes}]` | `logs[] = {timestamp: time/1000, fields: [{event: name}, …attributes]}` | `event` first, then attributes in stored order |
| — | `flags` omitted, `warnings: null`, `process` omitted | |

Example (request `GET /api/v1/query_range?query=…`, cache miss, one SQLite read):

```json
{"data":[{"traceID":"4bf92f3577b34da6a3ce929d0e0e4736","spans":[
 {"traceID":"4bf92f3577b34da6a3ce929d0e0e4736","spanID":"00f067aa0ba902b7","operationName":"HTTP GET /api/v1/query_range","references":[],
  "startTime":1757052300123456,"duration":8012,
  "tags":[{"key":"http.request.method","type":"string","value":"GET"},{"key":"http.route","type":"string","value":"/api/v1/query_range"},{"key":"url.path","type":"string","value":"/api/v1/query_range"},{"key":"url.query","type":"string","value":"query=sum(increase(github_commits_total[7d]))&start=1757024000&end=1757052000&step=900"},{"key":"url.scheme","type":"string","value":"https"},{"key":"server.address","type":"string","value":"divy.dev"},{"key":"network.protocol.version","type":"string","value":"1.1"},{"key":"http.response.status_code","type":"int64","value":200},{"key":"http.response.body.size","type":"int64","value":1834},{"key":"divy.cache","type":"string","value":"MISS"},{"key":"span.kind","type":"string","value":"server"},{"key":"otel.scope.name","type":"string","value":"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"},{"key":"otel.scope.version","type":"string","value":"0.71.0"}],
  "logs":[],"processID":"p1","warnings":null},
 {"traceID":"4bf92f3577b34da6a3ce929d0e0e4736","spanID":"53ce929d0e0e4736","operationName":"promql.eval",
  "references":[{"refType":"CHILD_OF","traceID":"4bf92f3577b34da6a3ce929d0e0e4736","spanID":"00f067aa0ba902b7"}],
  "startTime":1757052300124100,"duration":6900,
  "tags":[{"key":"divy.query","type":"string","value":"sum(increase(github_commits_total[7d]))"},{"key":"divy.steps","type":"int64","value":32},{"key":"divy.series","type":"int64","value":1},{"key":"span.kind","type":"string","value":"internal"},{"key":"otel.scope.name","type":"string","value":"divy.dev/api/internal/promql"}],
  "logs":[],"processID":"p1","warnings":null},
 {"traceID":"4bf92f3577b34da6a3ce929d0e0e4736","spanID":"b7ad6b7169203331","operationName":"sqlite.select",
  "references":[{"refType":"CHILD_OF","traceID":"4bf92f3577b34da6a3ce929d0e0e4736","spanID":"53ce929d0e0e4736"}],
  "startTime":1757052300124300,"duration":410,
  "tags":[{"key":"db.system.name","type":"string","value":"sqlite"},{"key":"db.operation.name","type":"string","value":"select"},{"key":"divy.table","type":"string","value":"samples"},{"key":"divy.rows","type":"int64","value":368},{"key":"span.kind","type":"string","value":"client"},{"key":"otel.scope.name","type":"string","value":"divy.dev/api/internal/store"}],
  "logs":[],"processID":"p1","warnings":null}],
 "processes":{"p1":{"serviceName":"divy-api","tags":[{"key":"service.version","type":"string","value":"v0.1.0"},{"key":"telemetry.sdk.language","type":"string","value":"go"},{"key":"telemetry.sdk.name","type":"string","value":"opentelemetry"},{"key":"telemetry.sdk.version","type":"string","value":"1.46.0"}]}},
 "warnings":null}],"total":0,"limit":0,"offset":0,"errors":null}
```

### L.4.4 Grafana Jaeger data source flow (verified against the standalone plugin, Grafana ≥ 12.3)

| Action | Request | Outcome |
|--------|---------|---------|
| Save & test | `GET /api/services` → `data: []string` | "Data source is working" |
| Search form | `GET /api/traces?service=gradr&operation=…&tags={"component":"dev-proxy"}&minDuration=…&maxDuration=…&limit=20&start=<µs>&end=<µs>` | list of traces; operations from `GET /api/services/gradr/operations` |
| Trace ID box | `GET /api/traces/<id>` → `data[0]` | works for `career` and any `X-Divy-Trace-Id` |

---

## L.5 OTel self-tracing

### L.5.1 Provider setup (`api/internal/trace/otel.go`, called by `divy serve` before the router)

```go
res, _ := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL,
    semconv.ServiceName(cfg.OTelServiceName),   // OTEL_SERVICE_NAME, default divy-api
    semconv.ServiceVersion(version.Version)))
exp := NewSQLiteExporter(st)                                           // §L.5.3
tp := sdktrace.NewTracerProvider(
    sdktrace.WithResource(res),
    sdktrace.WithSampler(sdktrace.ParentBased(NewRateLimitedSampler(cfg.OTelSampleRPS, cfg.OTelSampleBurst))), // §L.5.2
    sdktrace.WithBatcher(exp,
        sdktrace.WithBatchTimeout(1*time.Second),      // a trace id pasted 1 s after the response resolves
        sdktrace.WithMaxQueueSize(2048),               // bounded; overflow drops (SDK default, non-blocking)
        sdktrace.WithMaxExportBatchSize(256),          // one SQLite transaction per batch
        sdktrace.WithExportTimeout(5*time.Second)))
otel.SetTracerProvider(tp)
// no global propagator: inbound traceparent is never joined (public endpoint), outbound requests carry none
```

Pinned: `go.opentelemetry.io/otel`, `/sdk`, `/trace` v1.46.0; `semconv` = `go.opentelemetry.io/otel/semconv/v1.43.0` (the version otelhttp v0.71.0 emits); `otelhttp` v0.71.0. Shutdown: `tp.ForceFlush(5 s)` then `tp.Shutdown` before the store closes.

### L.5.2 Sampling

| Item | Decision |
|------|----------|
| Root decision | `RateLimitedSampler{lim: rate.NewLimiter(rate.Limit(OTEL_SAMPLE_RPS=100), OTEL_SAMPLE_BURST=200)}`: `ShouldSample` → `RecordAndSample` if `lim.Allow()`, else `Drop`. `Description()` = `divy-ratelimit{100/s,burst=200}`. Increments `divy_otel_spans_total{decision="sampled"\|"dropped"}` |
| Children | `ParentBased`: inherit the root's decision (sqlite/promql child spans exist iff the request span does) |
| Why not `TraceIDRatioBased` | every visitor must be able to paste their own header and find it; the per-IP limiter (20 r/s, burst 100) is below the sampler limit, so a single client can never exhaust it — only a >5-client flood drops spans |
| What is traced | every HTTP request (L-X9), every collector run (root span `collector.<name>`), retention runs |
| What is never recorded | client IP, `X-Forwarded-For`, user agent, cookies, request bodies, `Authorization` (§L.5.4) |
| Dropped spans | header still carries the generated trace id (the SDK creates a non-recording span with valid ids); `X-Divy-Trace-Sampled: 0`; lookup → 404 |

### L.5.3 Exporter (`api/internal/trace/exporter.go`)

```go
type SQLiteExporter struct{ st *store.Store; scrub map[attribute.Key]struct{} }
func (e *SQLiteExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error // one store.Write job per batch
func (e *SQLiteExporter) Shutdown(ctx context.Context) error                                  // no-op (store owns the DB)
```

| Rule | Detail |
|------|--------|
| Queue | the `BatchSpanProcessor` is the bounded async queue (2048 spans); when full the SDK drops new spans (non-blocking mode); the exporter itself is synchronous inside the processor goroutine |
| Write | `st.Write(ctx, func(tx) { tx.InsertSpans(rows) })` — `INSERT OR IGNORE INTO otel_spans …` (`UNIQUE(trace_id, span_id)`), one transaction per batch; `ctx` carries the 5 s export timeout |
| On error / timeout | the batch is dropped, `divy_otel_export_errors_total++`, one warning log line per minute at most; the HTTP path is never affected |
| Success | `divy_otel_exported_spans_total += len(rows)` |
| Row mapping | `trace_id`/`span_id`/`parent_span_id` = `SpanContext().TraceID().String()` etc. (`parent` = NULL when `Parent().SpanID()` is zero or `Parent()` is remote); `name`; `service` = resource `service.name`; `start_unix_nano`/`end_unix_nano` = `StartTime().UnixNano()`/`EndTime().UnixNano()`; `attributes` = JSON object of `Attributes()` **after scrubbing** plus `span.kind` (= `SpanKind().String()`), `otel.scope.name`, `otel.scope.version`, and `otel.dropped_attributes_count` when > 0; `events` = `[{"time_unix_nano":…, "name":…, "attributes":{…}}]`; `status_code` = OTLP number (L-X10); `status_msg` = `Status().Description` or NULL |
| Attribute value encoding | `BOOL` → JSON bool, `INT64` → JSON integer, `FLOAT64` → JSON number, `STRING` → string, slices → the JSON text of the slice as a **string** (Jaeger array rule) |
| Scrub list (never stored, whatever the SDK or otelhttp set) | `client.address`, `http.client_ip`, `network.peer.address`, `network.peer.port`, `user_agent.original`, `url.full`, `http.request.header.*`, `http.response.header.*` |
| Size guard | a stored attribute string is truncated to 1024 bytes (`…` suffix); `url.query` to 512 |

### L.5.4 HTTP middleware (`api/internal/server/middleware/tracing.go`)

Chain (storage §S.7 order): `recoverer → otelhttp.NewMiddleware("HTTP", …) → traceHeader → clientIP → rateLimit → cache → cors → handler`.

```go
otelhttp.NewMiddleware("HTTP",
    otelhttp.WithTracerProvider(tp),
    otelhttp.WithPublicEndpointFn(func(*http.Request) bool { return true }), // never join a visitor's trace
    otelhttp.WithSpanNameFormatter(func(op string, r *http.Request) string { return op + " " + r.Method }))

func traceHeader(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        span := trace.SpanFromContext(r.Context())
        sc := span.SpanContext()
        if sc.HasTraceID() {
            w.Header().Set("X-Divy-Trace-Id", sc.TraceID().String())          // set BEFORE any write → present on 404/429/500/static
            w.Header().Set("X-Divy-Trace-Sampled", map[bool]string{true: "1", false: "0"}[sc.IsSampled()])
        }
        next.ServeHTTP(w, r)
        if p := chi.RouteContext(r.Context()).RoutePattern(); p != "" {      // valid only after next (chi doc)
            span.SetName("HTTP " + r.Method + " " + p)
            span.SetAttributes(semconv.HTTPRoute(p))
        }
        if q := r.URL.RawQuery; q != "" && (strings.HasPrefix(r.URL.Path, "/api/v1/") || strings.HasPrefix(r.URL.Path, "/loki/")) {
            span.SetAttributes(attribute.String("url.query", truncate(q, 512)))
        }
    })
}
```

| Item | Decision |
|------|----------|
| Span name | `HTTP <METHOD> <chi route pattern>`: `HTTP GET /api/v1/query_range`, `HTTP GET /loki/api/v1/query_range`, `HTTP GET /api/traces/{id}`, `HTTP GET /` , `HTTP GET /*` (embedded static handler), `HTTP GET /metrics`; unrouted (chi NotFound) keeps `HTTP GET` with no `http.route` |
| Attributes kept (from otelhttp v0.71 / semconv v1.43) | `http.request.method`, `url.scheme`, `url.path`, `server.address`, `server.port`, `network.protocol.name`, `network.protocol.version`, `http.route`, `http.response.status_code`, `http.request.body.size`, `http.response.body.size`; ours: `url.query` (API routes only), `divy.cache` (`HIT`/`MISS`/`BYPASS`, set by the cache middleware), `divy.ratelimited` (`true` on 429) |
| Status | otelhttp sets Error for 5xx; we additionally `SetStatus(codes.Error, "panic")` in the recoverer |
| `divy ping` / uptime self-probe | traced like any request (`HTTP GET /readyz`) |
| Response header on errors | present on every response the router produces, including the recoverer's 500 (headers are set before the panic can happen in a handler) and 429 (the limiter runs after `traceHeader`) |

### L.5.5 Child spans

| Span | Kind | Where | Attributes |
|------|------|-------|------------|
| `sqlite.select` | client | `store.Select`, heartbeat/incident queries, `otel_spans` reads | `db.system.name="sqlite"`, `db.operation.name="select"`, `divy.table`, `divy.rows` |
| `sqlite.write` | client | inside the writer goroutine per job (parent = the job submitter's span, carried on the job) | `db.operation.name="write"`, `divy.rows` |
| `promql.eval` / `logql.eval` | internal | engines | `divy.query` (≤ 512 B), `divy.steps`, `divy.series`, `divy.samples` / `divy.entries` |
| `render.ascii`, `render.og` | internal | renderers | `divy.width` / `divy.postmortem` |
| `collector.github` (etc.) | internal, **root** | scheduler wraps `Run` | `divy.collector`, `divy.items`, `divy.ok`, `divy.result`; GitHub adds `divy.gh_cost`, `divy.gh_remaining` |
| `outbound GET api.github.com` / `pypistats.org` / `probe <target id>` | client | collectors | `server.address`, `http.request.method`, `http.response.status_code`; **no `url.full`** (probe URLs are public but the rule is uniform) |

### L.5.6 Retrieval, retention, ops

| Item | Value |
|------|-------|
| Retrieval | `GET /api/traces/<X-Divy-Trace-Id>` (JSON) or paste into `/trace/<id>` (UI). `X-Divy-Trace-Sampled: 0` explains a 404 |
| Retention | storage §S.1.7: 24 h **and** newest 20,000 spans; the trace page shows "self-traces expire after 24 h" |
| Env | `OTEL_SERVICE_NAME=divy-api`, `OTEL_SAMPLE_RPS=100`, `OTEL_SAMPLE_BURST=200` (L-X6) |
| Metrics | `divy_otel_spans_total{decision}`, `divy_otel_exported_spans_total`, `divy_otel_export_errors_total` |
| Verify | `curl -sI https://divy.dev/healthz \| grep -i x-divy-trace-id` then `curl -s https://divy.dev/api/traces/<id> \| jq '.data[0].spans[].operationName'` |

---

## L.6 Easter-egg and auxiliary endpoints

### L.6.1 `/` content negotiation and `/ascii`

| Rule | Detail |
|------|--------|
| `?format=ascii` on `/` | text, regardless of `Accept` |
| `GET /ascii` | text, always; `?width=` 60–200 (default 80) |
| `Accept` parsing | media ranges split on `,`; each `type/subtype;q=…` (`q` default 1, invalid → 1, `q=0` = not acceptable); `q_plain` = q of the most specific range matching `text/plain` (`text/plain` > `text/*` > `*/*`), `q_html` likewise for `text/html` |
| Decision | text iff `q_plain > 0` and (`q_html == 0` or `q_plain > q_html`); ties → HTML. `curl -H 'Accept: text/plain'` → text; browsers (`text/html,…,*/*;q=0.8`) → HTML; bare `curl` (`*/*`) → HTML; `Accept: text/*` → HTML |
| Headers (text) | `Content-Type: text/plain; charset=utf-8`, `Vary: Accept`, `Cache-Control: public, max-age=60`, `X-Divy-Trace-Id` |
| `HEAD` | same headers, no body |
| CLI | `divy export-ascii --width 80` uses the same renderer (`api/internal/ascii`) |

### L.6.2 ASCII waterfall rendering spec

Layout for width `W` (default 80): columns `service` (10) · space · `span` (30) · space · bar (`W − 42` = 38). Rows are right-trimmed. All lines ≤ `W` columns (runes, not bytes).

| Element | Rule |
|---------|------|
| Time axis | `t0` = root span's resolved start; `t1` = `max(now, latest planned end of any open span)`; `col(t) = floor((t − t0) / (t1 − t0) × barCols)`, clamped to `[0, barCols−1]` |
| Header row | year labels `YYYY` printed at `col(Jan 1)` for every year in `[t0, t1)` whose label fits before the next one |
| Order | DFS, children sorted by (resolved start, id) (Content §C.3.6) |
| Name column | `id` prefixed by `"  " × (depth−1) + "└ "` for depth ≥ 1; `status: error` → suffix ` [ERR]`; longer than 30 → cut to 29 + `…` |
| Bar glyphs | `▓` dated interval, `░` where a start/end came from a `TODO(divy)` fallback (whole bar when either side is TODO), `┄` from `col(now)` to the right edge (`open: true`, planned end or not), space elsewhere; minimum bar width 1 column |
| Header lines | `divy.career · <trace id> · <t0 date> → now · <N> spans`, then `rendered <RFC3339> · 1 col ≈ <months> mo · ▓ dated  ░ TODO(divy)  ┄ open`, then a rule of `─` × W |
| Footer | rule, then `JSON: curl -s https://divy.dev/api/traces/career | jq .data[0].spans` and `logs: /loki/api/v1/query_range?query={service="gradr"}   metrics: /metrics` (origin from `DIVY_PUBLIC_ORIGIN`) |
| Colour | none by default (`curl` friendly); `--color` in the CLI wraps service names in ANSI 24-bit using the service color |

Example (5 dated spans + 2 TODO-derived, rendered 2026-09-05; `t0` = 2023-01-01, `t1` = 2027-01-01, 38 bar columns ⇒ 1 col ≈ 1.26 months; `col(now)` = 34):

```
divy.career · 9f3a0703b53d5b0aae2fb3bdacea0ff6 · 2023-01-01 → now · 28 spans
rendered 2026-09-05T00:00:00Z · 1 col ≈ 1.3 mo · ▓ dated  ░ TODO(divy)  ┄ open
────────────────────────────────────────────────────────────────────────────────
service    span                           2023     2024      2025     2026
divy       divy.career                    ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓┄┄┄┄
edu        └ edu.btech-ece                ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓┄┄┄┄
ef-polymer └ ef-polymer.swe-intern                    ▓▓▓▓▓▓▓▓▓▓▓▓
euro-tech  └ euro-tech.go-iam-intern                              ▓▓▓
gradr      └ gradr.product-engineer                                     ▓▓▓▓┄┄┄┄
gradr        └ gradr.observability                                      ░░░░┄┄┄┄
gradr          └ gradr.inc-002 [ERR]                                    ░░░░
────────────────────────────────────────────────────────────────────────────────
JSON: curl -s https://divy.dev/api/traces/career | jq .data[0].spans
logs: /loki/api/v1/query_range?query={service="gradr"}   metrics: /metrics
```

Column arithmetic for the example: `ef-polymer.swe-intern` 2024-05 → 2025-08-01 = months 16 → 31 ⇒ cols 12 … 23; `euro-tech.go-iam-intern` 2025-08 → 2025-12-01 = 31 → 35 ⇒ cols 24 … 26; `gradr.product-engineer` 2026-03 (month 38) ⇒ col 30 … 33 solid, 34 … 37 `┄`. The full render lists all 28 spans; the golden test `testdata/ascii.golden` freezes `now`.

### L.6.3 `/favicon.svg` and `/favicon.ico`

| Item | Rule |
|------|------|
| Data | last 7 UTC days ending today from `github_commits_total`: `count(d) = grid(dayEnd(d)) − grid(dayEnd(d−1))` for complete days, `count(today) = live − grid(dayEnd(yesterday))` (storage §S.2.3 layout); a missing grid point counts as 0 |
| No data | no `github_commits_total` series at all (collector disabled / never run) → flat gray line + comment |
| Geometry | `viewBox="0 0 32 32"`; `x_i = 3 + i × 26/6`; `y_i = 27 − (v_i / max(1, max v)) × 20`, one decimal |
| Headers | `Content-Type: image/svg+xml`, `Cache-Control: public, max-age=3600`, `ETag` (sha256 of the body), `X-Divy-Trace-Id`; response cache 3600 s keyed on the 7 counts |
| `/favicon.ico` | static `web/static/favicon.ico` (32×32, a fixed sparkline glyph, committed) served by the static handler with `max-age=86400` — for clients that ignore `<link rel="icon" type="image/svg+xml">` (L-X7); it is not live and says so in `web/README` |
| `app.html` | `<link rel="icon" href="/favicon.svg" type="image/svg+xml"><link rel="alternate icon" href="/favicon.ico">` |

Exact body (counts `3 0 5 2 7 1 4`):

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" width="32" height="32">
<!-- github commits per UTC day, 2026-08-30..2026-09-05: 3 0 5 2 7 1 4 -->
<rect width="32" height="32" rx="6" fill="#0b0c0e"/>
<polyline fill="none" stroke="#73bf69" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" points="3,18.4 7.3,27 11.7,12.7 16,21.3 20.3,7 24.7,24.1 29,15.6"/>
<circle cx="29" cy="15.6" r="2" fill="#73bf69"/>
</svg>
```

No-data body:

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" width="32" height="32">
<!-- no commit data: GitHub collector disabled or no samples yet -->
<rect width="32" height="32" rx="6" fill="#0b0c0e"/>
<line x1="3" y1="27" x2="29" y2="27" stroke="#8e8e8e" stroke-width="2.5" stroke-linecap="round"/>
</svg>
```

### L.6.4 `/robots.txt` (exact body; origin from `DIVY_PUBLIC_ORIGIN`)

```
# Observability for humans: /metrics
# Also: /healthz  /readyz  /api/traces/career  /loki/api/v1/labels
# Try: curl -H 'Accept: text/plain' https://divy.dev/
User-agent: *
Allow: /
Disallow: /api/
Disallow: /loki/
Sitemap: https://divy.dev/sitemap.xml
```

`Content-Type: text/plain; charset=utf-8`, `Cache-Control: public, max-age=3600`.

### L.6.5 `/healthz` (liveness)

Exact body (Go struct field order; values from `profile.yaml`, Content §C.9.2):

```json
{"status":"ok","open_to":["backend intern","infra","sre"],"tz":"Asia/Kolkata"}
```

Always 200 while the process serves; `Content-Type: application/json`, `Cache-Control: no-store`. Exempt from the per-IP bucket (storage §S.7).

### L.6.6 `/readyz` (readiness)

```json
{"status":"ok","version":"v0.1.0","commit":"3f2a9c1","uptime_s":86412,
 "checks":{
  "db":{"ok":true,"latency_ms":0.31},
  "content":{"ok":true,"files":9,"spans":28,"log_lines":87,"todos":61,"loaded_at":"2026-09-04T10:00:03Z"},
  "collectors":{
   "github":{"ok":true,"last_success":"2026-09-05T09:45:02Z","age_s":896,"stale_after_s":2700},
   "pypi":{"ok":true,"last_success":"2026-09-05T09:02:11Z","age_s":3467,"stale_after_s":10800},
   "uptime":{"ok":true,"last_success":"2026-09-05T09:55:00Z","age_s":298,"stale_after_s":900},
   "manual":{"ok":true,"last_success":"2026-09-05T09:50:00Z","age_s":598,"stale_after_s":2700},
   "retention":{"ok":true,"last_success":"2026-09-05T09:00:40Z","age_s":3558,"stale_after_s":10800}}}}
```

| Field | Rule |
|-------|------|
| `db` | `SELECT 1` on the reader pool with a 1 s timeout; `ok=false` (+ `"error"`) on failure |
| `content` | counts from the loaded content model (always `ok: true` — invalid content never starts) |
| `collectors.<name>` | `last_success` = `max(finished_ms) WHERE ok=1` (storage §S.3 seed), `age_s` = now − that, `stale_after_s` = `max(3 × interval, 15m)` (storage §S.2.6), `ok = age_s ≤ stale_after_s`; never succeeded → `{"ok":null,"last_success":null,"age_s":null,"stale_after_s":…}`; disabled → `{"ok":null,"disabled":true}` |
| Status code | **503** with `"status":"unavailable"` when `db.ok` is false; **503** `"status":"shutting_down"` from the moment SIGTERM is received until exit (so the uptime probe and Caddy see a clean drain); collectors never affect the status code (a dead token must not take the site down) |
| Headers | `Cache-Control: no-store`; exempt from the per-IP bucket |
| Consumers | Docker `HEALTHCHECK` via `divy ping`, compose `depends_on`, the `self-api` uptime target, `deploy.sh`, `build-with-api.mjs` |

### L.6.7 `/og/postmortems/{id}.png` and `/og/default.png`

| Item | Rule |
|------|------|
| Size | 1200 × 630, PNG, `gg` + embedded Inter Bold / Inter Regular / JetBrains Mono Regular TTFs (repo §R.6.6) |
| Inputs | Content §C.5.4: `id`, `title`, `severity`, `date`, `duration`, `services[]`, `summary`, plus service colors from `spans.yaml` |
| Layout | background `#0b0c0e`; top-left `divy.dev / postmortems` (JetBrains Mono 28 px, `#8e8e8e`); top-right severity badge (rounded rect, 40 px mono, fill SEV1 `#f2495c` · SEV2 `#ff9830` · SEV3 `#f2cc0c` · SEV4 `#5794f2`, text `#0b0c0e`); title Inter Bold 56 px `#ffffff`, wrapped to 1080 px, ≤ 2 lines (ellipsis); summary Inter Regular 30 px `#c7d0d9`, ≤ 3 lines; footer mono 26 px: `INC-002 · <date or TODO(divy)> · <duration> · <services>` with a 14 px colored dot per service; 6 px bottom bar in the severity color |
| `/og/default.png` | `divy.dev` (Inter Bold 72 px), `a career, traced` (Inter 36 px), a 10-segment strip of the service colors |
| Cache | rendered lazily on first request; in-memory keyed by `(id, content hash)`; `Cache-Control: public, max-age=86400`, `ETag`, `Content-Type: image/png`; `HEAD` supported |
| 404 | id not matching `^INC-[0-9]{3}$` or not in content → 404 `{"error":"postmortem not found"}` |
| Consumers | `<meta property="og:image">`, `og:image:width=1200`, `og:image:height=630` on each postmortem page (repo §R.6.6) |

### L.6.8 `/api/content/*` and `/api/uptime`

All: JSON, `Cache-Control: public, max-age=60`, `ETag`, 404 `{"error":"…"}`; bodies are the validated content documents converted YAML → JSON (same key names) plus the computed fields listed.

| Endpoint | Body |
|----------|------|
| `GET /api/content/services` | `{"services":[{"id":"divy","title":"Divy","color":"#73bf69","counts_as_experience":false},…]}` in file order |
| `GET /api/content/spans` | `spans.yaml` verbatim as JSON: `{"version":1,"services":[…],"trace":{…children…}}` (raw date strings; resolution happens only in `/api/traces/career`) |
| `GET /api/content/logs` | the NDJSON file verbatim, `Content-Type: application/x-ndjson` |
| `GET /api/content/postmortems`, `/{id}`, `/{id}.md` | Content §C.5.4 |
| `GET /api/content/panels` | `panels.yaml` verbatim: `{"version":1,"dashboard":{…},"panels":[…]}` |
| `GET /api/content/alerts` | `alerts.yaml` verbatim `{"groups":[…]}` (Prometheus shape is `/api/v1/rules`, Content §C.7) |
| `GET /api/content/uptime` | `{"targets":[{"id":"github-profile","name":"GitHub profile","url":"https://github.com/divysinghvi","method":"HEAD","expected_status":[200],"timeout":"10s","interval":"5m","follow_redirects":true,"span":null,"configured":true},{"id":"savely-landing",…,"url":"TODO(divy)","configured":false}]}` (`self-api` shows the effective `UPTIME_SELF_URL`) |
| `GET /api/content/profile` | `profile.yaml` as JSON with computed pod fields: `"pods":[{"name":"gradr-observability","ready":"1/1","status":"Running","restarts_from":"postmortems","span":"gradr.observability","restarts":4,"age_s":…,"note":null},…]` (`restarts` = postmortems under the span, `age_s` = now − resolved span start; Content §C.3.7) |
| `GET /api/content/todos` | Content §C.10.3 |
| `GET /api/uptime` | alias of storage §S.4.3 `GET /api/uptime/heartbeats?days=90&bucket=1d` — same body (`targets[]` with `status`, `last`, `uptime{24h,7d,30d,90d}`, `buckets[]`, `incidents[]`) |

---

## L.7 Table-driven tests

### L.7.1 LogQL parser (`logql/parser_test.go`) — input → AST or error

| # | Input | Expected |
|---|-------|----------|
| 1 | `{service="gradr"}` | `LogQuery{Selector:[{service = gradr}]}` |
| 2 | `{service="gradr", level!="debug"}` | 2 matchers |
| 3 | `{service=~"gradr\|euro-tech"}` | matcher `=~`, regex `^(?:gradr\|euro-tech)$` |
| 4 | `{service!~"oss.*", level="info"}` | ok (level matcher satisfies the non-empty rule) |
| 5 | `{}` | error: `queries require at least one regexp or equality matcher…` |
| 6 | `{service=~".*"}` | same error |
| 7 | `{level!="debug"}` | same error |
| 8 | `{level!=""}` | ok |
| 9 | `{service="gradr"} \|= "promoted"` | `LineFilter{\|=, "promoted"}` |
| 10 | `{service="gradr"} != "intern" \|~ "Product (Engineer\|Manager)"` | two line filters in order |
| 11 | `{service="gradr"} \|~ "["` | error: `error parsing regexp: missing closing ]` |
| 12 | `` {service="gradr"} \|= `raw\d` `` | `LineFilter{\|=, "raw\\d"}` (raw string, no unescape) |
| 13 | `{service="gradr"} \|= 'x'` | error: `syntax error: unexpected '` |
| 14 | `{service="gradr"} \| json` | `Stages:[JSONParser]` |
| 15 | `{service="gradr"} \| json \| from="intern"` | `LabelFilter{LFString{from = intern}}` |
| 16 | `{service="gradr"} \| json \| containers > 60` | `LFNumber{containers > 60}` |
| 17 | `{service="gradr"} \| json \| containers >= 65 and resolved="true"` | `LFAnd{LFNumber, LFString}` |
| 18 | `{service="gradr"} \| json \| (incident="INC-001" or incident="INC-002"), resolved="true"` | `LFAnd{LFOr{…}, LFString}` |
| 19 | `{service="ef-polymer"} \| json \| months_with_team == 12` | `LFNumber{== 12}` |
| 20 | `{service="gradr"} \| json \| duration > 5m` | `LFNumber{> 300, IsDuration:true}` |
| 21 | `{service="gradr"} \| json \| __error__=""` | `LFString{__error__ = ""}` |
| 22 | `{service="gradr"} \| level="warn"` | label filter without parser: ok |
| 23 | `{service="gradr"} \| json \| containers > "x"` | error: `numeric comparison needs a number or duration, got string` |
| 24 | `{service="gradr"} \| logfmt` | error: `unsupported parser "logfmt" (supported: json)` |
| 25 | `{service="gradr"} \| pattern "<_> msg"` | error: `unsupported parser "pattern" …` |
| 26 | `{service="gradr"} \| line_format "{{.msg}}"` | error: `unsupported stage "line_format"` |
| 27 | `{service="gradr"} \| json \| label_format x=y` | error: `unsupported stage "label_format"` |
| 28 | `{service="gradr"} \| unwrap containers` | error: `unsupported stage "unwrap"` |
| 29 | `{service="gradr"} \| drop level` | error: `unsupported stage "drop"` |
| 30 | `{service="gradr"} json` | error: `syntax error: unexpected IDENTIFIER "json", expecting …` |
| 31 | `count_over_time({service="gradr"}[1h])` | `MetricQuery{Range:{count_over_time, 1h}}` |
| 32 | `rate({service="gradr"} \|= "incident" [7d])` | `Range:{rate, 168h, Log with 1 line filter}` |
| 33 | `count_over_time({service="gradr"}[1w])` | range 168h |
| 34 | `sum by (level) (count_over_time({service=~".+"}[30d]))` | `Agg:{sum, Labels:[level]}` |
| 35 | `sum(count_over_time({service="gradr"}[1d])) by (level, detected_level)` | trailing grouping accepted; `Labels:[level, detected_level]` |
| 36 | `avg without (component) (rate({level="warn"}[1d]))` | `Agg:{avg, Without:true, Labels:[component]}` |
| 37 | `sum by (a) (count_over_time({level="warn"}[1d])) by (b)` | error: `duplicate grouping` |
| 38 | `count_over_time({service="gradr"}[5m] offset 1h)` | error: `offset modifier is not supported` |
| 39 | `bytes_rate({service="gradr"}[5m])` | error: `unsupported function "bytes_rate" …` |
| 40 | `topk(3, count_over_time({service="gradr"}[1d]))` | error: `unsupported aggregation "topk" …` |
| 41 | `sum(count_over_time({service="gradr"}[1d])) / 2` | error: `binary operators are only supported between vector() literals` |
| 42 | `vector(1)+vector(1)` | `ScalarQuery{2}` |
| 43 | `(vector(2) - vector(0.5)) * vector(4) / vector(2)` | `ScalarQuery{3}` |
| 44 | `{service="gradr"` | error: `syntax error: unexpected $end, expecting }` at col 17 |
| 45 | `{service="gradr}` | error: `literal not terminated` |
| 46 | `{service="gradr"} \| json \| by="x"` | error: `reserved word "by" cannot be a label name` |

### L.7.2 Evaluator (`logql/eval_test.go`) — fixture = the 11 lines of Content §C.4.3 (`L1…L11` in file order; resolved ns as in §L.3.1), range = all unless stated

| # | Query / params | Expected |
|---|----------------|----------|
| 1 | `{service="gradr"}` backward, limit 100 | 4 entries in 3 streams; stream order `{component="dev-proxy",…}`, `{component="secrets-sidecar",…}`, `{level="info",service="gradr"}`; inside the last: L7 then L6 |
| 2 | `{service="gradr"}` backward, limit 2 | L9 (`…008`), L8 (`…007`) — one stream each |
| 3 | `{service="gradr"}` forward, limit 2 | L6, L7 |
| 4 | `{service="gradr"} \|= "promoted"` | L7 only |
| 5 | `{service="gradr"} != "promoted" \| json \| resolved="true"` | L8, L9 |
| 6 | `{service="gradr"} \| json \| containers > 60` | L9 only (L8 lacks `containers` → dropped, no error label) |
| 7 | `{service=~"euro-tech\|ef-polymer"} \|~ "(?i)shipped\|deployed"` | L5, L3 (backward) |
| 8 | `{service="edu"} \| json` | one entry with labels `{expected_graduation="2027", level="info", msg="enrolled: …", precision="year", service="edu", span="edu.btech-ece", ts="2023-01-01T00:00:00Z"}` — no `*_extracted` keys |
| 9 | `{service="ef-polymer"} \| json \| months_with_team == 12` | L3 |
| 10 | `{service="gradr"} \| json \| containers > 60 or from="intern"` | L9, L7 |
| 11 | `{level="debug"}` `start=2023-01-01T00:00:00Z` `end=2023-01-01T00:00:01Z` | L10, L11 (TODO fallbacks land on the root start) |
| 12 | `{service="gradr"} \| json \| incident > 1` | L8, L9 kept with `__error__="LabelFilterErr"`, `__error_details__` starting `strconv.ParseFloat`; L6, L7 dropped |
| 13 | synthetic stream `{level="info",service="test"}` line `not json`: `{service="test"} \| json` | entry kept with `__error__="JSONParserErr"`, `__error_details__` non-empty; `… \| json \| __error__=""` → empty result |
| 14 | synthetic line `{"a":{"b":1},"c":[1,2],"d":null,"e":true,"f":1.50,"weird key":"x","service":"other"}` in stream `{level="info",service="test"}`: `\| json` | labels `a_b="1"`, `e="true"`, `f="1.50"`, `weird_key="x"`, `service_extracted="other"`; no `c`, no `d` |
| 15 | `count_over_time({service="gradr"}[30d])` `start=2026-03-01T00:00:00Z` `end=2026-03-31T00:00:00Z` `step=2592000` | 3 series, each with the single point `[1774915200,"1"]` (no point at the first step) |
| 16 | `sum by (level) (count_over_time({service="gradr"}[30d]))` same range | `{level="info"}: [[1774915200,"1"]]`, `{level="warn"}: [[1774915200,"2"]]` |
| 17 | `rate({service="gradr"}[30d])` same range | value `"0.0000003858024691358025"` per stream at the second step |
| 18 | `/query` `sum(count_over_time({service=~".+"}[1y]))` `time=2026-09-05T00:00:00Z` | vector `{"metric":{},"value":[1757030400,"5"]}` (L5, L6, L7, L8, L9 in `(2025-09-05, 2026-09-05]`) |
| 19 | `/query` `vector(1)+vector(1)` | `[[t,"2"]]` with `metric: {}` |
| 20 | `sum by (level, detected_level) (count_over_time({service="gradr"}[1y]))` at `time=2026-09-05` | `{level="info"}` = 2, `{level="warn"}` = 2 (unknown label ignored) |
| 21 | `count_over_time({service="gradr"} \| json [1y])` at `time=2026-09-05` | 4 series (one per distinct extracted set), each `"1"` |
| 22 | labels/values: `Labels(all)` → `[component level service]`; `LabelValues("service", {level="warn"})` → `[gradr]`; `Series({service="gradr"})` → 3 label sets | |

### L.7.3 Trace JSON conversion (`trace/jaeger_test.go`) — SDK span (via `tracetest.SpanStub`) → row → Jaeger

| # | Input | Expected |
|---|-------|----------|
| 1 | server span, attrs `http.request.method="GET"`, `http.response.status_code=200` (int64), status Unset | tags typed `string` / `int64`; `span.kind="server"`; no `otel.status_code`, no `error` tag; row `status_code=0` |
| 2 | status `codes.Error`, description `"panic"` | row `status_code=2`, `status_msg="panic"`; tags `otel.status_code="ERROR"`, `error=true` (bool), `otel.status_description="panic"` |
| 3 | status `codes.Ok` | row `status_code=1`; tag `otel.status_code="OK"`; no `error` tag |
| 4 | child with parent span id `00f067aa0ba902b7` | `references=[{CHILD_OF, traceID, "00f067aa0ba902b7"}]`; root → `references=[]`, row `parent_span_id=NULL` |
| 5 | event `cache` at `t+1.2ms` with attr `key="abc"` | `logs=[{timestamp: µs(t+1.2ms), fields:[{event,string,"cache"},{key,string,"abc"}]}]` |
| 6 | attr `stack=[]string{"go","sqlite"}` | tag `{"key":"stack","type":"string","value":"[\"go\",\"sqlite\"]"}` |
| 7 | attrs `client.address`, `user_agent.original`, `network.peer.address`, `network.peer.port`, `http.client_ip` set by otelhttp | absent from the row's `attributes` JSON and from `tags` |
| 8 | attr `ratio=0.25` (float64), `hit=true` (bool) | types `float64`, `bool` |
| 9 | start `1757052300123456789` ns, end `+8012345` ns | `startTime=1757052300123456`, `duration=8012` (truncating division) |
| 10 | resource `service.name="divy-api"`, `service.version="v0.1.0"` | `processID="p1"`, `processes.p1={serviceName:"divy-api", tags:[service.version, telemetry.sdk.*]}` |
| 11 | career: `divy.career` | `traceID="9f3a0703b53d5b0aae2fb3bdacea0ff6"`, `spanID="9f3a0703b53d5b0a"`; golden `testdata/career.jaeger.json` at a frozen `now` |

### L.7.4 HTTP (`server/*_test.go`, `httptest` against the full router with a temp DB and the fixture content)

| # | Request | Expected |
|---|---------|----------|
| 1 | `GET /loki/api/v1/query_range?query={service="gradr"}&limit=2&direction=backward` | 200 `application/json`, `resultType=streams`, 2 entries, `X-Divy-Trace-Id` = 32 hex, `X-Divy-Trace-Sampled: 1`, `Cache-Control: public, max-age=15` |
| 2 | same with `query={service=~".*"}` | 400 `text/plain; charset=utf-8`, body = Loki's empty-compatible message |
| 3 | `query={service="gradr"} \| logfmt` | 400 `parse error at line 1, col 21: unsupported parser "logfmt" (supported: json)` |
| 4 | `limit=6000` | 400 `max entries limit per query exceeded, limit > max_entries_limit_per_query (6000 > 5000)` |
| 5 | `GET /loki/api/v1/query?query=vector(1)%2Bvector(1)` | 200 `resultType=vector`, one sample `"2"`, `metric={}` |
| 6 | `GET /loki/api/v1/labels` | `{"status":"success","data":["component","level","service"]}` |
| 7 | `GET /loki/api/v1/label/service/values?query={level="warn"}` | `data=["gradr"]` |
| 8 | `GET /loki/api/v1/series?match[]={service="gradr"}` and `POST` form | 3 label sets, identical bodies |
| 9 | `GET /loki/api/v1/index/stats?query={service="gradr"}&start=0&end=<now ns>` | `{"streams":3,"chunks":3,"entries":4,"bytes":<sum>}` |
| 10 | `GET /loki/api/v1/tail` | 404 text `not supported by divy.dev; …` |
| 11 | `GET /loki/api/v1/query_range?query={service="gradr"}&start=2026-03-01T00:00:00Z&end=1772323200.5` | 200 (mixed RFC3339 / float seconds accepted); `start=abc` → 400 `invalid parameter "start": …` |
| 12 | `GET /api/traces/career` | 200, `data[0].traceID = 9f3a…`, `Cache-Control: public, max-age=15`, spans in DFS order |
| 13 | `GET /api/traces/<X-Divy-Trace-Id of #12>` | 200, `data[0].spans[0].operationName = "HTTP GET /api/traces/{id}"`, `processes.p1.serviceName = "divy-api"` |
| 14 | `GET /api/traces/zzz`; `GET /api/traces/00000000000000000000000000000000` | 400 `{"error":"invalid trace id …"}`; 404 `{"error":"trace not found …"}`, `Cache-Control: no-store` |
| 15 | `GET /api/services`; `GET /api/services/gradr/operations`; `GET /api/traces?service=gradr`; `GET /api/traces` | contains `divy-api` and `gradr`; 8 operations; 1 trace with `total=1`; 400 `parameter 'service' is required` |
| 16 | `GET /` with `Accept: text/plain`; with a browser `Accept`; `GET /?format=ascii`; `GET /ascii` | text/plain whose first line starts `divy.career ·` and every line ≤ 80 runes, `Vary: Accept`; `text/html`; text; text |
| 17 | `GET /favicon.svg` (empty DB) ; (DB with 7 grid samples) | `image/svg+xml`, contains `<!-- no commit data`; contains `<polyline` with 7 points, `Cache-Control: public, max-age=3600` |
| 18 | `GET /robots.txt` | contains `# Observability for humans: /metrics` and `Sitemap: <origin>/sitemap.xml` |
| 19 | `GET /healthz`; `GET /readyz`; `/readyz` after closing the reader pool | exact healthz JSON; 200 with `checks.db.ok=true`; 503 `status="unavailable"` |
| 20 | `GET /og/postmortems/INC-001.png`; `HEAD` same; `GET /og/postmortems/INC-999.png` | `image/png`, PNG header decodes to 1200×630, `max-age=86400`; same headers, empty body; 404 `{"error":"postmortem not found"}` |
| 21 | `GET /nope`; 101 rapid requests from one IP to `/api/v1/query` | 404 and 429 responses both carry `X-Divy-Trace-Id` |
| 22 | `GET /api/content/profile` | `pods[0].restarts = 4`, `pods[0].age_s > 0`, `Cache-Control: public, max-age=60` |
| 23 | `GET /api/uptime` vs `GET /api/uptime/heartbeats?days=90&bucket=1d` | byte-identical bodies |

## Phase deliverables from this section

| Phase | Deliverables |
|-------|--------------|
| 1 | `internal/logql` (lexer, parser, json, eval + §L.7.1–L.7.2 tables), `/loki/api/v1/*` handlers (§L.2), `internal/trace` (career builder, OTel provider, sampler, SQLite exporter, Jaeger shaping + §L.7.3), `/api/traces*`, `/api/services*`, `/api/operations`, `traceHeader` middleware, `/healthz`, `/readyz`, `/robots.txt`, `/favicon.svg`, `/` negotiation + `/ascii` + `divy export-ascii` (§L.6.1–L.6.2 with `testdata/ascii.golden`), `/api/content/*`, `/api/uptime`, §L.7.4 HTTP table, `docs/logql-subset.md` |
| 2 | fixture content lands; goldens regenerated (`career.jaeger.json`, `ascii.golden`); README "add divy.dev as a Loki / Jaeger data source" paragraphs |
| 3–4 | trace page accepts `?span=` (L-X8); log line → span links (§L.3.4); live tail replay (§L.3.3); `/og/*` wired into `<meta>`; `favicon.ico` fallback committed |
| 5 | Caddy passes `Accept` through unchanged (no `header_up` rewrite) so `/` negotiation works behind the proxy; `X-Divy-Trace-Id` is not stripped |

## Open questions

1. Grafana Explore's log-volume histogram only works if metric queries are supported; this section implements `count_over_time`/`rate` + `sum/count/min/max/avg by/without` (≈ 200 lines on top of the log pipeline). If you would rather keep Phase 1 to the brief's literal list (selectors, line filters, `| json`), say so and metric queries move to Phase 4 — Grafana's Save & test (`vector(1)+vector(1)`) stays supported either way.
