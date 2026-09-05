# PromQL subset, Prometheus HTTP API, `/metrics` exposition

Reference behaviour is Prometheus 3.14.0 (module `github.com/prometheus/prometheus v0.314.0`, the version the Makefile pins for promtool) and the Grafana Prometheus data source as shipped in Grafana 12.4.0 (docs reviewed 2026-08-04). Every number in the test tables was produced by running the real Prometheus parser/engine on the fixtures; the implementation must reproduce them bit-for-bit.

## Cross-section notes

| # | Note | Affects |
|---|------|---------|
| P1 | **`[1d]` windows over daily-backfilled counters are always empty.** Range selection is left-open (`(t−1d, t]`), so a window of exactly one day over samples spaced one day apart holds one sample, and `rate`/`increase`/`delta` need two. Content §C.6 panel `pypi-downloads` must change `sum(increase(pypi_downloads_total{package="codemind-ci"}[1d]))` to `rate(pypi_downloads_total{package="codemind-ci"}[2d]) * 86400` (legend `downloads / day (2d avg)`): with two samples 1 d apart in a 2 d window the extrapolation factor is exactly 2, so `rate × 86400` = the last day's increase (§P.10.2 row E10). `sum(increase(github_commits_total[7d]))` (panel `commits-weekly`, alert `HighContributionRate`) stays: over boundary-only days it is 7⁄6 × the 6-day increase (Prometheus extrapolation, documented in "View query"); once the collector also writes intraday samples (P2) it converges to the true 7-day increase. | Content §C.6, §C.7 |
| P2 | **Every collector run must upsert a sample at the run time for every series it owns**, including the cumulative counters (value = cumulative through now). Otherwise the latest sample of `github_commits_total`/`github_merged_prs_total`/`pypi_downloads_total` is a day boundary, and every instant query on them (stat panels, alerts, `/metrics`) is empty for most of the day. Daily backfill samples are stamped at `00:00:00Z` of the day they close (CONVENTIONS #6 unchanged). | Collector |
| P3 | **Server lookback delta defaults to `2h`** (`QUERY_LOOKBACK_DELTA`, per-request `lookback_delta` honoured exactly as Prometheus). Prometheus' default is 5 m because it scrapes every 15 s; our cadences are 5 m/15 m/60 m, and a 5 m lookback would make `github_stars` invisible to Grafana between collections. 2 h still exposes a dead collector as a gap within two hours. | API, web, Grafana |
| P4 | **`/metrics` applies the same staleness rule**: a stored series whose latest sample is older than the lookback delta is not exposed (diverges from CONVENTIONS #7 "latest sample of every stored series"; reason: a scraper would otherwise ingest hours-old values as current). Live process metrics are unaffected. | API `/metrics` |
| P5 | Endpoints beyond the brief's four, all needed by Grafana: `/api/v1/label/{name}/values`, `/api/v1/metadata`, `/api/v1/status/buildinfo`, `/api/v1/rules` (Content §C.7), `/api/v1/alerts`, `/api/v1/query_exemplars` (empty). `GET /api/v1/label/__name__/values` covers Repo note R7. | API contract table |
| P6 | **Live series.** `divy_uptime_seconds`, `divy_build_info`, `divy_open_to_work`, `divy_experience_years` (Content X1) are not stored; the evaluator sees them through a `LiveSeries` provider that materialises one sample at every evaluation timestamp. `go_*`, `process_*`, `divy_http_*` are exposition-only and not queryable (documented in `/api/v1/metadata` by absence). | API, Collector |
| P7 | **Oracle tests** live in a nested module `api/tools/promql-oracle/` (own `go.mod`, depends on `github.com/prometheus/prometheus v0.314.0`; excluded from `./...` of the api module) that runs the same case tables through Prometheus' parser/engine and diffs them against ours. Adds `make promql-oracle` (CI job `api`, after `test-api`). | Repo §R.1, §R.4, §R.8 |
| P8 | Exposition `Content-Type` is `text/plain; version=0.0.4; charset=utf-8; escaping=underscores` (client_golang 1.24.1 appends the escaping parameter). Golden tests must not assume the bare `version=0.0.4` value. | API tests |

## P.1 Scope

| Area | Supported | Not supported (parse error, §P.4) |
|---|---|---|
| Selectors | instant selector with `=`, `!=`, `=~`, `!~`; `__name__` matcher; range selector `[d]` | `offset`, `@`, subqueries `[r:s]`, quoted UTF-8 names `{"a.b"}`, duration expressions `[5m * 2]` |
| Literals | float (decimal, exponent, hex, `Inf`, `NaN`, `_` separators), string (instant query only) | — |
| Operators | unary `+ -`; arithmetic `+ - * / % ^`; comparison `== != > < >= <=` with optional `bool`; scalar⊕scalar, vector⊕scalar, vector⊕vector one-to-one | `and`, `or`, `unless`, `atan2`, `on`, `ignoring`, `group_left`, `group_right`, `</`, `>/`, `fill*` |
| Aggregations | `sum avg min max count` with `by`/`without` in either position | `topk bottomk quantile stddev stdvar group count_values limitk limit_ratio` |
| Functions | `rate increase irate delta`, `sum_over_time avg_over_time min_over_time max_over_time count_over_time last_over_time`, `abs ceil floor round clamp_min clamp_max`, `time vector scalar` | every other Prometheus function |
| Queries | instant (`/api/v1/query`) and range (`/api/v1/query_range`) evaluation, `lookback_delta`, `timeout`, `limit` | `stats` output (parameter accepted, ignored), native histograms, exemplars |

## P.2 Lexer

Input is UTF-8; the lexer works on bytes and reports positions as 0-based byte offsets (rendered as `line:col`, §P.4). Whitespace (` \t\n\r`) separates tokens; `#` starts a comment to end of line. Longest match wins.

| Token | Pattern / rule | Notes |
|---|---|---|
| `EOF` | end of input | inside `(`/`[` → `unclosed left parenthesis` / `unclosed left bracket` |
| `IDENTIFIER` | `[a-zA-Z_][a-zA-Z0-9_]*` | keywords are checked case-insensitively after lexing (`SUM(` = `sum(`) |
| `METRIC_IDENTIFIER` | `[a-zA-Z_:][a-zA-Z0-9_:]*` containing at least one `:` | allowed only as a metric name |
| `NUMBER` | `[0-9]*\.?[0-9]+([eE][+-]?[0-9]+)?` with `_` allowed between digits; `0[xX][0-9a-fA-F_]+`; `inf`, `nan` (case-insensitive) | `1.5.2`, `1e`, `0x1.5` → `bad number syntax: "…"` |
| `DURATION` | `[0-9]+(y\|w\|d\|h\|m\|s\|ms)` repeated, e.g. `1h30m`; lexed only when the digits are followed by a unit char | validated by the duration grammar (§P.5.1); `1x` → `bad number or duration syntax: "1"` |
| `STRING` | `"…"`, `'…'` with Go escapes (`\n \t \\ \" \' \xNN \uNNNN \UNNNNNNNN \NNN`), or `` `…` `` raw | `unterminated quoted string`; invalid escape → `unknown escape sequence U+00XX 'X'` |
| `( ) { } [ ] ,` | delimiters | `)` with no open paren → `unexpected right parenthesis ')'`; `]` with no open bracket → `unexpected right bracket ']'` |
| `= != =~ !~` | matcher operators (only inside `{}`) | `!` not followed by `=` → `unexpected character after '!': 'x'` |
| `+ - * / % ^` | arithmetic | |
| `== != < <= > >=` | comparison (`!=` is shared with matchers; the parser decides by context) | a single `=` outside braces lexes as `EQL` and the parser rejects it: `unexpected "="` |
| `@` | AT | always rejected by the parser (§P.4) |
| keywords | `and or unless atan2 sum avg count min max group stddev stdvar topk bottomk count_values quantile limitk limit_ratio by without on ignoring group_left group_right bool offset` | an aggregator keyword directly followed by `{` or by end-of-selector context is a metric name (`sum{}` is `{__name__="sum"}`), matching Prometheus' `metric_identifier` rule |
| anything else | | `unexpected character: '§'` |

## P.3 Grammar and precedence

EBNF (terminals in quotes or UPPERCASE):

```
expr        = binary ;
binary      = unary { binop [ "bool" ] unary } ;          (* resolved by the precedence table below *)
unary       = ( "+" | "-" ) unary | power ;
power       = primary [ "^" unary ] ;                     (* right-associative; RHS may carry a unary sign *)
primary     = NUMBER
            | STRING
            | "(" expr ")"
            | aggregation
            | call
            | selector [ range ] ;
aggregation = aggr_op grouping "(" expr ")"
            | aggr_op "(" expr ")" [ grouping ] ;
aggr_op     = "sum" | "avg" | "min" | "max" | "count" ;
grouping    = ( "by" | "without" ) "(" [ label_list ] ")" ;
label_list  = IDENTIFIER { "," IDENTIFIER } [ "," ] ;
call        = IDENTIFIER "(" [ expr { "," expr } ] ")" ;  (* IDENTIFIER must be in the function table §P.5.5 *)
selector    = metric_name [ matchers ] | matchers ;
metric_name = IDENTIFIER | METRIC_IDENTIFIER | aggr_op ;
matchers    = "{" [ matcher { "," matcher } [ "," ] ] "}" ;
matcher     = IDENTIFIER match_op STRING ;
match_op    = "=" | "!=" | "=~" | "!~" ;
range       = "[" ( DURATION | NUMBER ) "]" ;             (* NUMBER = seconds; must be > 0 *)
binop       = "^" | "*" | "/" | "%" | "+" | "-" | "==" | "!=" | ">" | "<" | ">=" | "<=" ;
```

Precedence (highest first; identical to Prometheus for the supported operators):

| Level | Operators | Associativity | Note |
|---|---|---|---|
| 1 | `^` | right | `2 ^ 3 ^ 2` = `2 ^ (3 ^ 2)` = 512 |
| 2 | unary `+`, `-` | prefix | binds tighter than `*` but looser than `^`: `-2 ^ 2` = −4, `-x * 2` = `(-x) * 2` (Prometheus `%prec MUL`) |
| 3 | `*`, `/`, `%` | left | `2 * 3 % 2` = `(2 * 3) % 2` |
| 4 | `+`, `-` | left | |
| 5 | `==`, `!=`, `>`, `<`, `>=`, `<=` (optionally followed by `bool`) | left | |

Static checks after parsing (Prometheus `checkAST` equivalents; the message is the Prometheus text, position is the offending node):

| Rule | Message |
|---|---|
| binop operands must be scalar or instant vector | `binary expression must contain only scalar and instant vector types` |
| scalar `cmp` scalar needs `bool` | `comparisons between scalars must use BOOL modifier` |
| `bool` only on comparisons | `bool modifier can only be used on comparison operators` |
| selector needs a name or a non-empty matcher (`{}`, `{job=~".*"}`, `{a!=""}` alone is fine) | `vector selector must contain at least one non-empty matcher` |
| name given twice | `metric name must not be set twice: "github_commits_total" or "x"` |
| regex must compile (Go `regexp`, RE2) | Go's error verbatim, e.g. `error parsing regexp: missing closing ): `(`` |
| aggregation takes exactly one argument of type instant vector | `no arguments for aggregate expression provided` / `wrong number of arguments for aggregate expression provided, expected 1, got 2` / `expected type instant vector in aggregation expression, got range vector` |
| grouping label must be an identifier | `unexpected <desc> in grouping opts, expected label` |
| function arity | `expected 1 argument(s) in call to "rate", got 2`; `expected at most 2 argument(s) in call to "round", got 3`; `expected 0 argument(s) in call to "time", got 1` |
| function argument type | `expected type range vector in call to function "rate", got instant vector`; `expected type instant vector in call to function "scalar", got scalar` |
| range only on a selector | `ranges only allowed for vector selectors` |
| unary only on scalar/vector | `unary expression only allowed on expressions of type scalar or instant vector, got "range vector"` |
| range must be positive | `duration must be greater than 0` |
| trailing comma in call | `trailing commas not allowed in function call args` |

Type names used in messages: `scalar`, `instant vector`, `range vector`, `string`.

AST node types (package `api/internal/promql`): `NumberLiteral`, `StringLiteral`, `VectorSelector{Name, Matchers}`, `MatrixSelector{VectorSelector, Range}`, `UnaryExpr`, `BinaryExpr{Op, LHS, RHS, ReturnBool}`, `AggregateExpr{Op, Expr, Grouping, Without}`, `Call{Func, Args}`, `ParenExpr`. `Expr.String()` prints Prometheus' canonical form (durations via the `y w d h m s ms` formatter with `y`/`w` only when exact: `[7d]` → `[1w]`, `[90s]` → `[1m30s]`; `sum(x) by (a)` → `sum by (a) (x)`; grouping with an empty list is dropped) so the oracle can diff strings.

## P.4 Unsupported constructs and exact errors

Format of every parse error: `<line>:<col>: parse error: <message>` (`col` is 1-based, counted in bytes from the last newline; empty input reports `unknown position`). Through the API the string is wrapped: `invalid parameter "query": 1:5: parse error: unclosed left parenthesis`.

| Construct | Example | Position | Message |
|---|---|---|---|
| `offset` | `x offset 1d` | the keyword | `offset modifier is not supported` |
| `@` | `x @ 1609746000`, `x @ start()` | the `@` | `@ modifier is not supported` |
| subquery | `x[1h:5m]` | the `:` | `subqueries are not supported` |
| duration expression | `x[5m * 2]` | the operator | `unexpected <op:*> in range selector, expected "]"` |
| set operators | `a and b`, `a or b`, `a unless b` | the keyword | `set operator "and" is not supported` |
| `atan2` | `a atan2 b` | the keyword | `binary operator "atan2" is not supported` |
| matching modifiers | `a + on(x) b`, `a / ignoring(x) group_left b` | the keyword | `vector matching modifier "on" is not supported` (also `ignoring`, `group_left`, `group_right`) |
| other aggregations | `topk(3, x)`, `quantile(0.9, x)`, `count_values("v", x)`, `stddev(x)`, `stdvar(x)`, `group(x)`, `bottomk`, `limitk`, `limit_ratio` | the keyword | `aggregation operator "topk" is not supported` |
| Prometheus functions outside the table | `histogram_quantile(0.9, x)`, `label_join(...)`, `label_replace(...)`, `absent(x)`, `absent_over_time`, `predict_linear`, `deriv`, `changes`, `resets`, `idelta`, `sort`, `sort_desc`, `sort_by_label*`, `timestamp`, `day_of_*`, `hour`, `minute`, `month`, `year`, `days_in_month`, `exp`, `ln`, `log2`, `log10`, `sqrt`, `sgn`, `clamp`, trig functions, `quantile_over_time`, `stddev_over_time`, `stdvar_over_time`, `present_over_time`, `first_over_time`, `double_exponential_smoothing`, `info`, `histogram_*`, `pi`, `start`, `end`, `step`, `range`, `min_of`, `max_of` | the name | `function "histogram_quantile" is not supported` |
| unknown function | `foo(x)`, `holt_winters(x[1h], 0.5, 0.5)` | the name | `unknown function with name "foo"` (Prometheus text; `holt_winters` no longer exists in Prometheus 3) |
| quoted / UTF-8 metric or label names | `{"a.b"}`, `{"a.b"="1"}` | the string | `unexpected string "a.b" in label matching, expected identifier or "}"` |
| string as operand | `"a" + 1` | LHS | `binary expression must contain only scalar and instant vector types` |
| range on non-selector | `rate(x[5m])[5m]`, `(x)[5m]` | the `[` | `ranges only allowed for vector selectors` |
| range vector in binop | `a[5m] + b[5m]` | LHS | `binary expression must contain only scalar and instant vector types` |
| unary on range vector | `-x[5m]` | the expr | `unary expression only allowed on expressions of type scalar or instant vector, got "range vector"` |
| `fill`, `anchored`, `smoothed`, `</`, `>/` (Prometheus 3.x experimental) | | | not keywords here: `anchored` lexes as an identifier (`unexpected identifier "anchored"`), `</` as `<` then `/` (`unexpected <op:/>`) |

Generic syntax errors use Prometheus' `unexpected` composition: `unexpected <desc>[ in <context>][, expected <what>]` with `<desc>` ∈ `end of input`, `identifier "x"`, `number "5"`, `string "s"`, `duration "5m"`, `<bool>`/`<by>`/… (keywords), `<op:+>` (operators), `<aggr:sum>`, `")"`/`"}"`/`","` (delimiters). Contexts used: `label matching`, `grouping opts`, `aggregation`, `range selector`, `binary expression`.

## P.5 Semantics

### P.5.1 Selectors, durations, lookback

| Construct | Semantics | Prometheus reference matched |
|---|---|---|
| `name`, `name{m…}`, `{m…}` | Series set = every series whose label set satisfies all matchers; `name` is sugar for `{__name__="name"}` and may be combined with other `__name__` matchers only if not set twice. `=` / `!=` compare the full value; `=~` / `!~` compile the pattern as `^(?s:` + re + `)$` (Go `regexp`, RE2) and match the full value. A matcher against a label the series lacks sees the empty string, so `{repo=""}` matches series without `repo`; `{repo!=""}` requires it. Several matchers on one label are ANDed. | basics.md "Instant vector selectors"; `labels/regexp.go` |
| value at time `t` | For each matched series: the newest sample with `t_s ≤ t` and `t_s > t − lookback`; series with none are omitted. Result sample timestamp = `t` (not `t_s`). No stale markers exist in our store. | basics.md "Staleness"; `engine.go vectorSelectorSingle` |
| lookback | Request param `lookback_delta` (duration or float seconds) else server `QUERY_LOOKBACK_DELTA` (default `2h`, note P3). | api.md `lookback_delta` |
| `sel[d]` | Range vector: for each matched series, all samples with `t − d < t_s ≤ t` (left-open, right-closed) in ascending time order. Series with zero samples in the window are omitted. Lookback does not apply. | basics.md "Range Vector Selectors" |
| duration literal | Grammar: `INT unit { INT unit }`, units `y`(365d) `w`(7d) `d`(24h) `h` `m` `s` `ms`, strictly descending, each at most once; a bare `NUMBER` in brackets is seconds. `0`, `0s`, `-1d` are rejected (`duration must be greater than 0`); `1.5h` → `unknown unit "." in duration "1.5h"`; `1d1w` → `not a valid duration string: "1d1w"`; `1x` → `bad number or duration syntax: "1"`. Maximum 290 years (`duration out of range`). | `common/model/time.go ParseDuration`; basics.md |
| `__name__` | Ordinary label in matchers and in results; dropped by the operations listed in §P.5.6. | operators.md |

### P.5.2 Literals and unary operators

| Construct | Semantics |
|---|---|
| `NUMBER` | float64 scalar; `Inf`, `+Inf`, `-Inf`, `NaN` are values, not errors; hex and `_` separators as in Prometheus (`0x1F` = 31, `1_000` = 1000). |
| `STRING` | Result type `string`; allowed only as a whole instant query (returns `[t, "…"]`) or as a matcher value. In a range query → `invalid expression type "string" for range query, must be Scalar or instant Vector`. |
| `-e`, `+e` | On a scalar: negation / identity, folded into the literal (`- - 1` prints `1`). On a vector: applied to every sample; `__name__` dropped. |

### P.5.3 Binary operators

Operand kinds and results (`s` scalar, `v` vector). All float arithmetic is IEEE 754 through Go: `+ - *` native, `/` native (`1/0`=+Inf, `-1/0`=−Inf, `0/0`=NaN), `%` = `math.Mod` (`x % 0` = NaN), `^` = `math.Pow`. Comparisons on NaN are false except `!=`.

| Form | Result | Labels / `__name__` |
|---|---|---|
| `s ⊕ s` (arithmetic) | scalar | — |
| `s cmp bool s` | scalar 0/1 | — (`s cmp s` without `bool` is a parse error) |
| `v ⊕ s`, `s ⊕ v` (arithmetic) | vector: op applied to every sample (`s − v` computes `s − value`) | labels kept, `__name__` dropped |
| `v cmp s`, `s cmp v` | filter: keep samples for which `value cmp s` (resp. `s cmp value`) is true; value unchanged | labels and `__name__` kept |
| `v cmp bool s`, `s cmp bool v` | every sample kept, value 1 if true else 0 | `__name__` dropped |
| `v ⊕ v` (arithmetic) | one-to-one: signature = label set without `__name__`; each LHS sample pairs with the RHS sample of equal signature; unmatched samples dropped; value = `lhs ⊕ rhs` | result labels = LHS labels without `__name__` |
| `v cmp v` | one-to-one as above; keep the LHS sample if `lhs cmp rhs` | LHS labels incl. `__name__` |
| `v cmp bool v` | one-to-one; every matched pair kept, value 0/1 | LHS labels without `__name__` |

Execution errors (HTTP 422, errorType `execution`), raised when a signature repeats on one side:

- RHS duplicate: `found duplicate series for the match group {repo="a"} on the right hand-side of the operation: [{__name__="x", repo="a"}, {__name__="y", repo="a"}];many-to-many matching not allowed: matching labels must be unique on one side` (the two series printed in ascending label-set order).
- LHS duplicate: `multiple matches for labels: many-to-one matching must be explicit (group_left/group_right)`.

Short-circuit: if either side is empty the result is empty (no error is raised for duplicates in that case, as in Prometheus).

### P.5.4 Aggregations

`op [by|without (l1, l2,…)] (v)` or `op(v) [by|without (…)]`. Groups are keyed by the output label set:

| Clause | Output labels of a group |
|---|---|
| none | `{}` (one group) |
| `by (l…)` | only the listed labels that the input series has (missing labels are simply absent) |
| `without (l…)` | all input labels except the listed ones and `__name__` |

| Op | Group value | NaN rule |
|---|---|---|
| `sum` | Kahan-compensated sum (same order-independent result as Prometheus for integer-valued fixtures) | NaN propagates |
| `avg` | Kahan sum ÷ count | NaN propagates |
| `min` / `max` | running `if v < cur \|\| isNaN(cur) { cur = v }` (resp. `>`) | NaN only if every value is NaN |
| `count` | number of samples | — |

Output order: groups sorted by their label-set string (§P.6). `sum(nonexistent)` and `count(nonexistent)` return an empty vector (Prometheus never emits a group for zero inputs).

### P.5.5 Functions

`v` = instant vector, `r` = range vector, `s` = scalar. Unless stated, the function drops `__name__` and keeps the other labels.

| Signature | Semantics (Prometheus 3.14 behaviour) |
|---|---|
| `rate(r)`, `increase(r)`, `delta(r)` | Per series in `r` with ≥ 2 samples (else no output): let `first`/`last` be the first/last sample in the window, `rangeStart = t − range`, `rangeEnd = t`. 1) `Δ = last.v − first.v`; for `rate`/`increase` add `prev.v` to `Δ` at every counter reset (`cur.v < prev.v`). 2) `durationToStart = (first.t − rangeStart)/1000`, `durationToEnd = (rangeEnd − last.t)/1000`, `sampledInterval = (last.t − first.t)/1000`, `avg = sampledInterval/(n−1)`, `threshold = avg × 1.1` (seconds). 3) `if durationToStart ≥ threshold { durationToStart = avg/2 }`. 4) counters only: `durationToZero = durationToStart; if Δ > 0 && first.v ≥ 0 { durationToZero = sampledInterval × first.v/Δ }; if durationToZero < durationToStart { durationToStart = durationToZero }`. 5) `if durationToEnd ≥ threshold { durationToEnd = avg/2 }`. 6) `factor = (sampledInterval + durationToStart + durationToEnd)/sampledInterval`; `rate`: `factor /= range.Seconds()`. 7) result `Δ × factor`. Implement the float operations in exactly this order (bit-identical to `extrapolatedRate`). `delta` skips reset handling and step 4. |
| `irate(r)` | Per series with ≥ 2 samples: `a`, `b` = the last two samples; if `b.v < a.v` (reset) value = `b.v` else `b.v − a.v`; result = value ÷ `((b.t − a.t)/1000)`; if the two timestamps are equal, no output. |
| `sum_over_time(r)`, `avg_over_time(r)`, `min_over_time(r)`, `max_over_time(r)`, `count_over_time(r)` | Per series: Kahan sum / Kahan sum ÷ n / min / max (NaN only if all NaN, same running rule as the aggregations) / number of samples. |
| `last_over_time(r)` | Per series: the value of the newest sample in the window; **keeps `__name__`** (Prometheus does). |
| `abs(v)`, `ceil(v)`, `floor(v)` | `math.Abs`, `math.Ceil`, `math.Floor` per sample. |
| `round(v[, to_nearest=1])` | `inv = 1/to_nearest; math.Floor(x×inv + 0.5)/inv` (ties round up; `round(3.75, 0.5)` = 4; `round(4.3333, 0.01)` = 4.33). |
| `clamp_min(v, min)`, `clamp_max(v, max)` | `math.Max(x, min)` / `math.Min(x, max)`; a NaN bound yields NaN. |
| `time()` | scalar = evaluation timestamp in seconds (`float64(t_ms)/1000`), not wall-clock. |
| `vector(s)` | one sample with empty labels `{}` and value `s`. |
| `scalar(v)` | value of the single sample in `v`; if `v` has 0 or ≥ 2 samples → NaN. |

