// Package metrics is the metric catalogue (the single source of truth for
// every metric name, type, help text and label set), the live series that
// are functions of the evaluation time, and the client_golang registry
// behind GET /metrics.
package metrics

import (
	"sort"
	"time"
)

// Type is a Prometheus metric type.
type Type string

// Metric types.
const (
	Counter   Type = "counter"
	Gauge     Type = "gauge"
	Histogram Type = "histogram"
)

// Source says where a family's samples come from.
type Source string

// Sources.
const (
	// Stored families are written by collectors into the samples table and are queryable.
	SourceStored Source = "stored"
	// Live families are functions of the evaluation time (never stored) and are queryable.
	SourceLive Source = "live"
	// Process families are in-process client_golang metrics: exposition only, not queryable.
	SourceProcess Source = "process"
)

// Family is one metric family of the catalogue.
type Family struct {
	Name   string
	Type   Type
	Help   string
	Labels []string
	Source Source
	// Collector owns the family's samples (staleness on /metrics follows its cadence).
	Collector string
}

// Collector names and their default cadences (COLLECT_*_INTERVAL override them).
var DefaultIntervals = map[string]time.Duration{
	"github": 15 * time.Minute,
	"pypi":   60 * time.Minute,
	"uptime": 5 * time.Minute,
	"manual": 15 * time.Minute,
}

// Catalogue lists every metric family (conventions §7, storage §S.5,
// contract K-I3). github_stars_total from the brief is github_stars: promlint
// rejects the _total suffix on a gauge.
var Catalogue = []Family{
	{Name: "github_commits_total", Type: Counter, Help: "Cumulative commits by divysinghvi per day (GitHub contributionsCollection; private repositories counted, never named).", Source: SourceStored, Collector: "github"},
	{Name: "github_contributions_total", Type: Counter, Help: "Cumulative GitHub contributions per the contribution calendar (commits, issues, PRs, reviews).", Source: SourceStored, Collector: "github"},
	{Name: "github_merged_prs_total", Type: Counter, Help: "Cumulative merged pull requests authored by divysinghvi, by repository owner.", Labels: []string{"org"}, Source: SourceStored, Collector: "github"},
	{Name: "github_merged_prs_by_repo_total", Type: Counter, Help: "Cumulative merged pull requests authored by divysinghvi, by public repository.", Labels: []string{"org", "repo"}, Source: SourceStored, Collector: "github"},
	{Name: "github_stars", Type: Gauge, Help: "Current stargazer count of a repository.", Labels: []string{"repo"}, Source: SourceStored, Collector: "github"},
	{Name: "github_followers", Type: Gauge, Help: "Current follower count of the GitHub profile.", Source: SourceStored, Collector: "github"},
	{Name: "oss_prs_open", Type: Gauge, Help: "Open pull requests authored by divysinghvi in repositories not owned by divysinghvi.", Source: SourceStored, Collector: "github"},
	{Name: "pypi_downloads_total", Type: Counter, Help: "Cumulative PyPI downloads (pypistats.org overall, mirrors excluded).", Labels: []string{"package"}, Source: SourceStored, Collector: "pypi"},
	{Name: "pypi_package_info", Type: Gauge, Help: "Latest published version of a PyPI package (value is always 1).", Labels: []string{"package", "version"}, Source: SourceStored, Collector: "pypi"},
	{Name: "savely_active_users", Type: Gauge, Help: "Active users of the Savely Chrome extension (manual source, lower bound).", Source: SourceStored, Collector: "manual"},
	{Name: "lfx_applications", Type: Gauge, Help: "LFX Mentorship applications by status (manual source).", Labels: []string{"status"}, Source: SourceStored, Collector: "manual"},
	{Name: "divy_manual_metric_updated_timestamp_seconds", Type: Gauge, Help: "Unix time at which a manually maintained metric was last updated.", Labels: []string{"metric"}, Source: SourceStored, Collector: "manual"},
	{Name: "probe_success", Type: Gauge, Help: "Whether the last uptime probe of the target succeeded (1) or failed (0).", Labels: []string{"target"}, Source: SourceStored, Collector: "uptime"},
	{Name: "probe_duration_seconds", Type: Gauge, Help: "Duration of the last uptime probe of the target.", Labels: []string{"target"}, Source: SourceStored, Collector: "uptime"},
	{Name: "probe_http_status_code", Type: Gauge, Help: "HTTP status code returned by the last uptime probe of the target (0 on connection failure).", Labels: []string{"target"}, Source: SourceStored, Collector: "uptime"},
	{Name: "divy_uptime_seconds", Type: Gauge, Help: "Seconds since the API process started.", Source: SourceLive},
	{Name: "divy_build_info", Type: Gauge, Help: "Build metadata of the running binary (value is always 1).", Labels: []string{"version", "commit", "go_version"}, Source: SourceLive},
	{Name: "divy_open_to_work", Type: Gauge, Help: "Whether Divy is open to work (1) or not (0), from content/profile.yaml.", Source: SourceLive},
	{Name: "divy_experience_years", Type: Gauge, Help: "Years since the start of the earliest span counted as experience, from content/spans.yaml.", Source: SourceLive},
	{Name: "divy_collector_last_success_timestamp_seconds", Type: Gauge, Help: "Unix time of the last successful run of a collector.", Labels: []string{"collector"}, Source: SourceProcess},
	{Name: "divy_collector_runs_total", Type: Counter, Help: "Collector runs by collector and result.", Labels: []string{"collector", "result"}, Source: SourceProcess},
	{Name: "divy_collector_run_duration_seconds", Type: Histogram, Help: "Duration of collector runs in seconds, by collector.", Labels: []string{"collector"}, Source: SourceProcess},
	{Name: "divy_http_requests_total", Type: Counter, Help: "HTTP requests served by the API, by route, method and status code.", Labels: []string{"route", "method", "code"}, Source: SourceProcess},
	{Name: "divy_http_request_duration_seconds", Type: Histogram, Help: "HTTP request duration in seconds, by route and method.", Labels: []string{"route", "method"}, Source: SourceProcess},
	{Name: "divy_otel_spans_total", Type: Counter, Help: "Request spans started by the OpenTelemetry sampler, by decision.", Labels: []string{"decision"}, Source: SourceProcess},
	{Name: "divy_otel_exported_spans_total", Type: Counter, Help: "Spans written to the otel_spans table.", Source: SourceProcess},
	{Name: "divy_otel_export_errors_total", Type: Counter, Help: "Failed span export batches.", Source: SourceProcess},
}

var byName = func() map[string]Family {
	m := make(map[string]Family, len(Catalogue))
	for _, f := range Catalogue {
		m[f.Name] = f
	}
	return m
}()

// Lookup finds a family by name.
func Lookup(name string) (Family, bool) {
	f, ok := byName[name]
	return f, ok
}

// Queryable returns the families /api/v1/query can see (stored + live), sorted by name.
func Queryable() []Family {
	var out []Family
	for _, f := range Catalogue {
		if f.Source == SourceStored || f.Source == SourceLive {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// StaleAfter is the exposition freshness threshold of a collector cadence: max(3 × interval, 15m).
func StaleAfter(interval time.Duration) time.Duration {
	if d := 3 * interval; d > 15*time.Minute {
		return d
	}
	return 15 * time.Minute
}
