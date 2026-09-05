-- 0001_init.sql: divy.dev time-series store (SQLite dialect, also runs on Turso/libSQL).
-- Timestamps: unix milliseconds (samples, probe_results, collector_runs), unix nanoseconds (otel_spans).
-- Applied inside one transaction by the migrator; the schema_migrations table is created by the migrator itself.

CREATE TABLE IF NOT EXISTS series (
  id     INTEGER PRIMARY KEY,
  metric TEXT NOT NULL CHECK (length(metric) BETWEEN 1 AND 200),
  labels TEXT NOT NULL CHECK (labels = '{}' OR (substr(labels, 1, 2) = '{"' AND substr(labels, -1) = '}')),
  UNIQUE (metric, labels)
);

CREATE TABLE IF NOT EXISTS samples (
  series_id INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
  ts_ms     INTEGER NOT NULL CHECK (ts_ms > 0),
  value     REAL    NOT NULL,
  PRIMARY KEY (series_id, ts_ms)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS probe_results (
  target      TEXT    NOT NULL,
  ts_ms       INTEGER NOT NULL CHECK (ts_ms > 0),
  up          INTEGER NOT NULL CHECK (up IN (0, 1)),
  latency_ms  REAL    CHECK (latency_ms IS NULL OR latency_ms >= 0),
  status_code INTEGER NOT NULL DEFAULT 0 CHECK (status_code BETWEEN 0 AND 999),
  error       TEXT,
  PRIMARY KEY (target, ts_ms)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS probe_results_ts ON probe_results (ts_ms);

CREATE TABLE IF NOT EXISTS otel_spans (
  trace_id        TEXT    NOT NULL CHECK (length(trace_id) = 32),
  span_id         TEXT    NOT NULL CHECK (length(span_id) = 16),
  parent_span_id  TEXT    CHECK (parent_span_id IS NULL OR length(parent_span_id) = 16),
  name            TEXT    NOT NULL,
  service         TEXT    NOT NULL,
  start_unix_nano INTEGER NOT NULL,
  end_unix_nano   INTEGER NOT NULL CHECK (end_unix_nano >= start_unix_nano),
  attributes      TEXT    NOT NULL DEFAULT '{}',
  events          TEXT    NOT NULL DEFAULT '[]',
  status_code     INTEGER NOT NULL DEFAULT 0 CHECK (status_code IN (0, 1, 2)),
  status_msg      TEXT,
  UNIQUE (trace_id, span_id)
);
CREATE INDEX IF NOT EXISTS otel_spans_start ON otel_spans (start_unix_nano);
CREATE INDEX IF NOT EXISTS otel_spans_service_start ON otel_spans (service, start_unix_nano);

CREATE TABLE IF NOT EXISTS collector_runs (
  id          INTEGER PRIMARY KEY,
  collector   TEXT    NOT NULL,
  started_ms  INTEGER NOT NULL,
  finished_ms INTEGER,
  ok          INTEGER CHECK (ok IS NULL OR ok IN (0, 1)),
  error       TEXT,
  items       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS collector_runs_collector_started ON collector_runs (collector, started_ms);

CREATE TABLE IF NOT EXISTS collector_state (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
