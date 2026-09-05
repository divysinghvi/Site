# Facts — LogQL subset, Loki HTTP API, Jaeger API, OTel self-tracing, aux endpoints

Verified 2026-09-05. `grafana.com`, `jaegertracing.io` and `caniuse.com` are blocked by the egress proxy; the same documents were read from their source repositories on GitHub (raw.githubusercontent.com / GitHub code search) — the URLs below are the ones actually fetched.

## Loki HTTP API (docs source: grafana/loki `docs/sources/reference/loki-http-api.md`)

URL: https://raw.githubusercontent.com/grafana/loki/main/docs/sources/reference/loki-http-api.md

| # | Fact |
|---|------|
| L1 | `GET /loki/api/v1/query_range` params: `query` (required), `limit` (default **100**), `start` (default one hour ago), `end` (default now) — "nanosecond Unix epoch, RFC3339, or float seconds" — `since` (duration, start = end − since), `step` (duration or float seconds; default dynamic), `interval` (stream responses only), `direction` = `forward` \| `backward` (default **backward**). |
| L2 | Streams response: `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"<label>":"<value>"},"values":[["<nanosecond unix epoch>","<log line>"]]}],"stats":{…}}}`. Timestamps in `values` are **strings** of nanoseconds. |
| L3 | Matrix response: `{"resultType":"matrix","result":[{"metric":{…},"values":[[1588889221,"137.95"],…]}]}` — timestamp is a JSON **number** (unix seconds), value a **string**. |
| L4 | `stats` documented sections and keys: `ingester{compressedBytes,decompressedBytes,decompressedLines,headChunkBytes,headChunkLines,totalBatches,totalChunksMatched,totalDuplicates,totalLinesSent,totalReached}`, `store{compressedBytes,decompressedBytes,decompressedLines,chunksDownloadTime,totalChunksRef,totalChunksDownloaded,totalDuplicates}`, `summary{bytesProcessedPerSecond,execTime,linesProcessedPerSecond,queueTime,totalBytesProcessed,totalLinesProcessed}`. |
| L5 | `GET /loki/api/v1/query`: `query`, `limit` (100), `time` (default now), `direction`; `resultType` is `vector` (metric query) or `streams` (log query); vector items `{"metric":{…},"value":[<unix seconds>,"<value>"]}`. |
| L6 | `GET /loki/api/v1/labels` (`start` default 6h ago, `end`, `since`, `query`) → `{"status":"success","data":["<label_name>",…]}`; `GET /loki/api/v1/label/{name}/values` same params → `{"status":"success","data":["<value>",…]}`. |
| L7 | `GET` and `POST /loki/api/v1/series`: `match[]=<selector>` repeatable, `start`, `end`, `since` → `{"status":"success","data":[{"<label>":"<value>"},…]}`. |
| L8 | `GET /loki/api/v1/index/stats` (`query`, `start`, `end` required) → `{"streams":N,"chunks":N,"entries":N,"bytes":N}`. |
| L9 | `GET /loki/api/v1/index/volume` (`query`, `start`, `end`, `limit` default 100, `targetLabels`, `aggregateBy` = `series` \| `labels`) returns a Prometheus-style vector; `/index/volume_range` adds `step`. |
| L10 | `GET /loki/api/v1/status/buildinfo` fields: `version`, `revision`, `branch`, `buildDate`, `buildUser`, `goVersion`. |
| L11 | `GET /loki/api/v1/tail` is a WebSocket returning `{"streams":[…],"dropped_entries":[{"labels":{…},"timestamp":"<ns>"}]}`. |

Source `pkg/loghttp/params.go` — https://raw.githubusercontent.com/grafana/loki/main/pkg/loghttp/params.go

| # | Fact |
|---|------|
| L12 | `parseTimestamp`: value containing `.` → `strconv.ParseFloat` seconds; else `strconv.ParseInt` → **nanoseconds**; else `time.Parse(time.RFC3339Nano)`. |
| L13 | `step`: `strconv.ParseFloat` seconds or `model.ParseDuration`; default step = `max(floor((end−start).Seconds()/250), 1)` seconds. |
| L14 | `defaultQueryLimit = 100`, `defaultDirection = BACKWARD`, `defaultSince = 1h`; with `since`, `start = endOrNow − since`. |

Source `pkg/util/server/error.go` — https://raw.githubusercontent.com/grafana/loki/main/pkg/util/server/error.go

| # | Fact |
|---|------|
| L15 | Loki query errors are **plain text**: `Content-Type: text/plain; charset=utf-8`, `X-Content-Type-Options: nosniff`, body = `err.Error()`. No JSON envelope. Status: parse/limit errors → 400, timeouts → 504, rate limit → 429, other → 500. |

