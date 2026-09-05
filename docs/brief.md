# BRIEF (verbatim from the user, Divy)

Build prompt: `divy.dev` — a live observability stack whose only monitored service is me
You are a senior full-stack + SRE engineer building my personal portfolio. The concept is not "a Grafana-themed page." It is a working, queryable observability platform (metrics, logs, traces, uptime, alerts, postmortems) where the thing under observation is my career. Every panel must be backed by a real API you build; nothing is a static image or fake sparkline.
Work in explicit phases with checkpoints. At the end of each phase, stop, summarize what exists, list how to run it, and wait for my go-ahead before the next phase. Every command you give me must be runnable as a single paste. Be direct and concrete; no filler.

## 1. Who I am (source of truth for all content)

* Name: Divy. GitHub: `divysinghvi`. Based in Rajasthan, India.
* Education: B.Tech Electronics & Communication Engineering, College of Technology and Engineering, Udaipur, 2023–2027.
* Current role: Part-time Product Engineer at Gradr (Swedish EdTech startup, gradr.se). Joined Dec 2025 as Intern, promoted to Product Engineer Mar 2026. I own production observability infrastructure (Loki, Promtail, Prometheus, Grafana, self-hosted Sentry, Uptime Kuma, Caddy, Authelia, Infisical, on Hetzner) and ship full-stack product features.
* Previous:
   * Go/IAM Intern, Euro Technologies (Aug–Nov 2025) — built Euro-IAM from scratch: multi-tenant OIDC provider in Go/Gin/GORM/Postgres/Redis with Asynq workers, WebAuthn, TOTP, magic links, SSO.
   * Software Engineering Intern, EF Polymer Ltd. (Japanese AgriTech startup, May 2024–Jul 2025) — built a Sales & Warehouse Management System deployed across multiple warehouses; worked with the Japanese team remotely and in person over 12 months.
   * Freelance web/app development before that.
* Open source:
   * CNCF contributor: 15+ merged PRs in Kubernetes/minikube; contributions to Kubeflow.
   * Applied to LFX Mentorship 2026 Term 3 (Velero: CSI Snapshot E2E tests in Kind CI; also targeting OpenTelemetry).
   * Explored WasmEdge Wide Arithmetic proposal; built a 128-bit arithmetic library in C++/x86-64 as prep.
   * Exploring Playwright ecosystem contributions.
* Projects:
   * CodeMind (`codemind-ci` on PyPI, v0.2.0) — persistent Cognee-powered memory layer for codebases that uses git history to detect contradictory PRs; GitHub Actions workflows + CLI. Built for the WeMakeDevs Cognee hackathon.
   * Savely — Chrome price-comparison extension (FastAPI/Svelte/Postgres), 5,000+ active users.
* Community: Core team for GDG and AWS community events.
* Quant: WorldQuant IQC Stage 1 — 2nd Prize, ranked 98 globally. Active on WorldQuant BRAIN.
* Stack I actually use: SvelteKit, Prisma, Python/FastAPI, Go, Docker, Caddy, Hetzner, Postgres, Redis, Prometheus/Loki/Grafana/Sentry.
* Interests: low-level systems (x86-64 asm, WebAssembly, LLVM), competitive programming, quant finance.
* Open to: backend/infra/SRE internships and roles.

Where you need a fact I haven't given (exact dates, PR links, numbers), leave a clearly marked `TODO(divy)` placeholder in a single `content/` data file — never invent.

## 2. Architecture

```
divy.dev
├── web/          SvelteKit 2 + Svelte 5 (runes), TypeScript, Tailwind. Static-ish, SSR for OG tags.
├── api/          Go 1.23+, stdlib net/http + chi. Single binary. SQLite (modernc.org/sqlite) for time series.
├── collector/    Go cron (in the same binary, `api serve --collect`) that pulls GitHub GraphQL,
│                 PyPI stats, uptime probes, and writes samples.
├── content/      YAML/JSON: spans, log lines, postmortems, panels. The ONLY place prose lives.
├── deploy/       Dockerfile (multi-stage, distroless), docker-compose.yml, Caddyfile, systemd unit.
└── README.md
```

Rules:

* The frontend is a genuine client of the Go API. No content is hardcoded in Svelte components.
* The Go API speaks real protocols:
   * `GET /metrics` — Prometheus text exposition format. Must pass `promtool check metrics`.
   * `GET /api/v1/query`, `/api/v1/query_range`, `/api/v1/series`, `/api/v1/labels` — a Prometheus-HTTP-API-compatible subset (instant vector selectors, label matchers, `rate()`, `sum()`, `increase()`, `[range]`). Enough that a real Grafana instance can add `divy.dev` as a Prometheus data source and it works. Document exactly which PromQL subset is supported.
   * `GET /loki/api/v1/query_range` — Loki-compatible subset: stream selectors `{service="gradr"}`, line filters `|=`, `!=`, `|~`, and `| json`.
   * `GET /api/traces/:traceId` — Jaeger/OTLP-JSON shaped trace.
   * `GET /healthz`, `GET /readyz`.
