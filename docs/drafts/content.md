# Content: `content/` data schemas

## Cross-section notes

Read these first; they are the only places this section reaches into other sections.

| # | Note | Affects |
|---|------|---------|
| X1 | Adds one content-derived gauge **`divy_experience_years`** (computed at request time from `spans.yaml`, never stored in SQLite) so the "years of experience" stat is a PromQL target like every other panel. Extends the CONVENTIONS §7 catalogue by one name. | API `/metrics`, `/api/v1/query*` |
| X2 | Panels and alerts below use only: instant vector selectors with `=` label matchers, `sum()` (no `by`), `increase()`, `rate()`, range selectors `[1d]`/`[7d]`, and **vector-vs-scalar comparisons `>` and `==`** (the CONVENTIONS' own `divy_open_to_work == 1` already needs comparisons). Nothing else. | PromQL subset |
| X3 | `sum(pypi_package_info)` is used as "packages published"; the PyPI collector must keep exactly one `pypi_package_info{package,version}` series per package (delete the previous version's series, do not zero it). | Collector |
| X4 | Endpoints this content is served through (names are proposals; the API contract table is authoritative): `GET /api/traces/career` (Jaeger shape, §C.3.5), `GET /api/v1/rules` (§C.7), `GET /api/content/services`, `/api/content/panels`, `/api/content/postmortems`, `/api/content/postmortems/{id}`, `/api/content/postmortems/{id}.md`, `/api/content/profile`, `/api/content/uptime`, `/api/content/todos`, `GET /healthz` (§C.9). | API contract |
| X5 | Errors on `/api/traces/*` follow CONVENTIONS #14 (`{"error":"trace not found"}`, HTTP 404), not Jaeger's `errors[]` array; the success body uses Jaeger's `{"data":[…]}` wrapper. | API |
| X7 | Deep-link shapes used below (`/#trace?span=<id>`, `/#dashboard?panel=<id>` in alert `runbook_url` annotations and `span_url`) are placeholders; the frontend section owns the URL scheme (hash vs pathname router) and Phase 2 writes the final form into `alerts.yaml`. | Frontend |
| X6 | Service colors live in `spans.yaml services[]` and reach the frontend as Jaeger process tags; the four hex values from the brief are fixed, the other six are proposals the design section may change. | Design, frontend |

## C.1 Files

All prose about Divy lives here and nowhere else. Every file is validated by `divy validate` (§C.10) in CI and at `divy serve` startup.

| File | Format | Purpose | Consumed by |
|------|--------|---------|-------------|
| `content/spans.yaml` | YAML | Service list (with colors) + the career trace tree | `/api/traces/career`, ASCII export, `divy_experience_years`, healthz pods |
| `content/logs.ndjson` | NDJSON | 60–100 structured log lines, one JSON object per line | `/loki/api/v1/*` |
| `content/postmortems/INC-001.md` … `INC-004.md` | Markdown + YAML frontmatter | Blameless incident reports | `/api/content/postmortems*`, OG images |
| `content/panels.yaml` | YAML | Dashboard definition (panels, PromQL targets, grid) | `/api/content/panels` → dashboard page |
| `content/alerts.yaml` | YAML (Prometheus rule file) | Alert rules | `/api/v1/rules` → client-side evaluator |
| `content/uptime.yaml` | YAML | Probe targets | Collector (probes), uptime page |
| `content/manual_metrics.yaml` | YAML | Hand-maintained gauges with provenance | `/metrics`, `/api/v1/query*` |
| `content/profile.yaml` | YAML | Identity, links, healthz payload, escalation path, `kubectl get pods` rows | `/healthz`, `/contact`, promql console |
| `content/README.md` | Markdown | 20-line editing guide: id grammar, date grammar, `TODO(divy)` rule, how to run `divy validate` | humans |

## C.2 Shared conventions

### C.2.1 Identifiers

| Thing | Grammar (regex) | Example | Uniqueness |
|-------|-----------------|---------|------------|
| Span id | `^[a-z0-9]+(-[a-z0-9]+)*(\.[a-z0-9]+(-[a-z0-9]+)*)+$` — dotted lowercase kebab, ≥2 segments | `euro-iam.oidc-core`, `oss.minikube.pr-01` | global across `spans.yaml`; not hierarchical (a child's id need not start with its parent's) |
| Service id | `^[a-z0-9]+(-[a-z0-9]+)*$` | `ef-polymer` | `spans.yaml services[]` |
| Postmortem id | `^INC-[0-9]{3}$`; must equal the filename stem | `INC-003` | `content/postmortems/` |
| Panel id, uptime target id, pod name | `^[a-z0-9]+(-[a-z0-9]+)*$` | `commits-weekly`, `pypi-codemind` | per file |
| Log free-form field name | `^[a-zA-Z_][a-zA-Z0-9_]*$` (Prometheus/Loki label-name rule, so `| json` extracts it unchanged) | `from`, `containers` | per line |
| Alert name | `^[A-Z][A-Za-z0-9]*$` | `LFXApplicationPending` | `alerts.yaml` |

### C.2.2 Dates

Dates are strings; precision is implied by length. **Always quote them in YAML** (`start: "2023"` — unquoted `2023` is an integer, unquoted `2024-05-14` may decode as a timestamp; the validator rejects any non-string date with "quote the date").

| Grammar | Regex (anchored, ECMA-262/RE2 compatible) | Resolves as a *start* to | Resolves as an *end* to |
|---------|-------------------------------------------|--------------------------|-------------------------|
| `YYYY` | `^\d{4}$` | `YYYY-01-01T00:00:00Z` | `(YYYY+1)-01-01T00:00:00Z` |
| `YYYY-MM` | `^\d{4}-(0[1-9]\|1[0-2])$` | `YYYY-MM-01T00:00:00Z` | first day of the next month, 00:00:00Z |
| `YYYY-MM-DD` | `^\d{4}-(0[1-9]\|1[0-2])-(0[1-9]\|[12]\d\|3[01])$` | `YYYY-MM-DDT00:00:00Z` | next day 00:00:00Z |
| `TODO(divy)` | `^TODO\(divy\)(: .+)?$` | fallback: parent span's resolved start (root: fails validation) | fallback: parent span's resolved end (parent open → now) |

Combined schema for any date field: `{"type":"string","pattern":"^(\\d{4}(-(0[1-9]|1[0-2])(-(0[1-9]|[12]\\d|3[01]))?)?|TODO\\(divy\\)(: .+)?)$"}` plus a calendar check in Go (rejects `2025-02-30`). JSON Schema `format: date` is not used for these fields: it only accepts `YYYY-MM-DD` (RFC 3339 `full-date`), and the validator library asserts formats only when `AssertFormat()` is on — which `divy validate` turns on for the `date-time` fields in logs and `uri` fields in links.

Rendering of a resolved-from-TODO date: hatched bar segment + "date TODO" chip; the Jaeger export carries `divy.start_precision`/`divy.end_precision` = `todo` so the frontend never guesses.

### C.2.3 `TODO(divy)` rule

| Rule | Detail |
|------|--------|
| Marker | The literal text `TODO(divy)`, optionally followed by `: note`. Any string field in any content file may hold it (dates, URLs, tag values, list items). |
| Numbers | Numeric fields cannot hold the marker; use the sibling `note`/`todo` field and leave the number at its known lower bound (e.g. `5000` for "5,000+"). |
| `todo:` lists | Every item must start with `TODO(divy)` (validated) so a plain `grep -r 'TODO(divy)' content/` finds the same set the API reports. |
| Comments | YAML comments containing the marker are also inventoried (yaml.v3 nodes keep comments), reported with a line number and the nearest key path. |
| Inventory | `divy validate --todos` and `GET /api/content/todos` (§C.10.3). |
| Never | Never invent a value to avoid a TODO. Never delete a TODO without replacing it with the fact. |

### C.2.4 Services and colors

Defined once in `spans.yaml services[]`; used by spans, logs (`service` must be one of them), postmortems (`services[]`), and pods.

| id | title | color | counts_as_experience | Note |
|----|-------|-------|----------------------|------|
| `divy` | Divy | `#73bf69` (brief green) | no | root span, personal/debug log lines |
| `edu` | Education | `#96d98d` | no | |
| `freelance` | Freelance | `#c7d0d9` | yes | |
| `ef-polymer` | EF Polymer Ltd. | `#ff9830` | yes | |
| `euro-tech` | Euro Technologies | `#b877d9` | yes | |
| `gradr` | Gradr | `#5794f2` (brief blue) | yes | |
| `oss` | Open source | `#f2cc0c` (brief yellow) | no | |
| `project` | Projects | `#8ab8ff` | no | |
| `quant` | Quant | `#ff7383` | no | |
| `community` | Community | `#fade2a` | no | log lines only (GDG / AWS community core team); no span in the brief's tree |

`#f2495c` (brief red) is reserved for `status: error` spans and error-level UI, not for a service.

## C.3 `content/spans.yaml`

### C.3.1 Field table

Top level:

| Field | Type | Req | Meaning / constraints |
|-------|------|-----|-----------------------|
| `version` | int | yes | `1` |
| `services[]` | list of Service | yes | ≥1; ids unique |
| `trace` | Span | yes | root span; must have `id: divy.career`, `open: true`, a non-TODO `start` |

Service:

| Field | Type | Req | Constraints |
|-------|------|-----|-------------|
| `id` | string | yes | service-id grammar |
| `title` | string | yes | |
| `color` | string | yes | `^#[0-9a-f]{6}$` |
| `counts_as_experience` | bool | no | default `false`; feeds `divy_experience_years` |

Span:

| Field | Type | Req | Constraints / meaning |
|-------|------|-----|-----------------------|
| `id` | string | yes | span-id grammar; globally unique |
| `service` | string | yes | ∈ `services[].id` |
| `title` | string | no | human label; drawer heading. Default: `id` |
| `start` | date-string | yes | §C.2.2 |
| `end` | date-string | cond. | required unless `open: true`; with `open: true` it is the *planned* end and must resolve to > now |
| `open` | bool | no | default `false`; `true` = still running (end = now unless planned end given) |
| `status` | enum `ok`,`error` | no | `error` → red marker, Jaeger `error=true` + `otel.status_code=ERROR` |
| `tags` | map | no | well-known keys: `stack` (string or list), `role` (string), `lang` (string or list), `location` (string). Extra keys allowed; values string, number, bool, or list of strings. Keys match `^[a-z][a-z0-9_.]*$` |
| `events[]` | list of Event | no | see below |
| `links[]` | list of Link | no | see below |
| `todo[]` | list of string | no | each item starts with `TODO(divy)` |
| `children[]` | list of Span | no | order in file is NOT significant (see §C.3.6) |

Event:

| Field | Type | Req | Constraints |
|-------|------|-----|-------------|
| `ts` | date-string | yes | §C.2.2; resolved as a *start*; must lie within the span (skipped when TODO) |
| `name` | string | yes | |
| `attrs` | map string→scalar | no | |

Link:

| Field | Type | Req | Constraints |
|-------|------|-----|-------------|
| `kind` | enum `postmortem`,`repo`,`pr`,`pypi`,`url` | yes | |
| `ref` | string | if `kind: postmortem` | `INC-NNN`; the postmortem's frontmatter `span` must equal this span's id |
| `url` | string | if kind ≠ postmortem | `format: uri` or `TODO(divy)` |
| `label` | string | no | |

### C.3.2 JSON Schema (generated from `api/internal/model`; shown abbreviated)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://divy.dev/schema/spans.schema.json",
  "type": "object", "required": ["version", "services", "trace"], "additionalProperties": false,
  "properties": {
    "version": {"const": 1},
    "services": {"type": "array", "minItems": 1, "items": {"$ref": "#/$defs/Service"}},
    "trace": {"$ref": "#/$defs/Span"}
  },
  "$defs": {
    "DateOrTodo": {"type": "string",
      "pattern": "^(\\d{4}(-(0[1-9]|1[0-2])(-(0[1-9]|[12]\\d|3[01]))?)?|TODO\\(divy\\)(: .+)?)$"},
    "Scalar": {"type": ["string", "number", "boolean"]},
    "TagValue": {"anyOf": [{"$ref": "#/$defs/Scalar"}, {"type": "array", "items": {"type": "string"}}]},
    "Service": {"type": "object", "required": ["id", "title", "color"], "additionalProperties": false,
      "properties": {"id": {"type": "string", "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$"},
                     "title": {"type": "string", "minLength": 1},
                     "color": {"type": "string", "pattern": "^#[0-9a-f]{6}$"},
                     "counts_as_experience": {"type": "boolean", "default": false}}},
    "Event": {"type": "object", "required": ["ts", "name"], "additionalProperties": false,
      "properties": {"ts": {"$ref": "#/$defs/DateOrTodo"}, "name": {"type": "string", "minLength": 1},
                     "attrs": {"type": "object", "additionalProperties": {"$ref": "#/$defs/Scalar"}}}},
    "Link": {"type": "object", "required": ["kind"], "additionalProperties": false,
      "properties": {"kind": {"enum": ["postmortem", "repo", "pr", "pypi", "url"]},
                     "ref": {"type": "string", "pattern": "^INC-[0-9]{3}$"},
                     "url": {"type": "string", "anyOf": [{"format": "uri"}, {"pattern": "^TODO\\(divy\\)"}]},
                     "label": {"type": "string"}},
      "allOf": [{"if": {"properties": {"kind": {"const": "postmortem"}}}, "then": {"required": ["ref"]}, "else": {"required": ["url"]}}]},
    "Span": {"type": "object", "required": ["id", "service", "start"], "additionalProperties": false,
      "properties": {
        "id": {"type": "string", "pattern": "^[a-z0-9]+(-[a-z0-9]+)*(\\.[a-z0-9]+(-[a-z0-9]+)*)+$"},
        "service": {"type": "string"}, "title": {"type": "string"},
        "start": {"$ref": "#/$defs/DateOrTodo"}, "end": {"$ref": "#/$defs/DateOrTodo"},
        "open": {"type": "boolean", "default": false}, "status": {"enum": ["ok", "error"]},
        "tags": {"type": "object", "propertyNames": {"pattern": "^[a-z][a-z0-9_.]*$"},
                 "additionalProperties": {"$ref": "#/$defs/TagValue"}},
        "events": {"type": "array", "items": {"$ref": "#/$defs/Event"}},
        "links": {"type": "array", "items": {"$ref": "#/$defs/Link"}},
        "todo": {"type": "array", "items": {"type": "string", "pattern": "^TODO\\(divy\\)"}},
        "children": {"type": "array", "items": {"$ref": "#/$defs/Span"}}},
      "if": {"required": ["open"], "properties": {"open": {"const": true}}}, "then": {}, "else": {"required": ["end"]}}
  }
}
```

Checks the schema cannot express (done in Go by `divy validate`): id uniqueness, `service` membership, calendar validity, `end` > `start`, child ⊆ parent interval, open+planned-end > now, event `ts` ∈ span, postmortem back-links (§C.10).

### C.3.3 Complete initial tree

Every span named in brief §3.1. Month-precision dates are exactly the brief's; everything else is `TODO(divy)`.

```yaml
# content/spans.yaml — the career trace. Quote all dates. Run `divy validate` after editing.
version: 1

services:
  - {id: divy,       title: Divy,               color: "#73bf69"}
  - {id: edu,        title: Education,          color: "#96d98d"}
  - {id: freelance,  title: Freelance,          color: "#c7d0d9", counts_as_experience: true}
  - {id: ef-polymer, title: EF Polymer Ltd.,    color: "#ff9830", counts_as_experience: true}
  - {id: euro-tech,  title: Euro Technologies,  color: "#b877d9", counts_as_experience: true}
  - {id: gradr,      title: Gradr,              color: "#5794f2", counts_as_experience: true}
  - {id: oss,        title: Open source,        color: "#f2cc0c"}
  - {id: project,    title: Projects,           color: "#8ab8ff"}
  - {id: quant,      title: Quant,              color: "#ff7383"}
  - {id: community,  title: Community,          color: "#fade2a"}

trace:
  id: divy.career
  service: divy
  title: Divy — career
  start: "2023"
  open: true
  tags: {role: student + part-time product engineer, location: "Rajasthan, India"}
  children:

    - id: edu.btech-ece
      service: edu
      title: B.Tech Electronics & Communication Engineering — College of Technology and Engineering, Udaipur
      start: "2023"
      end: "2027"            # planned; open: true keeps the bar dashed up to it
      open: true
      tags: {role: student, location: "Udaipur, India"}
      todo:
        - "TODO(divy): enrolment month and expected graduation month (refines both dates to YYYY-MM)"

    - id: freelance.web-dev
      service: freelance
      title: Freelance web & app development
      start: TODO(divy)
      end: TODO(divy)
      tags: {role: freelance developer, stack: TODO(divy), lang: TODO(divy), location: "Rajasthan, India"}
      todo:
        - "TODO(divy): start/end months, stack, 2–3 sanitized project one-liners (become log lines)"

    - id: ef-polymer.swe-intern
      service: ef-polymer
      title: Software Engineering Intern — EF Polymer Ltd. (Japanese AgriTech)
      start: "2024-05"
      end: "2025-07"
      tags:
        role: Software Engineering Intern
        stack: TODO(divy)
        lang: TODO(divy)
        location: remote + on-site Japan
      events:
        - {ts: TODO(divy), name: first prod deploy, attrs: {system: sales-wms}}
      todo:
        - "TODO(divy): SWMS stack and languages; first-deploy month; number of warehouses"
      children:
        - id: sales-wms.build
          service: ef-polymer
          title: Sales & Warehouse Management System — build
          start: TODO(divy)
          end: TODO(divy)
          tags: {role: developer, stack: TODO(divy)}
        - id: sales-wms.multi-warehouse-rollout
          service: ef-polymer
          title: Sales & Warehouse Management System — multi-warehouse rollout
          start: TODO(divy)
          end: TODO(divy)
          tags: {role: developer}
          todo: ["TODO(divy): rollout months and warehouse count"]
        - id: japan.onsite
          service: ef-polymer
          title: On-site with the Japanese team
          start: TODO(divy)
          end: TODO(divy)
          tags: {location: Japan}
          todo: ["TODO(divy): city and on-site dates"]

    - id: euro-tech.go-iam-intern
      service: euro-tech
      title: Go/IAM Intern — Euro Technologies
      start: "2025-08"
      end: "2025-11"
      tags:
        role: Go/IAM Intern
        stack: [go, gin, gorm, postgres, redis, asynq]
        lang: [go]
        location: TODO(divy)
      events:
        - {ts: "2025-11", name: shipped Euro-IAM, attrs: {features: "multi-tenant OIDC, WebAuthn, TOTP, magic links, SSO"}}
      links:
        - {kind: repo, url: TODO(divy), label: Euro-IAM (if public)}
      children:
        - id: euro-iam.oidc-core
          service: euro-tech
          title: Euro-IAM — multi-tenant OIDC provider core
          start: TODO(divy)
          end: TODO(divy)
          tags: {stack: [go, gin, gorm, postgres, redis], lang: [go]}
        - id: euro-iam.webauthn
          service: euro-tech
          title: Euro-IAM — WebAuthn, TOTP, magic links, SSO
          start: TODO(divy)
          end: TODO(divy)
          tags: {stack: [webauthn, totp], lang: [go]}
        - id: euro-iam.asynq-workers
          service: euro-tech
          title: Euro-IAM — Asynq background workers
          start: TODO(divy)
          end: TODO(divy)
          tags: {stack: [asynq, redis], lang: [go]}

    - id: gradr.intern
      service: gradr
      title: Intern — Gradr (gradr.se, Swedish EdTech)
      start: "2025-12"
      end: "2026-03"
      tags: {role: Intern, location: TODO(divy), stack: TODO(divy)}

    - id: gradr.product-engineer
      service: gradr
      title: Product Engineer (part-time) — Gradr
      start: "2026-03"
      open: true
      tags: {role: Product Engineer (part-time), location: TODO(divy)}
      events:
        - {ts: "2026-03", name: promoted to Product Engineer, attrs: {from: intern}}
      children:
        - id: gradr.observability
          service: gradr
          title: Production observability infrastructure — owner
          start: TODO(divy)
          open: true
          tags:
            role: owner
            stack: [loki, promtail, prometheus, grafana, sentry, uptime-kuma, caddy, authelia, infisical, hetzner]
          events:
            - {ts: TODO(divy), name: first prod deploy, attrs: {component: TODO(divy)}}
          todo: ["TODO(divy): month you took ownership of the stack"]
          children:
            - id: gradr.inc-001
              service: gradr
              title: INC-001 — post-reboot secrets-injection race
              start: TODO(divy)
              end: TODO(divy)
              status: error
              tags: {component: secrets-sidecar}
              links: [{kind: postmortem, ref: INC-001}]
            - id: gradr.inc-002
              service: gradr
              title: INC-002 — cascading memory exhaustion on the proxy host
              start: TODO(divy)
              end: TODO(divy)
              status: error
              tags: {component: dev-proxy}
              links: [{kind: postmortem, ref: INC-002}]
            - id: gradr.inc-003
              service: gradr
              title: INC-003 — Sentry outbound email failures
              start: TODO(divy)
              end: TODO(divy)
              status: error
              tags: {component: sentry}
              links: [{kind: postmortem, ref: INC-003}]
            - id: gradr.inc-004
              service: gradr
              title: INC-004 — error-tracking signal quality
              start: TODO(divy)
              end: TODO(divy)
              status: error
              tags: {component: sentry}
              links: [{kind: postmortem, ref: INC-004}]
        - id: gradr.product-features
          service: gradr
          title: Full-stack product features
          start: TODO(divy)
          open: true
          tags: {stack: TODO(divy)}
          todo: ["TODO(divy): product stack; 2–3 shipped features (sanitized) as events"]

    - id: oss.minikube
      service: oss
      title: kubernetes/minikube — 15+ merged PRs
      start: TODO(divy)
      end: TODO(divy)
      tags: {role: contributor, org: kubernetes, repo: kubernetes/minikube, lang: [go]}
      todo: ["TODO(divy): first/last merge months; fill one child per merged PR (15+)"]
      children:
        - id: oss.minikube.pr-01
          service: oss
          title: "TODO(divy): PR title"
          start: TODO(divy)
          end: TODO(divy)
          tags: {repo: kubernetes/minikube, lang: [go]}
          links: [{kind: pr, url: TODO(divy)}]
          todo: ["TODO(divy): PR number, title, URL, merge date (start=end=merge day)"]
        - id: oss.minikube.pr-02
          service: oss
          title: "TODO(divy): PR title"
          start: TODO(divy)
          end: TODO(divy)
          tags: {repo: kubernetes/minikube, lang: [go]}
          links: [{kind: pr, url: TODO(divy)}]
          todo: ["TODO(divy): PR number, title, URL, merge date"]
        # Phase 2 writes pr-03 … pr-15 with the identical stub shape; add more as PRs are filled.

    - id: oss.kubeflow
      service: oss
      title: kubeflow — contributions
      start: TODO(divy)
      end: TODO(divy)
      tags: {role: contributor, org: kubeflow, lang: TODO(divy)}
      todo: ["TODO(divy): repos, PR links, months"]

    - id: oss.lfx-velero-application
      service: oss
      title: LFX Mentorship 2026 Term 3 — Velero (CSI Snapshot E2E tests in Kind CI)
      start: TODO(divy)
      open: true
      tags: {role: applicant, project: velero, status: pending, also_targeting: opentelemetry}
      todo:
        - "TODO(divy): application month; on a decision set end + tags.status and update manual_metrics.yaml"
        - "TODO(divy): was a separate OpenTelemetry application submitted? (then a sibling span + lfx_applications=2)"

    - id: oss.wasmedge-prep
      service: oss
      title: WasmEdge Wide Arithmetic proposal — prep (128-bit arithmetic library, C++/x86-64)
      start: TODO(divy)
      end: TODO(divy)
      tags: {role: explorer, lang: [cpp, x86-64-asm], stack: [wasmedge, webassembly]}
      links: [{kind: repo, url: TODO(divy), label: 128-bit arithmetic library}]

    - id: project.codemind
      service: project
      title: CodeMind — persistent Cognee-powered memory layer for codebases (codemind-ci)
      start: TODO(divy)
      end: TODO(divy)
      tags: {role: author, stack: [python, cognee, github-actions, pypi], lang: [python], hackathon: WeMakeDevs Cognee hackathon}
      events:
        - {ts: TODO(divy), name: published codemind-ci 0.2.0 to PyPI, attrs: {package: codemind-ci, version: 0.2.0}}
      links:
        - {kind: pypi, url: https://pypi.org/project/codemind-ci/, label: codemind-ci on PyPI}
        - {kind: repo, url: TODO(divy), label: source}
        - {kind: url, url: TODO(divy), label: demo}
      todo: ["TODO(divy): hackathon month; repo and demo URLs (demo URL is also uptime target codemind-demo)"]

    - id: project.savely
      service: project
      title: Savely — Chrome price-comparison extension
      start: TODO(divy)
      open: true
      tags: {role: author, stack: [fastapi, svelte, postgres, chrome-extension], lang: [python], users: "5000+"}
      links:
        - {kind: url, url: TODO(divy), label: landing page}
        - {kind: url, url: TODO(divy), label: Chrome Web Store}
      todo: ["TODO(divy): launch month; still maintained? (else set end and open: false)"]

    - id: quant.worldquant-iqc
      service: quant
      title: WorldQuant International Quant Championship — Stage 1
      start: TODO(divy)
      end: TODO(divy)
      tags: {role: participant, result: 2nd Prize, global_rank: 98}
      events:
        - {ts: TODO(divy), name: 2nd Prize — Stage 1, attrs: {global_rank: 98}}
      todo: ["TODO(divy): competition months; prize announcement date"]
```

### C.3.4 Identity

| Item | Value |
|------|-------|
| Trace id | `hex(sha256("divy.career")[0:16])` = `9f3a0703b53d5b0aae2fb3bdacea0ff6` (32 hex chars). `GET /api/traces/career` is an alias. |
| Span id | `hex(sha256(span.id)[0:8])` (16 hex chars). Examples: `divy.career` → `9f3a0703b53d5b0a`, `edu.btech-ece` → `4e76e10ea3071d79`, `gradr.inc-002` → `ef53e50f70cc9d38`. |
| Process id | `p-<service id>` (e.g. `p-gradr`). |

### C.3.5 Mapping to Jaeger JSON

Response = `{"data":[Trace],"total":0,"limit":0,"offset":0,"errors":null}`; one Trace. All times in **microseconds** (Jaeger `startTime` = µs since epoch, `duration` = µs).

| Content | Jaeger field | Type / rule |
|---------|--------------|-------------|
| trace id (§C.3.4) | `data[0].traceID`, every `spans[].traceID` | hex string |
| `span.id` | `spans[].spanID` | hex(sha256(id)[0:8]); also tag `divy.id` (string) so the UI can deep-link `#trace?span=<id>` |
| `span.id` | `spans[].operationName` | verbatim (the brief labels spans by id) |
| `span.title` | tag `divy.title` | string |
| `span.service` | `spans[].processID` = `p-<service>`; `processes[p-<service>] = {serviceName: <service>, tags: [divy.title, divy.color, divy.counts_as_experience]}` | color travels as a process tag; frontend never hardcodes it |
| parent | `references: [{refType: "CHILD_OF", traceID, spanID: <parent spanID>}]`; root: `[]` | `parentSpanID` omitted (references are the source of truth) |
| `start` (resolved, §C.2.2) | `startTime` | µs |
| `end` (resolved) − `start`; open without planned end: `now − start` | `duration` | µs; `now` evaluated per request (`Cache-Control: max-age=15`) |
| raw `start`/`end` strings | tags `divy.start`, `divy.end` (string; `divy.end` omitted when open without a planned end), `divy.start_precision` ∈ `year,month,day,todo`, `divy.end_precision` ∈ `year,month,day,todo,open` | lets the UI print "May 2024" instead of "2024-05-01" and hatch TODO segments |
| `open: true` | tag `divy.open` = `true` (bool); planned end → tag `divy.end_planned` = raw string | UI: dashed right edge |
| `status: error` | tags `otel.status_code` = `"ERROR"`, `error` = `true` (bool) | per OTel→Jaeger mapping; `status: ok` → `otel.status_code = "OK"`; absent → neither |
| `tags.*` | `tags[]` entries `{key, type, value}` | string→`string`, bool→`bool`, integer→`int64`, float→`float64`; list → JSON array string, type `string` (OTel array rule); tag keys as written |
| `events[]` | `logs[]` = `{timestamp: µs(resolve(ts)), fields: [{key:"event", type:"string", value: name}, …attrs]}` | an `attrs.event` key takes precedence over `name` (OTel rule); TODO `ts` → span start, plus field `divy.ts_precision = "todo"` |
| `links[]` | tag `divy.links` = JSON string of the list; postmortem links additionally tag `divy.postmortems` = `"INC-001"` (comma-joined, sorted) | Jaeger `references` are NOT used for links (they mean span causality) |
| `todo[]` | tag `divy.todo` = JSON array string; `divy.todo_count` (int64) | |
| depth | tag `divy.depth` (int64, root = 0) | convenience for the mobile vertical timeline |
| everything | `flags` omitted, `warnings: null` | |

Example (root + one child, trimmed):

```json
{"data":[{"traceID":"9f3a0703b53d5b0aae2fb3bdacea0ff6",
  "spans":[
    {"traceID":"9f3a0703b53d5b0aae2fb3bdacea0ff6","spanID":"9f3a0703b53d5b0a","operationName":"divy.career",
     "references":[],"startTime":1672531200000000,"duration":115862400000000,
     "tags":[{"key":"divy.id","type":"string","value":"divy.career"},{"key":"divy.title","type":"string","value":"Divy — career"},
             {"key":"divy.start","type":"string","value":"2023"},{"key":"divy.start_precision","type":"string","value":"year"},
             {"key":"divy.end_precision","type":"string","value":"open"},{"key":"divy.open","type":"bool","value":true},
             {"key":"role","type":"string","value":"student + part-time product engineer"},
             {"key":"location","type":"string","value":"Rajasthan, India"},{"key":"divy.depth","type":"int64","value":0},
             {"key":"divy.todo_count","type":"int64","value":0}],
     "logs":[],"processID":"p-divy","warnings":null},
    {"traceID":"9f3a0703b53d5b0aae2fb3bdacea0ff6","spanID":"da42f4e70b8baf7c","operationName":"gradr.product-engineer",
     "references":[{"refType":"CHILD_OF","traceID":"9f3a0703b53d5b0aae2fb3bdacea0ff6","spanID":"9f3a0703b53d5b0a"}],
     "startTime":1772323200000000,"duration":16070400000000,
     "tags":[{"key":"divy.id","type":"string","value":"gradr.product-engineer"},{"key":"divy.open","type":"bool","value":true},
             {"key":"divy.start","type":"string","value":"2026-03"},{"key":"divy.start_precision","type":"string","value":"month"},
             {"key":"role","type":"string","value":"Product Engineer (part-time)"},{"key":"location","type":"string","value":"TODO(divy)"},
             {"key":"divy.depth","type":"int64","value":1},{"key":"divy.todo_count","type":"int64","value":0}],
     "logs":[{"timestamp":1772323200000000,"fields":[{"key":"event","type":"string","value":"promoted to Product Engineer"},
                                                     {"key":"from","type":"string","value":"intern"}]}],
     "processID":"p-gradr","warnings":null}],
  "processes":{"p-divy":{"serviceName":"divy","tags":[{"key":"divy.title","type":"string","value":"Divy"},{"key":"divy.color","type":"string","value":"#73bf69"}]},
               "p-gradr":{"serviceName":"gradr","tags":[{"key":"divy.title","type":"string","value":"Gradr"},{"key":"divy.color","type":"string","value":"#5794f2"},{"key":"divy.counts_as_experience","type":"bool","value":true}]}},
  "warnings":null}],"total":0,"limit":0,"offset":0,"errors":null}
```

(`duration` values above are illustrative arithmetic for a request at 2026-09-05T00:00:00Z; the server computes them per request.)

### C.3.6 Ordering and layout (data contract for the frontend)

| Rule | Why |
|------|-----|
| `children[]` order in the file carries no meaning; the API emits spans in DFS order, children sorted by (resolved start, id). The frontend must sort again and never rely on array order. | Divy will insert spans anywhere. |
| Siblings may overlap in time (e.g. `edu.btech-ece` with every job; the four `gradr.inc-*` with `gradr.product-features`). The frontend assigns rows greedily: sort siblings by start, place each on the first row whose last end ≤ its start. Data provides only intervals. | "Overlapping spans render on parallel rows." |
| Open spans end at request time; two requests give different `duration`. The frontend treats `divy.open=true` as "extend to the right edge", not as a fixed number. | Honest live durations. |
| Spans whose start or end came from a TODO fallback are marked by `*_precision = "todo"`; render hatched, never solid. | No invented dates on screen. |
| `status: error` spans render with the error red marker regardless of service color. | Incidents stand out like in Jaeger. |
| The vertical mobile timeline is the same DFS order with `divy.depth` as indent. | 390 px target. |

### C.3.7 Derived values from spans

| Value | Definition | Used by |
|-------|------------|---------|
| `divy_experience_years` (gauge) | `(now − min(resolved start of every span whose service has counts_as_experience: true and whose start is not TODO)) / (365.25 × 86400 s)`, 2 decimals. With the tree above → measured from `2024-05`. | stat panel `stat-experience` |
| pod `AGE` | `now − resolved start` of the pod's `span` (profile.yaml) | promql console `kubectl get pods` |
| pod `RESTARTS` | number of postmortems whose `span` is the pod's span or a descendant | same |
| ASCII waterfall | same DFS order, 80 columns, `▓` solid / `░` hatched (TODO) / `┈` open tail | `Accept: text/plain` on `/` |

## C.4 `content/logs.ndjson`

### C.4.1 Line schema

One JSON object per line, UTF-8, `\n` terminated; empty lines ignored (documented per the NDJSON spec). No nested objects or arrays (keeps `| json` extraction flat and label names clean).

| Field | Type | Req | Constraints / meaning |
|-------|------|-----|-----------------------|
| `ts` | string | yes | RFC 3339 UTC (`format: date-time`, must end in `Z`) **or** `TODO(divy)` |
| `precision` | enum `day`,`month`,`year` | no | default `day`. `month` means "the first of the month stands for the month"; the UI prints "Mar 2026". |
| `level` | enum `debug`,`info`,`warn`,`error` | yes | brief: `info` milestones, `warn` incidents, `debug` fun details; `error` reserved for the incident-start lines |
| `service` | string | yes | ∈ `spans.yaml services[].id` |
| `msg` | string | yes | 1–200 chars, no trailing period |
| `span` | string | no | a span id from `spans.yaml`; dangling → validation error |
| `component` | string | no | `^[a-z0-9-]+$`, ≤ 20 distinct values across the file (Loki label cardinality) |
| any other key | string, number, bool | no | key matches the label-name grammar; not one of the reserved keys `__error__`, `stream`, `line` |

Ordering timestamp (used for sorting and as the Loki `values[][0]` nanosecond timestamp):

| `ts` | Ordering timestamp |
|------|--------------------|
| RFC 3339 | itself |
| `TODO(divy)` with `span` | that span's resolved start (fallbacks included) |
| `TODO(divy)` without `span` | the root span's resolved start (2023-01-01T00:00:00Z) |
| ties | file order, via `+ line_index` ns |

### C.4.2 Loki stream labels vs line content

| Becomes a stream label (`{…}` selector) | Stays in the line (reach with `| json` / line filters) |
|------------------------------------------|---------------------------------------------------------|
| `service` (10 values), `level` (4), `component` (≤20, only when present) | `ts`, `precision`, `msg`, `span`, every free-form field |

The served line is the original JSON text verbatim (so `{service="gradr"} | json | from="intern"` works and the expand-JSON view shows exactly what is in the file). Labels are copied from the line, never removed from it. Bounded label values follow Loki's own guidance ("think single digits, or maybe 10's of values"); ids like `span` stay in the line.

`/loki/api/v1/labels` → `["component","level","service"]`; `/loki/api/v1/label/service/values` → the service ids present in the file.

### C.4.3 Sample lines (facts from the brief only)

```json
{"ts":"2023-01-01T00:00:00Z","precision":"year","level":"info","service":"edu","span":"edu.btech-ece","msg":"enrolled: B.Tech Electronics & Communication Engineering, CTAE Udaipur","expected_graduation":"2027"}
{"ts":"2024-05-01T00:00:00Z","precision":"month","level":"info","service":"ef-polymer","span":"ef-polymer.swe-intern","msg":"joined EF Polymer Ltd. as Software Engineering Intern","sector":"agritech","company_country":"JP"}
{"ts":"2025-07-01T00:00:00Z","precision":"month","level":"info","service":"ef-polymer","span":"ef-polymer.swe-intern","msg":"internship complete: Sales & Warehouse Management System deployed across multiple warehouses","team":"japan","months_with_team":12}
{"ts":"2025-08-01T00:00:00Z","precision":"month","level":"info","service":"euro-tech","span":"euro-tech.go-iam-intern","msg":"joined Euro Technologies as Go/IAM Intern","lang":"go"}
{"ts":"2025-11-01T00:00:00Z","precision":"month","level":"info","service":"euro-tech","span":"euro-tech.go-iam-intern","msg":"shipped Euro-IAM: multi-tenant OIDC, WebAuthn, TOTP, magic links, SSO","lang":"go","stack":"gin,gorm,postgres,redis,asynq"}
{"ts":"2025-12-01T00:00:00Z","precision":"month","level":"info","service":"gradr","span":"gradr.intern","msg":"joined Gradr as Intern","company":"gradr.se","country":"SE"}
{"ts":"2026-03-01T00:00:00Z","precision":"month","level":"info","service":"gradr","span":"gradr.product-engineer","msg":"promoted to Product Engineer","from":"intern"}
{"ts":"TODO(divy)","level":"warn","service":"gradr","component":"secrets-sidecar","span":"gradr.inc-001","msg":"post-reboot race: secrets sidecar wrote .env after app containers started; Supabase-backed service down","incident":"INC-001","resolved":true}
{"ts":"TODO(divy)","level":"warn","service":"gradr","component":"dev-proxy","span":"gradr.inc-002","msg":"cascading memory exhaustion: sentry containers saturating swap","incident":"INC-002","containers":65,"resolved":true}
{"ts":"TODO(divy)","level":"debug","service":"oss","span":"oss.wasmedge-prep","msg":"first x86-64 asm routine of the 128-bit arithmetic library compiles","lang":"asm"}
{"ts":"TODO(divy)","level":"debug","service":"quant","msg":"alpha submitted on WorldQuant BRAIN","platform":"brain"}
```

### C.4.4 Phase 2 coverage plan (target 87 lines; allowed 60–100)

| Span (or service) | Lines | Levels | Content source |
|-------------------|-------|--------|----------------|
| `divy.career` (service `divy`) | 4 | info 2, debug 2 | open-to-work, interests (x86-64 asm, WebAssembly, LLVM, competitive programming, quant) |
| `community` (no span) | 3 | info 2, debug 1 | GDG core team, AWS community core team, one event line (`TODO(divy)` names/dates) |
| `edu.btech-ece` | 4 | info 2, debug 2 | enrolment, branch, `TODO(divy)` coursework highlights |
| `freelance.web-dev` | 3 | info 3 | `TODO(divy)` project one-liners |
| `ef-polymer.swe-intern` | 4 | info 3, debug 1 | join, first deploy, complete, remote collaboration |
| `sales-wms.build` | 3 | info 2, debug 1 | build milestones `TODO(divy)` |
| `sales-wms.multi-warehouse-rollout` | 3 | info 2, warn 1 | rollout milestones `TODO(divy)` |
| `japan.onsite` | 3 | info 2, debug 1 | on-site `TODO(divy)` |
| `euro-tech.go-iam-intern` | 3 | info 3 | join, ship, leave |
| `euro-iam.oidc-core` | 3 | info 2, debug 1 | OIDC provider, multi-tenancy, Postgres/Redis |
| `euro-iam.webauthn` | 3 | info 2, debug 1 | WebAuthn, TOTP, magic links, SSO |
| `euro-iam.asynq-workers` | 2 | info 1, debug 1 | Asynq workers |
| `gradr.intern` | 3 | info 3 | join, stack, first feature `TODO(divy)` |
| `gradr.product-engineer` | 3 | info 3 | promotion, ownership, part-time |
| `gradr.observability` | 6 | info 4, debug 2 | each stack component going live (`TODO(divy)` months) |
| `gradr.inc-001` … `gradr.inc-004` | 3 each = 12 | error 1 (start), warn 1 (impact), info 1 (resolved) per incident | postmortems |
| `gradr.product-features` | 3 | info 3 | `TODO(divy)` features |
| `oss.minikube` | 3 | info 2, debug 1 | first merged PR, 15+ merged, reviewer interactions (`TODO(divy)`) — per-PR lines are added when PR stubs are filled |
| `oss.kubeflow` | 2 | info 2 | `TODO(divy)` |
| `oss.lfx-velero-application` | 3 | info 2, debug 1 | applied, project scope, OTel interest |
| `oss.wasmedge-prep` | 3 | info 1, debug 2 | proposal explored, 128-bit lib, asm |
| `project.codemind` | 4 | info 3, debug 1 | hackathon, PyPI 0.2.0, GitHub Actions workflow, git-history contradiction detection |
| `project.savely` | 4 | info 3, debug 1 | launch `TODO(divy)`, 5,000+ users, stack, Chrome |
| `quant.worldquant-iqc` | 3 | info 2, debug 1 | Stage 1, 2nd Prize / rank 98, BRAIN |
| **Total** | **87** | ≈ info 55 · warn 8 · error 4 · debug 20 | |

## C.5 `content/postmortems/INC-00N.md`

### C.5.1 Frontmatter

| Field | Type | Req | Constraints |
|-------|------|-----|-------------|
| `id` | string | yes | `^INC-[0-9]{3}$`, equals filename stem |
| `title` | string | yes | ≤ 90 chars |
| `severity` | enum `SEV1`…`SEV4` | yes | table below |
| `date` | date-string | yes | incident start; `YYYY-MM-DD` expected, `YYYY-MM`/`TODO(divy)` accepted |
| `span` | string | yes | span id; that span must carry `links: [{kind: postmortem, ref: <id>}]` |
| `services` | list of string | yes | ⊆ `services[].id` |
| `duration` | string | yes | Prometheus duration (`2h30m`, `45m`) or `TODO(divy)` |
| `status` | enum `resolved`,`monitoring`,`open` | yes | |
| `tags` | list of string | no | `^[a-z0-9-]+$` |
| `summary` | string | yes | one sentence ≤ 160 chars; cards + OG image |

Severity definitions (fixed; shown in the badge tooltip):

| Severity | Definition | Assigned |
|----------|------------|----------|
| SEV1 | User-facing production outage or data-loss risk | INC-001 (Supabase-backed service outage) |
| SEV2 | Production degraded or partially unavailable; host-level resource exhaustion | INC-002 (proxy host memory/swap) |
| SEV3 | Internal tooling down or blind; no user-facing impact | INC-003 (Sentry email), INC-004 (dropped errors, duplicate issues) |
| SEV4 | Hygiene / near-miss; no outage | — |

### C.5.2 Body: required H2 sections, fixed order

`## Summary` · `## Impact` · `## Timeline (UTC)` · `## Root cause` · `## Detection` · `## Resolution` · `## Action items` · `## Lessons`. Exactly these eight H2s in this order (H3s allowed under any). Timeline is a table `| Time (UTC) | Event |`; unknown times are `TODO(divy)`. Action items are a task list `- [x] / - [ ] item — owner: divy — status`.

Skeleton for INC-001 (the other three follow the same shape; §3.5 of the brief gives their content):

```markdown
---
id: INC-001
title: Post-reboot race between the secrets-injection sidecar and app containers
severity: SEV1
date: TODO(divy)
span: gradr.inc-001
services: [gradr]
duration: TODO(divy)
status: resolved
tags: [startup-ordering, healthchecks, secrets, supabase]
summary: After a host reboot, app containers started before the secrets sidecar had written .env, taking a Supabase-backed service down until startup ordering and healthcheck gating were added.
---

## Summary
One paragraph. What broke, for how long, what fixed it.

## Impact
- Users: TODO(divy)
- Duration: TODO(divy)
- Services: the Supabase-backed app service

## Timeline (UTC)
| Time (UTC) | Event |
|------------|-------|
| TODO(divy) | Host reboot |
| TODO(divy) | App containers start; `.env` not yet written by the secrets sidecar |
| TODO(divy) | Service fails to reach Supabase; alert / user report (see Detection) |
| TODO(divy) | Startup ordering + healthcheck gating deployed; service recovers |

## Root cause
No ordering dependency between the secrets-injection sidecar and the app containers; the apps read `.env` once at boot.

## Detection
TODO(divy): how it was noticed (Uptime Kuma / Sentry / user).

## Resolution
Startup ordering (app containers depend on the sidecar's healthcheck) and a healthcheck that only passes once `.env` exists and is non-empty.

## Action items
- [x] Add `depends_on` with `condition: service_healthy` for every consumer of injected secrets — owner: divy — done
- [ ] TODO(divy): follow-ups

## Lessons
- Reboots are a deploy. Test cold start, not just rolling restarts.
```

### C.5.3 Sanitization checklist (enforced by `divy validate`, rule `pm.sanitize`)

| # | Check | Validator regex / rule | Level |
|---|-------|------------------------|-------|
| 1 | No secrets, tokens, keys | `-----BEGIN [A-Z ]*PRIVATE KEY-----`, `ghp_[A-Za-z0-9]{36}`, `github_pat_`, `AKIA[0-9A-Z]{16}`, `sk_(live|test)_`, `xox[abp]-`, `Bearer [A-Za-z0-9._-]{20,}`, `(password|passwd|secret|token|api[_-]?key)\s*[=:]\s*\S{8,}` | error |
| 2 | No internal hostnames | `[a-z0-9-]+\.(internal|local|lan|corp|intranet)\b`, any `*.gradr.se` subdomain (bare `gradr.se` allowed), `\b(10|172\.(1[6-9]|2\d|3[01])|192\.168)\.\d+\.\d+\b` | error |
| 3 | No IP addresses at all | `\b(?:\d{1,3}\.){3}\d{1,3}\b` | error |
| 4 | No customer or employee data | email addresses `[\w.+-]+@[\w-]+\.[\w.]+` (except `TODO(divy)`), phone-number shapes | error |
| 5 | Generic component names only | allowed vocabulary: `proxy-host`, `app-host`, `db`, `secrets-sidecar`, `dev-proxy`, `sentry`, `caddy`, `authelia`, `uptime-kuma`, `loki`, `promtail`, `prometheus`, `grafana`, `infisical`, `resend`; other `*-host`/`*-prod-*` tokens | warn |
| 6 | Env var NAMES ok, VALUES never | `[A-Z_]{4,}=\S+` outside code fences | warn |
| 7 | Numbers approximate where they came from memory | "~65 containers", "1000+ duplicate issues" — style rule, not enforced | — |
| 8 | Blameless language | no person names other than "Divy"; roles only — reviewed by Divy at the Phase 2 checkpoint | — |

The same secret/IP/hostname patterns (1–3) run over every content file, not only postmortems.

### C.5.4 Serving

Decision: **server-side rendering**. The API parses frontmatter (`go.abhg.dev/goldmark/frontmatter`), renders CommonMark+GFM with `goldmark` (`extension.GFM`, `parser.WithIDs(fixedSlugger)`), sanitizes with `bluemonday.UGCPolicy()` extended by `AllowAttrs("id").Matching(^[a-z0-9-]+$).OnElements("h2","h3")`, and caches the result at startup (content is immutable per process). No `html.WithUnsafe()`; raw HTML in the markdown is stripped. Heading ids use a fixed slug rule: lowercase, runs of `[^a-z0-9]` → `-`, trimmed (`Timeline (UTC)` → `timeline-utc`), so the sticky TOC anchors are stable.

| Endpoint | Body |
|----------|------|
| `GET /api/content/postmortems` | `{"items":[{id,title,severity,date,span,services,duration,status,tags,summary,todo_count,og_image}]}` sorted by id |
| `GET /api/content/postmortems/INC-001` | frontmatter fields + `"html"`, `"toc":[{"level":2,"id":"summary","text":"Summary"},…]`, `"markdown"` (raw file), `"span_url":"/#trace?span=gradr.inc-001"`, `"og_image"` |
| `GET /api/content/postmortems/INC-001.md` | raw file, `Content-Type: text/markdown; charset=utf-8` |
| unknown id | 404 `{"error":"postmortem not found"}` |

OG image inputs (route and renderer defined in the API/deploy sections; suggested path `/og/postmortems/INC-001.png`, 1200×630): `id`, `title`, `severity` (badge color: SEV1 red `#f2495c`, SEV2 orange `#ff9830`, SEV3 yellow `#f2cc0c`, SEV4 blue `#5794f2`), `date`, `duration`, `services[]` (service colors), `summary` (wrapped, ≤ 3 lines), site name `divy.dev`. The web page emits `<meta property="og:image">` pointing at it.

## C.6 `content/panels.yaml`

### C.6.1 Schema

Names borrowed from Grafana's dashboard JSON model (`gridPos` on a 24-column grid, `targets[].expr`, `legendFormat`, `unit`, `thresholds`, panel `type` ids `timeseries`/`stat`/`gauge`/`bargauge`) so the file reads familiarly; it is not a Grafana import file.

| Field | Type | Req | Constraints / meaning |
|-------|------|-----|-----------------------|
| `version` | int | yes | `1` |
| `dashboard.title` | string | yes | |
| `dashboard.refresh` | duration | no | default `60s`; the page re-polls `/api/v1/query_range` |
| `dashboard.time.default` | enum | yes | one of `options` |
| `dashboard.time.options[]` | list | yes | `[24h, 7d, 30d, 1y, all]`; `all` = from the root span's resolved start to now |
| `panels[].id` | string | yes | kebab; unique |
| `panels[].title` | string | yes | |
| `panels[].type` | enum `timeseries`,`stat`,`gauge`,`bargauge` | yes | |
| `panels[].gridPos` | `{x,y,w,h}` ints | yes | `0 ≤ x < 24`, `1 ≤ w ≤ 24`, `x+w ≤ 24`, `y ≥ 0`, `h ≥ 2`; row height 30 px, vertical margin 8 px (Grafana constants); no two panels overlap |
| `panels[].targets[]` | list | yes, ≥1 | `expr` (PromQL, subset §X2, must parse), `legendFormat` (`{{label}}` templating), `refId` (`A`,`B`…), `instant` (bool, default false → range query), `hide` (bool) |
| `panels[].unit` | string | no | Grafana unit id: `short`, `none`, `percent`, `s`, `dtdurations` |
| `panels[].decimals` | int | no | |
| `panels[].min` / `max` | number | no | gauges |
| `panels[].stack` | bool | no | timeseries only |
| `panels[].thresholds` | `{mode: absolute\|percentage, steps[]{value: number\|null, color}}` | no | first step `value: null`; `color` is a palette token: `green`,`yellow`,`red`,`blue`,`orange`,`purple` (resolved by the theme, so the Konami theme can remap) |
| `panels[].description` | string | yes | shown in the panel header (i) |
| `panels[].source` | `{kind: github\|pypi\|manual\|process\|content, cadence, note, updated_metric}` | yes | `manual` requires `updated_metric` (the companion timestamp series to show "last updated") |
| `panels[].options` | map | no | type-specific: stat `graph_mode: none\|area`, `color_mode: value\|background`; gauge `show_threshold_markers` |

Layout overrides in the URL hash (`#layout=<base64url json>&range=7d`) store only `{id: {x,y,w,h}}` for moved/resized panels and the range; the file stays the default.

### C.6.2 Full initial dashboard

```yaml
# content/panels.yaml
version: 1
dashboard:
  title: divy.dev — career metrics
  refresh: 60s
  time: {default: 1y, options: [24h, 7d, 30d, 1y, all]}

panels:
  # ---- stat row (y=0) — every number is a query result ----
  - id: stat-experience
    title: Years of experience
    type: stat
    gridPos: {x: 0, y: 0, w: 6, h: 4}
    targets: [{refId: A, expr: divy_experience_years, instant: true}]
    unit: none
    decimals: 1
    description: Years since the earliest dated work span (services flagged counts_as_experience in spans.yaml). Grows every second.
    source: {kind: content, note: "derived from content/spans.yaml at request time; TODO(divy) dates are skipped"}
    options: {graph_mode: none, color_mode: value}

  - id: stat-merged-prs
    title: Merged PRs
    type: stat
    gridPos: {x: 6, y: 0, w: 6, h: 4}
    targets: [{refId: A, expr: sum(github_merged_prs_total), instant: true}]
    unit: short
    description: All merged pull requests authored by divysinghvi across orgs (private orgs counted, never named).
    source: {kind: github, cadence: 15m, note: "GitHub search is:pr is:merged author:divysinghvi; cumulative counter backfilled from mergedAt"}
    options: {graph_mode: area}

  - id: stat-packages
    title: Packages published
    type: stat
    gridPos: {x: 12, y: 0, w: 6, h: 4}
    targets: [{refId: A, expr: sum(pypi_package_info), instant: true}]
    unit: short
    description: Number of packages on PyPI (one pypi_package_info series per package, value 1).
    source: {kind: pypi, cadence: 60m, note: "pypi.org JSON API"}
    options: {graph_mode: none}

  - id: stat-active-users
    title: Active users
    type: stat
    gridPos: {x: 18, y: 0, w: 6, h: 4}
    targets:
      - {refId: A, expr: savely_active_users, instant: true}
      - {refId: B, expr: 'divy_manual_metric_updated_timestamp_seconds{metric="savely_active_users"}', instant: true, hide: true}
    unit: short
    description: Savely Chrome extension users. Source is manual; the panel prints "last updated" from series B, or "unknown" if it is missing.
    source: {kind: manual, note: "content/manual_metrics.yaml", updated_metric: 'divy_manual_metric_updated_timestamp_seconds{metric="savely_active_users"}'}
    options: {graph_mode: none, color_mode: value}

  # ---- row 1 (y=4) ----
  - id: commits-weekly
    title: GitHub contributions — weekly rate
    type: timeseries
    gridPos: {x: 0, y: 4, w: 12, h: 8}
    targets: [{refId: A, expr: 'sum(increase(github_commits_total[7d]))', legendFormat: contributions / 7d}]
    unit: short
    decimals: 0
    description: github_commits_total counts GitHub contribution-calendar events (commits, PRs, issues, reviews) per day; increase over a 7-day window.
    source: {kind: github, cadence: 15m, note: "contributionsCollection.contributionCalendar, last 365 days, backfilled daily"}

  - id: merged-prs-by-org
    title: Merged PRs by org
    type: timeseries
    gridPos: {x: 12, y: 4, w: 12, h: 8}
    targets: [{refId: A, expr: github_merged_prs_total, legendFormat: "{{org}}"}]
    unit: short
    decimals: 0
    stack: true
    description: Cumulative merged PRs per repository owner (kubernetes, kubeflow, gradr — private, count only — and others).
    source: {kind: github, cadence: 15m, note: "GitHub search is:pr is:merged author:divysinghvi"}

  # ---- row 2 (y=12) ----
  - id: stars-by-repo
    title: Stars by repo
    type: bargauge
    gridPos: {x: 0, y: 12, w: 8, h: 8}
    targets: [{refId: A, expr: github_stars, legendFormat: "{{repo}}", instant: true}]
    unit: short
    description: Current stargazers per public repo. No history before the first collection.
    source: {kind: github, cadence: 15m, note: "gauge; starts at first collection"}

  - id: pypi-downloads
    title: codemind-ci downloads / day
    type: timeseries
    gridPos: {x: 8, y: 12, w: 8, h: 8}
    targets: [{refId: A, expr: 'sum(increase(pypi_downloads_total{package="codemind-ci"}[1d]))', legendFormat: codemind-ci}]
    unit: short
    decimals: 0
    description: Daily downloads from pypistats.org (mirrors excluded), backfilled for the last 180 days.
    source: {kind: pypi, cadence: 60m, note: "pypistats.org overall, mirrors=false"}

  - id: savely-active-users
    title: Savely active users
    type: stat
    gridPos: {x: 16, y: 12, w: 8, h: 8}
    targets:
      - {refId: A, expr: savely_active_users, legendFormat: users}
      - {refId: B, expr: 'divy_manual_metric_updated_timestamp_seconds{metric="savely_active_users"}', instant: true, hide: true}
    unit: short
    description: "Manual gauge. TODO(divy): source URL. Shows last-updated honestly."
    source: {kind: manual, note: "content/manual_metrics.yaml", updated_metric: 'divy_manual_metric_updated_timestamp_seconds{metric="savely_active_users"}'}
    options: {graph_mode: area}

  # ---- row 3 (y=20) ----
  - id: oss-prs-open
    title: Open OSS PRs
    type: gauge
    gridPos: {x: 0, y: 20, w: 8, h: 6}
    targets: [{refId: A, expr: oss_prs_open, instant: true}]
    unit: short
    min: 0
    max: 10
    thresholds: {mode: absolute, steps: [{value: null, color: blue}, {value: 1, color: green}]}
    description: Pull requests currently open in repositories not owned by divysinghvi.
    source: {kind: github, cadence: 15m, note: "GitHub search is:pr is:open author:divysinghvi -user:divysinghvi"}

  - id: lfx-pending
    title: LFX applications pending
    type: gauge
    gridPos: {x: 8, y: 20, w: 8, h: 6}
    targets:
      - {refId: A, expr: 'lfx_applications{status="pending"}', instant: true}
      - {refId: B, expr: 'divy_manual_metric_updated_timestamp_seconds{metric="lfx_applications"}', instant: true, hide: true}
    unit: short
    min: 0
    max: 3
    thresholds: {mode: absolute, steps: [{value: null, color: blue}, {value: 1, color: yellow}]}
    description: LFX Mentorship 2026 Term 3 (Velero). Manual; see manual_metrics.yaml.
    source: {kind: manual, note: "content/manual_metrics.yaml", updated_metric: 'divy_manual_metric_updated_timestamp_seconds{metric="lfx_applications"}'}

  - id: divy-uptime
    title: divy.dev uptime
    type: stat
    gridPos: {x: 16, y: 20, w: 8, h: 6}
    targets: [{refId: A, expr: divy_uptime_seconds, instant: true}]
    unit: dtdurations
    description: Process uptime of the API serving this page. A joke that is also real.
    source: {kind: process, note: "seconds since the divy binary started"}
    options: {graph_mode: none}
```

"View query" shows `targets[].expr` verbatim and `curl -sG 'https://divy.dev/api/v1/query_range' --data-urlencode 'query=<expr>' --data-urlencode 'start=<unix>' --data-urlencode 'end=<unix>' --data-urlencode 'step=<s>'` (or `/api/v1/query` for `instant: true`).

## C.7 `content/alerts.yaml`

Pure Prometheus rule file (no custom keys) so `promtool check rules content/alerts.yaml` passes and the API parses it with `model/rulefmt` (`Parse(..., ignoreUnknownFields=false, …)`). The `HighContributionRate` threshold is named by the rule label `threshold_per_week`, which templating exposes as `{{ $labels.threshold_per_week }}`; `divy validate` (rule `alerts.threshold-matches`) requires the numeric literal in `expr` to equal that label.

```yaml
# content/alerts.yaml — Prometheus rule file. Evaluated client-side every 15s against /api/v1/query.
groups:
  - name: divy
    interval: 15s
    rules:
      - alert: DivyAvailableForHire
        expr: divy_open_to_work == 1
        for: 30s
        labels:
          severity: page
        annotations:
          summary: "Open to backend/infra internships. Runbook: /contact"
          runbook_url: /contact

      - alert: HighContributionRate
        # threshold: TODO(divy) — 20 is a placeholder default; change both the literal and the label together
        expr: sum(increase(github_commits_total[7d])) > 20
        for: 15s
        labels:
          severity: info
          threshold_per_week: "20"
        annotations:
          summary: "High contribution rate: {{ $value }} contributions in the last 7 days (threshold {{ $labels.threshold_per_week }})"
          runbook_url: /#dashboard?panel=commits-weekly

      - alert: LFXApplicationPending
        expr: lfx_applications{status="pending"} > 0
        for: 0s
        labels:
          severity: warning
        annotations:
          summary: "{{ $value }} LFX Mentorship application(s) pending (2026 Term 3 — Velero)"
          runbook_url: /#dashboard?panel=lfx-pending
```

`GET /api/v1/rules` (Prometheus shape; the server does not evaluate, so `state` is `inactive`, `alerts` is `[]`, `health` is `unknown`, `lastEvaluation` is the zero time and `evaluationTime` 0 — the browser owns state):

```json
{"status":"success","data":{"groups":[{"name":"divy","file":"content/alerts.yaml","interval":15,"limit":0,
 "evaluationTime":0,"lastEvaluation":"0001-01-01T00:00:00Z",
 "rules":[
  {"name":"DivyAvailableForHire","type":"alerting","query":"divy_open_to_work == 1","duration":30,"keepFiringFor":0,
   "labels":{"severity":"page"},"annotations":{"summary":"Open to backend/infra internships. Runbook: /contact","runbook_url":"/contact"},
   "state":"inactive","health":"unknown","alerts":[],"evaluationTime":0,"lastEvaluation":"0001-01-01T00:00:00Z"},
  {"name":"HighContributionRate","type":"alerting","query":"sum(increase(github_commits_total[7d])) > 20","duration":15,"keepFiringFor":0,
   "labels":{"severity":"info","threshold_per_week":"20"},"annotations":{"summary":"High contribution rate: {{ $value }} contributions in the last 7 days (threshold {{ $labels.threshold_per_week }})","runbook_url":"/#dashboard?panel=commits-weekly"},
   "state":"inactive","health":"unknown","alerts":[],"evaluationTime":0,"lastEvaluation":"0001-01-01T00:00:00Z"},
  {"name":"LFXApplicationPending","type":"alerting","query":"lfx_applications{status=\"pending\"} > 0","duration":0,"keepFiringFor":0,
   "labels":{"severity":"warning"},"annotations":{"summary":"{{ $value }} LFX Mentorship application(s) pending (2026 Term 3 — Velero)","runbook_url":"/#dashboard?panel=lfx-pending"},
   "state":"inactive","health":"unknown","alerts":[],"evaluationTime":0,"lastEvaluation":"0001-01-01T00:00:00Z"}]}]}}
```

Client evaluator contract: poll `/api/v1/query?query=<expr>` every `groups[].interval`; a non-empty result = condition true; `pending` from first true, `firing` once true for ≥ `duration` s (for `DivyAvailableForHire` that is 30 s after page load); annotations rendered with `{{ $value }}` and `{{ $labels.x }}` substituted (only these two forms; no other template functions). "Silence" stores `alertname` in `sessionStorage`.

## C.8 `content/uptime.yaml`

| Field | Type | Req | Constraints / default |
|-------|------|-----|-----------------------|
| `targets[].id` | string | yes | kebab; unique; becomes the `target` label of `probe_success{target}`, `probe_duration_seconds{target}`, `probe_http_status_code{target}` |
| `name` | string | yes | display name |
| `url` | string | yes | `format: uri` (http/https) or `TODO(divy)`; TODO → collector skips, status `unconfigured` (grey, never green) |
| `method` | enum `GET`,`HEAD` | no | `GET` |
| `expected_status` | int or list of int | no | `[200]` |
| `timeout` | duration | no | `10s` |
| `interval` | duration | no | `5m` |
| `follow_redirects` | bool | no | `true` |
| `span` | string | no | span id (links the status row to the trace) |

Env override: `UPTIME_SELF_URL` (default `https://divy.dev/readyz`) replaces the `url` of the target whose id is `self-api` so `docker compose up` locally probes `http://api:8080/readyz` instead of the public host.

```yaml
# content/uptime.yaml — probed by the collector every `interval`; 90-day raw history.
targets:
  - id: savely-landing
    name: Savely landing page
    url: TODO(divy)
    span: project.savely
  - id: codemind-demo
    name: CodeMind demo
    url: TODO(divy)
    span: project.codemind
  - id: pypi-codemind
    name: codemind-ci on PyPI
    url: https://pypi.org/project/codemind-ci/
    span: project.codemind
  - id: github-profile
    name: GitHub profile
    url: https://github.com/divysinghvi
    method: HEAD
  - id: self-api
    name: divy.dev API (self)
    url: https://divy.dev/readyz
    timeout: 5s
```

## C.9 `content/manual_metrics.yaml` and `content/profile.yaml`

### C.9.1 `manual_metrics.yaml`

| Field | Type | Req | Meaning |
|-------|------|-----|---------|
| `metrics[].metric` | string | yes | metric name (`[a-zA-Z_:][a-zA-Z0-9_:]*`), must be one of the manual names in the catalogue (`savely_active_users`, `lfx_applications`) |
| `labels` | map | no | label set; (`metric`,`labels`) unique |
| `value` | number | yes | exposed as a gauge on `/metrics` and as one sample per collector run |
| `source` | string | yes | where the number comes from; may be `TODO(divy)` |
| `updated_at` | string | yes | `YYYY-MM-DD` or `TODO(divy)`; exposed as `divy_manual_metric_updated_timestamp_seconds{metric}` (unix seconds at 00:00Z); TODO → that series is omitted and panels print "last updated: unknown" |
| `note` | string | no | free text |

```yaml
# content/manual_metrics.yaml — hand-maintained gauges. Never round up; write the lower bound you can defend.
metrics:
  - metric: savely_active_users
    value: 5000
    source: TODO(divy)   # Chrome Web Store stats page or internal analytics
    updated_at: TODO(divy)
    note: "5,000+ per profile; lower bound"
  - metric: lfx_applications
    labels: {status: pending}
    value: 1
    source: LFX Mentorship 2026 Term 3 application — Velero (CSI Snapshot E2E tests in Kind CI)
    updated_at: TODO(divy)
    note: "TODO(divy): set to 2 if the OpenTelemetry application was also submitted; on a decision move the count to status=accepted or status=rejected"
```

### C.9.2 `profile.yaml`

| Field | Type | Req | Meaning / constraints |
|-------|------|-----|-----------------------|
| `name` | string | yes | |
| `handle` | string | yes | GitHub login |
| `location` | string | yes | |
| `tz` | string | yes | IANA zone; `/healthz` `tz` |
| `open_to_work` | bool | yes | → `divy_open_to_work` gauge |
| `open_to[]` | list of string | yes | → `/healthz` `open_to`, verbatim order |
| `links.github` | uri | yes | |
| `links.email`, `links.linkedin`, `links.resume`, `links.calendar` | uri / `mailto:` / `TODO(divy)` | yes | `resume` may be a site-relative path (`/resume.pdf`) once the PDF is committed under `web/static/` |
| `escalation[]` | `{step, channel, target, response_time, note}` | yes, ≥1 | the runbook's "Escalation path" |
| `pods[]` | `{name, ready, status, restarts_from: postmortems\|none, span, note}` | yes | `kubectl get pods` rows; `status` ∈ `Running`,`Pending`,`Completed`,`CrashLoopBackOff`; `ready` matches `^\d+/\d+$`; AGE derived from `span` (§C.3.7) |

```yaml
# content/profile.yaml
name: Divy
handle: divysinghvi
location: Rajasthan, India
tz: Asia/Kolkata
open_to_work: true
open_to: ["backend intern", "infra", "sre"]

links:
  github: https://github.com/divysinghvi
  email: TODO(divy)        # mailto:you@example.com
  linkedin: TODO(divy)
  resume: TODO(divy)       # /resume.pdf once web/static/resume.pdf exists
  calendar: TODO(divy)     # calendly-style booking link

escalation:
  - {step: 1, channel: email, target: TODO(divy), response_time: TODO(divy), note: "preferred for roles and internships"}
  - {step: 2, channel: github, target: https://github.com/divysinghvi, response_time: TODO(divy), note: "open an issue on any repo; PRs welcome"}
  - {step: 3, channel: linkedin, target: TODO(divy), response_time: TODO(divy)}
  - {step: 4, channel: calendar, target: TODO(divy), response_time: "next free slot", note: "book directly"}

pods:
  - {name: gradr-observability, ready: "1/1", status: Running, restarts_from: postmortems, span: gradr.observability}
  - {name: savely,              ready: "1/1", status: Running, restarts_from: none, span: project.savely, note: "5000+ users"}
  - {name: codemind-ci,         ready: "1/1", status: Running, restarts_from: none, span: project.codemind, note: "v0.2.0 on PyPI"}
  - {name: lfx-velero,          ready: "0/1", status: Pending, restarts_from: none, span: oss.lfx-velero-application}
```

`GET /healthz` → `{"status":"ok","open_to":["backend intern","infra","sre"],"tz":"Asia/Kolkata"}` — `open_to` and `tz` copied from this file, `status` from the process.

`kubectl get pods` (promql console) prints `NAME READY STATUS RESTARTS AGE NOTE` from `pods[]`, e.g. `gradr-observability 1/1 Running 4 <age>` where `4` = postmortems under `gradr.observability` and `<age>` = now − resolved start of that span.

## C.10 Cross-file linking, validation, TODO inventory

### C.10.1 Rules

`divy validate [--strict] [--json] [--todos] [dir]` — schema pass (JSON Schema 2020-12 via `santhosh-tekuri/jsonschema/v6`, `AssertFormat()` on, YAML decoded with `gopkg.in/yaml.v3` into `any`; NDJSON line by line) then the Go rules below. Exit 1 on any error; `--strict` promotes warnings.

| Rule id | File(s) | Check | On failure |
|---------|---------|-------|------------|
| `schema` | all | instance validates against `schema/<name>.schema.json` (same schemas the TS types are generated from) | error, `BasicOutput()` path + message |
| `yaml.date-quoted` | all YAML | every date field is a string (not int/timestamp) | error "quote the date" |
| `spans.id-unique` | spans | span ids globally unique | error |
| `spans.root` | spans | `trace.id == divy.career`, `open: true`, non-TODO `start` | error |
| `spans.service` | spans, logs, postmortems | every `service` ∈ `services[].id` | error |
| `spans.dates` | spans | calendar-valid; resolved `end` > `start`; child ⊆ parent (TODO sides skipped); `open` + `end` ⇒ end > now; `open: false` ⇒ end ≤ now; event `ts` ∈ span | error |
| `spans.todo-prefix` | spans | `todo[]` items start with `TODO(divy)` | error |
| `spans.link-url` | spans, uptime, profile | `url` is `http(s)://…`, `mailto:` (profile only), site-relative `/…` (resume only), or `TODO(divy)` | error |
| `links.postmortem-bidirectional` | spans ↔ postmortems | for each postmortem `P`: span `P.span` exists AND has `links[{kind: postmortem, ref: P.id}]`; for each such link the target file exists and its `span` is this span | error |
| `logs.ndjson` | logs | each non-empty line parses as a flat JSON object; unknown reserved keys rejected; free-form keys match the label grammar; scalar values only | error with line number |
| `logs.ts` | logs | `ts` RFC 3339 `Z` or `TODO(divy)`; `precision` consistent (`month` ⇒ day 01 00:00:00; `year` ⇒ Jan 01) | error |
| `logs.span` | logs | `span` exists | error |
| `logs.cardinality` | logs | ≤ 20 distinct `component` values | warn |
| `logs.count` | logs | 60 ≤ lines ≤ 100 | warn (error in `--strict`; Phase 2 CI runs `--strict`) |
| `logs.coverage` | logs ↔ spans | every span id with a non-TODO `start` or with `status: error` is referenced by ≥1 line; unreferenced spans listed | warn |
| `pm.frontmatter` | postmortems | schema; `id` == filename stem; `services ⊆ services[]`; `duration` parses as a Prometheus duration or TODO | error |
| `pm.sections` | postmortems | exactly the 8 H2s in order (§C.5.2) | error |
| `pm.sanitize` | all content | §C.5.3 patterns 1–4 | error (5–6 warn) |
| `panels.grid` | panels | `x+w ≤ 24`, `w ≥ 1`, `h ≥ 2`, no overlaps, ids unique | error |
| `panels.expr` | panels, alerts | every `expr` parses in the supported PromQL subset (same parser as the API); metric names referenced exist in the catalogue (§7 + `divy_experience_years`) | error |
| `panels.manual-source` | panels | `source.kind: manual` ⇒ `updated_metric` set and referenced by a hidden target | error |
| `alerts.rulefmt` | alerts | `rulefmt.Parse` returns no errors; alert names match `^[A-Z][A-Za-z0-9]*$`; annotations use only `{{ $value }}` / `{{ $labels.<x> }}` | error |
| `alerts.threshold-matches` | alerts | numeric literal after the comparison operator in `expr` equals `labels.threshold_per_week` when that label exists | error |
| `alerts.required` | alerts | the three alert names from the brief exist | error |
| `uptime.ids` | uptime | ids unique; `span` exists; ≥1 target with a real URL | error / warn |
| `manual.catalogue` | manual_metrics | metric names ∈ manual catalogue; (`metric`,`labels`) unique; `updated_at` date or TODO | error |
| `profile.healthz` | profile | `open_to` non-empty; `tz` loads with `time.LoadLocation`; `pods[].span` exists; pod names unique | error |
| `todo.inventory` | all | count and list every `TODO(divy)` (values, list items, YAML comments, markdown text) | info; printed with `--todos` |

### C.10.2 Output format

Human (default), one finding per line `file:line:col level rule message`, then a summary; exit code 1 if any error:

```
content/spans.yaml:141:9  error  links.postmortem-bidirectional  span gradr.inc-002 links INC-002 but content/postmortems/INC-002.md has span: gradr.inc-003
content/logs.ndjson:57    error  logs.span                       span "gradr.inc-009" not found in content/spans.yaml
content/postmortems/INC-003.md:12  warn  pm.sanitize            looks like an email address
validate: 9 files, 2 errors, 1 warning, 61 TODO(divy) — FAIL
```

`--json`:

```json
{"ok":false,"files":9,
 "errors":[{"file":"content/spans.yaml","line":141,"col":9,"rule":"links.postmortem-bidirectional","path":"$.trace.children[5].children[0].children[1].links[0]","message":"span gradr.inc-002 links INC-002 but content/postmortems/INC-002.md has span: gradr.inc-003"}],
 "warnings":[{"file":"content/postmortems/INC-003.md","line":12,"col":1,"rule":"pm.sanitize","path":"","message":"looks like an email address"}],
 "todos":{"count":61,"items":[{"file":"content/spans.yaml","line":30,"col":14,"path":"$.trace.children[1].start","context":"freelance.web-dev","text":"TODO(divy)"}]}}
```

`divy serve` runs the same validation at startup and refuses to start on errors (prints the same lines to stderr).

### C.10.3 TODO inventory — `GET /api/content/todos`

```json
{"generated_at":"2026-09-05T00:00:00Z","count":61,
 "by_file":{"content/spans.yaml":44,"content/logs.ndjson":6,"content/postmortems/INC-001.md":5,"content/uptime.yaml":2,"content/manual_metrics.yaml":3,"content/profile.yaml":9,"content/panels.yaml":1},
 "items":[
  {"file":"content/spans.yaml","line":30,"col":14,"path":"$.trace.children[1].start","context":"freelance.web-dev","text":"TODO(divy)"},
  {"file":"content/spans.yaml","line":33,"col":11,"path":"$.trace.children[1].todo[0]","context":"freelance.web-dev","text":"TODO(divy): start/end months, stack, 2–3 sanitized project one-liners (become log lines)"},
  {"file":"content/logs.ndjson","line":8,"col":8,"path":"$.ts","context":"post-reboot race: secrets sidecar wrote .env after app containers started; Supabase-backed service down","text":"TODO(divy)"},
  {"file":"content/postmortems/INC-001.md","line":22,"col":3,"path":"","context":"INC-001 › Timeline (UTC)","text":"TODO(divy)"},
  {"file":"content/alerts.yaml","line":17,"col":9,"path":"$.groups[0].rules[1]","context":"HighContributionRate (comment)","text":"TODO(divy) — 20 is a placeholder default; change both the literal and the label together"}]}
```

| Field | Rule |
|-------|------|
| `path` | JSONPath of the value (YAML: from the yaml.v3 node tree; NDJSON: within the line object; markdown: empty) |
| `context` | nearest enclosing `id` / `alert` / `metric` / pod `name`; NDJSON → the line's `msg`; markdown → `<postmortem id> › <nearest heading>` |
| `text` | the marker with its trailing note, whole value or whole comment |
| ordering | by file (fixed order of §C.1), then line |
| caching | computed at startup with the content; `Cache-Control: public, max-age=60` |

The counts above are the expected totals for the initial tree in §C.3.3 and the samples in §C.4.3; the real number is whatever the scanner finds.
