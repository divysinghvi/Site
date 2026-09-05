# facts-content.md — verified facts for the content/ schemas section

Verified 2026-09-05. prometheus.io, grafana.com, jaegertracing.io and opentelemetry.io are blocked by the egress proxy; the same documents were read from their official GitHub sources (raw.githubusercontent.com) and pkg.go.dev.

## Jaeger HTTP JSON trace format

| # | Fact | Source |
|---|------|--------|
| J1 | Jaeger docs: "a trace can be retrieved via a GET request to `https://jaeger-query:16686/api/traces/{trace-id-hex-string}`"; "This JSON API is intentionally undocumented and subject to change." The stable alternative is `/api/v3/*` (JSON/HTTP version of `api_v3/query_service.proto`, OTLP-shaped). | https://raw.githubusercontent.com/jaegertracing/documentation/main/content/docs/v2/_dev/architecture/apis.md |
| J2 | Go model (`model/json/model.go`, v1.62.0): `Trace{traceID, spans[], processes map[processID]Process, warnings[]}`; `Span{traceID, spanID, parentSpanID(omitempty), flags(omitempty), operationName, references[], startTime uint64 // microseconds since Unix epoch, duration uint64 // microseconds, tags[], logs[], processID(omitempty), process(omitempty), warnings[]}`; `Reference{refType, traceID, spanID}`; `Process{serviceName, tags[]}`; `Log{timestamp uint64, fields []KeyValue}`; `KeyValue{key, type(omitempty), value any}`. | https://raw.githubusercontent.com/jaegertracing/jaeger/v1.62.0/model/json/model.go |
| J3 | Constants: `ReferenceType` = `CHILD_OF`, `FOLLOWS_FROM`; `ValueType` = `string`, `bool`, `int64`, `float64`, `binary`. | same as J2 |
| J4 | Query HTTP handler (v1.62.0): responses are wrapped in `structuredResponse{data, total, limit, offset, errors[]}`; `structuredError{code, msg, traceID}`; routes `GET /api/traces/{traceID}`, `GET /api/traces`, `GET /api/services`, `GET /api/services/{service}/operations`; trace id path param parsed with `model.TraceIDFromString` (hex); unknown trace → HTTP 404 with "trace not found". | https://raw.githubusercontent.com/jaegertracing/jaeger/v1.62.0/cmd/query/app/http_handler.go |
| J5 | pkg.go.dev confirms the same field list and the µs units for `startTime`/`duration`. | https://pkg.go.dev/github.com/jaegertracing/jaeger/model/json |

## OpenTelemetry → Jaeger mapping (spec v1.20.0; the SDK-exporter page was removed from later spec versions, the mapping is unchanged)

| # | Fact | Source |
|---|------|--------|
| O1 | Resource → `Process` tags; `serviceName` from `service.name`. SpanKind → tag `span.kind` = client/server/consumer/producer; INTERNAL omitted. Status: "When Span `Status` is set to `ERROR`, an `error` span tag MUST be added with the Boolean value of `true`." Events → Logs: "OpenTelemetry Event's `time_unix_nano` and `attributes` fields map directly to Jaeger Log's `timestamp` and `fields`"; the event `name` is added to `fields` under key `event` (an explicit `event` attribute takes precedence). Links → references with `FOLLOWS_FROM`. Primitive attribute types map to Jaeger tag types; array values are serialized as JSON strings. | https://raw.githubusercontent.com/open-telemetry/opentelemetry-specification/v1.20.0/specification/trace/sdk_exporters/jaeger.md |
| O2 | Status tag names: `otel.status_code` = "OK" or "ERROR", "MUST NOT be set if the status code is UNSET"; `otel.status_description` set only when it has a value. Scope tags `otel.scope.name`, `otel.scope.version` (`otel.library.*` deprecated). Dropped-count tags `otel.dropped_attributes_count`, `otel.dropped_events_count`, `otel.dropped_links_count`. | https://raw.githubusercontent.com/open-telemetry/opentelemetry-specification/v1.20.0/specification/common/mapping-to-non-otlp.md |

## Prometheus rule files and /api/v1/rules