Limits — GitHub code search in grafana/loki `docs/sources/shared/configuration.md`, `docs/sources/query/logcli/getting-started.md`, `docs/sources/shared/troubleshoot-query.md`

| # | Fact |
|---|------|
| L16 | `max_entries_limit_per_query` default **5000** ("By default, this limit is 5000 lines"); error text: `max entries limit per query exceeded, limit > max_entries_limit_per_query (<requested> > <limit>)`. |

## LogQL semantics (docs source: grafana/loki `docs/sources/query/log_queries/_index.md`, `metric_queries.md`)

URLs: https://raw.githubusercontent.com/grafana/loki/main/docs/sources/query/log_queries/_index.md , https://raw.githubusercontent.com/grafana/loki/main/docs/sources/query/metric_queries.md

| # | Fact |
|---|------|
| Q1 | Stream selector operators `=`, `!=`, `=~`, `!~`; selector regexes "must match against the entire string, including newlines" (fully anchored). |
| Q2 | Line filters `\|=` contains, `!=` does not contain, `\|~` regex match, `!~` no regex match; line-filter regexes are **not anchored**. |
| Q3 | `\| json`: nested properties flattened with `_` (`request.time` → `request_time`); "Arrays are skipped"; invalid JSON adds an `__error__` label and does not filter the line; label names sanitized to Prometheus rules (invalid characters replaced). `\| json a="b[0]"` expression form exists. |
| Q4 | Label filters: string ops `=`, `!=`, `=~`, `!~`; numeric ops `==`, `!=`, `>`, `>=`, `<`, `<=` on duration / bytes / number literals; `and`, `or` (`and` binds tighter); comma also means `and`. Conversion failure: "the log line is not filtered and an `__error__` label is added". |
| Q5 | Metric functions: `rate(log-range)` = entries per second; `count_over_time(log-range)` = entries per stream in range; also `bytes_rate`, `bytes_over_time`, `absent_over_time`. Syntax `count_over_time({job="mysql"}[5m])`; `offset` goes right after the range: `[5m] offset 5m`. |
| Q6 | Aggregation operators: `sum, avg, min, max, stddev, stdvar, count, topk, bottomk, sort, sort_desc`; grouping syntax `<aggr-op>([parameter,] <vector expression>) [without\|by (<label list>)]` (both `sum by (x) (…)` and `sum(…) by (x)`). |

Source `pkg/logql/syntax/parser.go`, `pkg/logql/log/error.go`, `pkg/logqlmodel/error.go` (GitHub code search)

| # | Fact |
|---|------|
| Q7 | Empty-matcher rule error text (verbatim): `queries require at least one regexp or equality matcher that does not have an empty-compatible value. For instance, app=~".*" does not meet this requirement, but app=~".+" will`. Tests confirm `{env=~".*"}` and `{env!="dev"}` are rejected. |
| Q8 | `__error__` values: `JSONParserErr`, `LogfmtParserErr`, `SampleExtractionErr`, `LabelFilterErr`; `__error_details__` carries the message (e.g. `Value looks like object, but can't find closing '}' symbol`). |
| Q9 | Parse error format: `parse error at line %d, col %d: %s` (e.g. `parse error at line 1, col 6: literal not terminated`, `… syntax error: unexpected %, expecting } or ,`). |

## Grafana Loki data source (standalone plugin grafana/grafana-loki-datasource)

URLs: https://raw.githubusercontent.com/grafana/grafana-loki-datasource/main/src/datasource.ts , https://raw.githubusercontent.com/grafana/grafana-loki-datasource/main/src/LanguageProvider.ts , https://raw.githubusercontent.com/grafana/grafana-loki-datasource/main/pkg/loki/healthcheck.go , https://raw.githubusercontent.com/grafana/grafana-loki-datasource/main/src/plugin.json

| # | Fact |
|---|------|
| G1 | Plugin id `loki`, `grafanaDependency ">=12.3.0-0"` (the data source is a standalone plugin from Grafana 12.3). |
| G2 | **Save & test** = Go backend `CheckHealth`: instant query `vector(1)+vector(1)`, `Step "1s"`, `QueryType "instant"`; expects 1 frame, 2 fields, value `2`; messages `Data source successfully connected.` / `Unable to connect with Loki. Please check the server logs for more details.` |
| G3 | `DEFAULT_MAX_LINES = 1000` (query_range `limit`), `DEFAULT_MAX_LINES_SAMPLE = 10`. |
| G4 | Log-volume histogram (Explore) = supplementary query `` `sum by (${field}) (count_over_time(${expr}[$__auto]))` `` (`$__auto` is resolved by Grafana before sending). |
| G5 | LanguageProvider resources: `labels` (start,end in ns), `label/${label}/values` (start,end,`query`), `series` (`match[]`,start,end), `detected_fields`, `detected_field/${label}/values`. Errors: labels → `[]`, label values → caught, logged, `[]`; series → `{}`; detected_* → rejected or `[]` depending on `throwError`. |
| G6 | `index/stats` used by `getQueryStats()`; failures are swallowed (`catch (e) { break; }`). |

