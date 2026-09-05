# Deviations from the spec (append-only; one line per deviation: "- <area>: <what> — <why>")

- content: `uptime.yaml` `self-api.url` is the literal `$SITE_ORIGIN/readyz` (vercel-adaptation), not a `format: uri` value as content.md §C.8 requires — the loader must expand `$SITE_ORIGIN` before URI validation and the collector must skip the target (status `unconfigured`) when `SITE_ORIGIN` is unset.
- content: `logs.ndjson` uses level `error` for the four incident-start lines (content.md §C.4.1 reserves `error` for them) even though the brief lists only info/warn/debug — the incident impact lines stay `warn` as the brief asks.
- content: `oss.kubeflow` has 2 stub children (`oss.kubeflow.contrib-01/02`) although the brief gives no count — "contributions" is plural; Divy adds/removes stubs to match.