| # | Fact | Source |
|---|------|--------|
| P1 | Alerting rule example: `groups: - name: example / labels: {team: myteam} / rules: - alert: HighRequestLatency / expr: ... > 0.5 / for: 10m / keep_firing_for: 5m / labels: {severity: page} / annotations: {summary: ...}`. `for`: alert is *pending* until the condition has held for the duration, then *firing*. `keep_firing_for`: keep firing for that long after the condition last held. Templating in labels/annotations: `{{ $labels.<name> }}`, `{{ $value }}`, `$externalLabels`. | https://raw.githubusercontent.com/prometheus/prometheus/main/docs/configuration/alerting_rules.md |
| P2 | Rule-file skeleton: `<rule_group>`: `name` (unique within file), `interval`, `limit`, `query_offset`, `labels`, `rules[]`; alerting `<rule>`: `alert`, `expr`, `[for | default 0s]`, `[keep_firing_for | default 0s]`, `labels`, `annotations`. Check with `promtool check rules /path/to/example.rules.yml`. | https://raw.githubusercontent.com/prometheus/prometheus/main/docs/configuration/recording_rules.md |
| P3 | Go types `model/rulefmt`: `RuleGroups{Groups []RuleGroup yaml:"groups"}`, `RuleGroup{Name, Interval model.Duration, QueryOffset *model.Duration, Limit int, Rules []Rule, Labels map[string]string}`, `Rule{Record, Alert, Expr, For model.Duration, KeepFiringFor model.Duration, Labels, Annotations map[string]string}`; `Parse(content []byte, ignoreUnknownFields bool, nameValidationScheme, parser, logger) (*RuleGroups, []error)`. | https://pkg.go.dev/github.com/prometheus/prometheus/model/rulefmt |
| P4 | `/api/v1/rules` response: `data.groups[]{name, file, interval, limit, evaluationTime, lastEvaluation, rules[]{name, type:"alerting", query, duration (seconds), keepFiringFor, labels, annotations, health, state, alerts[]{state, activeAt, value, labels, annotations}}}`. Params: `type=alert|record`, `rule_name[]`, `rule_group[]`, `file[]`, `exclude_alerts`, `match[]`, `group_limit`. `/api/v1/alerts` → `data.alerts[]`. Envelope `{status, data, errorType, error, warnings, infos}`; 400 bad params, 422 unexecutable expr, 503 timeout. | https://raw.githubusercontent.com/prometheus/prometheus/main/docs/querying/api.md |
| P5 | Go types `web/api/v1`: `RuleDiscovery{groups, groupNextToken}`, `RuleGroup{name, file, rules, interval float64, limit, evaluationTime float64, lastEvaluation time.Time}`, `AlertingRule{state, name, query, duration float64, keepFiringFor float64, labels, annotations, alerts, health, lastError(omitempty), evaluationTime, lastEvaluation, type}`, `Alert{labels, annotations, state, activeAt(omitempty), keepFiringSince(omitempty), value string}`. | https://pkg.go.dev/github.com/prometheus/prometheus/web/api/v1 |
| P6 | `model.Duration` (used by `for`) is parsed by `ParseDuration`; units include ms, s, m, h, d, w, y with "a year always has 365d, a week always has 7d, and a day always has 24h"; negative durations rejected. | https://pkg.go.dev/github.com/prometheus/common/model#ParseDuration |
| P7 | Naming: metric names `[a-zA-Z_:][a-zA-Z0-9_:]*`, label names `[a-zA-Z_][a-zA-Z0-9_]*`, `__`-prefixed label names reserved; label values any UTF-8. | https://raw.githubusercontent.com/prometheus/docs/main/docs/concepts/data_model.md |

## Grafana dashboard JSON conventions (names borrowed for panels.yaml)

