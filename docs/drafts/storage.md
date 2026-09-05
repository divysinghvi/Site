# Storage, collectors, metric catalogue, caching, rate limiting

## Cross-section notes

Divergences from, or additions to, other sections. Everything else here follows CONVENTIONS §5–§7, §10 and the repo section (`draft-repo.md`: subcommands, env names, package layout) verbatim.

| # | Note | Affects |
|---|------|---------|
| S-X1 | **`github_commits_total` counts commits only.** A per-day commits-only series is obtainable (`commitContributionsByRepository[].contributions.nodes{occurredAt, commitCount}` — "How many commits were made on this day to this repository by the user"), so the metric keeps its name honestly. The calendar's mixed count (commits + issues + PRs + reviews + repo creations) is exposed as an additional counter **`github_contributions_total`**. Content §C.6 `commits-weekly` description and `HighContributionRate` summary must say "commits", not "contribution-calendar events". | Content, alerts |
| S-X2 | **Engine contract.** Stored series are daily-gridded (one sample per UTC day boundary + one live sample), so the PromQL engine's default lookback delta must be **26h** (constant `promql.DefaultLookback`, overridable per query with the real Prometheus `lookback_delta` parameter). The engine should implement `rate()`/`increase()` with Prometheus's exact extrapolation (`extrapolatedRate`, threshold 1.1 × average sample spacing) and the 11,000-points-per-series cap so numbers equal what a real Prometheus would compute on the same data. | PromQL section |
| S-X3 | **Live series.** `divy_uptime_seconds`, `divy_build_info`, `divy_open_to_work`, `divy_experience_years` are never stored; they are functions of `t` served by a `LiveSeries` provider (§S.2.5) that both `/metrics` and `/api/v1/query*` consult before SQLite. | PromQL section, API |
| S-X4 | Adds the **`manual` collector** (samples `content/manual_metrics.yaml` into SQLite so its history is real) with `COLLECT_MANUAL_INTERVAL` (default `15m`) — one more row in the env table and one more `--only` value. | Repo (env table, `divy collect --only`) |
| S-X5 | Adds **`divy backup <out.db>`** (`VACUUM INTO`). `divy migrate --to N` is forward-only (N below the current version is an error; rollback = restore a backup). | Repo (subcommand table) |
| S-X6 | `GET /api/uptime/heartbeats` (repo R7) is specified in §S.4.3 as a superset of R7's shape: adds `bucket=1d\|1h`, per-window uptime ratios (`null` when no data), `status`, `last`, `incidents[]`. Field names `target`, `buckets[].ts/up_ratio/avg_latency_ms/samples` are kept. | API contract, frontend |
| S-X7 | `.env.example` comment on `GITHUB_TOKEN` should read: "classic PAT with `repo` + `read:user` to count private-org PRs/commits; `public_repo` + `read:user` (or a fine-grained PAT) for public data only". Fine-grained tokens include public read only (verified); org-private access with them is not verified. | Repo (`.env.example`) |
| S-X8 | 429 bodies on `/api/v1/*` use the Prometheus envelope with `errorType: "unavailable"` (a real Prometheus errorType); `/loki/*` uses the Loki section's error shape; everything else `{"error": "..."}`. | API |
| S-X9 | Adds `divy_collector_run_duration_seconds{collector}` (histogram) to the catalogue. | Catalogue |

## S.1 SQLite

### S.1.1 Opening the database

Two `*sql.DB` handles on the same file (`DIVY_DB`), both `modernc.org/sqlite` (driver name `sqlite`, CGo-free; `CGO_ENABLED=0` build verified).

| Handle | DSN | Pool | Purpose |
|--------|-----|------|---------|
| `w` (writer) | `file:{DIVY_DB}?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_txlock=immediate` | `SetMaxOpenConns(1)`, `SetMaxIdleConns(1)`, `SetConnMaxLifetime(0)` | migrations, every write (one goroutine, §S.1.5) |
| `r` (readers) | `file:{DIVY_DB}?_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)` | `SetMaxOpenConns(max(4, GOMAXPROCS))` | all query/read paths; WAL readers never block the writer and are never blocked by it |

Rules:

| Rule | Detail |
|------|--------|
| PRAGMAs are per connection | `_pragma=` runs on every pooled connection at open. `journal_mode=WAL` is persistent in the file (a plain second connection reports `wal`); `busy_timeout`, `synchronous`, `foreign_keys` are not, hence the DSN on both handles. |
| WAL is mandatory | After opening `w`, `PRAGMA journal_mode` must return `wal`; otherwise exit 1 with `sqlite: WAL not available at <path> (network filesystem?)`. |
| `_txlock=immediate` on the writer | Write transactions take the reserved lock at `BEGIN`, so a busy database waits in `busy_timeout` instead of failing on lock upgrade. |
| `synchronous=NORMAL` | Durable against process crash; a power loss may lose the last transactions but cannot corrupt the file in WAL mode. Collector data is re-collectable, so this trade is correct. |
| Directory | `os.MkdirAll(filepath.Dir(DIVY_DB), 0o750)` before open. |
| Startup order | open `w` → WAL check → migrations (§S.1.4) → open `r` → load series cache + latest index (§S.1.6) → start writer goroutine. |
| Shutdown | stop collectors → drain writer queue → `PRAGMA wal_checkpoint(TRUNCATE)` → close `w`, `r`. |
| Measured with the pinned driver (SQLite 3.53.4) | 1M-row insert in one transaction 2.05 s; PK range read of 1,000 rows 252 µs; a reader during an open write transaction returned 20,000 rows in 1.19 ms; a second writer with `busy_timeout(0)` fails immediately (`SQLITE_BUSY`), with 5000 it waited 331 ms for a 300 ms holder and succeeded. |

### S.1.2 DDL — `api/internal/store/migrations/0001_init.sql` (complete)

