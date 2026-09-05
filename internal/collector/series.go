package collector

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"divy.dev/internal/store"
)

// DayMs is one UTC day in milliseconds; a sample whose timestamp is a
// multiple of it sits on the daily grid (storage §S.2.2).
const DayMs int64 = 86_400_000

// DayKey is the YYYY-MM-DD form of a UTC day; DailyCounts are keyed by it.
func DayKey(t time.Time) string { return t.UTC().Format("2006-01-02") }

// DayOf returns 00:00:00Z of t's UTC day.
func DayOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// DayEnd returns the grid timestamp carrying "the value through the end of
// day d": (d + 1 day) 00:00:00Z in unix milliseconds.
func DayEnd(d time.Time) int64 { return DayOf(d).AddDate(0, 0, 1).UnixMilli() }

// IsGrid reports whether a sample timestamp is a UTC day boundary.
func IsGrid(tsMs int64) bool { return tsMs%DayMs == 0 }

// DailyCounts holds non-negative per-day counts keyed by DayKey.
type DailyCounts map[string]float64

// Add increments the count of t's UTC day.
func (d DailyCounts) Add(t time.Time, n float64) { d[DayKey(t)] += n }

// First returns the earliest day with a count (ok=false when empty).
func (d DailyCounts) First() (time.Time, bool) {
	var first string
	for k := range d {
		if first == "" || k < first {
			first = k
		}
	}
	if first == "" {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", first)
	return t, err == nil
}

// Total sums every count.
func (d DailyCounts) Total() float64 {
	var s float64
	for _, v := range d {
		s += v
	}
	return s
}

// CounterSamples lays out a cumulative counter on the daily grid (storage
// §S.2.3): one grid sample at DayEnd(d) for every day from w0 through
// yesterday (days without events repeat the previous value) and one live
// sample at now carrying the value through now. base is the stored grid
// value at DayEnd(w0 − 1) — the frozen prefix of a source with a bounded
// window (rule 4); when haveBase is false the series starts with a 0 marker
// at DayEnd(w0 − 1). Counts for days before w0 are ignored.
func CounterSamples(counts DailyCounts, w0, now time.Time, base float64, haveBase bool) (grid []store.Sample, live store.Sample) {
	w0 = DayOf(w0)
	today := DayOf(now)
	cum := base
	if !haveBase {
		cum = 0
		grid = append(grid, store.Sample{TsMs: DayEnd(w0.AddDate(0, 0, -1)), Value: 0})
	}
	for d := w0; d.Before(today); d = d.AddDate(0, 0, 1) {
		cum += counts[DayKey(d)]
		grid = append(grid, store.Sample{TsMs: DayEnd(d), Value: cum})
	}
	live = store.Sample{TsMs: now.UnixMilli(), Value: cum + counts[DayKey(today)]}
	return grid, live
}

// Existing is the stored samples of several series keyed by series id then timestamp.
type Existing map[int64]map[int64]float64

// LoadExisting reads every sample of a metric's series since sinceMs (one
// query per metric, whatever the number of series) so that a backfill can
// write only the grid points that are missing or changed — the write budget
// of a hosted database is the scarce resource, not the read budget.
func LoadExisting(ctx context.Context, st *store.Store, metric string, sinceMs, untilMs int64) (Existing, error) {
	data, err := st.QueryRange(ctx, []store.Matcher{{Name: "__name__", Type: store.MatchEqual, Value: metric}}, sinceMs-1, untilMs)
	if err != nil {
		return nil, err
	}
	out := Existing{}
	for _, sd := range data {
		m := make(map[int64]float64, len(sd.Samples))
		for _, s := range sd.Samples {
			m[s.TsMs] = s.Value
		}
		out[sd.ID] = m
	}
	return out, nil
}

// GridValue returns the stored value of a series at tsMs.
func (e Existing) GridValue(seriesID, tsMs int64) (float64, bool) {
	v, ok := e[seriesID][tsMs]
	return v, ok
}

// Changed filters samples to those the store does not already hold with the same value.
func (e Existing) Changed(seriesID int64, samples []store.Sample) []store.Sample {
	var out []store.Sample
	for _, s := range samples {
		if v, ok := e[seriesID][s.TsMs]; !ok || v != s.Value {
			out = append(out, s)
		}
	}
	return out
}

// Batch accumulates sample writes and commits them in one transaction, so a
// collector run is either wholly visible or not at all (storage §S.1.5).
type Batch struct {
	st      *store.Store
	upserts []upsert
	offGrid []int64
}

type upsert struct {
	id int64
	s  store.Sample
}

// NewBatch creates an empty batch against st.
func NewBatch(st *store.Store) *Batch { return &Batch{st: st} }

// Upsert queues samples for a series.
func (b *Batch) Upsert(seriesID int64, samples ...store.Sample) {
	for _, s := range samples {
		b.upserts = append(b.upserts, upsert{id: seriesID, s: s})
	}
}

// DeleteOffGrid queues the deletion of the series' live (off-grid) samples;
// deletions run before the batch's upserts, so the new live sample survives.
func (b *Batch) DeleteOffGrid(seriesID int64) { b.offGrid = append(b.offGrid, seriesID) }

// Counter queues a counter layout: the changed grid samples, the removal of
// the previous live sample and the new live sample. It returns the number of
// samples queued.
func (b *Batch) Counter(seriesID int64, ex Existing, grid []store.Sample, live store.Sample) int {
	changed := ex.Changed(seriesID, grid)
	b.Upsert(seriesID, changed...)
	b.DeleteOffGrid(seriesID)
	b.Upsert(seriesID, live)
	return len(changed) + 1
}

// Len is the number of queued samples.
func (b *Batch) Len() int { return len(b.upserts) }

// Commit writes the batch in one transaction and returns the number of
// samples written. An empty batch with no deletions is a no-op.
func (b *Batch) Commit(ctx context.Context) (int, error) {
	if len(b.upserts) == 0 && len(b.offGrid) == 0 {
		return 0, nil
	}
	for _, u := range b.upserts {
		if math.IsNaN(u.s.Value) || math.IsInf(u.s.Value, 0) {
			return 0, fmt.Errorf("collector: non-finite sample for series %d at %d", u.id, u.s.TsMs)
		}
		if u.s.TsMs <= 0 {
			return 0, fmt.Errorf("collector: non-positive timestamp %d for series %d", u.s.TsMs, u.id)
		}
	}
	err := b.st.Write(ctx, func(tx *sql.Tx) error {
		if len(b.offGrid) > 0 {
			del, err := tx.PrepareContext(ctx, "DELETE FROM samples WHERE series_id = ? AND ts_ms % 86400000 != 0")
			if err != nil {
				return err
			}
			defer del.Close()
			for _, id := range b.offGrid {
				if _, err := del.ExecContext(ctx, id); err != nil {
					return err
				}
			}
		}
		if len(b.upserts) == 0 {
			return nil
		}
		ins, err := tx.PrepareContext(ctx, "INSERT INTO samples(series_id, ts_ms, value) VALUES (?, ?, ?) ON CONFLICT(series_id, ts_ms) DO UPDATE SET value = excluded.value")
		if err != nil {
			return err
		}
		defer ins.Close()
		for _, u := range b.upserts {
			if _, err := ins.ExecContext(ctx, u.id, u.s.TsMs, u.s.Value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	n := len(b.upserts)
	b.upserts, b.offGrid = nil, nil
	return n, nil
}

// LatestIndex is the newest sample of every stored series, keyed by metric and canonical labels.
type LatestIndex map[string]store.Latest

func latestKey(metric string, labels store.Labels) string {
	return metric + "\x00" + store.CanonicalLabels(labels)
}

// LoadLatest reads the latest sample of every series in one query.
func LoadLatest(ctx context.Context, st *store.Store) (LatestIndex, error) {
	rows, err := st.LatestPerSeries(ctx)
	if err != nil {
		return nil, err
	}
	idx := make(LatestIndex, len(rows))
	for _, l := range rows {
		idx[latestKey(l.Metric, l.Labels)] = l
	}
	return idx, nil
}

// Get returns the latest sample of (metric, labels).
func (idx LatestIndex) Get(metric string, labels store.Labels) (store.Latest, bool) {
	l, ok := idx[latestKey(metric, labels)]
	return l, ok
}

// GaugeHeartbeat is the longest an unchanged gauge goes without a new sample (storage §S.2.4).
const GaugeHeartbeat = time.Hour

// GaugeDue reports whether a gauge value must be written at now: no sample
// yet, a changed value, or the heartbeat elapsed. Probe gauges bypass this
// (every probe is a measurement).
func GaugeDue(idx LatestIndex, metric string, labels store.Labels, value float64, now time.Time) bool {
	l, ok := idx.Get(metric, labels)
	if !ok {
		return true
	}
	if l.Value != value {
		return true
	}
	return now.Sub(time.UnixMilli(l.TsMs)) >= GaugeHeartbeat
}

// Gauge applies the gauge policy and queues the sample when due; it returns
// whether a sample was queued.
func (b *Batch) Gauge(ctx context.Context, idx LatestIndex, metric string, labels store.Labels, value float64, now time.Time) (bool, error) {
	if !GaugeDue(idx, metric, labels, value, now) {
		return false, nil
	}
	id, err := b.st.EnsureSeries(ctx, metric, labels)
	if err != nil {
		return false, err
	}
	b.Upsert(id, store.Sample{TsMs: now.UnixMilli(), Value: value})
	return true, nil
}

// SortedKeys returns the keys of a DailyCounts in date order (tests, logs).
func (d DailyCounts) SortedKeys() []string {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
