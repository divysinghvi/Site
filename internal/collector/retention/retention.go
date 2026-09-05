// Package retention enforces the storage limits (conventions §5, storage
// §S.1.7): samples 2 years, probe samples and probe_results 90 days,
// otel_spans 24 hours and at most 20 000 rows, collector_runs 30 days,
// abandoned runs, orphan series, then a WAL checkpoint. It runs hourly in
// the scheduler and, through the collect endpoint, whenever it is due.
package retention

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"divy.dev/internal/collector"
	"divy.dev/internal/store"
)

// Config configures the collector.
type Config struct {
	Interval time.Duration
	Now      func() time.Time
	Logger   *slog.Logger
}

// Collector implements collector.Collector.
type Collector struct {
	cfg Config
	st  *store.Store
}

// New builds the collector.
func New(cfg Config, st *store.Store) *Collector {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Collector{cfg: cfg, st: st}
}

// Name is "retention".
func (c *Collector) Name() string { return "retention" }

// Interval is the configured cadence.
func (c *Collector) Interval() time.Duration { return c.cfg.Interval }

// Run deletes expired rows; Items is the number of rows deleted (or marked).
func (c *Collector) Run(ctx context.Context) (collector.Result, error) {
	now := c.cfg.Now().UTC()
	var total int64
	var parts []string
	steps := []struct {
		name string
		fn   func() (int64, error)
	}{
		{"samples", func() (int64, error) {
			return c.st.DeleteSamplesBefore(ctx, now.Add(-store.RetainSamples).UnixMilli(), "!probe_")
		}},
		{"probe_samples", func() (int64, error) {
			return c.st.DeleteSamplesBefore(ctx, now.Add(-store.RetainProbes).UnixMilli(), "probe_")
		}},
		{"probe_results", func() (int64, error) {
			return c.st.DeleteProbesBefore(ctx, now.Add(-store.RetainProbes).UnixMilli())
		}},
		{"spans_age", func() (int64, error) {
			return c.st.DeleteSpansBefore(ctx, now.Add(-store.RetainSpans).UnixNano())
		}},
		{"spans_cap", func() (int64, error) { return c.st.CapSpans(ctx, store.MaxSpans) }},
		{"runs_abandoned", func() (int64, error) {
			return c.st.MarkAbandonedRuns(ctx, now.Add(-store.AbandonRunAfter).UnixMilli())
		}},
		{"runs", func() (int64, error) {
			return c.st.DeleteRunsBefore(ctx, now.Add(-store.RetainRuns).UnixMilli())
		}},
		{"orphan_series", func() (int64, error) { return c.st.DeleteOrphanSeries(ctx) }},
	}
	for _, s := range steps {
		if ctx.Err() != nil {
			return collector.Result{Items: int(total)}, ctx.Err()
		}
		n, err := s.fn()
		if err != nil {
			return collector.Result{Items: int(total)}, fmt.Errorf("retention %s: %w", s.name, err)
		}
		total += n
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", s.name, n))
		}
	}
	if err := c.st.Checkpoint(ctx); err != nil {
		c.cfg.Logger.Warn("retention: checkpoint", "err", err.Error())
	}
	note := "nothing expired"
	if len(parts) > 0 {
		note = strings.Join(parts, " ")
	}
	return collector.Result{Items: int(total), Note: note}, nil
}