### P.5.6 `__name__` summary

| Kept | Dropped |
|---|---|
| selectors, `paren`, `v cmp s` / `v cmp v` without `bool`, `last_over_time` | unary `-`/`+` on vectors, every arithmetic op involving a vector, any `bool` comparison, all aggregations (also `by (__name__)` is not special: it keeps it like any label), every other function |

## P.6 Evaluation model

| Topic | Rule |
|---|---|
| Instant query | parse → static checks → evaluate at `time` (default: server now, ms precision) → `resultType` = expression type (`scalar`, `vector`, `matrix` for a bare range selector, `string`). |
| Range query | expression type must be scalar or vector (else 400, §P.4). Timestamps `ts = start, start+step, …` while `ts ≤ end` (inclusive; no alignment — Grafana aligns client-side). Each step is a full instant evaluation; points are collected per output series (identity = label set); a scalar expression becomes one series with metric `{}`; `resultType` is always `matrix`. No subqueries, so the per-step cost is bounded by the sample fetch below. |
| Sample fetch | One SQL read per selector per query, not per step: instant selectors read `ts_ms > start_ms − lookback_ms AND ts_ms ≤ end_ms`, range selectors `ts_ms > start_ms − range_ms AND ts_ms ≤ end_ms` (`SELECT series_id, ts_ms, value FROM samples WHERE series_id IN (…) AND ts_ms > ? AND ts_ms <= ? ORDER BY series_id, ts_ms`), then a moving window per step in memory. Matchers run in Go over the in-memory series index (`series` table, ≤ a few hundred rows, reloaded when the writer adds a series and at most every 15 s). Live series (P6) contribute one sample at every `ts`. |
| Guards | `(end − start)/step > 11000` → 400 (§P.7). Samples loaded per query capped by `QUERY_MAX_SAMPLES` (default 2 000 000) → 422 `query processing would load too many samples into memory in query execution`. Wall time capped by `min(timeout param, QUERY_TIMEOUT default 30s)` → 503 `query timed out in query execution`. Concurrency capped by `QUERY_MAX_CONCURRENCY` (default 20, semaphore; waiting counts against the timeout). Client disconnect cancels the context (no response is written). |
| Ordering | Vector and matrix results are sorted by the canonical label-set string (labels sorted by name, rendered `{a="1", b="2"}`); points within a series by time. Prometheus does not guarantee order; ours is deterministic so golden tests are byte-stable. |
| NaN / Inf | Stored and emitted as-is (`"NaN"`, `"+Inf"`, `"-Inf"` strings in JSON). Never filtered; comparisons follow IEEE (so `NaN > 0` filters the sample out, `NaN != 0` keeps it). |
| Empty results | `{"status":"success","data":{"resultType":"vector","result":[]}}`; matrix likewise `[]`; never `null`. A scalar or string result is never empty. |
| Timestamps in output | The evaluation timestamp(s), as float seconds with millisecond precision, never the sample's own timestamp (Prometheus semantics). |
| Caching | Handler-level: `Cache-Control: public, max-age=15`, weak ETag = hash of the body; in-memory response cache keyed by `(path, normalized query string, lookback, method)` for 15 s (CONVENTIONS #10). Keys normalise `time`/`start`/`end` to ms and `step` to ms. |

## P.7 Prometheus HTTP API

### P.7.1 Common rules

| Rule | Detail |
|---|---|
| Methods | `GET` with a query string, and `POST` with `Content-Type: application/x-www-form-urlencoded` (Go `r.ParseForm`, so POST bodies and query strings merge; body limit 1 MiB) for `query`, `query_range`, `series`, `labels`, `query_exemplars`. `label/{name}/values`, `metadata`, `status/buildinfo`, `rules`, `alerts` are `GET` only. Any other method → `405` with `Allow` and the JSON envelope below. |
| Timestamps (`time`, `start`, `end`) | Unix seconds as a float (`1788602400`, `1788602400.5`; fraction rounded to ms) or RFC 3339 / RFC 3339-nano (`2026-09-05T10:00:00Z`, `2026-09-05T10:00:00.123+05:30`). Anything else → 400 `invalid parameter "start": cannot parse "x" to a valid timestamp`. Missing `start`/`end` on `query_range` → the same message with `""`. |
| Durations (`step`, `timeout`, `lookback_delta`) | Float seconds (`15`, `0.5`) or a duration literal (`15s`, `1h30m`, `1d`). Else 400 `invalid parameter "step": cannot parse "x" to a valid duration`. |
| `limit` | non-negative integer; `0` = disabled; truncation adds `"warnings":["results truncated due to limit"]`. |
| Repeated params | `match[]` may repeat; the union of the matcher sets is used. |
| Unknown params | ignored (Grafana "custom query parameters" pass through). |
| Response headers | `Content-Type: application/json`, `Cache-Control: public, max-age=15`, `ETag`, `X-Divy-Trace-Id`; CORS per CONVENTIONS #15 (Grafana proxies server-side and needs none). |
| Success envelope | `{"status":"success","data":…}` plus optional `"warnings":[…]`. |
| Error envelope | `{"status":"error","errorType":"<type>","error":"<message>"}` with the status code from the table below; also used for 404/405/429 under `/api/v1/`. |

| HTTP | `errorType` | When | Example `error` |
|---|---|---|---|
| 400 | `bad_data` | bad/missing parameter, parse error, > 11 000 points, bad selector | `invalid parameter "query": 1:5: parse error: unclosed left parenthesis` |
| 404 | `not_found` | unknown `/api/v1/*` path | `path not found` |
| 405 | `bad_data` | wrong method | `method not allowed` |
| 422 | `execution` | duplicate-signature matching, too many samples, evaluator errors | `query processing would load too many samples into memory in query execution` |
| 429 | `unavailable` | rate limit (with `Retry-After`) | `rate limit exceeded` |
| 499 | — | client closed connection (logged, no body) | |
| 500 | `internal` | SQLite error, panic recovered | `internal error: <id>` (details only in logs) |
| 503 | `timeout` | `QUERY_TIMEOUT`/`timeout` exceeded | `query timed out in query execution` |

### P.7.2 Endpoint table

| Method | Path | Parameters (type, required, default) | `data` |
|---|---|---|---|
| GET/POST | `/api/v1/query` | `query` string **req**; `time` timestamp, default now; `timeout` duration, default `QUERY_TIMEOUT`; `limit` int ≥ 0, default 0; `lookback_delta` duration, default `QUERY_LOOKBACK_DELTA`; `stats` ignored | `{"resultType":"vector"\|"scalar"\|"matrix"\|"string","result":…}` |
| GET/POST | `/api/v1/query_range` | `query` **req**; `start` **req**; `end` **req** (≥ start); `step` **req** (> 0, and `(end−start)/step ≤ 11000`); `timeout`; `limit`; `lookback_delta`; `stats` ignored | `{"resultType":"matrix","result":[…]}` |
| GET/POST | `/api/v1/series` | `match[]` selector, **≥ 1 req**; `start`, `end` timestamps, default −∞/+∞; `limit` | array of label-set objects (incl. `__name__`), sorted; a series is listed when it has a sample in `[start, end]` (live series always) |
| GET/POST | `/api/v1/labels` | `start`, `end`, `match[]` (optional), `limit` | sorted label names (`__name__` first by sort order) |
| GET | `/api/v1/label/{name}/values` | `{name}` must match `[a-zA-Z_][a-zA-Z0-9_]*` (else 400 `invalid label name: "1bad"`); `start`, `end`, `match[]`, `limit` | sorted distinct values |
| GET | `/api/v1/metadata` | `limit` int (default −1 = all); `limit_per_metric` (always ≤ 1 here); `metric` name filter | `{"<family>":[{"type":"counter","help":"…","unit":""}]}` for every queryable family (catalogue + live series; not `go_*`/`process_*`/`divy_http_*`) |
| GET | `/api/v1/status/buildinfo` | — | `{"version","revision","branch","buildUser","buildDate","goVersion"}` (no `features` key ⇒ Grafana classifies the source as Prometheus) |
| GET | `/api/v1/rules` | `type` `alert`\|`record` (groups left empty by the filter are omitted, as Prometheus does); `rule_name[]`, `rule_group[]`, `file[]` honoured; `exclude_alerts`, `match[]`, `group_limit`, `group_next_token` accepted and ignored | Content §C.7 shape |
| GET | `/api/v1/alerts` | — | `{"alerts":[]}` (the server never evaluates alerts; the browser does) |
| GET/POST | `/api/v1/query_exemplars` | `query` (must parse), `start`, `end` (Grafana sends **milliseconds** here; accepted as huge second values, never rejected) | `[]` |
| OPTIONS | `/api/v1/*` | CORS preflight | 204 |
| any | other `/api/v1/*` | | 404 envelope |

### P.7.3 Examples (evaluation time 1788602400 = 2026-09-05T10:00:00Z)

`GET /api/v1/query?query=sum%20by%20(org)%20(github_merged_prs_total)`

```json
{"status":"success","data":{"resultType":"vector","result":[
 {"metric":{"org":"gradr"},"value":[1788602400,"3"]},
 {"metric":{"org":"kubeflow"},"value":[1788602400,"2"]},
 {"metric":{"org":"kubernetes"},"value":[1788602400,"15"]}]}}
```

`POST /api/v1/query` body `query=1%2B1&time=4` (Grafana health check) → `{"status":"success","data":{"resultType":"scalar","result":[4,"2"]}}`

`GET /api/v1/query?query=%22hello%22` → `{"status":"success","data":{"resultType":"string","result":[1788602400,"hello"]}}`

`GET /api/v1/query_range?query=rate(pypi_downloads_total%5B2d%5D)*86400&start=1788480000&end=1788566400&step=1d`

```json
{"status":"success","data":{"resultType":"matrix","result":[
 {"metric":{"package":"codemind-ci"},"values":[[1788480000,"30"],[1788566400,"60"]]}]}}
```

`GET /api/v1/query_range?query=github_stars&start=1788566400&end=1788602400&step=1` (36 000 points) → 400 `{"status":"error","errorType":"bad_data","error":"exceeded maximum resolution of 11,000 points per timeseries. Try decreasing the query resolution (?step=XX)"}`; the same range with `step=10` (3 600 points) succeeds

`GET /api/v1/series?match[]=github_stars&match[]=probe_success{target="pypi"}`

```json
{"status":"success","data":[
 {"__name__":"github_stars","repo":"codemind"},
 {"__name__":"github_stars","repo":"savely"},
 {"__name__":"probe_success","target":"pypi"}]}
```

`GET /api/v1/labels` → `{"status":"success","data":["__name__","collector","metric","org","package","repo","result","status","target","version"]}` (exact list depends on stored series; `commit`, `go_version` come from the live `divy_build_info`)

`GET /api/v1/label/__name__/values?limit=40000&start=1788516000&end=1788602400` → `{"status":"success","data":["divy_build_info","divy_collector_last_success_timestamp_seconds",…,"probe_success"]}`

`GET /api/v1/label/org/values?match[]=github_merged_prs_total` → `{"status":"success","data":["gradr","kubeflow","kubernetes"]}`

`GET /api/v1/metadata?metric=github_stars` → `{"status":"success","data":{"github_stars":[{"type":"gauge","help":"Current stargazer count of a repository.","unit":""}]}}`

`GET /api/v1/status/buildinfo` → `{"status":"success","data":{"version":"v0.1.0","revision":"3f2a9c1","branch":"main","buildUser":"ci","buildDate":"2026-09-05T09:12:44Z","goVersion":"go1.26.8"}}` (`version`/`revision`/`buildDate` from `-ldflags` §R.3; `branch` = `main` when built from a tag, else the branch name)

`GET /api/v1/alerts` → `{"status":"success","data":{"alerts":[]}}`

`GET /api/v1/query_exemplars?query=test&start=1788600600000&end=1788602400000` → `{"status":"success","data":[]}`

`GET /api/v1/query?query=sum(` → `400 {"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": 1:5: parse error: unclosed left parenthesis"}`

`GET /api/v1/query?query={target=\"pypi\"}+{target=\"pypi\"}` → `422 {"status":"error","errorType":"execution","error":"multiple matches for labels: many-to-one matching must be explicit (group_left/group_right)"}` (two series share the signature `{target="pypi"}` on the left)

`GET /api/v1/nope` → `404 {"status":"error","errorType":"not_found","error":"path not found"}`

## P.8 Grafana

Verified against Grafana 12.4.0 (backend `pkg/promlib`, frontend `packages/grafana-prometheus`) and the current docs. From Grafana 13.2 the same code ships as the standalone `grafana-prometheus-datasource` plugin (not separately verified). Data source settings assumed: URL `https://divy.dev`, HTTP method `POST` (default), Prometheus type/version unset, "Use series endpoint" off, series limit 40000 (default), scrape interval blank (=15 s).

| Grafana action | Requests it sends to us (in order) | What we must return |
|---|---|---|
| **Save & test** | 1. `POST /api/v1/query` body `query=1%2B1&time=4` (+ `timeout=<value>` only if "Query timeout" was set). 2. `GET /api/v1/status/buildinfo`. | 1. `200 {"status":"success","data":{"resultType":"scalar","result":[4,"2"]}}`. 2. `200` buildinfo (a 404 is tolerated but then the type cannot be detected). UI shows "Successfully queried the Prometheus API." |
| Data source load (any dashboard/Explore using it) | `POST /api/v1/rules` (form body, empty) — on 405/400 retried as `GET`; `GET /api/v1/query_exemplars?query=test&start=<ms>&end=<ms>`. | rules JSON (errors are ignored by Grafana); `200 {"status":"success","data":[]}` (exemplar toggle then appears but always yields nothing). |
| Explore / Code mode / Metrics browser open | `GET /api/v1/label/__name__/values?limit=40000&start=<s>&end=<s>`; `POST /api/v1/labels` body `limit=40000&start=<s>&end=<s>`; `GET /api/v1/metadata?limit=40000`. | sorted names; sorted label names; metadata map (type + help drive autocomplete tooltips). |
| Metrics browser: pick a metric, then a label | `POST /api/v1/labels` body `match[]={__name__="github_stars"}&limit=40000&start=&end=`; `GET /api/v1/label/repo/values?match[]={__name__="github_stars"}&limit=40000&start=&end=`; "Validate selector": `POST /api/v1/series` body `match[]=…&start=&end=&limit=40000`. | label names of matching series; values; series list (count shown). |
| Builder mode metric drop-down / Metrics explorer | same `__name__` values + `metadata` calls (cached by cache level). | |
| Dashboard panel / Explore run, type **Range** | `POST /api/v1/query_range` body `query=<expr>&start=<s[.ffffff]>&end=<s>&step=<seconds as float, e.g. 15 or 3600>` (+ `timeout=`). `start`/`end` are aligned down to the step; `step = max(max(scrape interval 15s, Min step), RoundInterval((to−from)/maxDataPoints))` and never below `RoundInterval((to−from)/11000)`, so the 11 000-point guard never trips. | matrix JSON; the streaming parser needs `resultType` (before or after `result`), numeric timestamps, **string** values. |
| type **Instant** (stat/gauge/table panels) | `POST /api/v1/query` body `query=<expr>&time=<end s>` | vector JSON |
| type **Both** (the default) | both of the above | |
| `$__rate_interval` in a query | expands client-side to `max(step + scrape, 4 × scrape)`; with a `1d` Min step it becomes `4d`, which is the only way daily samples produce a non-empty `rate()` in Grafana (note P1). | |
| Alerting → data-source-managed rules | ruler proxy `GET /api/v1/rules`, `GET /api/v1/alerts` ("Manage alerts via Alerting UI" on by default; Prometheus type is read-only). | our rules (state `inactive`, health `unknown`) and `alerts: []`. |

Compatibility notes: Grafana never sends `lookback_delta`, so its panels see the server default (P3). Non-2xx responses are parsed as the JSON envelope and surfaced as `bad_data: …`; plain-text errors would surface as "response from prometheus couldn't be parsed" — hence every `/api/v1/*` error is JSON, 404/405/429 included. Grafana adds `X-Grafana-Org-Id`, `X-Dashboard-UID`, `X-Panel-Id` headers; ignored.

## P.9 `/metrics` exposition

| Topic | Decision |
|---|---|
| Handler | `promhttp.HandlerFor(registry, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError, MaxRequestsInFlight: 8})` on a dedicated `prometheus.NewRegistry()` with `collectors.NewGoCollector()`, `collectors.NewProcessCollector(...)`, the HTTP middleware metrics, and one custom `Collector` that reads the latest sample per stored series (age ≤ `QUERY_LOOKBACK_DELTA`, note P4) plus the live series (P6). OpenMetrics negotiation disabled. |
| Content-Type | Negotiated by client_golang from `Accept`: default (`curl`, browsers, Prometheus 3 falling back) `text/plain; version=0.0.4; charset=utf-8; escaping=underscores`; `application/vnd.google.protobuf; proto=io.prometheus.client.MetricFamily; encoding=delimited; escaping=underscores` when a Prometheus scraper asks for protobuf. Prometheus 3 rejects scrapes without a valid `Content-Type`; promhttp always sets one. |
| Ordering | client_golang emits families sorted by name; within a family, samples sorted by label values; `HELP` then `TYPE` precede each family; one family = one contiguous block (required by the text format). |
| Timestamps | **No sample timestamps.** (1) With timestamps Prometheus ingests each sample at that time; our stored samples can be minutes to a day old, and Prometheus rejects samples older than its out-of-order window ("out of bounds"), so scrapes would partially fail. (2) Self-timestamped series get different staleness handling (they linger 5 min after disappearing) and `honor_timestamps` semantics that surprise operators. (3) Grafana/Prometheus users expect "value as of scrape". Freshness is exposed instead through `divy_collector_last_success_timestamp_seconds{collector}` and the staleness cut-off (P4). |
| Escaping | client_golang escapes `\`, `"`, `\n` in label values and HELP; label names in the catalogue are fixed and safe. |
| Compression | promhttp gzip when `Accept-Encoding: gzip` (Caddy also encodes; both are fine). |

HELP/TYPE lines for every catalogue family (verified promlint-clean; `go_*`/`process_*` come from client_golang):

```
# HELP github_commits_total Cumulative GitHub contributions (commits, PRs, issues, reviews) per GitHub's contribution calendar.
# TYPE github_commits_total counter
# HELP github_merged_prs_total Cumulative merged pull requests authored by divysinghvi, by repository owner.
# TYPE github_merged_prs_total counter
# HELP github_merged_prs_by_repo_total Cumulative merged pull requests authored by divysinghvi, by public repository.
# TYPE github_merged_prs_by_repo_total counter
# HELP github_stars Current stargazer count of a repository.
# TYPE github_stars gauge
# HELP github_followers Current follower count of the GitHub profile.
# TYPE github_followers gauge
# HELP pypi_downloads_total Cumulative PyPI downloads (pypistats.org overall, mirrors excluded).
# TYPE pypi_downloads_total counter
# HELP pypi_package_info Latest published version of a PyPI package (value is always 1).
# TYPE pypi_package_info gauge
# HELP savely_active_users Active users of the Savely Chrome extension (manual source, lower bound).
# TYPE savely_active_users gauge
# HELP divy_manual_metric_updated_timestamp_seconds Unix time at which a manually maintained metric was last updated.
# TYPE divy_manual_metric_updated_timestamp_seconds gauge
# HELP oss_prs_open Open pull requests authored by divysinghvi in repositories not owned by divysinghvi.
# TYPE oss_prs_open gauge
# HELP lfx_applications LFX Mentorship applications by status (manual source).
# TYPE lfx_applications gauge
# HELP divy_uptime_seconds Seconds since the API process started.
# TYPE divy_uptime_seconds gauge
# HELP divy_build_info Build metadata of the running binary (value is always 1).
# TYPE divy_build_info gauge
# HELP divy_open_to_work Whether Divy is open to work (1) or not (0), from content/profile.yaml.
# TYPE divy_open_to_work gauge
# HELP divy_experience_years Years since the start of the earliest span counted as experience, from content/spans.yaml.
# TYPE divy_experience_years gauge
# HELP probe_success Whether the last uptime probe of the target succeeded (1) or failed (0).
# TYPE probe_success gauge
# HELP probe_duration_seconds Duration of the last uptime probe of the target.
# TYPE probe_duration_seconds gauge
# HELP probe_http_status_code HTTP status code returned by the last uptime probe of the target (0 on connection failure).
# TYPE probe_http_status_code gauge
# HELP divy_collector_last_success_timestamp_seconds Unix time of the last successful run of a collector.
# TYPE divy_collector_last_success_timestamp_seconds gauge
# HELP divy_collector_runs_total Collector runs by collector and result.
# TYPE divy_collector_runs_total counter
# HELP divy_http_requests_total HTTP requests served by the API, by route, method and status code.
# TYPE divy_http_requests_total counter
# HELP divy_http_request_duration_seconds HTTP request duration in seconds, by route and method.
# TYPE divy_http_request_duration_seconds histogram
```

promlint rules (client_golang 1.24.1, all nine defaults run by `promtool check metrics`) and how the catalogue complies:

| Rule | Message on violation | Catalogue compliance |
|---|---|---|
| `LintHelp` | `no help text` | every family has HELP (above) |
| `LintMetricUnits` | `use base unit "seconds" instead of "minutes"` | only base units appear: `_seconds` (`divy_uptime_seconds`, `probe_duration_seconds`, `*_timestamp_seconds`, `divy_http_request_duration_seconds`) |
| `LintCounter` | `counter metrics should have "_total" suffix` / `non-counter metrics should not have "_total" suffix` | counters all end in `_total`; **`github_stars_total` (brief) is renamed `github_stars`** because it is a gauge — the rename is the only catalogue change forced by the linter |
| `LintHistogramSummaryReserved` | `non-histogram metrics should not have "_bucket" suffix` / `… "_count" suffix` / `… "_sum" suffix` / `… "le" label` / `non-summary metrics should not have "quantile" label` | only `divy_http_request_duration_seconds` (a real histogram) uses `_bucket`/`_sum`/`_count`/`le` |
| `LintMetricTypeInName` | `metric name should not include type 'counter'` | no name contains `_counter`, `_gauge`, `_histogram`, `_summary` |
| `LintReservedChars` | `metric names should not contain ':'` | none |
| `LintCamelCase` | `metric names should be written in 'snake_case' not 'camelCase'` / `label names …` | all names and labels snake_case (`go_version`, not `goVersion`) |
| `LintUnitAbbreviations` | `metric names should not contain abbreviated units` | no `_ms`, `_kb`, `_sec`, … segments; `probe_http_status_code` is fine (`code` is not a unit) |
| `LintDuplicateMetric` | `metric not unique` | the custom collector emits one sample per `(metric, labels)`; `pypi_package_info` keeps exactly one `version` per package (Content X3) |

CI (target `promtool-check`, Repo §R.4), single paste, exit code 3 on any lint problem, 1 on a parse error:

```bash
cd api && go build -tags noweb -o ../bin/divy-noweb ./cmd/divy && (../bin/divy-noweb serve --addr 127.0.0.1:18080 --db "$(mktemp -d)/promtool.db" --content ../content & echo $! > /tmp/divy.pid) && until ../bin/divy-noweb ping --url http://127.0.0.1:18080/readyz; do sleep 1; done && curl -sf http://127.0.0.1:18080/metrics | go run github.com/prometheus/prometheus/cmd/promtool@v0.314.0 check metrics --extended; rc=$?; kill "$(cat /tmp/divy.pid)"; exit $rc
```

Unit-level: `api/internal/metrics/exposition_test.go` gathers the registry, runs `promlint.New(reader).Lint()` (same package promtool uses) and asserts zero problems, and parses the output with `expfmt.TextParser` to compare against `testdata/exposition.golden` family-by-family (order-independent).

## P.10 Test tables

Location: `api/internal/promql/{lexer,parser,eval}_test.go` (table-driven, `t.Run` per row) and `api/internal/server/promapi_test.go` (httptest). The same rows are mirrored in `api/tools/promql-oracle/oracle_test.go`, which runs them through Prometheus and diffs outputs (note P7).

### P.10.1 Parser cases

`want` is `Expr.String()`; `err` is the full error string. Rows marked ★ are Prometheus-verified outputs; rows marked ✚ are this subset's own errors.

| # | Input | Expected |
|---|---|---|
| 1 ★ | `github_commits_total` | `github_commits_total` |
| 2 ★ | `{__name__="github_commits_total"}` | `{__name__="github_commits_total"}` |
| 3 ★ | `github_merged_prs_total{org!="gradr",repo=~"k.*"}` | `github_merged_prs_total{org!="gradr",repo=~"k.*"}` |
| 4 ★ | `github_stars{repo!~"a\|b",}` | `github_stars{repo!~"a\|b"}` |
| 5 ★ | `{job=~".*"}` | err `1:1: parse error: vector selector must contain at least one non-empty matcher` |
| 6 ★ | `{}` | err `1:1: parse error: vector selector must contain at least one non-empty matcher` |
| 7 ★ | `github_commits_total{__name__="x"}` | err `1:1: parse error: metric name must not be set twice: "github_commits_total" or "x"` |
| 8 ★ | `github_commits_total[7d]` | `github_commits_total[1w]` |
| 9 ★ | `github_commits_total[1h30m]` | `github_commits_total[1h30m]` |
| 10 ★ | `github_commits_total[90s]` | `github_commits_total[1m30s]` |
| 11 ★ | `github_commits_total[1.5h]` | err `1:22: parse error: unknown unit "." in duration "1.5h"` |
| 12 ★ | `github_commits_total[0s]` | err `1:22: parse error: duration must be greater than 0` |
| 13 ★ | `github_commits_total[7d` | err `1:24: parse error: unclosed left bracket` |
| 14 ★ | `github_commits_total[1x]` | err `1:22: parse error: bad number or duration syntax: "1"` |
| 15 ★ | `1e3` / `0x1F` / `Inf` / `NaN` / `1_000` | `1000` / `31` / `+Inf` / `NaN` / `1000` |
| 16 ★ | `-github_stars` | `-github_stars` |
| 17 ★ | `- - 1` | `1` |
| 18 ★ | `1 + 2 * 3 ^ 2 ^ 1` | `1 + 2 * 3 ^ 2 ^ 1` (tree: `1 + (2 * (3 ^ (2 ^ 1)))`) |
| 19 ★ | `github_stars / github_followers` | `github_stars / github_followers` |
| 20 ★ | `divy_open_to_work == 1` | `divy_open_to_work == 1` |
| 21 ★ | `1 == 2` | err `1:3: parse error: comparisons between scalars must use BOOL modifier` |
| 22 ★ | `1 == bool 2` | `1 == bool 2` |
| 23 ★ | `github_stars + bool 1` | err `1:14: parse error: bool modifier can only be used on comparison operators` |
| 24 ★ | `sum by (org) (github_merged_prs_total)` | `sum by (org) (github_merged_prs_total)` |
| 25 ★ | `sum(github_merged_prs_total) by (org,)` | `sum by (org) (github_merged_prs_total)` |
| 26 ★ | `sum without (org) (github_merged_prs_total)` | `sum without (org) (github_merged_prs_total)` |
| 27 ★ | `sum(github_merged_prs_total) by ()` | `sum(github_merged_prs_total)` |
| 28 ★ | `sum()` | err `1:1: parse error: no arguments for aggregate expression provided` |
| 29 ★ | `sum(` | err `1:5: parse error: unclosed left parenthesis` |
| 30 ★ | `sum(github_stars, github_followers)` | err `1:1: parse error: wrong number of arguments for aggregate expression provided, expected 1, got 2` |
| 31 ★ | `rate(github_commits_total[7d])` | `rate(github_commits_total[1w])` |
| 32 ★ | `rate(github_commits_total)` | err `1:6: parse error: expected type range vector in call to function "rate", got instant vector` |
| 33 ★ | `rate(github_commits_total[7d], 1)` | err `1:1: parse error: expected 1 argument(s) in call to "rate", got 2` |
| 34 ★ | `increase(x[1d])`, `irate(x[1d])`, `delta(x[1d])`, `sum_over_time(x[1h])`, `avg_over_time(x[1h])`, `min_over_time(x[1h])`, `max_over_time(x[1h])`, `count_over_time(x[1h])`, `last_over_time(x[1h])` | same text |
| 35 ★ | `abs(x)`, `ceil(x)`, `floor(x)`, `round(x)`, `round(x, 0.5)`, `clamp_min(x, 0)`, `clamp_max(x, 10)`, `vector(1)`, `scalar(x)`, `time()` | same text |
| 36 ★ | `round(x, 1, 2)` | err `1:1: parse error: expected at most 2 argument(s) in call to "round", got 3` |
| 37 ★ | `time(x)` | err `1:1: parse error: expected 0 argument(s) in call to "time", got 1` |
| 38 ★ | `scalar(1)` | err `1:8: parse error: expected type instant vector in call to function "scalar", got scalar` |
| 39 ★ | `foo(x)` | err `1:1: parse error: unknown function with name "foo"` |
| 40 ★ | `holt_winters(x[1h], 0.5, 0.5)` | err `1:1: parse error: unknown function with name "holt_winters"` |
| 41 ★ | `"a string"` | `"a string"` (type string) |
| 42 ★ | `(github_stars` | err `1:14: parse error: unclosed left parenthesis` |
| 43 ★ | `github_stars)` | err `1:14: parse error: unexpected right parenthesis ')'` |
| 44 ★ | `` (empty) | err `unknown position: parse error: no expression found in input` |
| 45 ★ | `github_stars +` | err `1:15: parse error: unexpected end of input` |
| 46 ★ | `github stars` | err `1:8: parse error: unexpected identifier "stars"` |
| 47 ★ | `github_stars{repo="a" repo="b"}` | err `1:23: parse error: unexpected identifier "repo" in label matching, expected "," or "}"` |
| 48 ★ | `github_stars{repo=}` | err `1:19: parse error: unexpected "}" in label matching, expected string` |
| 49 ★ | `github_stars{repo="a"` | err `1:22: parse error: unexpected end of input inside braces` |
| 50 ★ | `github_stars{repo=~"("}` | err ``1:14: parse error: error parsing regexp: missing closing ): `(` `` |
| 51 ★ | `a[5m] + b[5m]` | err `1:1: parse error: binary expression must contain only scalar and instant vector types` |
| 52 ★ | `rate(x[5m])[5m]` | err `1:12: parse error: ranges only allowed for vector selectors` |
| 53 ★ | `count(github_stars) by (repo) > 0` | `count by (repo) (github_stars) > 0` |
| 54 ★ | `bool` | err `1:1: parse error: unexpected <bool>` |
| 55 ★ | `x and` | err `1:6: parse error: unexpected end of input` |
| 56 ✚ | `github_commits_total offset 1d` | err `1:22: parse error: offset modifier is not supported` |
| 57 ✚ | `github_commits_total @ 1609746000` | err `1:22: parse error: @ modifier is not supported` |
| 58 ✚ | `rate(github_commits_total[7d:1h])` | err `1:29: parse error: subqueries are not supported` |
| 59 ✚ | `rate(x[5m * 2])` | err `1:11: parse error: unexpected <op:*> in range selector, expected "]"` |
| 60 ✚ | `github_stars and github_followers` | err `1:14: parse error: set operator "and" is not supported` |
| 61 ✚ | `github_stars or vector(0)` / `a unless b` | err `… set operator "or" is not supported` / `… "unless" …` |
| 62 ✚ | `github_stars + on(repo) github_stars` | err `1:16: parse error: vector matching modifier "on" is not supported` |
| 63 ✚ | `github_stars + ignoring(repo) group_left github_followers` | err `1:16: parse error: vector matching modifier "ignoring" is not supported` |
| 64 ✚ | `topk(3, github_stars)` / `quantile(0.9, x)` / `count_values("v", x)` / `stddev(x)` | err `1:1: parse error: aggregation operator "topk" is not supported` (etc.) |
| 65 ✚ | `histogram_quantile(0.9, x)` / `label_join(x, "a", ",", "b")` / `absent(x)` / `predict_linear(x[1h], 3600)` / `changes(x[1h])` / `sort(x)` / `timestamp(x)` / `day_of_week()` / `clamp(x, 0, 10)` | err `1:1: parse error: function "histogram_quantile" is not supported` (etc.) |
| 66 ✚ | `{"a.b"="1"}` | err `1:2: parse error: unexpected string "a.b" in label matching, expected identifier or "}"` |
| 67 ✚ | `sum(rate(github_commits_total[7d])) > 20` | `sum(rate(github_commits_total[1w])) > 20` |
| 68 ✚ | `sum(increase(pypi_downloads_total{package="codemind-ci"}[1d]))` | `sum(increase(pypi_downloads_total{package="codemind-ci"}[1d]))` |
| 69 ✚ | `lfx_applications{status="pending"} > 0` | `lfx_applications{status="pending"} > 0` |
| 70 ✚ | `SUM(X)` / `Rate(x[5m])` | `sum(X)` / err `1:1: parse error: unknown function with name "Rate"` (keywords case-insensitive, function names case-sensitive, as in Prometheus) |

