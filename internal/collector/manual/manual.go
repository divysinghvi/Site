// Package manual samples content/manual_metrics.yaml into the store so the
// hand-maintained gauges (savely_active_users, lfx_applications{status})
// have real history, together with
// divy_manual_metric_updated_timestamp_seconds{metric} for the honest
// "last updated" stamp.
package manual

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"divy.dev/internal/collector"
	"divy.dev/internal/content"
	"divy.dev/internal/model"
	"divy.dev/internal/store"
)

// MetricUpdated carries the updated_at date of each manual metric as unix seconds.
const MetricUpdated = "divy_manual_metric_updated_timestamp_seconds"

// Config configures the collector.
type Config struct {
	Metrics  []model.ManualMetric
	Interval time.Duration
	Now      func() time.Time
	Logger   *slog.Logger
}

// Collector implements collector.Collector.
type Collector struct {
	cfg Config
	st  *store.Store
}

// New builds the collector from the loaded content.
func New(cfg Config, c *content.Content, st *store.Store) *Collector {
	if cfg.Metrics == nil && c != nil {
		cfg.Metrics = c.Manual.Metrics
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 15 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Collector{cfg: cfg, st: st}
}

// Name is "manual".
func (c *Collector) Name() string { return "manual" }

// Interval is the configured cadence.
func (c *Collector) Interval() time.Duration { return c.cfg.Interval }

// Run writes every manual gauge under the gauge policy (changed value or
// hourly heartbeat) and the updated_at timestamp of the ones that have a
// date; a TODO(divy) updated_at writes no timestamp, so the panel says
// "last updated: unknown" instead of inventing one.
func (c *Collector) Run(ctx context.Context) (collector.Result, error) {
	now := c.cfg.Now().UTC()
	idx, err := collector.LoadLatest(ctx, c.st)
	if err != nil {
		return collector.Result{}, err
	}
	b := collector.NewBatch(c.st)
	var noDate []string
	for _, m := range c.cfg.Metrics {
		labels := store.Labels{}
		for k, v := range m.Labels {
			labels[k] = v
		}
		if _, err := b.Gauge(ctx, idx, m.Metric, labels, m.Value, now); err != nil {
			return collector.Result{}, err
		}
		if m.UpdatedAt.IsTodo() {
			noDate = append(noDate, m.Metric)
			continue
		}
		d, err := time.Parse("2006-01-02", string(m.UpdatedAt))
		if err != nil {
			return collector.Result{}, fmt.Errorf("manual: %s updated_at %q: %w", m.Metric, m.UpdatedAt, err)
		}
		if _, err := b.Gauge(ctx, idx, MetricUpdated, store.Labels{"metric": m.Metric}, float64(d.Unix()), now); err != nil {
			return collector.Result{}, err
		}
	}
	n, err := b.Commit(ctx)
	if err != nil {
		return collector.Result{}, err
	}
	res := collector.Result{Items: n, Note: fmt.Sprintf("metrics=%d", len(c.cfg.Metrics))}
	if len(noDate) > 0 {
		res.Note += " updated_at_todo=" + strings.Join(noDate, ",")
	}
	return res, nil
}