## Jaeger

URLs: https://raw.githubusercontent.com/jaegertracing/jaeger/main/internal/uimodel/model.go , https://raw.githubusercontent.com/jaegertracing/jaeger/main/cmd/jaeger/internal/extension/jaegerquery/internal/http_handler.go , https://raw.githubusercontent.com/jaegertracing/documentation/main/content/docs/v2/2.5/architecture/apis.md

| # | Fact |
|---|------|
| J1 | UI model (`internal/uimodel/model.go`): `Trace{traceID, spans[], processes{}, warnings}`; `Span{traceID, spanID, parentSpanID(omitempty), flags(omitempty), operationName, references[], startTime (µs since epoch, uint64), duration (µs), tags[], logs[], processID(omitempty), process(omitempty), warnings}`; `Reference{refType, traceID, spanID}`; `Process{serviceName, tags[]}`; `Log{timestamp, fields[]}`; `KeyValue{key, type(omitempty), value}`. |
| J2 | `ReferenceType`: `CHILD_OF`, `FOLLOWS_FROM`. `ValueType`: `string`, `bool`, `int64`, `float64`, `binary`. |
| J3 | Envelope: `structuredResponse{data, total, limit, offset, errors[]}`; `structuredError{code(omitempty), msg, traceID(omitempty)}`. `GET /api/traces/{traceID}` → 404 with `spanstore.ErrTraceNotFound` when missing; malformed id → 400. Routes registered under prefix `api`: `/traces/{traceID}`, `/archive/{traceID}`, `/transform`, `/dependencies`, `/deep-dependencies`, `/metrics/*`, `/quality-metrics`. |
| J4 | Docs (v2.5 `architecture/apis.md`): port 16686 HTTP `/api/*` = "Internal (unofficial) JSON API", status **Internal**: "This JSON API is intentionally undocumented and subject to change"; `/api/v3/*` (OTLP-based JSON, has an OpenAPI file) and gRPC `jaeger.api_v3.QueryService` are the stable APIs. |

Grafana Jaeger data source (standalone plugin grafana/grafana-jaeger-datasource) — https://raw.githubusercontent.com/grafana/grafana-jaeger-datasource/main/pkg/jaeger/jaeger.go , https://raw.githubusercontent.com/grafana/grafana-jaeger-datasource/main/pkg/jaeger/client.go , GitHub code search in `pkg/jaeger/client_test.go`

| # | Fact |
|---|------|
| J5 | **Save & test** = `CheckHealth` → `JaegerClient.Services()` = `GET <url>/api/services`, decoded into `{Data []string}`; success message `Data source is working`, failure = the error text. |
| J6 | Operations: `GET /api/services/<service>/operations`. Search: `GET /api/traces?service=…&operation=…&tags=<JSON object>&minDuration=1s&maxDuration=5s&limit=10&start=<µs>&end=<µs>` (test URL: `end=1738368000000000&start=1735689600000000` = microseconds). Trace: `GET /api/traces/<id>` decoded into `{Data []Trace}`, first element used. Non-2xx → error. |

## OpenTelemetry Go

URLs: https://pkg.go.dev/go.opentelemetry.io/otel/sdk/trace , https://raw.githubusercontent.com/open-telemetry/opentelemetry-go/main/sdk/trace/span.go , https://raw.githubusercontent.com/open-telemetry/opentelemetry-go/main/sdk/trace/sampling.go , https://raw.githubusercontent.com/open-telemetry/opentelemetry-go/main/sdk/trace/event.go , https://pkg.go.dev/go.opentelemetry.io/otel/trace , https://pkg.go.dev/go.opentelemetry.io/otel/codes , https://pkg.go.dev/go.opentelemetry.io/otel/sdk/resource , https://pkg.go.dev/go.opentelemetry.io/otel@v1.46.0/semconv , https://pkg.go.dev/go.opentelemetry.io/otel/semconv/v1.37.0