### P.10.2 Evaluator cases

Fixture (`api/internal/promql/testdata/fixture.txt`, promqltest syntax; the Go test loads it into a temp SQLite `samples` table with `ts_ms = day × 86 400 000`, `_` = no sample, lookback set per row):

```
load 1d
  github_commits_total                         0 3 3 7 12 12 20
  pypi_downloads_total{package="codemind-ci"}  _ _ _ 100 130 190
  reset_total                                  10 14 3 9
  github_merged_prs_total{org="kubernetes"}    5 6 6 7
  github_merged_prs_total{org="kubeflow"}      1 1 2 2
  github_merged_prs_total{org="gradr"}         0 0 3 3
  github_stars{repo="codemind"}                12 12 13 13
  github_stars{repo="savely"}                  40 41 41 42
  gauge_neg                                    -2.5 1.25 NaN 3.75
  divy_open_to_work                            1 1 1 1
  lfx_applications{status="pending"}           1 1 1 1
  probe_success{target="pypi"}                 1 0 1 1
```

`@Nd` = evaluation at `N × 86400` s. Outputs use Prometheus' `Value.String()` form; every row is Prometheus-verified.

| # | Query @ time (lookback) | Expected | Derivation |
|---|---|---|---|
| E1 | `github_commits_total` @6d (5m) | `{__name__="github_commits_total"} => 20 @[518400000]` | newest sample ≤ t |
| E2 | `github_commits_total` @6d+4m (5m) / @6d+5m (5m) / @6d+5m (2h) | `20` / empty / `20` | sample must satisfy `t_s > t − lookback`; at exactly +5 m it does not; default 2 h lookback keeps it |
| E3 | `github_commits_total[2d]` @2d | `3 @[86400000], 3 @[172800000]` | left-open: the sample at 0 d is excluded |
| E4 | `count_over_time(github_commits_total[2d])` @2d | `{} => 2` | |
| E5 | `increase(github_commits_total[7d])` @6d | `{} => 20` | window (−1d, 6d] → 7 samples; Δ=20; durationToStart=86400, durationToEnd=0, sampledInterval=518400, avg=86400, threshold=95040 → durationToStart kept; counter zero-clamp: durationToZero = 518400×0/20 = 0 → durationToStart=0; factor = 518400/518400 = 1 |
| E6 | `rate(github_commits_total[7d])` @6d | `{} => 0.00003306878306878307` | E5 ÷ 604800 |
| E7 | `increase(pypi_downloads_total[3d])` @5d | `{package="codemind-ci"} => 135` | samples 3d,4d,5d; Δ=90; durationToStart=86400 < threshold 95040 → kept; durationToZero = 172800×100/90 = 192000 > 86400 → unchanged; factor = (172800+86400+0)/172800 = 1.5 |
| E8 | `rate(pypi_downloads_total[3d])` @5d | `… => 0.0005208333333333333` | 135 ÷ 259200 |
| E9 | `increase(reset_total[3d])` @3d | `{} => 13.5` | window (0,3d] → 14,3,9; Δ = 9−14 = −5, reset at 2d adds 14 → 9; durationToStart=86400 kept; durationToZero = 172800×14/9 = 268800 → unchanged; factor 1.5 |
| E10 | range `increase(github_commits_total[2d])` start 1d end 6d step 1d | `{} => 3 @1d, 0 @2d, 7 @3d, 10 @4d, 0 @5d, 16 @6d` | each window holds 2 samples, sampledInterval=86400, durationToStart=86400, durationToEnd=0 → factor 2 unless the zero-clamp applies: @1d first=0 → durationToZero 0 → factor 1 → 3; @3d Δ=4, first=3 → durationToZero = 86400×3/4 = 64800 → factor 1.75 → 7; @4d Δ=5, first=7 → 120960 > 86400 → factor 2 → 10; @6d Δ=8, first=12 → factor 2 → 16; Δ=0 rows stay 0. This is why `rate(x[2d]) × 86400` over daily samples equals the day's increase once counters are large (note P1) |
| E11 | `increase(reset_total[1d])` @3d / `delta(github_stars[1d])` @3d / `increase(github_commits_total[36h])` @1.5d | empty | one sample in the window |
| E12 | `irate(reset_total[3d])` @3d / `irate(reset_total[2d])` @2d | `{} => 0.00006944444444444444` / `{} => 0.00003472222222222222` | (9−3)/86400; reset case uses the last value 3/86400 |
| E13 | `delta(gauge_neg[3d])` @3d | `{} => 3.75` | 3.75−1.25 = 2.5 × factor 1.5 (no reset logic, no zero-clamp); the NaN in the middle is irrelevant |
| E14 | `sum(github_merged_prs_total)` / `sum by (org) (…)` / `sum without (org) (…)` @3d | `{} => 12` / `{org="gradr"} => 3, {org="kubeflow"} => 2, {org="kubernetes"} => 7` (sorted) / `{} => 12` | |
| E15 | `min(github_stars)` / `max` / `avg` / `count` @3d | `13` / `42` / `27.5` / `2` (all `{}`) | |
| E16 | `max(gauge_neg)` / `sum(gauge_neg)` @2d | `{} => NaN` / `{} => NaN` | the only sample is NaN |
| E17 | `github_stars * 2` / `2 * github_stars` / `github_stars + github_stars` @3d | `{repo="codemind"} => 26, {repo="savely"} => 84` | `__name__` dropped |
| E18 | `github_stars > 20` / `20 < github_stars` @3d | `{__name__="github_stars", repo="savely"} => 42` | filter keeps name and value |
| E19 | `github_stars > bool 20` @3d | `{repo="codemind"} => 0, {repo="savely"} => 1` | |
| E20 | `1 == bool 1` / `3 + 4 * 2` / `2 ^ 3 ^ 2` / `7 % 3` / `1 / 0` / `-1 / 0` / `0 / 0` @3d | `scalar: 1` / `11` / `512` / `1` / `+Inf` / `-Inf` / `NaN` | |
| E21 | `divy_open_to_work == 1` / `lfx_applications{status="pending"} > 0` @3d | `{__name__="divy_open_to_work"} => 1` / `{__name__="lfx_applications", status="pending"} => 1` | alert expressions |
| E22 | `sum(increase(github_commits_total[7d])) > 20` / `> 19` @6d | empty / `{} => 20` | E5 filtered |
| E23 | `abs(gauge_neg)` @0d / `ceil(gauge_neg)` @1d / `floor` @1d / `round` @1d / `round(gauge_neg, 0.5)` @3d / `round(github_stars / 3, 0.01)` @3d | `{} => 2.5` / `2` / `1` / `1` / `4` / `{repo="codemind"} => 4.33, {repo="savely"} => 14` | |
| E24 | `clamp_min(gauge_neg, 0)` @0d / `clamp_max(gauge_neg, 1)` @1d | `{} => 0` / `{} => 1` | |
| E25 | `time()` / `vector(1)` / `scalar(divy_open_to_work)` / `scalar(github_stars)` / `scalar(nonexistent)` @3d | `scalar: 259200` / `{} => 1` / `scalar: 1` / `scalar: NaN` / `scalar: NaN` | |
| E26 | `sum_over_time(probe_success[3d])` / `avg_over_time` / `min_over_time` / `max_over_time` / `count_over_time` / `last_over_time` @3d | `{target="pypi"} => 2` / `0.6666666666666666` / `0` / `1` / `3` / `{__name__="probe_success", target="pypi"} => 1` | samples 0,1,1; only `last_over_time` keeps the name |
| E27 | `avg_over_time(gauge_neg[3d])` / `max_over_time(gauge_neg[3d])` @3d | `{} => NaN` / `{} => 3.75` | NaN propagates in sums, not in max |
| E28 | `nonexistent` / `sum(nonexistent)` / `count(nonexistent)` / `rate(nonexistent[1d])` / `github_stars + github_followers_nonexistent` / `github_stars > 100` @3d | empty vector | |
| E29 | `sum(github_stars) + sum(github_merged_prs_total)` @3d | `{} => 67` | `{}` matches `{}` |
| E30 | `{target="pypi"} + {target="pypi"}` with an extra fixture series `probe_duration_seconds{target="pypi"} 0.2 0.2 0.2 0.2` | error `multiple matches for labels: many-to-one matching must be explicit (group_left/group_right)` | LHS signature repeats |
| E31 | range `github_commits_total` 0d..3d step 12h (5m) | `0 @0, 3 @1d, 3 @2d, 7 @3d` | half-day steps find no sample within 5 m |
| E32 | range `1+1` 1s..4s step 1s | `{} => 2 @[1000], 2 @[2000], 2 @[3000], 2 @[4000]` | Grafana health check shape |
| E33 | range `github_stars > 40` 0d..3d step 1d | `{__name__="github_stars", repo="savely"} => 41 @1d, 41 @2d, 42 @3d` | |
| E34 | range `time()` 0d..1d step 12h | `{} => 0 @0, 43200 @12h, 86400 @1d` | scalar → matrix with `{}` |
| E35 | range `github_stars[1d]` 0d..1d step 1d | 400 `invalid expression type "range vector" for range query, must be Scalar or instant Vector` | |

