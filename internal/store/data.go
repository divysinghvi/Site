package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Labels is a label set without __name__.
type Labels map[string]string

var (
	metricNameRe = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelNameRe  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// CanonicalLabels renders a label set as canonical JSON (sorted keys, no
// HTML escaping, no whitespace); the empty set is "{}".
func CanonicalLabels(l Labels) string {
	if len(l) == 0 {
		return "{}"
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]string(l))
	return strings.TrimRight(buf.String(), "\n")
}

// ParseLabels decodes canonical JSON labels.
func ParseLabels(s string) (Labels, error) {
	if s == "" || s == "{}" {
		return Labels{}, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return Labels(m), nil
}

// ValidateSeries checks metric and label names/values as the writer does.
func ValidateSeries(metric string, labels Labels) error {
	if !metricNameRe.MatchString(metric) || len(metric) > 200 {
		return fmt.Errorf("store: invalid metric name %q", metric)
	}
	if len(labels) > 8 {
		return fmt.Errorf("store: too many labels for %s (%d > 8)", metric, len(labels))
	}
	for k, v := range labels {
		if !labelNameRe.MatchString(k) || strings.HasPrefix(k, "__") {
			return fmt.Errorf("store: invalid label name %q", k)
		}
		if len(v) > 256 {
			return fmt.Errorf("store: label %s value too long", k)
		}
	}
	return nil
}

// Sample is one observation.
type Sample struct {
	TsMs  int64
	Value float64
}

// Series identifies a stored series.
type Series struct {
	ID     int64
	Metric string
	Labels Labels
}

// SeriesData is a series with its samples in ascending time order.
type SeriesData struct {
	Series
	Samples []Sample
}

// Latest is the newest sample of a series.
type Latest struct {
	Series
	TsMs  int64
	Value float64
}

// MatchType is a label matcher operator.
type MatchType string

// Matcher operators.
const (
	MatchEqual     MatchType = "="
	MatchNotEqual  MatchType = "!="
	MatchRegexp    MatchType = "=~"
	MatchNotRegexp MatchType = "!~"
)

// Matcher is one label matcher; Name "__name__" matches the metric name.
type Matcher struct {
	Name  string
	Type  MatchType
	Value string
	re    *regexp.Regexp
}

// NewMatcher builds a matcher, compiling regexes anchored as ^(?:…)$.
func NewMatcher(name string, t MatchType, value string) (Matcher, error) {
	m := Matcher{Name: name, Type: t, Value: value}
	if t == MatchRegexp || t == MatchNotRegexp {
		re, err := regexp.Compile("^(?:" + value + ")$")
		if err != nil {
			return m, fmt.Errorf("store: bad regex %q: %w", value, err)
		}
		m.re = re
	}
	return m, nil
}

// Matches reports whether the matcher accepts v.
func (m Matcher) Matches(v string) bool {
	switch m.Type {
	case MatchEqual:
		return v == m.Value
	case MatchNotEqual:
		return v != m.Value
	case MatchRegexp:
		return m.re != nil && m.re.MatchString(v)
	case MatchNotRegexp:
		return m.re != nil && !m.re.MatchString(v)
	}
	return false
}

func matchSeries(ms []Matcher, s Series) bool {
	for _, m := range ms {
		v := ""
		if m.Name == "__name__" {
			v = s.Metric
		} else {
			v = s.Labels[m.Name]
		}
		if !m.Matches(v) {
			return false
		}
	}
	return true
}

// EnsureSeries returns the id of (metric, labels), creating the row if needed.
func (s *Store) EnsureSeries(ctx context.Context, metric string, labels Labels) (int64, error) {
	if err := ValidateSeries(metric, labels); err != nil {
		return 0, err
	}
	canon := CanonicalLabels(labels)
	key := metric + "\x00" + canon
	s.seriesMu.Lock()
	id, ok := s.series[key]
	s.seriesMu.Unlock()
	if ok {
		return id, nil
	}
	// fast path: already in the database (another process/instance)
	if err := s.r.QueryRowContext(ctx, "SELECT id FROM series WHERE metric = ? AND labels = ?", metric, canon).Scan(&id); err == nil {
		s.cacheSeries(key, id)
		return id, nil
	}
	err := s.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO series(metric, labels) VALUES (?, ?)", metric, canon); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, "SELECT id FROM series WHERE metric = ? AND labels = ?", metric, canon).Scan(&id)
	})
	if err != nil {
		return 0, err
	}
	s.cacheSeries(key, id)
	s.bump()
	return id, nil
}