| # | Fact |
|---|------|
| O1 | Versions: `go.opentelemetry.io/otel` / `sdk` / `trace` **v1.46.0** (2026-08-25); newest semconv package in that module: `go.opentelemetry.io/otel/semconv/v1.43.0`. |
| O2 | `type SpanExporter interface { ExportSpans(ctx context.Context, spans []ReadOnlySpan) error; Shutdown(ctx context.Context) error }`. |
| O3 | `ReadOnlySpan`: `Name() string`, `SpanContext() trace.SpanContext`, `Parent() trace.SpanContext` (invalid if no parent), `SpanKind() trace.SpanKind`, `StartTime()/EndTime() time.Time`, `Attributes() []attribute.KeyValue`, `Links() []Link`, `Events() []Event`, `Status() Status`, `InstrumentationScope() instrumentation.Scope`, `Resource() *resource.Resource`, `DroppedAttributes()/DroppedLinks()/DroppedEvents()/ChildSpanCount() int`. "methods may be added to this interface in minor releases". |
| O4 | `type Event struct { Name string; Attributes []attribute.KeyValue; DroppedAttributeCount int; Time time.Time }`; `type Status struct { Code codes.Code; Description string }`. |
| O5 | `codes.Code`: `Unset = 0`, `Error = 1`, `Ok = 2` — the doc notes OTLP uses different numbers (OTLP: OK = 1, ERROR = 2); "The value of this enum is only relevant to the internals of the Go SDK". |
| O6 | Samplers: `type Sampler interface { ShouldSample(parameters SamplingParameters) SamplingResult; Description() string }`; `SamplingParameters{ParentContext, TraceID, Name, Kind, Attributes, Links}`; `SamplingResult{Decision, Attributes, Tracestate}`; `Drop`, `RecordOnly`, `RecordAndSample`; `AlwaysSample()`, `NeverSample()`, `TraceIDRatioBased(fraction)`, `ParentBased(root, opts...)`. |
| O7 | `NewBatchSpanProcessor` defaults: MaxQueueSize 2048, BatchTimeout 5000 ms, ExportTimeout 30000 ms, MaxExportBatchSize 512; `WithBlocking()` optional (otherwise spans are dropped when the queue is full). `NewSimpleSpanProcessor` "not recommended for production use". `NewTracerProvider` options `WithBatcher`, `WithSyncer`, `WithSampler`, `WithResource`, `WithSpanProcessor`, `WithIDGenerator`; `TracerProvider.ForceFlush(ctx)` and `Shutdown(ctx)` exist. |
| O8 | API: `SpanFromContext(ctx) Span`, `SpanContextFromContext(ctx) SpanContext`; `SpanContext.TraceID()/SpanID()/IsSampled()`; `TraceID.String()`/`SpanID.String()` = lowercase hex, 32 / 16 chars; `Span.SetName`, `SetAttributes`, `SetStatus(code, description)`, `AddEvent`, `End`; `Tracer.Start(ctx, name, opts...)`; `SpanKindServer/Client/Internal` with `String()`. |
| O9 | Resource: `resource.NewWithAttributes(schemaURL string, attrs ...attribute.KeyValue) *Resource`; `resource.Default()` carries `service.name = "unknown_service:<exe>"` + `telemetry.sdk.*`; `resource.Merge(a, b)` — b wins on conflicts; `resource.New(ctx, WithFromEnv() …)` reads `OTEL_RESOURCE_ATTRIBUTES` / `OTEL_SERVICE_NAME`. |
| O10 | semconv helpers (v1.37.0 page; same names in v1.43.0): `HTTPRoute(string)`, `HTTPResponseStatusCode(int)`, `URLPath`, `URLScheme`, `ServerAddress`, `ServiceName`, `ServiceVersion`, `ClientAddress`, `UserAgentOriginal`, `NetworkProtocolVersion` return `attribute.KeyValue`. |

otelhttp — https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp , https://raw.githubusercontent.com/open-telemetry/opentelemetry-go-contrib/main/instrumentation/net/http/otelhttp/handler.go , https://raw.githubusercontent.com/open-telemetry/opentelemetry-go-contrib/main/instrumentation/net/http/otelhttp/internal/semconv/server.go