### P.10.3 HTTP cases

Server under test: `httptest.NewServer` with the fixture store, `QUERY_LOOKBACK_DELTA=2h`, `QUERY_TIMEOUT=2s`, rate limit disabled except H14.

| # | Request | Expected status + body (envelope) |
|---|---|---|
| H1 | `GET /api/v1/query?query=up` | 200 `{"status":"success","data":{"resultType":"vector","result":[]}}` |
| H2 | `POST /api/v1/query` `application/x-www-form-urlencoded` `query=1%2B1&time=4` | 200 `{"status":"success","data":{"resultType":"scalar","result":[4,"2"]}}` |
| H3 | `GET /api/v1/query` (no `query`) | 400 `{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": unknown position: parse error: no expression found in input"}` |
| H4 | `GET /api/v1/query?query=sum(` | 400 `… "error":"invalid parameter \"query\": 1:5: parse error: unclosed left parenthesis"` |
| H5 | `GET /api/v1/query?query=x%20offset%201d` | 400 `… "error":"invalid parameter \"query\": 1:3: parse error: offset modifier is not supported"` |
| H6 | `GET /api/v1/query?query=up&time=yesterday` | 400 `… "error":"invalid parameter \"time\": cannot parse \"yesterday\" to a valid timestamp"` |
| H7 | `GET /api/v1/query_range?query=up&start=10&end=5&step=1` | 400 `… "error":"invalid parameter \"end\": end timestamp must not be before start time"` |
| H8 | `…&start=0&end=10&step=0` / `step=abc` | 400 `invalid parameter "step": zero or negative query resolution step widths are not accepted. Try a positive integer` / `invalid parameter "step": cannot parse "abc" to a valid duration` |
| H9 | `…&start=0&end=100000&step=1` | 400 `exceeded maximum resolution of 11,000 points per timeseries. Try decreasing the query resolution (?step=XX)` (11 001 points passes; 11 002 fails) |
| H10 | `GET /api/v1/query_range?query=up%5B5m%5D&start=0&end=60&step=15` | 400 `invalid parameter "query": invalid expression type "range vector" for range query, must be Scalar or instant Vector` |
| H11 | `GET /api/v1/query_range?query=github_commits_total&start=2026-09-01T00:00:00Z&end=1788566400.5&step=1d` (mixed formats, POST equivalent too) | 200 matrix (RFC 3339 and float accepted in the same request) |
| H12 | `GET /api/v1/series` / `?match[]={job=~".*"}` / `?match[]=github_stars{` | 400 `no match[] parameter provided` / `invalid parameter "match[]": match[] must contain at least one non-empty matcher` / `invalid parameter "match[]": 1:14: parse error: unexpected end of input inside braces` |
| H13 | `GET /api/v1/label/1bad/values` / `POST /api/v1/label/repo/values` | 400 `invalid label name: "1bad"` / 405 `{"status":"error","errorType":"bad_data","error":"method not allowed"}` + `Allow: GET` |
| H14 | 101 requests in 1 s from one IP to `/api/v1/query` | the 101st: 429 `{"status":"error","errorType":"unavailable","error":"rate limit exceeded"}` + `Retry-After` |
| H15 | `GET /api/v1/query?query=count_over_time(github_commits_total[1y])&timeout=0.001` (store fault injected to sleep) | 503 `{"status":"error","errorType":"timeout","error":"query timed out in query execution"}` |
| H16 | `GET /api/v1/status/buildinfo` | 200 `data` has exactly the six keys, `goVersion` = `runtime.Version()` |
| H17 | `GET /api/v1/rules?type=record` / `?type=alert` | 200 `{"status":"success","data":{"groups":[]}}` / the full Content §C.7 document |
| H18 | `GET /api/v1/alerts` | 200 `{"status":"success","data":{"alerts":[]}}` |
| H19 | `GET /api/v1/query_exemplars?query=test&start=1788600600000&end=1788602400000` | 200 `{"status":"success","data":[]}` |
| H20 | `GET /api/v1/nope` / `DELETE /api/v1/query` | 404 `{"status":"error","errorType":"not_found","error":"path not found"}` / 405 |
| H21 | `GET /api/v1/query?query=github_stars` twice within 15 s | second response identical, `ETag` equal, `Cache-Control: public, max-age=15`, both carry distinct `X-Divy-Trace-Id` |
| H22 | `GET /api/v1/query?query=up&lookback_delta=5m` / `&lookback_delta=x` | 200 / 400 `error parsing lookback delta duration: cannot parse "x" to a valid duration` |

