package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"divy.dev/internal/store"
)

// Options configure the registry.
type Options struct {
	// Store supplies the latest sample per series (nil = no stored series exposed).
	Store *store.Store
	// Live supplies the live series (nil = none).
	Live *Live
	// Intervals are the configured collector cadences; missing names use DefaultIntervals.
	Intervals map[string]time.Duration
	// Now overrides the clock (tests).
	Now    func() time.Time
	Logger *slog.Logger
}

// Registry owns the client_golang registry behind /metrics and the
// in-process metrics of the server and the collector runner.
type Registry struct {
	reg          *prometheus.Registry
	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	collRuns     *prometheus.CounterVec
	collDuration *prometheus.HistogramVec
	opts         Options
}

// New builds the registry: Go and process collectors, the HTTP and collector
// metrics, and the catalogue collector that exposes the latest stored sample
// per series (subject to the staleness cut-off) plus the live series.
func New(o Options) *Registry {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	r := &Registry{reg: prometheus.NewRegistry(), opts: o}
	r.reg.MustRegister(collectors.NewGoCollector())
	r.reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	r.httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "divy_http_requests_total", Help: help("divy_http_requests_total")}, []string{"route", "method", "code"})
	r.httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "divy_http_request_duration_seconds", Help: help("divy_http_request_duration_seconds"), Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}}, []string{"route", "method"})
	r.collRuns = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "divy_collector_runs_total", Help: help("divy_collector_runs_total")}, []string{"collector", "result"})
	r.collDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "divy_collector_run_duration_seconds", Help: help("divy_collector_run_duration_seconds"), Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120}}, []string{"collector"})
	r.reg.MustRegister(r.httpRequests, r.httpDuration, r.collRuns, r.collDuration)
	r.reg.MustRegister(&catalogueCollector{r: r})
	return r
}

func help(name string) string {
	f, _ := Lookup(name)
	return f.Help
}

// Gatherer exposes the registry (tests, promlint).
func (r *Registry) Gatherer() prometheus.Gatherer { return r.reg }

// Handler serves GET /metrics: text exposition (protobuf when asked), gzip
// when accepted, at most 8 scrapes in flight, Cache-Control: no-store.
func (r *Registry) Handler() http.Handler {
	h := promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError, MaxRequestsInFlight: 8})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, req)
	})
}

// OnResult is the collector.Runner hook: counts runs by outcome and observes durations.
func (r *Registry) OnResult(name, outcome string, d time.Duration) {
	r.collRuns.WithLabelValues(name, outcome).Inc()
	if outcome != "skipped" {
		r.collDuration.WithLabelValues(name).Observe(d.Seconds())
	}
}

// Middleware records divy_http_requests_total and divy_http_request_duration_seconds
// per chi route pattern (never the raw path).
func (r *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		ww, ok := w.(middleware.WrapResponseWriter)
		if !ok {
			ww = middleware.NewWrapResponseWriter(w, req.ProtoMajor)
		}
		next.ServeHTTP(ww, req)
		route := "/*"
		if rctx := chi.RouteContext(req.Context()); rctx != nil {
			if p := rctx.RoutePattern(); p != "" {
				route = p
			}
		}
		method := req.Method
		switch method {
		case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		default:
			method = "OTHER"
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		r.httpRequests.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
		r.httpDuration.WithLabelValues(route, method).Observe(time.Since(start).Seconds())
	})
}

// interval returns the configured or default cadence of a collector.
func (r *Registry) interval(collector string) time.Duration {
	if d, ok := r.opts.Intervals[collector]; ok && d > 0 {
		return d
	}
	if d, ok := DefaultIntervals[collector]; ok {
		return d
	}
	return 15 * time.Minute
}

// catalogueCollector is an unchecked collector: the newest sample of every
// stored series that is not stale, the live series at scrape time, and the
// last successful run time per collector.
type catalogueCollector struct {
	r *Registry
}

// Describe sends nothing: the set of series is dynamic (unchecked collector).
func (c *catalogueCollector) Describe(chan<- *prometheus.Desc) {}

// Collect implements prometheus.Collector.
func (c *catalogueCollector) Collect(ch chan<- prometheus.Metric) {
	now := c.r.opts.Now()
	if l := c.r.opts.Live; l != nil {
		for _, s := range l.All() {
			fam, ok := Lookup(s.Metric())
			if !ok {
				continue
			}
			v, present := s.Value(now)
			if !present {
				continue
			}
			names, values := labelPairs(s.Labels().Map())
			ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(fam.Name, fam.Help, names, nil), valueType(fam.Type), v, values...)
		}
	}
	st := c.r.opts.Store
	if st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	latest, err := st.LatestPerSeries(ctx)
	if err != nil {
		c.r.opts.Logger.Warn("metrics: latest samples", "err", err.Error())
	}
	for _, l := range latest {
		fam, ok := Lookup(l.Metric)
		if !ok || fam.Source != SourceStored {
			continue
		}
		age := now.Sub(time.UnixMilli(l.TsMs))
		if age > StaleAfter(c.r.interval(fam.Collector)) {
			continue
		}
		names, values := labelPairs(l.Labels)
		ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(fam.Name, fam.Help, names, nil), valueType(fam.Type), l.Value, values...)
	}
	last, err := st.LastSuccess(ctx)
	if err != nil {
		c.r.opts.Logger.Warn("metrics: last success", "err", err.Error())
		return
	}
	fam, _ := Lookup("divy_collector_last_success_timestamp_seconds")
	names := make([]string, 0, len(last))
	for n := range last {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(fam.Name, fam.Help, []string{"collector"}, nil), prometheus.GaugeValue, float64(last[n])/1000, n)
	}
}

func valueType(t Type) prometheus.ValueType {
	if t == Counter {
		return prometheus.CounterValue
	}
	return prometheus.GaugeValue
}

// labelPairs returns sorted label names and their values.
func labelPairs(m map[string]string) ([]string, []string) {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	values := make([]string, len(names))
	for i, n := range names {
		values[i] = m[n]
	}
	return names, values
}

// ContentTypeText is the default exposition content type of client_golang.
const ContentTypeText = "text/plain; version=0.0.4; charset=utf-8"

// IsExpositionContentType reports whether ct is a text exposition content type (with any escaping parameter).
func IsExpositionContentType(ct string) bool {
	return strings.HasPrefix(ct, ContentTypeText)
}