| # | Fact |
|---|------|
| O11 | `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` **v0.71.0** (2026-08-26). `NewHandler(handler http.Handler, operation string, opts ...Option) http.Handler`; `NewMiddleware(operation string, opts ...Option) func(http.Handler) http.Handler`; options `WithSpanNameFormatter(func(operation string, r *http.Request) string)`, `WithFilter(Filter)` (`type Filter func(*http.Request) bool`, true = trace; a rejected request is passed straight through), `WithServerName`, `WithPublicEndpointFn(func(*http.Request) bool)` (link instead of child for incoming context), `WithTracerProvider`, `WithPropagators`, `WithMeterProvider`. `WithMetricAttributesFn` deprecated in favour of `Labeler`. |
| O12 | handler.go: span started with `trace.SpanKindServer`, name = `spanNameFormatter(operation, r)`; after the handler, if `r.Pattern` is set, `span.SetName(...)` is re-run (Go 1.22 ServeMux patterns); response status recorded via `ResponseTraceAttrs`. |
| O13 | internal/semconv/server.go imports **`go.opentelemetry.io/otel/semconv/v1.43.0`** only (no legacy `http.method`/`http.status_code` keys). Request attrs: `server.address`, `http.request.method`, `url.scheme`, `server.port` (cond.), `http.request.method_original` (cond.), `network.peer.address`, `network.peer.port`, `user_agent.original`, `client.address`, `url.path`, `network.protocol.name`, `network.protocol.version`, `http.route` (from `r.Pattern` via `Route()`). Response attrs: `http.request.body.size`, `http.response.body.size`, `http.response.status_code`. `RequestTraceAttrsOpts.HTTPClientIP` → `http.client_ip`. The only remaining mention of `OTEL_SEMCONV_STABILITY_OPT_IN` in that package is a benchmark comment. |

chi — https://pkg.go.dev/github.com/go-chi/chi/v5#Context.RoutePattern ; otelchi — https://raw.githubusercontent.com/riandyrn/otelchi/master/middleware.go

| # | Fact |
|---|------|
| O14 | chi v5.3.2: `Context.RoutePattern()` "builds the routing pattern string for the particular request, at the particular point during routing" and "should only be accessed after calling the next handler"; `RouteContext(ctx) *Context`; `Mux.Use` middlewares run before route matching. |
| O15 | otelchi reads `chi.RouteContext(r.Context()).RoutePattern()` **after** `next.ServeHTTP`, then `span.SetName(...)` and `semconv.HTTPRoute(routePattern)`; writes the trace id header with `w.Header().Add(key, span.SpanContext().TraceID().String())` before calling next. |

OTLP → Jaeger mapping (OpenTelemetry Collector translator, the reference implementation Jaeger itself uses) — https://raw.githubusercontent.com/open-telemetry/opentelemetry-collector-contrib/main/pkg/translator/jaeger/traces_to_jaegerproto.go

| # | Fact |
|---|------|
| O16 | SpanKind → tag `span.kind` ∈ `client`,`server`,`producer`,`consumer`,`internal`. Status → tags `otel.status_code` (`OK`/`ERROR`), `error=true` (bool) for ERROR, `otel.status_description`. Scope → `otel.scope.name`, `otel.scope.version`. Events → logs; event name becomes field `event` (an existing `event` attribute is not de-duplicated — both are emitted). Parent → `CHILD_OF` reference; links → `FOLLOWS_FROM`. `service.name` → `Process.ServiceName`, other resource attributes → process tags. Attribute types string/bool/int64/float64 map directly; maps and slices → JSON string. |

## UNVERIFIED

- Grafana Loki data source **supported Loki version range** (docs page blocked; `plugin.json` states only the Grafana dependency `>=12.3.0-0`).
- Loki's rule that an extracted label colliding with a stream label is renamed `<key>_extracted` (remembered from the docs; the fetched summary did not include it). The plan adopts its own collision rule (§L.1.6) and says so.
- `otelhttp.WithPublicEndpoint()` (no-argument form): only `WithPublicEndpointFn` appeared in the fetched summary. The plan uses `WithPublicEndpointFn(func(*http.Request) bool { return true })`, which is verified.
- Jaeger v2 `main` registration of the internal `/api/services`, `/api/services/{service}/operations` and `/api/traces` (search) routes: not located in the fetched handler file (which listed traces/{id}, archive, dependencies, metrics). Their request/response shapes are taken from the Grafana Jaeger plugin client (J5/J6) and the Jaeger UI model (J1). The `total` field value Jaeger sets for a by-id response is not verified; the plan keeps the Content section's `total: 0`.
- Browser support for SVG favicons (caniuse blocked). The plan ships a static `/favicon.ico` fallback without naming browsers.
- Loki semantics of an **instant** (`/query`) log-selector query (which entries, relative to `time`, are returned). The plan defines its own rule (§L.2.3).
- Loki's `stats` object in current 3.x releases may contain more sections than the three documented ones (L4); the plan emits exactly the documented three.