## P.11 What each phase owes this section

| Phase | Deliverable |
|---|---|
| 1 | `api/internal/promql` (lexer, parser, AST + printer, evaluator, functions, `Storage` interface with SQLite and live-series implementations), `api/internal/server/promapi.go` (all §P.7.2 endpoints, envelope, status mapping, param parsing), `api/internal/metrics` (registry, custom collector, staleness cut-off), `QUERY_*` env vars in `config` and `.env.example` (`QUERY_LOOKBACK_DELTA=2h`, `QUERY_TIMEOUT=30s`, `QUERY_MAX_SAMPLES=2000000`, `QUERY_MAX_CONCURRENCY=20`), the three test tables, `api/tools/promql-oracle` + `make promql-oracle`, `make promtool-check` passing, `docs/promql-subset.md` = §P.1–P.6 verbatim. |
| 2 | `content/panels.yaml` and `content/alerts.yaml` expressions parse under this subset (`divy validate` rule `panels.expr` uses this parser); the `pypi-downloads` panel uses the P1 expression. |
| 3 | `web/src/lib/api/prom.ts`: sends `lookback_delta` only when a panel overrides it; computes `step` for the five ranges (24h → 5m, 7d → 1h, 30d → 6h, 1y → 1d, all → 1d) keeping ≤ 11 000 points; "View query" curl matches Content §C.6. |
| 5 | README "Add divy.dev as a Prometheus data source" walk-through = §P.8 rows 1–3 with expected screenshots' text; CI runs `promtool-check` and `promql-oracle`. |