| # | Fact | Source |
|---|------|--------|
| G1 | `#GridPos`: `h: uint32 & >0 | *9` ("number of rows from the top edge of the panel"), `w: uint32 & >0 & <=24 | *12` ("number of columns from the left edge"), `x: uint32 & >=0 & <24`, `y: uint32 & >=0`. `#Panel`: `type`, `id`, `title`, `description`, `gridPos`, `targets[]`, `datasource`, `transparent`, `links`, `fieldConfig{defaults, overrides}`, `options{}`. `#FieldConfig`: `displayName`, `unit`, `decimals`, `min`, `max`, `mappings`, `thresholds`, `color`. `#ThresholdsConfig{mode: "absolute"|"percentage", steps[]{value: number|null, color}}` ("first value is always -Infinity"). Targets are datasource-defined. | https://raw.githubusercontent.com/grafana/grafana/main/kinds/dashboard/dashboard_kind.cue |
| G2 | Grid constants: `GRID_CELL_HEIGHT = 30` px, `GRID_CELL_VMARGIN = 8`, `GRID_COLUMN_COUNT = 24`, `MIN_PANEL_HEIGHT = 90`. | https://raw.githubusercontent.com/grafana/grafana/main/public/app/core/constants.ts |
| G3 | Panel plugin ids: `timeseries` ("Time series"), `stat`, `gauge`, `bargauge` ("Bar gauge"). | https://raw.githubusercontent.com/grafana/grafana/main/public/app/plugins/panel/{timeseries,stat,gauge,bargauge}/plugin.json |
| G4 | Unit ids: `none`, `short`, `percent`, `percentunit`; time `ns`, `µs`, `ms`, `s`, `m`, `h`, `d`, `dtdurations` ("duration (s)"), `dthms`; throughput `reqps`, `ops`, `cps`; data `bytes`, `decbytes`. | https://raw.githubusercontent.com/grafana/grafana/main/packages/grafana-data/src/valueFormats/categories.ts |

## Loki (only what the content schema depends on)

| # | Fact | Source |
|---|------|--------|
| L1 | Stream selector `{label="value"}` with `=`, `!=`, `=~`, `!~` (regex matchers fully anchored). Line filters `|=`, `!=`, `|~`, `!~` (not anchored). `| json` "will extract all json properties as labels if the log line is a valid json document. Nested properties are flattened into label keys using the `_` separator"; keys sanitized to Prometheus label-name characters; invalid JSON adds `__error__` label instead of dropping the line. | https://raw.githubusercontent.com/grafana/loki/main/docs/sources/query/log_queries/_index.md |
| L2 | Label guidance: labels for static things (application, environment…); "Label values must always be bounded"; "think single digits, or maybe 10's of values for a dynamic label"; high-cardinality identifiers (requestId) belong in the line and are found with filter expressions. | https://raw.githubusercontent.com/grafana/loki/main/docs/sources/get-started/labels/bp-labels.md |
| L3 | `query_range` streams response: `{"status":"success","data":{"resultType":"streams","result":[{"stream":{...},"values":[["<ns epoch>","<line>"]]}],"stats":{}}}`; params `query, limit (default 100), start, end, since, step, interval, direction (forward|backward, default backward)`; `/loki/api/v1/labels` and `/label/<name>/values` → `{"status":"success","data":[...]}`. | https://raw.githubusercontent.com/grafana/loki/main/docs/sources/reference/loki-http-api.md |

## NDJSON

| # | Fact | Source |
|---|------|--------|
| N1 | Each line a JSON text per RFC 8259 followed by `\n` (`\r\n` also accepted by parsers); UTF-8; parsers MAY ignore empty lines (must document); media type `application/x-ndjson`; extension `.ndjson`. | https://raw.githubusercontent.com/ndjson/ndjson-spec/master/README.md |

## JSON Schema 2020-12 and the Go libraries