* Rate limit public endpoints (token bucket per IP, generous). Cache collector results. GitHub token only via env.
* Everything must run locally with `docker compose up` and a sample `.env`.

## 3. The site, panel by panel

### 3.1 Hero: the career trace
A full-width distributed trace waterfall (Jaeger/Tempo style). Root span `divy.career` from 2023 to now. Child spans:

* `edu.btech-ece` (2023 → 2027, still open, dashed end)
* `freelance.web-dev`
* `ef-polymer.swe-intern` (May 2024 → Jul 2025) with child spans `sales-wms.build`, `sales-wms.multi-warehouse-rollout`, `japan.onsite`
* `euro-tech.go-iam-intern` (Aug → Nov 2025) with children `euro-iam.oidc-core`, `euro-iam.webauthn`, `euro-iam.asynq-workers`
* `gradr.intern` (Dec 2025 → Mar 2026) → `gradr.product-engineer` (Mar 2026 → open) with a long child `gradr.observability` and nested short spans for real incidents (see §3.5), plus `gradr.product-features`
* `oss.minikube` (many small spans, one per merged PR — stub with TODOs), `oss.kubeflow`, `oss.lfx-velero-application`, `oss.wasmedge-prep`
* `project.codemind`, `project.savely`
* `quant.worldquant-iqc` (event marker: 2nd Prize)

Each span has: service color, duration, tags (`stack`, `role`, `lang`, `location`), and span events (promotion, first prod deploy, outage resolved). Clicking a span opens a right-hand detail drawer exactly like Jaeger's. Overlapping spans render on parallel rows. Time axis has a draggable minimap. Keyboard nav: `j/k` between spans, `Enter` to open, `Esc` to close. Must be fully usable on a 390px phone (collapse to a vertical timeline).

### 3.2 Metrics dashboard
A grid of Grafana-style panels, all fed by `/api/v1/query_range`:

