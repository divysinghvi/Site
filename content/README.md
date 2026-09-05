# content/ — the only place prose about Divy lives

Every fact the site shows comes from this directory, through the Go API. Nothing about Divy is
hardcoded in the web app. The source of truth for these files is `docs/brief.md`; the schemas are
in `docs/drafts/content.md` (and `schema/*.schema.json` once generated).

| File | What it is | Consumed by |
|------|------------|-------------|
| `spans.yaml` | Service list (with colors) + the career trace tree: every job, project, contribution and incident as a span with dates, tags, events and links | `/api/traces/career`, the hero waterfall, the ASCII trace, `divy_experience_years`, `kubectl get pods` ages |
| `logs.ndjson` | 60–100 structured log lines (one JSON object per line) telling the same history as the trace; each line carries a `span` id | `/loki/api/v1/*`, the logs explorer |
| `postmortems/INC-001.md` … `INC-004.md` | Blameless incident reports (YAML frontmatter + eight fixed H2 sections in order: Summary · Impact · Timeline (UTC) · Root cause · Detection · Resolution · Action items · Lessons) | `/api/content/postmortems*`, OG images |
| `panels.yaml` | The metrics dashboard: panels, PromQL targets, units, thresholds, 24-column `gridPos` | `/api/content/panels` → dashboard page |
| `alerts.yaml` | Prometheus rule file with the three alerts (`DivyAvailableForHire`, `HighContributionRate`, `LFXApplicationPending`) | `/api/v1/rules` → client-side evaluator |
| `uptime.yaml` | Probe targets for the status page (`$SITE_ORIGIN/readyz` is the self-probe) | collector, uptime page |
| `manual_metrics.yaml` | Hand-maintained gauges with provenance (`source`, `updated_at`) | `/metrics`, `/api/v1/query*` |
| `profile.yaml` | Identity, links, `/healthz` payload, escalation path, `kubectl get pods` rows | `/healthz`, `/contact`, promql console |

## Conventions

- **Dates** are strings, always quoted in YAML: `"2023"`, `"2024-05"`, `"2026-03-01"`. Precision is
  implied by length; month precision is `YYYY-MM`. Log timestamps are RFC 3339 UTC; a month-precision
  event is written as the first of the month with `"precision":"month"`.
- **`TODO(divy)`** marks every fact the brief did not give (exact dates, PR links, URLs, counts). It may
  stand alone or carry a note (`"TODO(divy): PR number and URL"` — quote it when it has a note). Numbers
  cannot hold the marker: leave the known lower bound and put the TODO in the sibling `note`. Never
  invent a value to avoid a TODO; never delete a TODO without replacing it with the fact.
  `make todos` (or `GET /api/content/todos`) lists every marker with file and line.
- **Ids**: span ids are dotted lowercase kebab (`euro-iam.oidc-core`), globally unique; postmortem ids
  are `INC-NNN` and equal the file name; `service` values must exist in `spans.yaml services[]`.
- **Sanitized**: no secrets, no IP addresses, no internal hostnames (generic `proxy-host`, `app-host`,
  `secrets-sidecar` only), no customer or employee data, no person names other than Divy.
- **Honest numbers**: manual metrics are labelled `source` + `updated_at` and shown as such; approximate
  values are written approximately (`containers_approx`, `"1000+"`).

## Validate

```bash
make validate
```

Runs `divy validate --strict content/` (schema check, id/link/date rules, PromQL parse of every panel
and alert expression, postmortem section order, sanitizer) and exits non-zero on any error. The same
validation runs at `divy serve` startup and in CI. Quick local checks without the binary:

```bash
python3 -c 'import yaml,sys; [yaml.safe_load(open(f)) for f in sys.argv[1:]]' content/*.yaml && python3 -c 'import json,sys; [json.loads(l) for l in open("content/logs.ndjson") if l.strip()]' && promtool check rules content/alerts.yaml
```