| # | Fact | Source |
|---|------|--------|
| S1 | Meta-schema `$id`/`$schema` = `https://json-schema.org/draft/2020-12/schema`; vocabularies core, applicator, unevaluated, validation, meta-data, format-annotation, content. | https://json-schema.org/draft/2020-12/schema |
| S2 | `format: date` = RFC 3339 `full-date` (`YYYY-MM-DD` only). `pattern` = ECMA-262 regex, **not implicitly anchored** (write `^…$`). `format` is annotation-only unless the format-assertion vocabulary is enabled. `enum`/`const` defined as equality against the listed values. | https://json-schema.org/draft/2020-12/json-schema-validation |
| S3 | `santhosh-tekuri/jsonschema/v6`: supports drafts 4, 6, 7, 2019-09, 2020-12 (latest is default when `$schema` absent; `DefaultDraft()` overrides). Format assertions are OFF for 2019-09+ unless `Compiler.AssertFormat()` is called. Built-in formats: regex, uuid, ipv4, ipv6, hostname, email, date, time, date-time, duration, json-pointer, relative-json-pointer, uri, uri-reference, uri-template, iri, iri-reference, period, semver. `RegisterFormat(&Format{Name, Validate func(any) error})` for custom formats. `UseRegexpEngine` — default is Go's `regexp` (RE2). Validation errors offer `Error()`, `FlagOutput()`, `BasicOutput()`, `DetailedOutput()`. `Compile(loc)`, `MustCompile`, `AddResource(url, doc)`, `UnmarshalJSON(io.Reader)`. | https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6 and https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6#Compiler |
| S4 | README: YAML with non-string keys is not valid JSON; with `gopkg.in/yaml.v3` no map-key conversion is needed (it decodes to `map[string]interface{}`). `jv` CLI supports `-assertformat`. | https://raw.githubusercontent.com/santhosh-tekuri/jsonschema/master/README.md |
| S5 | `invopop/jsonschema` generates draft 2020-12; struct tags `jsonschema:"required"`, `enum=`, `pattern=`, `format=`, `minimum=`, `maximum=`, `minLength=`, `maxLength=`, `title=`, `description=`, `default=`, `example=`, `oneof_required=`, `anyof_required=`, `nullable`; `jsonschema_extras:"k=v"`; `json:",omitempty"` makes a field optional (otherwise required by default); Reflector options `Anonymous`, `ExpandedStruct`, `DoNotReference`, `AllowAdditionalProperties`, `RequiredFromJSONSchemaTags`. | https://raw.githubusercontent.com/invopop/jsonschema/main/README.md |

## Markdown rendering / sanitizing (postmortems)

| # | Fact | Source |
|---|------|--------|
| M1 | `github.com/yuin/goldmark` v1.8.6, CommonMark 0.31.2 compliant; `goldmark.WithExtensions(extension.GFM)`, `parser.WithAutoHeadingID()`, custom id generation via `parser.WithIDs(...)`; `html.WithUnsafe()` exists (we do NOT use it). Built-ins: Table, Strikethrough, Linkify, TaskList, DefinitionList, Footnote, Typographer. | https://pkg.go.dev/github.com/yuin/goldmark |
| M2 | `go.abhg.dev/goldmark/frontmatter` v0.3.0: YAML frontmatter delimited by `---`; `frontmatter.Get(ctx).Decode(&struct)`; `Mode: frontmatter.SetMetadata` alternative. (`github.com/yuin/goldmark-meta` v1.1.0 is the older alternative: separator must be on the first line; `meta.Get(ctx)` → `map[string]interface{}`.) | https://pkg.go.dev/go.abhg.dev/goldmark/frontmatter ; https://pkg.go.dev/github.com/yuin/goldmark-meta |
| M3 | `github.com/microcosm-cc/bluemonday` v1.0.27, allowlist HTML sanitizer; `UGCPolicy()` (broad safe set, no iframe/object/embed/style/script); `p.AllowAttrs("id").Matching(regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)).OnElements("h1",…,"h6")`; `Sanitize(string) string`, `SanitizeBytes([]byte) []byte`. | https://pkg.go.dev/github.com/microcosm-cc/bluemonday |

## Given by the task / brief (not web-verified, treated as user-supplied)

- PyPI page `https://pypi.org/project/codemind-ci/`, GitHub profile `https://github.com/divysinghvi`, self target `https://divy.dev/readyz`.
- Brief palette: bg `#0b0c0e`, panel `#181b1f`, green `#73bf69`, yellow `#f2cc0c`, red `#f2495c`, blue `#5794f2`.

## UNVERIFIED

- Whether `promtool check rules` (current version) rejects unknown top-level keys in a rule file — the design avoids the question by keeping `content/alerts.yaml` a pure rule file (no custom keys).
- goldmark's *default* auto-heading-id slug for "Timeline (UTC)" — the design does not rely on it: the API installs its own `parser.WithIDs` generator with a fixed slug rule.
- The additional Grafana classic-palette hex values used for services beyond the four in the brief (`#ff9830` orange, `#b877d9` purple, `#8ab8ff`, `#96d98d`, `#ff7383`, `#fade2a`) — presented as proposals; the design section owns the final values.
- The exact list of `state` values in Prometheus `AlertingRule.state` (`inactive|pending|firing`) and `health` (`ok|err|unknown`) — inferred from the Go type comments seen in the API package, not quoted verbatim.