```sql
-- 0001_init.sql — divy.dev time-series store. Timestamps: unix milliseconds (samples, probe_results,
-- collector_runs), unix nanoseconds (otel_spans). Applied inside one transaction by the migrator.

CREATE TABLE schema_migrations (
  version    INTEGER PRIMARY KEY,           -- NNNN from the file name
  name       TEXT    NOT NULL,              -- file name without version/extension, e.g. 'init'
  applied_ms INTEGER NOT NULL
);

CREATE TABLE series (
  id     INTEGER PRIMARY KEY,               -- rowid; referenced by samples
  metric TEXT NOT NULL CHECK (length(metric) BETWEEN 1 AND 200),
  labels TEXT NOT NULL CHECK (labels = '{}' OR (substr(labels, 1, 2) = '{"' AND substr(labels, -1) = '}')),
  UNIQUE (metric, labels)                   -- also the index used for metric lookups (leftmost prefix)
);

CREATE TABLE samples (
  series_id INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
  ts_ms     INTEGER NOT NULL CHECK (ts_ms > 0),
  value     REAL    NOT NULL,               -- finite float64; NaN/Inf rejected in Go (SQLite stores NaN as NULL)
  PRIMARY KEY (series_id, ts_ms)
) WITHOUT ROWID;                            -- small fixed rows; the PK *is* the table: range scans by (series, time)

CREATE TABLE probe_results (
  target      TEXT    NOT NULL,             -- uptime.yaml targets[].id
  ts_ms       INTEGER NOT NULL CHECK (ts_ms > 0),
  up          INTEGER NOT NULL CHECK (up IN (0, 1)),
  latency_ms  REAL    CHECK (latency_ms IS NULL OR latency_ms >= 0),
  status_code INTEGER NOT NULL DEFAULT 0 CHECK (status_code BETWEEN 0 AND 999),   -- 0 = no HTTP response
  error       TEXT,                         -- NULL when up = 1; '<class>: <sanitized message>' otherwise
  PRIMARY KEY (target, ts_ms)
) WITHOUT ROWID;
CREATE INDEX probe_results_ts ON probe_results (ts_ms);          -- retention delete by age

CREATE TABLE otel_spans (
  trace_id        TEXT    NOT NULL CHECK (length(trace_id) = 32),   -- lowercase hex, OTel/Jaeger style
  span_id         TEXT    NOT NULL CHECK (length(span_id) = 16),
  parent_span_id  TEXT    CHECK (parent_span_id IS NULL OR length(parent_span_id) = 16),
  name            TEXT    NOT NULL,
  service         TEXT    NOT NULL,         -- resource service.name (OTEL_SERVICE_NAME)
  start_unix_nano INTEGER NOT NULL,
  end_unix_nano   INTEGER NOT NULL CHECK (end_unix_nano >= start_unix_nano),
  attributes      TEXT    NOT NULL DEFAULT '{}',   -- JSON object; includes span.kind, http.*, otel.scope.*
  events          TEXT    NOT NULL DEFAULT '[]',   -- JSON array of {time_unix_nano, name, attributes}
  status_code     INTEGER NOT NULL DEFAULT 0 CHECK (status_code IN (0, 1, 2)),   -- OTel: 0 UNSET, 1 OK, 2 ERROR
  status_msg      TEXT,
  UNIQUE (trace_id, span_id)
);                                          -- rowid table: rows are large (JSON), WITHOUT ROWID would hurt
CREATE INDEX otel_spans_start ON otel_spans (start_unix_nano);   -- retention by age and the 20k cap

CREATE TABLE collector_runs (
  id          INTEGER PRIMARY KEY,
  collector   TEXT    NOT NULL,             -- github | pypi | uptime | manual | retention
  started_ms  INTEGER NOT NULL,
  finished_ms INTEGER,                      -- NULL while running
  ok          INTEGER CHECK (ok IS NULL OR ok IN (0, 1)),
  error       TEXT,                         -- ≤ 500 chars, secrets scrubbed
  items       INTEGER NOT NULL DEFAULT 0    -- samples/rows written (or deleted, for retention)
);
CREATE INDEX collector_runs_collector_started ON collector_runs (collector, started_ms);
```

Example rows:

```
series:         id=7  metric='github_merged_prs_total'  labels='{"org":"kubernetes"}'
samples:        series_id=7  ts_ms=1756944000000 (2026-09-04T00:00:00Z)  value=15
probe_results:  target='github-profile' ts_ms=1757052300000 up=1 latency_ms=142.3 status_code=200 error=NULL
otel_spans:     trace_id='4bf92f3577b34da6a3ce929d0e0e4736' span_id='00f067aa0ba902b7' parent_span_id=NULL
                name='GET /api/v1/query_range' service='divy-api' start_unix_nano=1757052300123456789
                end_unix_nano=1757052300131456789 attributes='{"http.request.method":"GET","http.route":"/api/v1/query_range","http.response.status_code":200,"span.kind":"server"}'
                events='[]' status_code=0 status_msg=NULL
collector_runs: id=912 collector='github' started_ms=1757052000123 finished_ms=1757052001987 ok=1 error=NULL items=1461
```

### S.1.3 Column tables

`series`

| Column | Type | Constraints | Meaning |
|--------|------|-------------|---------|
| `id` | INTEGER | PK (rowid) | series identifier used by `samples`; stable for the file's lifetime |
| `metric` | TEXT | not null, 1–200 chars, `[a-zA-Z_:][a-zA-Z0-9_:]*` (checked in Go) | metric family name (`__name__`) |
| `labels` | TEXT | not null, canonical JSON object (§S.2.1) | label set without `__name__`; `{}` when none |
| (unique) | | `UNIQUE(metric, labels)` | series identity |

`samples`

| Column | Type | Constraints | Meaning |
|--------|------|-------------|---------|
| `series_id` | INTEGER | FK → `series.id`, cascade delete | |
| `ts_ms` | INTEGER | > 0 | sample time, unix ms UTC |
| `value` | REAL | not null; finite | float64 sample value |
| (pk) | | `PRIMARY KEY(series_id, ts_ms)` WITHOUT ROWID | one value per series per millisecond; upsert target |

`probe_results`

| Column | Type | Constraints | Meaning |
|--------|------|-------------|---------|
| `target` | TEXT | not null | `uptime.yaml` target id (= `target` label of `probe_*`) |
| `ts_ms` | INTEGER | > 0 | probe start time |
| `up` | INTEGER | 0/1 | 1 iff a response arrived and its status ∈ `expected_status` |
| `latency_ms` | REAL | ≥ 0 or NULL | wall time from request start to body drained (or to the error) |
| `status_code` | INTEGER | 0–999, default 0 | final HTTP status; 0 when no response |
| `error` | TEXT | NULL when up | `<class>: <message>` (§S.4.3), ≤ 200 chars |

`otel_spans`

| Column | Type | Constraints | Meaning |
|--------|------|-------------|---------|
| `trace_id` / `span_id` / `parent_span_id` | TEXT | hex 32 / 16 / 16-or-NULL | OTel ids as hex |
| `name`, `service` | TEXT | not null | span name; resource `service.name` |
| `start_unix_nano`, `end_unix_nano` | INTEGER | end ≥ start | |
| `attributes` | TEXT | JSON object, validated in Go | span attributes + `span.kind` + `otel.scope.name` |
| `events` | TEXT | JSON array | `[{"time_unix_nano":…, "name":…, "attributes":{…}}]` |
| `status_code`, `status_msg` | INTEGER, TEXT | 0/1/2 | OTel status |

`collector_runs`

| Column | Type | Constraints | Meaning |
|--------|------|-------------|---------|
| `id` | INTEGER | PK | |
| `collector` | TEXT | one of the collector names | |
| `started_ms`, `finished_ms` | INTEGER | finished NULL while running | |
| `ok` | INTEGER | NULL/0/1 | NULL while running |
| `error` | TEXT | ≤ 500 chars | first error; `abandoned` if the process died mid-run (set by retention) |
| `items` | INTEGER | ≥ 0 | rows written (deleted for retention) |

### S.1.4 Migrations

| Item | Decision |
|------|----------|
| Mechanism | Hand-rolled, forward-only, embedded SQL: `//go:embed migrations/*.sql` in `api/internal/store`. No goose: six tables, no down migrations, one fewer dependency and version table. `pressly/goose/v3` (v3.28.0, `SetBaseFS` + `SetDialect("sqlite3")`) is the named fallback if the count passes ~20 files or a down path is ever needed. |
| File naming | `NNNN_snake_name.sql` — `NNNN` zero-padded, strictly increasing, unique; `name` = the rest. A file is immutable once merged (CI test compares embedded files against `testdata/migrations.sha256`). |
| Statements | Plain SQL; multiple statements per file (multi-statement `Exec` verified with the driver). No `-- +goose` annotations. |
| Algorithm | On `w`: `CREATE TABLE IF NOT EXISTS schema_migrations(...)`; read applied versions; assert the applied set is exactly a prefix of the embedded set (else exit 1: `migration gap: 0003 applied but 0002 not` / `database is newer than binary: has 0007, binary knows 0005`); for each pending file in order: `BEGIN IMMEDIATE` → exec file → `INSERT INTO schema_migrations(version, name, applied_ms)` → `COMMIT`. A failing file rolls back only itself; the process exits 1. |
| Where it runs | `divy serve`, `divy collect` (any mode) and `divy migrate` all run it before doing anything else. Two processes racing (e.g. `collect --once` beside `serve`) serialize on the write lock; the second sees nothing pending. |
| `divy migrate` | `--status` prints `version  name  applied_at` for every embedded file plus `pending`/`applied`; no flags = apply all pending; `--to N` = apply pending files with version ≤ N; `N < current` → exit 1 `down migrations are not supported; restore a backup (divy backup)`. |
| Tests | `TestMigrate_Fresh` (all files on a temp DB, idempotent second run), `TestMigrate_Gap`, `TestMigrate_NewerDB`, `TestSchema_Matches` (`sqlite_schema` snapshot golden file so unintended DDL edits fail CI). |

