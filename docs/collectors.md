# Collectors, uptime API, favicon sparkline, retention

Everything the site shows about GitHub, PyPI and uptime is written by the collectors in `internal/collector/` into the time-series store and read back through `/api/v1/*`, `/metrics`, `/api/uptime` and `/favicon.svg`. Nothing is faked: a source that is not configured or not reachable leaves its series absent and its run recorded as skipped or failed.

## Running them

| Mode | How | Cadence |
|---|---|---|
| Local / Docker | `divy serve --collect` (in-process scheduler) or `divy collect` (scheduler without the HTTP server) | one goroutine per collector, `COLLECT_<NAME>_INTERVAL`, jittered start `i×5s + rand(0,10s)` (retention 60 s), per-run timeout `min(interval/2, 2m)` |
| One round | `divy collect --once [--only github,pypi] [--budget 8s]` prints `collector ok items duration error`, exits 1 when a collector failed (skipped collectors do not fail the command) | every selected collector runs, cadence ignored |
| Vercel | `GET\|POST /api/collect` with `Authorization: Bearer $DIVY_COLLECT_TOKEN` (or `$CRON_SECRET`); called every 5 minutes by `.github/workflows/collect.yml` and daily by the Vercel cron | one bounded round (`DIVY_COLLECT_BUDGET`, default 8 s; `?budget=` can only narrow it). A collector whose last successful run is younger than its interval minus `min(interval/10, 1m)` is reported as `skipped: not due`; `?force=1` runs every collector |

Registration order (fixed): `github, pypi, uptime, manual, retention` (`cmd/api/main.go` `registerCollectors`). `COLLECT_DISABLED=pypi,uptime` registers the named collectors as disabled (every run `skipped: disabled by COLLECT_DISABLED`, no `collector_runs` row).

Every run is recorded in `collector_runs` (`ok`, `items`, `error` ≤ 500 chars, secrets redacted) and counted in `divy_collector_runs_total{collector,result}` (`ok|error|timeout|skipped`), `divy_collector_run_duration_seconds{collector}`; `divy_collector_last_success_timestamp_seconds{collector}` is read from `collector_runs` at scrape time (survives restarts and serverless instances). `/readyz` `checks.collectors.<name>` reports `ok` (fresher than `stale_after_s = max(3×interval, 15m)`), `last_success`, `age_s` and `disabled: true` for a collector without configuration; `checks.db.storage` is `file` (SQLite on disk), `libsql` (Turso) or `ephemeral` (`VERCEL` set without a database URL: a file under `/tmp` that vanishes with the instance — the server logs a warning).

### Environment

| Variable | Default | Used by |
|---|---|---|
| `DIVY_GITHUB_TOKEN` | empty → GitHub collector disabled | github (the only token variable the binary reads) |
| `DIVY_GITHUB_LOGIN` | `divysinghvi` | github (identity guard, search queries) |
| `DIVY_GITHUB_PRIVATE_ORGS` | `gradr` | github privacy rule |
| `PYPI_PACKAGES` | `codemind-ci` | pypi — unioned with the packages found in `content/spans.yaml` links of kind `pypi` (`https://pypi.org/project/<name>/`) |
| `UPTIME_SELF_URL` | `$SITE_ORIGIN/readyz` | uptime (`self-api` target) |
| `PROBE_TIMEOUT` | `10s` | uptime — caps every target's `timeout` |
| `COLLECT_GITHUB_INTERVAL` / `COLLECT_PYPI_INTERVAL` / `COLLECT_UPTIME_INTERVAL` / `COLLECT_MANUAL_INTERVAL` / `COLLECT_RETENTION_INTERVAL` | `15m` / `60m` / `5m` / `15m` / `60m` | scheduler cadence and the `/api/collect` due check; minimum `1m` |
| `COLLECT_DISABLED` | empty | comma list of collector names to register as disabled |
| `DIVY_COLLECT_TOKEN`, `CRON_SECRET`, `DIVY_COLLECT_BUDGET` | empty, empty, `8s` | `/api/collect` |
| `SITE_ORIGIN` | | outbound `User-Agent`: `divy.dev-collector/<version> (+<origin>)` for GitHub/PyPI, `divy-uptime/1.0 (+<origin>)` for probes |