* `github_commits_total` (weekly rate, 1y), `github_merged_prs_total{org=...}` stacked by org (`kubernetes`, `kubeflow`, `gradr` private → count only, others), `github_stars_total{repo=...}`
* `pypi_downloads_total{package="codemind-ci"}` (from pypistats)
* `savely_active_users` (manual gauge with TODO source; show "last updated" honestly)
* `oss_prs_open` gauge, `lfx_applications{status="pending"}` gauge
* `divy_uptime_seconds` (process uptime, a little joke that's also real)
* A stat panel row: years of experience, merged PRs, packages published, active users — every number computed from data, not typed. Panels support time range picker (24h / 7d / 30d / 1y / all), hover crosshair, legend toggle, and a "View query" button that shows the exact PromQL and a `curl` you can copy.

### 3.3 Logs explorer
Loki-style. My history as structured log lines stored in `content/logs.ndjson`, e.g.

```
{"ts":"2026-03-01T00:00:00Z","level":"info","service":"gradr","msg":"promoted to Product Engineer","from":"intern"}
{"ts":"2025-11-20T00:00:00Z","level":"info","service":"euro-tech","msg":"shipped Euro-IAM: multi-tenant OIDC, WebAuthn, TOTP, magic links, SSO","lang":"go"}
{"ts":"...","level":"warn","service":"gradr","component":"dev-proxy","msg":"cascading memory exhaustion: sentry containers saturating swap","resolved":true}
```

Write ~60–100 lines from §1 covering every span. Levels: `info` for milestones, `warn` for incidents, `debug` for fun details (first line of asm written, LeetCode streak, etc.). UI: query bar with LogQL autocomplete, level filter chips, live-tail toggle that replays lines with a typewriter cadence, click a line to expand JSON, link from log lines to their trace span.

### 3.4 Uptime
Uptime Kuma-style status page with real probes run by the collector every 5 min: Savely landing page, CodeMind demo URL, PyPI package page, my GitHub profile, this site's own API. 90-day heartbeat bars, current latency, incident history. If something is down, it shows red. Do not fake green.

### 3.5 Postmortems (this replaces "Projects")
Write 4 incident reports in blameless-SRE format: Summary · Impact · Timeline (UTC) · Root cause · Detection · Resolution · Action items · Lessons. Sanitize: no secrets, no internal hostnames beyond generic ones, no customer data. Base them on:

1. `INC-001` Post-reboot race: secrets-injection sidecar writing `.env` after app containers had already started → Supabase-backed service outage. Fixed with startup ordering + healthcheck gating.
2. `INC-002` Cascading memory exhaustion on the proxy host: self-hosted Sentry's ~65 containers dominating RAM/swap. Triage, profiling, right-sizing to an errors-only profile.
3. `INC-003` Sentry self-hosted outbound email failures → switched relay to Resend on 2465 and fixed from-address config.
4. `INC-004` Error-tracking signal quality: a capture gate silently dropping critical infra errors + unbounded fingerprinting creating 1000+ duplicate issues. Fixes and alert hygiene. Each postmortem links to its span in the hero trace. Render as monospace-heavy documents with a sticky TOC and a severity badge.

### 3.6 Alerts
An Alertmanager-style panel with live rules evaluated client-side against the API:

* `DivyAvailableForHire` — `for: 30s` after page load, severity `page`, annotation: "Open to backend/infra internships. Runbook: /contact"
* `HighContributionRate` — fires when commits/week > threshold
* `LFXApplicationPending` — fires while `lfx_applications{status="pending"} > 0` Firing alerts slide in as toasts, exactly like Grafana's, with a "Silence" button that actually silences for the session.

### 3.7 Contact / runbook
`/contact` styled as a runbook: "Escalation path", email, GitHub, LinkedIn, resume PDF download, calendly-style link (TODO). Include a copyable `curl divy.dev/healthz` that returns `{"status":"ok","open_to":["backend intern","infra","sre"],"tz":"Asia/Kolkata"}`.

## 4. Easter eggs (all must actually work)

* `curl divy.dev/metrics` returns a real, valid exposition. Mention in README: "Add divy.dev as a Prometheus data source in your Grafana."
* `curl -H "Accept: text/plain" divy.dev/` returns an ASCII-art trace waterfall of my career in the terminal.
* `/robots.txt` includes `# Observability for humans: /metrics`.
* Typing `promql` anywhere on the site opens a floating query console; `kubectl get pods` in it returns my projects as pods (`gradr-observability 1/1 Running`, `savely 1/1 Running 5000+ users`, `lfx-velero 0/1 Pending`).
* Konami code switches the whole UI to a dark-blue 2017-era Grafana theme.
* The favicon is a tiny live sparkline of my last 7 days of commits (SVG, generated by the API).
* `X-Divy-Trace-Id` header on every response; paste it into the trace viewer and it resolves to the request's own span (self-tracing with OpenTelemetry SDK in the Go API — real OTel, real spans).

## 5. Design direction

* Dark by default, light theme supported. Palette inspired by Grafana 11 (near-black bg `#0b0c0e`, panel `#181b1f`, accent green `#73bf69`, warn `#f2cc0c`, error `#f2495c`, blue `#5794f2`) but do not copy Grafana's logo or wordmark.
* Type: JetBrains Mono for data, Inter for prose. Dense but breathable; panels have real headers, kebab menus, and resize handles (drag-to-rearrange the dashboard grid with layout persisted in the URL hash so I can share layouts).
* Motion: sparklines draw in, trace bars grow from the left, log tail streams. 60fps, `prefers-reduced-motion` respected.
* Every panel has an "Explore" affordance that jumps to the underlying query — the site should feel like a tool, not a brochure.
* Lighthouse ≥ 95 on all four. Full keyboard accessibility. Real OG images per postmortem (generated server-side).

## 6. Phases and checkpoints
Phase 0 — Plan. Repo layout, data schemas (spans, logs, samples, postmortems), the exact PromQL/LogQL subset, and an API contract table. Stop for approval.
Phase 1 — Go API + collector. `/metrics`, `/api/v1/*`, `/loki/api/v1/*`, `/api/traces/*`, `/healthz`, OTel self-tracing, SQLite schema + migrations, GitHub/PyPI/uptime collectors, tests (`go test ./...`), `promtool check metrics` passing. Provide `make dev`. Stop.
Phase 2 — Content. All of `content/`: complete span tree, 60–100 log lines, 4 postmortems, alert rules, panel definitions. Every unknown as `TODO(divy)`. Stop; I will fill TODOs.
Phase 3 — Frontend core. SvelteKit app, API client, hero trace viewer (desktop + mobile), metrics dashboard with time range picker. Stop.
Phase 4 — Frontend rest. Logs explorer, uptime, postmortems, alerts, contact, easter eggs, theming, drag-grid. Stop.
Phase 5 — Deploy. Multi-stage Dockerfile, compose, Caddyfile with automatic TLS for `divy.dev`, systemd unit, Hetzner deploy script, GitHub Actions CI (lint, test, build, deploy on tag). README with one-command local run and one-command deploy. Stop.
At each checkpoint give me: what was built, exact files touched, one paste-able command to run/verify it, and open questions (max 5). Never move to the next phase without my "go".

## 7. Non-negotiables

* No fake data. If a metric can't be sourced yet, show it with an explicit "source: manual / last updated" label.
* No content in components; everything from `content/` via the API.
* Type-safe end to end (Go structs → JSON schema → TS types generated, not hand-written twice).
* Tests for the PromQL/LogQL parsers with table-driven cases.
* Secrets only from env; sample `.env.example` committed.
* Mobile is a first-class target, not a fallback.