### S.1.5 Single writer

```go
// api/internal/store/store.go
type Store struct {
    w, r    *sql.DB
    jobs    chan job            // capacity 64
    series  *seriesCache        // id ↔ (metric, labels), all series; only the writer mutates it
    latest  *latestIndex        // series id → newest (ts_ms, value); only the writer mutates it
    gen     atomic.Uint64       // bumped after every committed write; the response cache keys on it
}

type job struct {
    ctx  context.Context
    fn   func(tx *Tx) error     // runs inside one SQLite transaction
    done chan error
}

// Write queues fn and blocks until it committed or failed. ctx cancellation before dequeue returns ctx.Err().
func (s *Store) Write(ctx context.Context, fn func(tx *Tx) error) error
```

| Rule | Detail |
|------|--------|
| One goroutine | `run()` loops over `jobs`: `BEGIN IMMEDIATE` (via `w.BeginTx`), `fn(tx)`, `COMMIT`; on success it applies the staged `seriesCache`/`latestIndex` mutations recorded on `Tx`, increments `gen`, then replies on `done`. |
| One transaction per unit of work | A collector run's whole backfill is one job (readers see the old or the new grid, never half). The OTel exporter sends one job per export batch. Retention sends one job per table/chunk (≤ ~100 ms of lock time each). |
| `Tx` helpers | `SeriesID(metric string, labels Labels) (int64, error)` (`INSERT … ON CONFLICT(metric, labels) DO UPDATE SET metric = excluded.metric RETURNING id`, cached), `UpsertSamples(id, []Sample)`, `DeleteSamples(id, fromMs, toMs)`, `DeleteOffGrid(id)` (`ts_ms % 86400000 != 0`), `DeleteSeriesWhere(metric, func(Labels) bool)`, `InsertProbes([]Probe)`, `InsertSpans([]Span)`, `StartRun(name) int64`, `FinishRun(id, ok, err, items)`, `Delete*Before(...)`. Prepared statements are per transaction (`tx.Stmt`). |
| Back-pressure | Queue full → `Write` blocks (bounded by the caller's ctx); collectors have run timeouts, so a stuck disk surfaces as `result="timeout"` not as unbounded memory. |
| Cross-process | `divy collect --once` is a second writer process; SQLite serializes them (`busy_timeout` 5 s). All collector writes are idempotent upserts, so interleaving is safe. A wait beyond 5 s fails that job with `database is locked`; the run is recorded as an error and retried at the next tick. |

### S.1.6 Read path (what `query_range` needs)

Engine → store contract: `Select(ctx, metric string, matchers []Matcher, fromMs, toMs int64) ([]SeriesData, error)` where `SeriesData{Labels, Samples []Sample}` and samples satisfy `fromMs < ts_ms ≤ toMs`, ascending.

| Step | How | Why |
|------|-----|-----|
| 1. Resolve series | In memory: `seriesCache.byMetric[metric]` → apply matchers (`=`, `!=`, `=~`, `!~`; regex anchored `^(?:…)$`) in Go. | ≤ ~100 series total; no SQL round trip; regex matchers need no SQL support. |
| 2. Fetch samples | ≤ 16 ids: one statement per series `SELECT ts_ms, value FROM samples WHERE series_id = ? AND ts_ms > ? AND ts_ms <= ? ORDER BY ts_ms`. > 16 ids: `… WHERE series_id IN (?,…) AND ts_ms > ? AND ts_ms <= ? ORDER BY series_id, ts_ms` in chunks of 100 ids. | Both use the PK (`SEARCH samples USING PRIMARY KEY (series_id=? AND ts_ms>? AND ts_ms<?)`, verified plan); 1,000 rows in 0.25 ms. |
| 3. Window | The engine asks for `[start − max(lookback, largest range in the expression), end]` once per expression, then evaluates every step from that slice. | One SQL pass per series per request regardless of `step`; the lookback sample before `start` is included. |
| 4. Guards | Engine: `(end − start)/step > 11000` → `bad_data` "exceeded maximum resolution of 11,000 points per timeseries" (Prometheus's message). Store: > 2,000,000 rows selected → `execution` "query selects too many samples". | Same limits a real Prometheus applies; bounds memory. |
| 5. Consistency | Reads use `r`; each statement sees a WAL snapshot. The series cache may be a few ms ahead of the snapshot (a brand-new series with no visible samples yields an empty series → dropped). | Acceptable; no locking on the read path. |
| Live series | Before step 1 the engine asks `live.Lookup(metric)`; a hit is evaluated per step from the function (§S.2.5) and SQLite is not touched. | |
| Latest values | `latestIndex` (loaded at startup with `SELECT series_id, max(ts_ms), value …` per series, then maintained by the writer) serves `/metrics` (§S.2.6) without SQL. | `/metrics` must be cheap and synchronous. |

### S.1.7 Retention job

Collector name `retention`, every `COLLECT_RETENTION_INTERVAL` (60m), first run 60 s after start. Limits are constants in `store/retention.go` (CONVENTIONS fixes them; no env knobs):

| Data | Keep | Statement(s) (each its own write job) |
|------|------|----------------------------------------|
| `samples`, metrics not starting with `probe_` | 730 days | per series: `DELETE FROM samples WHERE series_id = ? AND ts_ms < ?` (PK range) |
| `samples`, `probe_*` | 90 days | same, per probe series |
| `probe_results` | 90 days | `DELETE FROM probe_results WHERE ts_ms < ?` (`probe_results_ts`) |
| `otel_spans` | 24 h **and** newest 20,000 | `DELETE FROM otel_spans WHERE start_unix_nano < ?`; then `DELETE FROM otel_spans WHERE rowid IN (SELECT rowid FROM otel_spans ORDER BY start_unix_nano DESC LIMIT -1 OFFSET 20000)` |
| `collector_runs` | 30 days; runs with `finished_ms IS NULL AND started_ms < now − 1h` → `ok = 0, error = 'abandoned'` | two statements |
| `series` without samples | delete | `DELETE FROM series WHERE id NOT IN (SELECT DISTINCT series_id FROM samples)` (also drops them from the cache and latest index) |
| WAL | checkpoint | `PRAGMA wal_checkpoint(PASSIVE)` at the end |

The run's `items` = rows deleted. Retention never touches the current UTC day.

### S.1.8 Size estimate at steady state (2 years)

Measured basis (§facts S6): ≈ 19 B per `samples` row, ≈ 75 B per `probe_results` row including its index.

| Data | Rows | Size |
|------|------|------|
| Backfilled counters, daily grid (`github_commits_total`, `github_contributions_total`, `pypi_downloads_total`×1, `github_merged_prs_total`×~5 orgs, `github_merged_prs_by_repo_total`×~20 repos) | ≈ 28 series × 730 | ≈ 20k rows, 0.4 MB |
| Gauges, change-or-hourly policy (`github_stars`×~40 repos, `github_followers`, `oss_prs_open`, `pypi_package_info`, manual ×4) | ≈ 47 series × 17,520 | ≈ 820k rows, 16 MB (upper bound; changes are rarer than the heartbeat) |
| Probe samples (5 targets × 3 metrics × 288/day × 90 d, rolling) | 389k | 7.4 MB |
| `probe_results` (5 × 288 × 90) | 130k | 9.7 MB |
| `otel_spans` (cap 20,000 × ~1 KB) | 20k | ≤ 20 MB |
| `collector_runs` (30 d × ~530/day) | 16k | < 2 MB |
| **Total** | | **≈ 55 MB** (+ WAL up to a few MB between checkpoints). A 1 GB volume is an order of magnitude more than needed. |

### S.1.9 Backup

| Item | Decision |
|------|----------|
| Command | `divy backup [--db PATH] <out.db>` → `VACUUM INTO ?` on the reader handle. Produces one compact, transactionally consistent file with no `-wal`/`-shm` companions (verified). Exit 1 if `<out.db>` exists. |
| Schedule | Deploy section: daily systemd timer / cron `docker compose exec api /divy backup /data/backups/divy-$(date -u +%F).db` and keep the last 7. |
| Never | copy `divy.db` alone while the service runs — committed rows may still be in `divy.db-wal` until the next checkpoint. |
| Restore | stop the service, replace `divy.db` (delete stale `-wal`/`-shm`), start; migrations are a no-op. |

## S.2 Sample semantics

### S.2.1 Series identity and label canonicalization

| Rule | Detail |
|------|--------|
| Identity | `(metric, labels)`; `labels` is the canonical JSON object of the label set **without** `__name__`. |
| Canonical JSON | Go `encoding/json` on `map[string]string` with `Encoder.SetEscapeHTML(false)`, no indentation, trailing newline trimmed. Keys are sorted bytewise (guaranteed by `encoding/json`); values JSON-escaped. Empty set → `{}`. |
| Validation (writer) | metric `^[a-zA-Z_:][a-zA-Z0-9_:]*$`; label names `^[a-zA-Z_][a-zA-Z0-9_]*$`, not starting with `__`; values any UTF-8, ≤ 256 bytes; ≤ 8 labels per series. |
| Examples | `github_merged_prs_by_repo_total` + `{"org":"kubernetes","repo":"minikube"}`; `probe_success` + `{"target":"github-profile"}`; `github_followers` + `{}`. |
| Cardinality | Bounded by the catalogue (§S.5): repos and targets are the only open-ended label values; both are lists the user controls. |

### S.2.2 Samples

| Rule | Detail |
|------|--------|
| A sample | `(series_id, ts_ms, value)`: an observation that the series had `value` at instant `ts_ms` (unix ms, UTC). |
| Timestamp source | Collector-derived: day boundaries for backfilled history, `time.Now().UnixMilli()` for live observations (§S.2.3–4). Never the source's own server time. |
| Upsert | `INSERT INTO samples(series_id, ts_ms, value) VALUES (?,?,?) ON CONFLICT(series_id, ts_ms) DO UPDATE SET value = excluded.value`. Re-collecting the same instant overwrites — this is what makes backfill idempotent and lets retroactive source changes replace history instead of stacking on it. |
| Values | float64, finite. The writer rejects NaN/±Inf with an error before SQL (SQLite would store NaN as NULL and violate `NOT NULL`). Counters and `probe_success` are integers stored as REAL. |
| Grid vs live | `grid sample` ⇔ `ts_ms % 86_400_000 == 0` (a UTC day boundary); every other sample is `live`. `dayEnd(d)` = `(d + 1 day) 00:00:00Z` in ms; a grid sample at `dayEnd(d)` carries the value "through the end of day d". |

### S.2.3 Counter layout (all collector `*_total` metrics)

1. **Grid.** One sample at `dayEnd(d)` for every day `d` from the series' first known day through yesterday (UTC). Value = cumulative count through the end of `d`. Days without events repeat the previous value, so the maximum gap between samples is 24 h.
2. **Live.** Exactly one off-grid sample at `now` = cumulative through now. In the same transaction the previous live sample(s) of the series are deleted (`DeleteOffGrid`). A 24h-range graph therefore shows yesterday's boundary and the current value; intraday resolution is not pretended where the source has none.
3. **Monotonic by construction.** Grid values are prefix sums of non-negative daily counts; live ≥ last grid. `rate()`/`increase()` therefore never see a false counter reset. A day whose count is *lowered* by the source (deleted commits, un-merged PR) rewrites all later grid values downward in one transaction — still monotonic within the new series, and Prometheus-correct: the history simply becomes what the source now says.
4. **Frozen prefix (sources with a bounded window).** Let `w0` be the first day inside the source window. `base` = stored grid value at `dayEnd(w0 − 1)` if present, else 0 (and a 0 sample is written at `dayEnd(w0 − 1)` so the series has a start). Grid inside the window = `base + running sum`. Samples before the window are never touched. Consequence: a retroactive change inside the window is absorbed; one older than the window cannot be seen and stays as collected.
5. **Query implications** (engine, S-X2): with daily samples and lookback 26h every instant query resolves; `increase(github_commits_total[7d])` at `t` sees 7–8 samples and Prometheus's extrapolation applies (e.g. 7 grid points spanning 6 days extrapolate by 7/6 toward the range edges). The "View query" button shows exactly this PromQL, so a Grafana instance pointed at `/api/v1/query_range` reproduces the panel's number; `/metrics` only carries the latest value per series and is not where history comes from.

Worked example — `github_merged_prs_total{org="kubernetes"}`, merges on 2026-08-30 (2) and 2026-09-03 (1), run at 2026-09-05T10:15Z, first merge ever 2026-08-30:

```
ts (UTC)               value  kind
2026-08-30T00:00:00Z   0      grid (dayEnd(08-29): start marker)
2026-08-31T00:00:00Z   2      grid (through 08-30)
2026-09-01T00:00:00Z   2      grid
2026-09-02T00:00:00Z   2      grid
2026-09-03T00:00:00Z   2      grid
2026-09-04T00:00:00Z   3      grid (through 09-03)
2026-09-05T00:00:00Z   3      grid (through 09-04)
2026-09-05T10:15:00Z   3      live
```

### S.2.4 Gauge write policy

| Metric class | Policy |
|--------------|--------|
| Collected gauges (`github_stars`, `github_followers`, `oss_prs_open`, `pypi_package_info`, manual gauges and their timestamps) | Write a sample at `now` iff `value != latest.value` **or** `now − latest.ts ≥ 1h` (heartbeat). Idle series cost 24 rows/day; a 26h lookback always finds a sample. |
| Probe gauges (`probe_*`) | Always written — each probe is a measurement. |
| History depth | Starts at first collection (no source history); the plan says so per panel (`source.note`). |

### S.2.5 Live series (never stored)

```go
// api/internal/metrics/live.go
type LiveSeries interface {
    Metric() string
    Help() string
    Type() prometheus.ValueType
    Eval(t time.Time) []LabeledValue   // empty = absent at t
}
```

| Metric | `Eval(t)` | Labels |
|--------|-----------|--------|
| `divy_uptime_seconds` | `t − processStart` in seconds; absent for `t < processStart` | — |
| `divy_build_info` | `1` for all `t` | `version`, `commit`, `go_version` (`runtime.Version()`) |
| `divy_open_to_work` | `profile.open_to_work ? 1 : 0` for all `t` | — |
| `divy_experience_years` | `(t − E) / 365.25 d` where `E` = earliest resolved `start` among spans whose service has `counts_as_experience: true` (TODO dates skipped); absent for `t < E` | — |

Both `/metrics` (evaluated at scrape time) and the PromQL engine (evaluated per step) use the same provider, so `divy_uptime_seconds` over a range is a real ramp, not a repeated constant.

### S.2.6 `/metrics` derivation and staleness

| Rule | Detail |
|------|--------|
| Source | `latestIndex` (one entry per stored series) + live series + client_golang collectors (`NewGoCollector`, `NewProcessCollector`, HTTP and collector metrics). Private registry; `promhttp.HandlerFor(reg, promhttp.HandlerOpts{})`. |
| Stored series | An unchecked `prometheus.Collector` (`Describe` sends nothing) whose `Collect` emits `MustNewConstMetric(desc(metric), type(metric), latest.value, labelValues…)` for every series whose latest sample is **not stale**. Descs (HELP/TYPE) come from the catalogue (§S.5). |
| Staleness rule | A series is hidden when `now − latest.ts > staleAfter(metric)`, `staleAfter = max(3 × collector interval, 15m)`: GitHub 45 m, PyPI 3 h, probes 15 m, manual 45 m. |
| Why hide | A scraper stamps whatever we expose with its own scrape time. Re-exposing a value the collector has not confirmed for three cycles would manufacture freshness (brief §7: no fake data). The correct signal is the series going stale in the scraper plus `divy_collector_last_success_timestamp_seconds{collector}` staying visible and old. History in `/api/v1/query_range` is unaffected. |
| Timestamps | None on `/metrics` (Prometheus guidance: "You should not set timestamps on the metrics you expose"). |
| Ordering | Families sorted by name; series by canonical label string; deterministic output → golden test `testdata/metrics.golden` + `promlint` run inside `go test` (`promlint.New(bytes.NewReader(body)).Lint()` must return no problems) in addition to `promtool check metrics` in CI. |
| Example | `github_merged_prs_total{org="kubernetes"} 15` `probe_success{target="github-profile"} 1` `divy_collector_last_success_timestamp_seconds{collector="uptime"} 1.757052301e+09` |

## S.3 Collector framework

```go
// api/internal/collector/collector.go
type Collector interface {
    Name() string                                   // github | pypi | uptime | manual | retention
    Interval() time.Duration                        // COLLECT_<NAME>_INTERVAL
    Run(ctx context.Context) (items int, err error) // one complete collection; must respect ctx
}
```

Collectors receive `*store.Store`, the loaded content model and `config.Config` at construction (`github.New(cfg, st)`, …).

| Concern | Specification |
|---------|---------------|
| Scheduler | One goroutine per collector. Initial delay `i × 5s + rand(0, 10s)` (`i` = registration index; retention waits 60 s), then `run → sleep(interval × U(0.9, 1.1)) → repeat`. Jitter prevents lockstep hits on GitHub/PyPI after restarts. |
| Per-run timeout | `min(interval / 2, 2m)` via `context.WithTimeout`; result `timeout` when `ctx.Err() == DeadlineExceeded`. Uptime targets keep their own per-probe timeouts inside that budget. |
| Single-flight | Per collector `sync.Mutex.TryLock()`; a tick arriving while the previous run still holds it is skipped (`result="skipped"`). |
| Run recording | `StartRun(name)` inserts `collector_runs(collector, started_ms)`; `FinishRun(id, ok, err, items)` fills the rest. Error text ≤ 500 chars; the GitHub client redacts the `Authorization` header from any error it wraps. |
| Metrics | `divy_collector_runs_total{collector,result}` (`ok`, `error`, `timeout`, `skipped`; in-process CounterVec, resets on restart), `divy_collector_last_success_timestamp_seconds{collector}` (GaugeVec; seeded at startup from `max(finished_ms) WHERE ok=1` per collector so it survives restarts; absent if never succeeded), `divy_collector_run_duration_seconds{collector}` (histogram, buckets 0.05…120 s). |
| Logging | One structured line per run: `collector=github ok=true items=1461 dur=1.9s gh_cost=6 gh_remaining=4988`. |
| Disabled collectors | `github` without `GITHUB_TOKEN`: registered as a stub whose `Run` returns `ErrDisabled` → `result="skipped"` each tick, no `collector_runs` row, one startup warning. All `github_*`/`oss_prs_open` families are absent from `/metrics` and `/api/v1/*`; `divy_collector_last_success_timestamp_seconds{collector="github"}` is absent. The frontend renders "source unavailable: GitHub collector disabled" for panels with `source.kind: github` when that series is absent, and "stale since …" when it is older than 3 × cadence. `COLLECT_DISABLED=pypi,uptime` (comma list; default empty) disables others the same way. |
| `divy collect --once [--only a,b]` | Runs the selected collectors sequentially in the fixed order `github, pypi, uptime, manual, retention`, prints `collector  ok  items  duration  error`, exits 1 if any failed. `divy collect` without `--once` runs the scheduler with no HTTP server. Safe beside a running `serve --collect` (§S.1.5). |
| Env | `COLLECT_GITHUB_INTERVAL=15m`, `COLLECT_PYPI_INTERVAL=60m`, `COLLECT_UPTIME_INTERVAL=5m`, `COLLECT_MANUAL_INTERVAL=15m`, `COLLECT_RETENTION_INTERVAL=60m`, `COLLECT_DISABLED=`, `GITHUB_TOKEN`, `GITHUB_LOGIN=divysinghvi`, `GITHUB_PRIVATE_ORGS=gradr`, `PYPI_PACKAGES=codemind-ci`, `UPTIME_SELF_URL`, `PROBE_TIMEOUT=10s`. Intervals below `1m` are rejected at startup. |
| Graceful shutdown | `Scheduler.Stop(ctx)` cancels the loop context (in-flight HTTP calls abort), waits ≤ 10 s on a `WaitGroup`; runs cut short are recorded `ok=0 error="shutdown"`; then the store drains its queue and checkpoints. |
| Outbound HTTP | One shared `http.Client` per collector: `Timeout` 30 s (GitHub/PyPI), `User-Agent: divy.dev-collector/<version> (+https://divy.dev)`; probes use their own client (§S.4.3). No proxies, no cookies. |

## S.4 Collectors

### S.4.1 GitHub (`api/internal/collector/github`)

Transport: `POST https://api.github.com/graphql`, `Authorization: bearer $GITHUB_TOKEN`, `Content-Type: application/json`, `User-Agent` as above. One retry after 2 s on network errors / 5xx; on 401 → error `token rejected`; on 403/429 or `x-ratelimit-remaining: 0` → error `rate limited until <reset>` (no retry); any GraphQL `errors[]` → error with the first message. Every query selects `rateLimit { cost remaining resetAt }`; `remaining < 200` aborts the run.

Identity guard: the first query's `viewer.login` must equal `GITHUB_LOGIN` (case-insensitive) or the run fails with `token belongs to <login>, expected <GITHUB_LOGIN>` and writes nothing.

Token: classic PAT `repo` + `read:user` to see private-org PRs and commits (counts only are ever stored); `public_repo` + `read:user` or a fine-grained PAT for public data only.

**Q1 — commits and contributions per day, followers.** Run 4 times with consecutive windows covering `[today − 365d, now]`, each ≤ 92 days, `from` = window start `00:00:00Z`, `to` = window end (last window: `now`). ≤ 92 day-nodes per repository per window ⇒ `contributions(first: 100)` never paginates. Cost 1–2 points each.

```graphql
query Contributions($login: String!, $from: DateTime!, $to: DateTime!) {
  viewer { login }
  user(login: $login) {
    followers { totalCount }
    contributionsCollection(from: $from, to: $to) {
      contributionYears
      totalCommitContributions
      totalRepositoriesWithContributedCommits
      restrictedContributionsCount
      hasAnyRestrictedContributions
      contributionCalendar {
        totalContributions
        weeks { contributionDays { date contributionCount } }
      }
      commitContributionsByRepository(maxRepositories: 100) {
        repository { nameWithOwner isPrivate owner { login } }
        contributions(first: 100, orderBy: { field: OCCURRED_AT, direction: ASC }) {
          totalCount
          pageInfo { hasNextPage endCursor }
          nodes { occurredAt commitCount }
        }
      }
    }
  }
  rateLimit { cost remaining resetAt }
}
```

| Output | Feeds | Mapping |
|--------|-------|---------|
| `contributionDays[].{date, contributionCount}` | `github_contributions_total` | `count[date] = contributionCount` |
| `commitContributionsByRepository[].contributions.nodes[].{occurredAt, commitCount}` | `github_commits_total` | `commits[date(occurredAt, UTC)] += commitCount` summed over all repositories, private ones included as numbers; repository names never leave the collector (logs print counts only) |
| `followers.totalCount` | `github_followers` | gauge policy |
| `contributionYears` | Q2 year windows | |
| `totalCommitContributions`, `totalRepositoriesWithContributedCommits`, `hasNextPage`, `restrictedContributionsCount` | sanity | `sum(commits) != totalCommitContributions`, `> 100` repositories, or any `hasNextPage` → run still `ok`, warning `commit series may be incomplete` logged with the numbers; the `restricted` numbers are logged every run (they settle the private-visibility question at Phase 1). |

**Backfill (both daily counters), every run:** window = the 365 collected days; `w0` = first day; apply §S.2.3 rule 4 (`base` from the stored grid at `dayEnd(w0 − 1)` or 0); grid samples at `dayEnd(d)` for `w0 ≤ d ≤ yesterday`; `DeleteOffGrid`; live sample at `now` = grid(yesterday) + count(today). Yesterday's value is rewritten every 15 min, today's live value moves during the day, and GitHub's UTC day boundary matches ours. Retroactive calendar changes (late-pushed commits, private-repo visibility toggles) rewrite the affected day and every later grid value in the same transaction.

**Q2 — merged PRs.** For each calendar year `Y` from `min(contributionYears)` to now: `$q = "is:pr is:merged author:<login> merged:Y-01-01..Y-12-31"`, pages of 100 (1 point each) until `hasNextPage` is false. If a window's `issueCount > 1000` (the search cap) bisect the `merged:` range and recurse.

```graphql
query MergedPRs($q: String!, $after: String) {
  search(type: ISSUE, query: $q, first: 100, after: $after) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {
        mergedAt
        repository { name isPrivate owner { login } }
      }
    }
  }
  rateLimit { cost remaining resetAt }
}
```

| Rule | Detail |
|------|--------|
| `org` label | `repository.owner.login` as returned. |
| Privacy | `repository.isPrivate == true` **or** `owner.login ∈ GITHUB_PRIVATE_ORGS` (case-insensitive) → only `github_merged_prs_total{org}` is updated. Otherwise also `github_merged_prs_by_repo_total{org,repo}` with `repo = repository.name`. Private repository names never reach SQLite, logs or errors. |
| Backfill | Events `(mergedAt, org, repo?)` sorted; per series: grid from `dayEnd(firstMerge − 1)` (value 0) through yesterday with carry-forward, `DeleteOffGrid`, live at `now` = total. The full history is recomputed every run from the full search result, so a deleted/renamed repository or un-merged PR is reflected on the next run. Series that stop receiving events (repo deleted) go stale on `/metrics` after 45 m; their history stays. |

**Q3 — open OSS PRs.** `search(type: ISSUE, query: "is:pr is:open author:<login> -user:<login>", first: 1) { issueCount }` → `oss_prs_open` (gauge policy).

**Q4 — stars.**

```graphql
query Repos($login: String!, $after: String) {
  user(login: $login) {
    repositories(first: 100, after: $after, ownerAffiliations: [OWNER], privacy: PUBLIC, isFork: false,
                 orderBy: { field: STARGAZERS, direction: DESC }) {
      totalCount
      pageInfo { hasNextPage endCursor }
      nodes { name stargazerCount isArchived }
    }
  }
  rateLimit { cost remaining resetAt }
}
```

`github_stars{repo=<name>}` for every public, non-fork repository owned by the login (archived included; 0-star repos included — panels filter with `github_stars > 0`). Gauge policy.

Budget: 4 (Q1) + ≤ 3 (Q2, small history) + 1 (Q3) + 1 (Q4) ≈ 9 points per run, 96 runs/day ≈ 900 points/day against 5,000/hour.

Without a token: see §S.3 "Disabled collectors". With the sample `.env` (`GITHUB_TOKEN=` empty) `docker compose up` shows exactly that state; nothing is faked.

### S.4.2 PyPI (`api/internal/collector/pypi`)

For each package in `PYPI_PACKAGES` (comma list), one transaction per package (a failing package does not block the others; the run is `error` if any failed):

| Request | Response used | Notes |
|---------|---------------|-------|
| `GET https://pypistats.org/api/packages/{pkg}/overall?mirrors=false` | `{"data":[{"category":"without_mirrors","date":"YYYY-MM-DD","downloads":N},…],"package":"…","type":"overall_downloads"}` | 180-day window; rows with missing `downloads` skipped; HTTP 429 → error `rate limited`, no retry inside the run (next run is an hour later). `data` absent → package unknown → error. |
| `GET https://pypi.org/pypi/{pkg}/json` with `If-None-Match: <last ETag>` | `info.version`; `releases[version][].upload_time_iso_8601` (logged only) | PyPI serves `ETag` and `Cache-Control: max-age=900, public`; a 304 reuses the previous body. UA includes a contact URL per PyPI guidance. |

**Backfill** — `pypi_downloads_total{package}` (§S.2.3 rule 4): `w0` = first `date` in `data`; `base` = grid value at `dayEnd(w0 − 1)` or a written 0; for `d` from `w0` to `max(date)`: `cum += downloads(d)` (missing days = 0) → grid at `dayEnd(d)`; days after `max(date)` through yesterday carry `cum` forward (pypistats lags about a day); `DeleteOffGrid`; live at `now` = `cum`. Yesterday's count, once published or revised by pypistats, replaces the carried value on the next run. Because the window slides, days that leave it become the frozen prefix — the cumulative never restarts.

`pypi_package_info{package,version} = 1`: gauge policy; when `info.version` changes, `DeleteSeriesWhere("pypi_package_info", labels.package == pkg && labels.version != new)` in the same transaction, then write the new series — exactly one series per package (Content X3: `sum(pypi_package_info)` = packages published).

### S.4.3 Uptime (`api/internal/collector/uptime`)

Targets from `content/uptime.yaml` (§C.8) with `self-api`'s `url` replaced by `UPTIME_SELF_URL`. Targets whose `url` is `TODO(divy)` are skipped: no `probe_results` row, no samples, status `unconfigured` (never green).

| Item | Specification |
|------|---------------|
| Schedule | Every `COLLECT_UPTIME_INTERVAL` tick, each target is probed iff `now − lastProbe(target) ≥ target.interval − 10s` (per-target `interval` is a multiple of the tick; default 5m). |
| Concurrency | Semaphore 5; each probe has its own `context.WithTimeout(target.timeout)` where `timeout ≤ PROBE_TIMEOUT` (validator caps it). |
| Client | `http.Client{Timeout: timeout, Transport: &http.Transport{DisableKeepAlives: true, TLSHandshakeTimeout: timeout, ForceAttemptHTTP2: true}}`; `CheckRedirect`: Go default (≤ 10 hops) when `follow_redirects: true`, else `return http.ErrUseLastResponse`. Headers `User-Agent: divy.dev-uptime/<version> (+https://divy.dev/uptime)`, `Accept: */*`. No cookies, no auth. |
| Method | `GET` (default) or `HEAD`. Body: `io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))`. |
| Latency | Wall time from `client.Do` start to body drained (or to the error). `probe_duration_seconds = latency_ms / 1000`. |
| Up | `up = 1` iff a response arrived **and** its final status ∈ `expected_status` (default `[200]`). With `follow_redirects: false` a 3xx counts only if listed. |
| Status code | Final response status; `0` when none. |
| Error classes | `dns` (`*net.DNSError`), `timeout` (`context.DeadlineExceeded` or `net.Error.Timeout()`), `tls` (`*tls.CertificateVerificationError`, `x509.*Error`, `tls.RecordHeaderError`, `tls.AlertError`), `connect` (`*net.OpError` wrapping `ECONNREFUSED`/`EHOSTUNREACH`/`ENETUNREACH`/`ECONNRESET` before headers), `redirect` (too many redirects), `read` (error after headers), `http_status` (response with unexpected code, message `got 503, want [200]`), `other`. Stored as `"<class>: <message>"`, message ≤ 200 chars with the URL's query string and any userinfo stripped. |
| Writes | One transaction per tick: `probe_results` rows (`INSERT … ON CONFLICT(target, ts_ms) DO UPDATE`) and three samples per probed target (`probe_success`, `probe_duration_seconds`, `probe_http_status_code`), always written (no dedup). |
| Retention | 90 days for `probe_results` and `probe_*` samples (§S.1.7). |
| Metrics | `probe_success{target}`, `probe_duration_seconds{target}`, `probe_http_status_code{target}` — blackbox_exporter names, so existing Grafana uptime dashboards work against `/api/v1/query_range`. |

**Rollup and API.** `GET /api/uptime/heartbeats?days=90&bucket=1d` (`bucket` ∈ `1d` (default, `days` ≤ 90) | `1h` (`days` ≤ 7)); `Cache-Control: public, max-age=60`; response cache 60 s.

```json
{"generated_at":"2026-09-05T10:20:00Z","days":90,"bucket":"1d",
 "targets":[
  {"target":"github-profile","name":"GitHub profile","url":"https://github.com/divysinghvi","span":null,
   "status":"up",
   "last":{"ts":"2026-09-05T10:15:03Z","up":true,"latency_ms":142.3,"status_code":200,"error":null},
   "uptime":{"24h":1.0,"7d":0.9993,"30d":0.9991,"90d":null},
   "buckets":[{"ts":"2026-06-08T00:00:00Z","samples":288,"up_ratio":1.0,"avg_latency_ms":151.2,"max_latency_ms":802.0}],
   "incidents":[{"started_at":"2026-09-01T03:05:00Z","ended_at":"2026-09-01T03:15:00Z","duration_s":600,"probes":2,"first_error":"timeout: context deadline exceeded"}]},
  {"target":"savely-landing","name":"Savely landing page","url":"TODO(divy)","span":"project.savely",
   "status":"unconfigured","last":null,"uptime":{"24h":null,"7d":null,"30d":null,"90d":null},"buckets":[],"incidents":[]}]}
```

| Field | Rule |
|-------|------|
| `status` | `up` (last probe up), `down` (last probe not up), `unconfigured` (TODO url), `unknown` (configured, no probe yet) |
| `uptime.<window>` | `sum(up)/count` over `probe_results` in the window; `null` when the window has no rows (a fresh deploy shows `null`, not 100 %) |
| `buckets[]` | `SELECT (ts_ms / B) * B AS bucket_ms, count(*), sum(up), avg(latency_ms), max(latency_ms) FROM probe_results WHERE target = ? AND ts_ms >= ? GROUP BY bucket_ms ORDER BY bucket_ms` with `B` = 86 400 000 or 3 600 000; only buckets that have rows are returned (the frontend paints missing buckets grey). `up_ratio` = `sum(up)/samples`. |
| `incidents[]` | maximal runs of consecutive `up = 0` rows per target within `days`, computed in Go from that target's rows (≤ 25,920), newest first, `ended_at: null` while ongoing |
| `url` | shown so the page can link the target; `TODO(divy)` verbatim when unconfigured |

### S.4.4 Manual (`api/internal/collector/manual`)

Every `COLLECT_MANUAL_INTERVAL` (15m): for each entry of the loaded `content/manual_metrics.yaml` (§C.9.1) write `<metric>{labels} = value` under the gauge policy and `divy_manual_metric_updated_timestamp_seconds{metric=<metric>} = unix(updated_at at 00:00:00Z)` (skipped when `updated_at` is `TODO(divy)` — the panel then prints "last updated: unknown"). Content is loaded once at startup, so a changed value appears as a step at the next deploy — genuine history of the hand-maintained number. `items` = samples written (0 on an idle heartbeat-free run is normal).

### S.4.5 Process (live, not a collector)

`divy_uptime_seconds`, `divy_build_info`, `divy_open_to_work`, `divy_experience_years` via `LiveSeries` (§S.2.5); `go_*`, `process_*` from client_golang; `divy_http_requests_total{route,method,code}` and `divy_http_request_duration_seconds{route,method}` from the server middleware (`route` = chi route pattern, e.g. `/api/v1/query_range`, never the raw path). None are stored.

## S.5 Metric catalogue

All names pass promlint (`counter … "_total" suffix`, `non-counter … not "_total"`, base units, snake_case, no abbreviated unit tokens). `github_stars_total` from the brief is exposed as **`github_stars`** because promlint rejects `_total` on gauges.

| Metric | Type | Labels | Source | Cadence | History depth | promlint note |
|--------|------|--------|--------|---------|---------------|---------------|
| `github_commits_total` | counter | — | GitHub Q1 `commitContributionsByRepository` (commits only, private repos as counts) | 15m | 365 d backfilled per run; frozen prefix beyond; 2 y retention | `_total` on counter |
| `github_contributions_total` | counter | — | GitHub Q1 `contributionCalendar` (commits + issues + PRs + reviews + discussions + repo creations) | 15m | same | addition (S-X1) |
| `github_merged_prs_total` | counter | `org` | GitHub Q2 search | 15m | full history from first merge | |
| `github_merged_prs_by_repo_total` | counter | `org`, `repo` | GitHub Q2, public repos outside `GITHUB_PRIVATE_ORGS` | 15m | full history | |
| `github_stars` | gauge | `repo` | GitHub Q4 | 15m | from first collection; heartbeat 1 h | renamed from `github_stars_total` |
| `github_followers` | gauge | — | GitHub Q1 | 15m | from first collection | |
| `oss_prs_open` | gauge | — | GitHub Q3 | 15m | from first collection | |
| `pypi_downloads_total` | counter | `package` | pypistats overall, `mirrors=false` | 60m | 180 d backfilled per run; frozen prefix beyond | |
| `pypi_package_info` | gauge (=1) | `package`, `version` | pypi.org JSON | 60m | current version only (old version's series deleted) | `_info` gauge, no unit |
| `savely_active_users` | gauge | — | manual | 15m | from first collection | |
| `lfx_applications` | gauge | `status` | manual | 15m | from first collection | |
| `divy_manual_metric_updated_timestamp_seconds` | gauge | `metric` | manual | 15m | from first collection | `_timestamp_seconds` base unit |
| `probe_success` | gauge | `target` | uptime | 5m | 90 d | blackbox_exporter name |
| `probe_duration_seconds` | gauge | `target` | uptime | 5m | 90 d | seconds |
| `probe_http_status_code` | gauge | `target` | uptime | 5m | 90 d | |
| `divy_uptime_seconds` | gauge | — | live | scrape | function of `t` | |
| `divy_build_info` | gauge (=1) | `version`, `commit`, `go_version` | live | scrape | constant | |
| `divy_open_to_work` | gauge | — | live (`profile.yaml`) | scrape | constant | |
| `divy_experience_years` | gauge | — | live (`spans.yaml`) | scrape | function of `t` | `years` is not in promlint's unit table (`days`/`weeks` are) → passes |
| `divy_collector_last_success_timestamp_seconds` | gauge | `collector` | scheduler (seeded from `collector_runs`) | per run | survives restarts | |
| `divy_collector_runs_total` | counter | `collector`, `result` | scheduler | per run | in-process (resets on restart) | |
| `divy_collector_run_duration_seconds` | histogram | `collector` | scheduler | per run | in-process | addition (S-X9) |
| `divy_http_requests_total` | counter | `route`, `method`, `code` | server middleware | per request | in-process | |
| `divy_http_request_duration_seconds` | histogram | `route`, `method` | server middleware | per request | in-process | |
| `go_*`, `process_*` | mixed | — | client_golang default collectors | scrape | in-process | |

HELP strings live in `api/internal/metrics/catalogue.go` next to the type and the owning collector (used for `/metrics`, `/api/v1/metadata`-style lookups, staleness thresholds and the validator's "metric exists" rule).

## S.6 Response caching

| Item | Specification |
|------|---------------|
| Where | `server/middleware/cache.go`, after the rate limiter, before handlers; in-memory only (one process). |
| Routes and TTL | `GET /api/v1/query`, `/query_range`, `/series`, `/labels`, `/label/*/values`, `/loki/api/v1/*`: **15 s**. `GET /api/content/*`, `/api/v1/rules`, `/api/traces/career`, `/api/uptime/heartbeats`: **60 s**. Everything else (`/metrics`, `/healthz`, `/readyz`, `/api/traces/{otel id}`, `/favicon.svg`, `/og/*`, static assets): not cached by this layer. |
| Key | `method \n path \n canonical query \n gen` where canonical query = parameters sorted by name (repeated names keep order), values percent-decoded then re-encoded, and `gen` = `store.Generation()` for the 15 s group or the content hash (constant per process) for the 60 s group. No time rounding: the frontend aligns `end` to `floor(now/step)×step` and `start = end − range` so concurrent visitors share entries (Grafana does the same). |
| Entry | status (200 only), `Content-Type`, `ETag`, body bytes, `expires`. Bodies > 4 MiB and non-200 responses are never cached. |
| Bounds | LRU, 2,000 entries or 32 MiB, whichever first. |
| Invalidation | Every committed collector/exporter write bumps `gen`; old keys become unreachable and age out. Content never changes at runtime. |
| Headers | `Cache-Control: public, max-age=<ttl>`; `ETag: W/"<first 16 hex of sha256(body)>"`; `X-Cache: HIT\|MISS`. `If-None-Match` equal to the ETag → `304 Not Modified` with the same `ETag`/`Cache-Control` and no body. |
| Bypass | None (Prometheus/Grafana send no cache directives; 15 s is below every collector cadence). |
| Example | `GET /api/v1/query_range?query=sum(increase(github_commits_total[7d]))&start=1757024000&end=1757052000&step=900` → key `GET\n/api/v1/query_range\nend=1757052000&query=sum%28increase%28github_commits_total%5B7d%5D%29%29&start=1757024000&step=900\n41983`; first response `X-Cache: MISS`, `ETag: W/"9f1c2a7b3d4e5f60"`; a repeat within 15 s `X-Cache: HIT`; with `If-None-Match: W/"9f1c2a7b3d4e5f60"` → `304`. |

## S.7 Rate limiting

| Item | Specification |
|------|---------------|
| Algorithm | `golang.org/x/time/rate`: one `rate.NewLimiter(rate.Limit(RATE_LIMIT_RPS), RATE_LIMIT_BURST)` per client IP (defaults 20 r/s, burst 100). |
| Client IP | `middleware.ClientIPFromXFF(TRUSTED_PROXIES...)` when `TRUSTED_PROXIES` is non-empty (CIDRs; compose sets Caddy's network), else `ClientIPFromRemoteAddr()`; read with `middleware.GetClientIP(ctx)`. `X-Forwarded-For` from an untrusted peer is ignored. Key = the IP string (IPv6 as-is). |
| Store | `map[string]*visitor{lim, lastSeen}` + `sync.Mutex`; sweep every 1 m deletes entries idle > 10 m; hard cap 100,000 entries (oldest evicted). |
| Scope | Every route except: `/healthz`, `/readyz`, `/metrics` (exempt from per-IP buckets, capped by one global limiter 50 r/s burst 200) and embedded static assets under `/_app/*` (immutable files; page loads must not spend the visitor's budget). `/`, `/favicon.svg`, `/og/*`, `/api/*`, `/loki/*` are limited. |
| Decision | `lim.Allow()`; on false: `429`, headers `Retry-After: 1` (ceil of `ReserveN(now,1).Delay()`, reservation cancelled; minimum 1), `Cache-Control: no-store`, plus the body below. Counted in `divy_http_requests_total{code="429"}`. |
| 429 body | `/api/v1/*`: `{"status":"error","errorType":"unavailable","error":"rate limit exceeded: 20 req/s per client, burst 100; retry after 1s"}`; `/loki/*`: the Loki section's error shape with the same message; everything else: `{"error":"rate limit exceeded: 20 req/s per client, burst 100; retry after 1s"}`. |
| Order | recover → `X-Divy-Trace-Id`/OTel → client IP → rate limit → response cache → CORS → handler. Rate limiting precedes the cache so cached hits still consume tokens (the limit is about the client, not our cost). |
| Tests | Table-driven: 100 immediate requests pass, the 101st is 429 with `Retry-After`; XFF honoured only from a trusted prefix; exempt routes never 429 below the global cap; sweep evicts idle entries. |

## Assumptions to verify at Phase 1 (not open questions)

- Private-repository commits/contributions appear in Q1 for the token owner (schema documents only the restricted counts). `divy collect --once --only github` prints `calendar_total`, `commit_total`, `restricted`, `has_restricted` per window; README states the outcome.
- `commitContributionsByRepository(maxRepositories: 100)` is accepted (schema documents only the default 25); `totalRepositoriesWithContributedCommits` detects truncation either way.

## Open questions

1. GitHub token type: a classic PAT with `repo` (private-org PR and commit **counts** appear under `org="gradr"` and in `github_commits_total`) versus a public-only token (`public_repo`/fine-grained: no private counts at all, the `gradr` bucket stays empty). The plan assumes the classic `repo` token; nothing else changes if the answer is public-only.