func (s *Store) cacheSeries(key string, id int64) {
	s.seriesMu.Lock()
	s.series[key] = id
	s.seriesMu.Unlock()
}

// UpsertSamples writes samples for a series; re-writing an instant overwrites its value.
func (s *Store) UpsertSamples(ctx context.Context, seriesID int64, samples []Sample) error {
	if len(samples) == 0 {
		return nil
	}
	for _, sm := range samples {
		if math.IsNaN(sm.Value) || math.IsInf(sm.Value, 0) {
			return fmt.Errorf("store: non-finite sample value for series %d at %d", seriesID, sm.TsMs)
		}
		if sm.TsMs <= 0 {
			return fmt.Errorf("store: non-positive sample timestamp %d", sm.TsMs)
		}
	}
	err := s.Write(ctx, func(tx *sql.Tx) error {
		return upsertSamplesTx(ctx, tx, seriesID, samples)
	})
	if err == nil {
		s.bump()
	}
	return err
}

func upsertSamplesTx(ctx context.Context, tx *sql.Tx, seriesID int64, samples []Sample) error {
	st, err := tx.PrepareContext(ctx, "INSERT INTO samples(series_id, ts_ms, value) VALUES (?, ?, ?) ON CONFLICT(series_id, ts_ms) DO UPDATE SET value = excluded.value")
	if err != nil {
		return err
	}
	defer st.Close()
	for _, sm := range samples {
		if _, err := st.ExecContext(ctx, seriesID, sm.TsMs, sm.Value); err != nil {
			return err
		}
	}
	return nil
}

// WriteSeries is EnsureSeries + UpsertSamples in one call.
func (s *Store) WriteSeries(ctx context.Context, metric string, labels Labels, samples []Sample) (int64, error) {
	id, err := s.EnsureSeries(ctx, metric, labels)
	if err != nil {
		return 0, err
	}
	return id, s.UpsertSamples(ctx, id, samples)
}

// DeleteOffGrid deletes the samples of a series that are not on a UTC day boundary.
func (s *Store) DeleteOffGrid(ctx context.Context, seriesID int64) (int64, error) {
	return s.writeCount(ctx, "DELETE FROM samples WHERE series_id = ? AND ts_ms % 86400000 != 0", seriesID)
}

// DeleteSamples deletes samples with fromMs ≤ ts_ms < toMs.
func (s *Store) DeleteSamples(ctx context.Context, seriesID, fromMs, toMs int64) (int64, error) {
	return s.writeCount(ctx, "DELETE FROM samples WHERE series_id = ? AND ts_ms >= ? AND ts_ms < ?", seriesID, fromMs, toMs)
}

func (s *Store) writeCount(ctx context.Context, q string, args ...any) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err == nil {
		s.bump()
	}
	return n, err
}