## Sample layout (shared by every counter)

Counters (`*_total`) are stored as **cumulative** values on a daily grid (storage draft §S.2.3): one sample at `dayEnd(d)` = `(d+1) 00:00:00Z` for every day `d` from the series' first known day through yesterday (days without events repeat the previous value), plus exactly one live sample at `now` carrying the value through now; the previous live sample is deleted in the same transaction. Sources with a bounded window keep a frozen prefix: the base is the stored grid value at `dayEnd(w0−1)` (or a written `0` marker when the series is new), so history older than the window is never touched and a retroactive change inside the window rewrites every later grid value. Every run recomputes the whole window and writes only the grid points that are missing or changed (one read per metric per run) — a hosted database meters row writes.

Gauges follow the gauge policy: a sample at `now` iff the value changed or the last sample is ≥ 1 h old (heartbeat). Probe gauges are always written (each probe is a measurement).

Helpers: `collector.CounterSamples`, `collector.Batch` (one transaction per run), `collector.LoadExisting`, `collector.GaugeDue` (`internal/collector/series.go`).

## Metrics

| Metric | Type | Labels | Collector / source | Cadence | History / backfill | Notes |
|---|---|---|---|---|---|---|
| `github_commits_total` | counter | — | github Q1 `contributionsCollection.commitContributionsByRepository[].contributions.nodes{occurredAt, commitCount}`, summed over all repositories | 15m | last 365 days recomputed every run; frozen prefix before; 2 y retention | **counts commits only** (what the catalogue HELP says). Private repositories are counted as numbers, never named |
| `github_contributions_total` | counter | — | github Q1 `contributionCalendar.weeks[].contributionDays[].contributionCount` | 15m | same | commits + issues + PRs + reviews + discussions + repository creations per GitHub's calendar |
| `github_merged_prs_total` | counter | `org` | github Q2 `search(type: ISSUE, query: "is:pr is:merged author:<login>")`, `org` = `repository.owner.login` | 15m | full history from the first merge, recomputed at the end of every scan | private repositories and owners in `DIVY_GITHUB_PRIVATE_ORGS` are counted here only |
| `github_merged_prs_by_repo_total` | counter | `org`, `repo` | github Q2 | 15m | full history | **public repositories outside the private owners only** |
| `github_stars` | gauge | `repo` | github Q4 `repositories(ownerAffiliations: [OWNER], privacy: PUBLIC, isFork: false)` | 15m | from first collection; hourly heartbeat | archived and zero-star repositories included (panels filter with `> 0`) |
| `github_followers` | gauge | — | github Q1 `followers.totalCount` | 15m | from first collection | |
| `oss_prs_open` | gauge | — | github Q3 `search("is:pr is:open author:<login> -user:<login>").issueCount` | 15m | from first collection | |
| `pypi_downloads_total` | counter | `package` | pypi `GET https://pypistats.org/api/packages/<pkg>/overall?mirrors=false`, category `without_mirrors` | 60m | 180 days (pypistats' window) recomputed every run; frozen prefix before | days after the last published date carry the value forward (pypistats lags about a day); once published, yesterday replaces the carried value |
| `pypi_package_info` | gauge (=1) | `package`, `version` | pypi `GET https://pypi.org/pypi/<pkg>/json` `info.version` (with `If-None-Match`; ETag and version cached in `collector_state`) | 60m | current version only: older version series are deleted in the same run | `sum(pypi_package_info)` = packages published |
| `savely_active_users` | gauge | — | manual `content/manual_metrics.yaml` | 15m | from first collection | labelled manual on the panel |
| `lfx_applications` | gauge | `status` | manual | 15m | from first collection | |
| `divy_manual_metric_updated_timestamp_seconds` | gauge | `metric` | manual: `updated_at` as unix seconds at 00:00Z | 15m | from first collection | **absent** while `updated_at` is `TODO(divy)` — the panel prints "last updated: unknown" rather than a made-up date |
| `probe_success` | gauge | `target` | uptime | 5m (per-target `interval`) | 90 days | 1 iff a response arrived and its final status is accepted |
| `probe_duration_seconds` | gauge | `target` | uptime | 5m | 90 days | wall time from request start to body drained (or to the error) |
| `probe_http_status_code` | gauge | `target` | uptime | 5m | 90 days | 0 when no response |

`/metrics` exposes the latest sample of every stored series that is fresher than `max(3 × cadence, 15m)`; `/api/v1/query_range` serves the history.

### Privacy rule (GitHub)

A repository is *private* when `repository.isPrivate` is true **or** its owner login is in `DIVY_GITHUB_PRIVATE_ORGS` (case-insensitive). Private repositories contribute numbers to `github_commits_total`, `github_contributions_total` and `github_merged_prs_total{org}`, and nothing else: their names never reach the database (series labels or `collector_state`), the logs, the run notes or an error message. The merged-PR scan state stores `{"d": "<day>", "o": "<owner>"}` for private events and adds `"r": "<repo>"` only for public ones. The token is redacted from every error text.

### GitHub transport and limits

- `POST https://api.github.com/graphql`, `Authorization: bearer <token>`; one retry after 2 s on network errors and 5xx; 401 → `token rejected` (no retry); 403/429 or `x-ratelimit-remaining: 0` → `rate limited until <reset>` (no retry); GraphQL `errors[]` → the first message.
- Every query selects `rateLimit { cost remaining resetAt }`; a run aborts with `rate limited: N points remaining` when fewer than 200 points remain. Budget per run ≈ 4 (Q1, one query per ≤ 92-day window, issued concurrently) + 1 (Q3) + 1–2 (Q4) + 1–5 (Q2 pages) ≈ 8–12 points.
- Identity guard: `viewer.login` must equal `DIVY_GITHUB_LOGIN` (case-insensitive) on every Q1 response or the run fails and writes nothing.
- `commitContributionsByRepository(maxRepositories: 100)` is the API maximum; `totalRepositoriesWithContributedCommits > 100`, a `hasNextPage` on any repository's contributions, or `sum(commitCount) ≠ totalCommitContributions` mark the run note `commit series may be incomplete` (the run still succeeds). `restrictedContributionsCount` / `hasAnyRestrictedContributions` are logged at debug level per window and never stored.
- Merged-PR scan: one unbounded search first; if `issueCount > 1000` (GitHub's result cap) the window is replaced by one window per `contributionYears` year, and a bounded window over the cap is halved. Up to 5 pages (100 PRs each) per run; the cursor, the windows and the events so far are persisted in `collector_state` key `github.prs` after every page, so the scan resumes on the next round or instance; when it completes, the series are rewritten from the full event list and the next run starts a fresh scan. Note text: `prs=N (scan complete, P pages)` or `prs scan in progress (window i/n, E events so far)`.
- Phases commit independently (Q1 counters → gauges → Q2), so a timeout in a later phase keeps the earlier data; the run is still reported failed and retried.

### Token: what you see without one

| With `DIVY_GITHUB_TOKEN` empty | Behaviour |
|---|---|
| Startup | one warning: `DIVY_GITHUB_TOKEN is empty: the GitHub collector is disabled; github_* and oss_prs_open series stay absent (nothing is faked)` |
| Every run / round | `github  false  0  0ms  skipped: DIVY_GITHUB_TOKEN is empty`; no `collector_runs` row; `divy_collector_runs_total{collector="github",result="skipped"}` increments |
| `/readyz` | `checks.collectors.github = {"ok": null, "last_success": null, "age_s": null, "stale_after_s": 2700, "disabled": true}` |
| `/metrics`, `/api/v1/*` | no `github_*`, no `oss_prs_open` series; `divy_collector_last_success_timestamp_seconds{collector="github"}` absent |
| Dashboard panels with `source.kind: github` | the frontend shows "source unavailable: GitHub collector disabled" (series absent) |
| `/favicon.svg` | flat grey baseline with `<!-- no github samples yet: … -->` |
| `/api/uptime` | unaffected (the `github-profile` probe is an HTTP GET/HEAD of the public profile, no token) |

Token type: a classic PAT with `repo` + `read:user` counts private-org PRs and commits (counts only are ever stored); `public_repo` + `read:user` or a fine-grained PAT gives public data only (the `gradr` bucket then stays empty). Whether private-repository commits appear in Q1 for the token owner is verified by the debug line `github contributions window … restricted=N has_restricted=true|false` on the first run with a token.

## PyPI

Packages = union of `PYPI_PACKAGES` and every `content/spans.yaml` link of kind `pypi` (currently `codemind-ci` from `project.codemind`). Per package the two sources are independent: a pypistats failure (`rate limited (429)`, `package unknown (404)`, a blocked network) does not stop the version lookup and vice versa; the run is reported failed listing every error, with `items` = the samples that were written. `collector_state` keys: `pypi.etag.<pkg>`, `pypi.version.<pkg>`. A package with no download rows yet writes no `pypi_downloads_total` sample (note `no download rows`).

## Uptime prober

Targets come from `content/uptime.yaml` (`$SITE_ORIGIN` expanded at load; `self-api` url replaced by `UPTIME_SELF_URL`). A target whose `url` is `TODO(divy)` is **unconfigured**: never probed, no `probe_results` row, no samples, status `unconfigured`, never green.

| Item | Rule |
|---|---|
| Schedule | every `COLLECT_UPTIME_INTERVAL` tick (or `/api/collect` round) a configured target is probed iff `now − lastProbe ≥ target.interval − 10s` (default `5m`); at most 5 probes run concurrently |
| Request | `GET` (or `method: HEAD`) with `User-Agent: divy-uptime/1.0 (+<SITE_ORIGIN>)`, `Accept: */*`, no cookies, no keep-alive; per-target timeout (default `10s`, capped by `PROBE_TIMEOUT`); at most **5 redirects** when `follow_redirects` is true (default), none otherwise; body drained up to 1 MiB |
| Up | a response arrived **and** its final status is in the target's `expected_status` list; a target without an explicit list accepts any **2xx or 3xx** |
| Row | `probe_results(target, ts_ms, up, latency_ms, status_code, error)`; `error` is `NULL` when up, else `<class>: <message>` (≤ 200 chars, the URL's query string and userinfo stripped) |
| Error classes | `dns` (name resolution), `tls` (handshake / certificate), `timeout` (the target's own timeout), `conn` (refused / reset / unreachable), `http` (`got 503, want 2xx or 3xx` — a response with an unexpected status), `redirect` (more than 5 hops), `read` (failure after the headers), `other` |
| Budget cut | a probe that is still running when the collection round's budget expires records **nothing** (a gap), not a red timeout; the run note says `cut_by_budget=N` and the run is reported failed |
| Retention | `probe_results` and `probe_*` samples: 90 days |
| Self probe | `self-api` is probed from the same process/function that serves the site: a full outage shows as a gap, not red — the target's `note` says so and `/api/uptime` passes it through |

## `GET /api/uptime` and `/api/uptime/heartbeats`

`/api/uptime` is byte-identical to `/api/uptime/heartbeats?days=90&bucket=1d`. Parameters: `days` 1..90 (default 90), `bucket` `1d` (default) or `1h` (requires `days ≤ 7`). Errors: 400 `{"error":"days must be between 1 and 90"}`, `{"error":"bucket=1h requires days<=7"}`, `{"error":"bucket must be 1d or 1h"}`. Headers: `Cache-Control: public, max-age=60, s-maxage=60`, weak `ETag` (304 on `If-None-Match`). Rollups are computed on read from the raw `probe_results` rows of the window.

```json
{
  "generated_at": "2026-09-05T10:43:40Z",
  "days": 90,
  "bucket": "1d",
  "targets": [
    {
      "target": "github-profile",
      "name": "GitHub profile",
      "url": "https://github.com/divysinghvi",
      "span": null,
      "note": null,
      "status": "down",
      "last": {"ts": "2026-09-05T10:43:02Z", "up": false, "latency_ms": 244.738, "status_code": 403, "error": "http: got 403, want 2xx or 3xx"},
      "uptime": {"24h": 0, "7d": 0, "30d": 0, "90d": 0},
      "buckets": [{"ts": "2026-09-05T00:00:00Z", "samples": 1, "up_ratio": 0, "avg_latency_ms": 244.738, "max_latency_ms": 244.738}],
      "incidents": []
    },
    {
      "target": "savely-landing",
      "name": "Savely landing page",
      "url": "TODO(divy)",
      "span": "project.savely",
      "note": null,
      "status": "unconfigured",
      "last": null,
      "uptime": {"24h": null, "7d": null, "30d": null, "90d": null},
      "buckets": [],
      "incidents": []
    },
    {
      "target": "self-api",
      "name": "This site's API (self)",
      "url": "http://127.0.0.1:18082/readyz",
      "span": null,
      "note": "probed from the same function that serves the site: a full outage shows as a gap, not red",
      "status": "up",
      "last": {"ts": "2026-09-05T10:43:02Z", "up": true, "latency_ms": 2.062, "status_code": 200, "error": null},
      "uptime": {"24h": 1, "7d": 1, "30d": 1, "90d": 1},
      "buckets": [{"ts": "2026-09-05T00:00:00Z", "samples": 1, "up_ratio": 1, "avg_latency_ms": 2.062, "max_latency_ms": 2.062}],
      "incidents": [{"started_at": "2026-09-03T00:00:00Z", "ended_at": "2026-09-03T00:15:00Z", "duration_s": 900, "probes": 3, "first_error": "timeout: context deadline exceeded"}]
    }
  ]
}
```

(The incident above is illustrative; the live run had none.)

| Field | Rule |
|---|---|
| `targets[]` | every target of `uptime.yaml` in file order; `url` verbatim (`TODO(divy)` when unconfigured), `span` and `note` null when absent |
| `status` | `up` (last probe up) · `down` (last probe not up) · `unconfigured` (TODO url) · `unknown` (configured, no probe yet) |
| `last` | newest probe ever (may be older than the window), or null |
| `uptime.<24h|7d|30d|90d>` | `sum(up)/count` over the probes of the trailing window; **null** when the window is longer than `days` or has no probes — a fresh deploy shows null, not 100 % |
| `buckets[]` | one entry per UTC day (`1d`) or hour (`1h`) **that has probes**, ascending: `samples`, `up_ratio` = up/samples, `avg_latency_ms` (over probes with a latency), `max_latency_ms`. Days without probes are simply absent — the page paints them grey, never green |
| `incidents[]` | maximal runs of **≥ 2 consecutive failed probes** inside the window, newest first: `started_at` (first failed probe), `ended_at` (first successful probe after the run, or null while ongoing), `duration_s` (to `ended_at` or to now), `probes` (failed probes in the run), `first_error` (`<class>: <message>` of the first failed probe). A single failed probe is a blip: visible in the day's `up_ratio`, not an incident |

## `GET /favicon.svg` (and `/favicon.ico`)

A 32×32 sparkline of the last seven UTC days of commits, derived from the stored `github_commits_total` series: `count(d) = grid(dayEnd(d)) − grid(dayEnd(d−1))` for complete days, `count(today) = live − grid(dayEnd(yesterday))`; a missing grid point (or a downward rewrite) counts as 0. Geometry: `viewBox="0 0 32 32"`, `x_i = 3 + i×26/6`, `y_i = 27 − v_i / max(1, max v) × 20` (one decimal), background `#0b0c0e` (`rx=6`), green `#73bf69` polyline (`stroke-width 2.5`, round caps/joins) with a dot on the last point. Headers: `Content-Type: image/svg+xml`, `Cache-Control: public, max-age=3600, s-maxage=3600`, strong `ETag` (sha256 of the body, 304 on `If-None-Match`); `HEAD` supported.

Example body for counts `3 0 5 2 7 1 4`:

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" width="32" height="32">
<!-- github commits per UTC day, 2026-08-30..2026-09-05: 3 0 5 2 7 1 4 -->
<rect width="32" height="32" rx="6" fill="#0b0c0e"/>
<polyline fill="none" stroke="#73bf69" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" points="3,18.4 7.3,27 11.7,12.7 16,21.3 20.3,7 24.7,24.1 29,15.6"/>
<circle cx="29" cy="15.6" r="2" fill="#73bf69"/>
</svg>
```

No data (no `github_commits_total` samples at all — collector disabled or never run): the same background with a flat grey (`#5b6069`) baseline at `y=27` and the comment `<!-- no github samples yet: github_commits_total has no series (GitHub collector disabled or never run) -->`; never a fake curve.

`/favicon.ico` → **404** `{"error":"not found: the favicon is the live sparkline at /favicon.svg"}` with `Cache-Control: public, max-age=86400, s-maxage=86400` (review coverage-17: a committed sparkline-shaped `.ico` would be a static sparkline; the SVG `<link rel="icon" type="image/svg+xml">` is the only favicon).

## Manual collector

Every run writes each entry of `content/manual_metrics.yaml` as `<metric>{labels} = value` under the gauge policy (`savely_active_users`, `lfx_applications{status="pending"}`) and `divy_manual_metric_updated_timestamp_seconds{metric}` = `updated_at` at 00:00Z — skipped while `updated_at` is `TODO(divy)` (run note `updated_at_todo=<metrics>`). Content is loaded once at startup, so a changed number appears as a step at the next deploy: genuine history of the hand-maintained value. `items` = samples written (0 on an idle run inside the heartbeat is normal).

## Retention collector

Hourly (`COLLECT_RETENTION_INTERVAL`), 60 s after start in the scheduler, and on `/api/collect` when due. Limits are constants in `internal/store` (no env knobs): samples of non-`probe_` metrics 730 days; `probe_*` samples and `probe_results` 90 days; `otel_spans` 24 h **and** the newest 20 000; `collector_runs` 30 days, unfinished runs older than 1 h marked `ok=0, error='abandoned'`; series without samples deleted; then a WAL checkpoint (file mode). `items` = rows deleted or marked; note lists the per-table counts or `nothing expired`.

## Live check in the build sandbox (2026-09-05, no token)

The sandbox proxy blocks `pypistats.org` and answers 403 for `github.com/divysinghvi` (GET and HEAD, confirmed with curl outside the binary); `pypi.org` and the local API are reachable. `divy collect --once` against a fresh database:

```
collector    ok     items  duration  error
github       false      0       0ms  skipped: DIVY_GITHUB_TOKEN is empty
pypi         false      1     282ms  codemind-ci downloads: pypistats: Get "https://pypistats.org/api/packages/codemind-ci/overall?mirrors=false": Forbidden
uptime       true      12     253ms
manual       true       2      19ms
retention    true       0      17ms
budget 8000ms truncated=false
```

- pypi: the pypi.org half succeeded (`pypi_package_info{package="codemind-ci",version="0.2.0"} 1`), the pypistats half is recorded as the run's error; `/readyz` shows `pypi.ok: null` (never succeeded) until pypistats is reachable.
- uptime: `pypi-codemind` up (200, 94 ms), `self-api` up (200, 2 ms), `github-profile` **down** — `http: got 403, want 2xx or 3xx` (the sandbox egress; outside it the profile answers 200), `savely-landing` and `codemind-demo` unconfigured.
- `/metrics` exposes the three `probe_*` families for the three probed targets, `pypi_package_info`, `savely_active_users`, `lfx_applications{status="pending"}` and `divy_collector_last_success_timestamp_seconds` for `manual`, `retention`, `uptime`; `make promtool-check` passes.
- `/api/collect` 45 s later reported `uptime`, `manual`, `retention` as `skipped: not due (last success 46s ago, interval …)`, `github` skipped (no token), `pypi` failed again (it never succeeded, so it stays due); `?force=1` ran every collector; without a bearer token the endpoint answers 401.
- `VERCEL=1` without a database URL: `/readyz` `checks.db.storage = "ephemeral"` and a startup warning.
