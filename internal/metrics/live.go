package metrics

import (
	"runtime"
	"time"

	"divy.dev/internal/content"
	"divy.dev/internal/promql"
)

// LiveSeries is one live series: a metric name, static labels and a value function.
type LiveSeries struct {
	name   string
	labels promql.Labels
	value  func(t time.Time) (float64, bool)
}

// Metric implements promql.LiveSeries.
func (s LiveSeries) Metric() string { return s.name }

// Labels implements promql.LiveSeries (without __name__).
func (s LiveSeries) Labels() promql.Labels { return s.labels }

// Value implements promql.LiveSeries.
func (s LiveSeries) Value(t time.Time) (float64, bool) { return s.value(t) }

// Live is the provider of the live series: divy_uptime_seconds,
// divy_build_info, divy_open_to_work and divy_experience_years. Both /metrics
// and the PromQL engine evaluate the same functions, so a range query over
// divy_uptime_seconds is a real ramp.
type Live struct {
	series []LiveSeries
}

// NewLive builds the provider from the loaded content and the build identity.
func NewLive(c *content.Content, startedAt time.Time, version, commit string) *Live {
	l := &Live{}
	l.series = append(l.series, LiveSeries{
		name: "divy_uptime_seconds", labels: promql.Labels{},
		value: func(t time.Time) (float64, bool) {
			if t.Before(startedAt) {
				return 0, false
			}
			return t.Sub(startedAt).Seconds(), true
		},
	})
	l.series = append(l.series, LiveSeries{
		name:   "divy_build_info",
		labels: promql.NewLabels(map[string]string{"version": version, "commit": commit, "go_version": runtime.Version()}),
		value:  func(time.Time) (float64, bool) { return 1, true },
	})
	if c != nil {
		open := 0.0
		if c.Profile.OpenToWork {
			open = 1
		}
		l.series = append(l.series, LiveSeries{
			name: "divy_open_to_work", labels: promql.Labels{},
			value: func(time.Time) (float64, bool) { return open, true },
		})
		l.series = append(l.series, LiveSeries{
			name: "divy_experience_years", labels: promql.Labels{},
			value: func(t time.Time) (float64, bool) { return c.ExperienceYears(t) },
		})
	}
	return l
}

// LiveSeries implements promql.LiveProvider.
func (l *Live) LiveSeries() []promql.LiveSeries {
	out := make([]promql.LiveSeries, len(l.series))
	for i, s := range l.series {
		out[i] = s
	}
	return out
}

// All returns the concrete series (for /metrics).
func (l *Live) All() []LiveSeries { return l.series }