// ListSeries returns every series accepted by the matchers, sorted by metric then labels.
func (s *Store) ListSeries(ctx context.Context, matchers []Matcher) ([]Series, error) {
	q := "SELECT id, metric, labels FROM series"
	var args []any
	for _, m := range matchers {
		if m.Name == "__name__" && m.Type == MatchEqual {
			q += " WHERE metric = ?"
			args = append(args, m.Value)
			break
		}
	}
	rows, err := s.r.QueryContext(ctx, q+" ORDER BY metric, labels", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Series
	for rows.Next() {
		var sr Series
		var labels string
		if err := rows.Scan(&sr.ID, &sr.Metric, &labels); err != nil {
			return nil, err
		}
		if sr.Labels, err = ParseLabels(labels); err != nil {
			return nil, err
		}
		if matchSeries(matchers, sr) {
			out = append(out, sr)
		}
	}
	return out, rows.Err()
}

// QueryRange returns, per matching series, the samples with startMs < ts_ms ≤ endMs in ascending order.
func (s *Store) QueryRange(ctx context.Context, matchers []Matcher, startMs, endMs int64) ([]SeriesData, error) {
	series, err := s.ListSeries(ctx, matchers)
	if err != nil {
		return nil, err
	}
	out := make([]SeriesData, 0, len(series))
	for _, sr := range series {
		rows, err := s.r.QueryContext(ctx, "SELECT ts_ms, value FROM samples WHERE series_id = ? AND ts_ms > ? AND ts_ms <= ? ORDER BY ts_ms", sr.ID, startMs, endMs)
		if err != nil {
			return nil, err
		}
		sd := SeriesData{Series: sr}
		for rows.Next() {
			var sm Sample
			if err := rows.Scan(&sm.TsMs, &sm.Value); err != nil {
				rows.Close()
				return nil, err
			}
			sd.Samples = append(sd.Samples, sm)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		out = append(out, sd)
	}
	return out, nil
}

// LatestPerSeries returns the newest sample of every series, sorted by metric then labels.
func (s *Store) LatestPerSeries(ctx context.Context) ([]Latest, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT s.id, s.metric, s.labels, m.ts_ms, m.value
FROM series s JOIN samples m ON m.series_id = s.id
WHERE m.ts_ms = (SELECT max(ts_ms) FROM samples WHERE series_id = s.id)
ORDER BY s.metric, s.labels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Latest
	for rows.Next() {
		var l Latest
		var labels string
		if err := rows.Scan(&l.ID, &l.Metric, &labels, &l.TsMs, &l.Value); err != nil {
			return nil, err
		}
		if l.Labels, err = ParseLabels(labels); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteSeriesWhere deletes the series of a metric whose labels satisfy pred (samples cascade).
func (s *Store) DeleteSeriesWhere(ctx context.Context, metric string, pred func(Labels) bool) (int64, error) {
	series, err := s.ListSeries(ctx, []Matcher{{Name: "__name__", Type: MatchEqual, Value: metric}})
	if err != nil {
		return 0, err
	}
	var ids []int64
	for _, sr := range series {
		if pred(sr.Labels) {
			ids = append(ids, sr.ID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	var n int64
	err = s.Write(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, "DELETE FROM samples WHERE series_id = ?", id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM series WHERE id = ?", id); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err == nil {
		s.seriesMu.Lock()
		for k, id := range s.series {
			for _, d := range ids {
				if id == d {
					delete(s.series, k)
				}
			}
		}
		s.seriesMu.Unlock()
		s.bump()
	}
	return n, err
}

// ---- probes ----

// Probe is one uptime probe result.
type Probe struct {
	Target     string
	TsMs       int64
	Up         bool
	LatencyMs  *float64
	StatusCode int
	Error      *string
}

// WriteProbeResults upserts probe rows.
func (s *Store) WriteProbeResults(ctx context.Context, probes []Probe) error {
	if len(probes) == 0 {
		return nil
	}
	err := s.Write(ctx, func(tx *sql.Tx) error {
		st, err := tx.PrepareContext(ctx, `INSERT INTO probe_results(target, ts_ms, up, latency_ms, status_code, error) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(target, ts_ms) DO UPDATE SET up = excluded.up, latency_ms = excluded.latency_ms, status_code = excluded.status_code, error = excluded.error`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, p := range probes {
			up := 0
			if p.Up {
				up = 1
			}
			if _, err := st.ExecContext(ctx, p.Target, p.TsMs, up, p.LatencyMs, p.StatusCode, p.Error); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.bump()
	}
	return err
}

// ReadProbes returns a target's probes with ts_ms ≥ sinceMs, ascending.
func (s *Store) ReadProbes(ctx context.Context, target string, sinceMs int64) ([]Probe, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT target, ts_ms, up, latency_ms, status_code, error FROM probe_results WHERE target = ? AND ts_ms >= ? ORDER BY ts_ms", target, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Probe
	for rows.Next() {
		var p Probe
		var up int
		if err := rows.Scan(&p.Target, &p.TsMs, &up, &p.LatencyMs, &p.StatusCode, &p.Error); err != nil {
			return nil, err
		}
		p.Up = up == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// LastProbe returns the newest probe of a target (ok=false when none).
func (s *Store) LastProbe(ctx context.Context, target string) (Probe, bool, error) {
	var p Probe
	var up int
	err := s.r.QueryRowContext(ctx, "SELECT target, ts_ms, up, latency_ms, status_code, error FROM probe_results WHERE target = ? ORDER BY ts_ms DESC LIMIT 1", target).Scan(&p.Target, &p.TsMs, &up, &p.LatencyMs, &p.StatusCode, &p.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	p.Up = up == 1
	return p, true, nil
}

// ---- otel spans ----

// Span is one otel_spans row.
type Span struct {
	TraceID       string
	SpanID        string
	ParentSpanID  *string
	Name          string
	Service       string
	StartUnixNano int64
	EndUnixNano   int64
	Attributes    json.RawMessage // JSON object
	Events        json.RawMessage // JSON array
	StatusCode    int             // 0 unset, 1 ok, 2 error
	StatusMsg     *string
}

// WriteSpans inserts spans (duplicates ignored). It does not bump Generation.
func (s *Store) WriteSpans(ctx context.Context, spans []Span) error {
	if len(spans) == 0 {
		return nil
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		st, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO otel_spans(trace_id, span_id, parent_span_id, name, service, start_unix_nano, end_unix_nano, attributes, events, status_code, status_msg)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, sp := range spans {
			attrs := string(sp.Attributes)
			if attrs == "" {
				attrs = "{}"
			}
			events := string(sp.Events)
			if events == "" {
				events = "[]"
			}
			if _, err := st.ExecContext(ctx, sp.TraceID, sp.SpanID, sp.ParentSpanID, sp.Name, sp.Service, sp.StartUnixNano, sp.EndUnixNano, attrs, events, sp.StatusCode, sp.StatusMsg); err != nil {
				return err
			}
		}
		return nil
	})
}

const spanCols = "trace_id, span_id, parent_span_id, name, service, start_unix_nano, end_unix_nano, attributes, events, status_code, status_msg"

func scanSpans(rows *sql.Rows) ([]Span, error) {
	defer rows.Close()
	var out []Span
	for rows.Next() {
		var sp Span
		var attrs, events string
		if err := rows.Scan(&sp.TraceID, &sp.SpanID, &sp.ParentSpanID, &sp.Name, &sp.Service, &sp.StartUnixNano, &sp.EndUnixNano, &attrs, &events, &sp.StatusCode, &sp.StatusMsg); err != nil {
			return nil, err
		}
		sp.Attributes, sp.Events = json.RawMessage(attrs), json.RawMessage(events)
		out = append(out, sp)
	}
	return out, rows.Err()
}

// ReadTrace returns the spans of a trace ordered by start time (empty when unknown).
func (s *Store) ReadTrace(ctx context.Context, traceID string) ([]Span, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT "+spanCols+" FROM otel_spans WHERE trace_id = ? ORDER BY start_unix_nano, span_id", traceID)
	if err != nil {
		return nil, err
	}
	return scanSpans(rows)
}

// SearchTraces returns distinct trace ids of a service (optionally one span
// name) starting within [startNano, endNano], newest first.
func (s *Store) SearchTraces(ctx context.Context, service, name string, startNano, endNano int64, limit int) ([]string, error) {
	q := "SELECT DISTINCT trace_id FROM otel_spans WHERE service = ? AND start_unix_nano BETWEEN ? AND ?"
	args := []any{service, startNano, endNano}
	if name != "" {
		q += " AND name = ?"
		args = append(args, name)
	}
	q += " ORDER BY start_unix_nano DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Operations returns the distinct span names of a service since sinceNano, sorted.
func (s *Store) Operations(ctx context.Context, service string, sinceNano int64) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT DISTINCT name FROM otel_spans WHERE service = ? AND start_unix_nano >= ? ORDER BY name", service, sinceNano)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ---- collector runs and state ----

// CollectorRun is one collector_runs row.
type CollectorRun struct {
	ID         int64
	Collector  string
	StartedMs  int64
	FinishedMs *int64
	OK         *bool
	Error      *string
	Items      int
}

// StartRun inserts a running row and returns its id.
func (s *Store) StartRun(ctx context.Context, collector string) (int64, error) {
	var id int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "INSERT INTO collector_runs(collector, started_ms) VALUES (?, ?)", collector, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// FinishRun completes a run; the error text is truncated to 500 characters.
func (s *Store) FinishRun(ctx context.Context, id int64, ok bool, errText string, items int) error {
	var errPtr *string
	if errText != "" {
		if len(errText) > 500 {
			errText = errText[:500]
		}
		errPtr = &errText
	}
	okInt := 0
	if ok {
		okInt = 1
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE collector_runs SET finished_ms = ?, ok = ?, error = ?, items = ? WHERE id = ?", time.Now().UnixMilli(), okInt, errPtr, items, id)
		return err
	})
}

// DeleteRun removes a run row (used when a collector turns out to be disabled).
func (s *Store) DeleteRun(ctx context.Context, id int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM collector_runs WHERE id = ?", id)
		return err
	})
}

// RecordCollectorRun inserts a completed run in one statement.
func (s *Store) RecordCollectorRun(ctx context.Context, r CollectorRun) error {
	var okInt *int
	if r.OK != nil {
		v := 0
		if *r.OK {
			v = 1
		}
		okInt = &v
	}
	if r.Error != nil && len(*r.Error) > 500 {
		t := (*r.Error)[:500]
		r.Error = &t
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO collector_runs(collector, started_ms, finished_ms, ok, error, items) VALUES (?, ?, ?, ?, ?, ?)", r.Collector, r.StartedMs, r.FinishedMs, okInt, r.Error, r.Items)
		return err
	})
}

// LastSuccess returns, per collector, the finished_ms of the newest successful run.
func (s *Store) LastSuccess(ctx context.Context) (map[string]int64, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT collector, max(finished_ms) FROM collector_runs WHERE ok = 1 AND finished_ms IS NOT NULL GROUP BY collector")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var c string
		var ms int64
		if err := rows.Scan(&c, &ms); err != nil {
			return nil, err
		}
		out[c] = ms
	}
	return out, rows.Err()
}

// RecentRuns returns the newest n runs of a collector ("" = all), newest first.
func (s *Store) RecentRuns(ctx context.Context, collector string, n int) ([]CollectorRun, error) {
	q := "SELECT id, collector, started_ms, finished_ms, ok, error, items FROM collector_runs"
	var args []any
	if collector != "" {
		q += " WHERE collector = ?"
		args = append(args, collector)
	}
	q += " ORDER BY started_ms DESC, id DESC LIMIT ?"
	args = append(args, n)
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CollectorRun
	for rows.Next() {
		var r CollectorRun
		var ok sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Collector, &r.StartedMs, &r.FinishedMs, &ok, &r.Error, &r.Items); err != nil {
			return nil, err
		}
		if ok.Valid {
			b := ok.Int64 == 1
			r.OK = &b
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetState reads a collector_state value.
func (s *Store) GetState(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.r.QueryRowContext(ctx, "SELECT value FROM collector_state WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetState upserts a collector_state value.
func (s *Store) SetState(ctx context.Context, key, value string) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO collector_state(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
		return err
	})
}

// DeleteState removes a key.
func (s *Store) DeleteState(ctx context.Context, key string) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM collector_state WHERE key = ?", key)
		return err
	})
}

// ---- retention ----

// Retention limits (CONVENTIONS §5); not configurable.
const (
	RetainSamples   = 730 * 24 * time.Hour
	RetainProbes    = 90 * 24 * time.Hour
	RetainSpans     = 24 * time.Hour
	MaxSpans        = 20000
	RetainRuns      = 30 * 24 * time.Hour
	AbandonRunAfter = time.Hour
)

// DeleteSamplesBefore deletes samples older than cutoffMs for series whose
// metric name matches prefix ("" = all, "probe_" = probe series, "!probe_" = the rest).
func (s *Store) DeleteSamplesBefore(ctx context.Context, cutoffMs int64, prefix string) (int64, error) {
	q := "DELETE FROM samples WHERE ts_ms < ? AND series_id IN (SELECT id FROM series"
	args := []any{cutoffMs}
	switch {
	case prefix == "":
		q += ")"
	case strings.HasPrefix(prefix, "!"):
		q += " WHERE metric NOT LIKE ?)"
		args = append(args, prefix[1:]+"%")
	default:
		q += " WHERE metric LIKE ?)"
		args = append(args, prefix+"%")
	}
	return s.writeCount(ctx, q, args...)
}

// DeleteProbesBefore deletes probe rows older than cutoffMs.
func (s *Store) DeleteProbesBefore(ctx context.Context, cutoffMs int64) (int64, error) {
	return s.writeCount(ctx, "DELETE FROM probe_results WHERE ts_ms < ?", cutoffMs)
}

// DeleteSpansBefore deletes spans that started before cutoffNano (does not bump Generation).
func (s *Store) DeleteSpansBefore(ctx context.Context, cutoffNano int64) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM otel_spans WHERE start_unix_nano < ?", cutoffNano)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n, err
}

// CapSpans keeps only the newest keep spans.
func (s *Store) CapSpans(ctx context.Context, keep int) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM otel_spans WHERE rowid IN (SELECT rowid FROM otel_spans ORDER BY start_unix_nano DESC LIMIT -1 OFFSET ?)", keep)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n, err
}

// DeleteRunsBefore deletes collector runs started before cutoffMs.
func (s *Store) DeleteRunsBefore(ctx context.Context, cutoffMs int64) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM collector_runs WHERE started_ms < ?", cutoffMs)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n, err
}

// MarkAbandonedRuns marks unfinished runs older than olderThanMs as failed ("abandoned").
func (s *Store) MarkAbandonedRuns(ctx context.Context, olderThanMs int64) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE collector_runs SET finished_ms = ?, ok = 0, error = 'abandoned' WHERE finished_ms IS NULL AND started_ms < ?", time.Now().UnixMilli(), olderThanMs)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n, err
}

// DeleteOrphanSeries deletes series without samples.
func (s *Store) DeleteOrphanSeries(ctx context.Context) (int64, error) {
	n, err := s.writeCount(ctx, "DELETE FROM series WHERE id NOT IN (SELECT DISTINCT series_id FROM samples)")
	if err == nil && n > 0 {
		s.seriesMu.Lock()
		s.series = map[string]int64{}
		s.seriesMu.Unlock()
	}
	return n, err
}

// MetricNames returns the distinct metric names, sorted.
func (s *Store) MetricNames(ctx context.Context) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT DISTINCT metric FROM series ORDER BY metric")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	sort.Strings(out)
	return out, rows.Err()
}
