package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"divy.dev/internal/content"
	"divy.dev/internal/metrics"
	"divy.dev/internal/model"
	"divy.dev/internal/promql"
	"divy.dev/internal/store"
)

// promAPI serves the Prometheus HTTP API under /api/v1 (docs/promql-subset.md).
type promAPI struct {
	s      *Server
	engine *promql.Engine
	live   *metrics.Live
}

// storeQuerier adapts the store to the engine's Storage interface.
type storeQuerier struct{ st *store.Store }

func (q storeQuerier) Select(ctx context.Context, matchers []*promql.Matcher, startMs, endMs int64) ([]promql.SeriesData, error) {
	sm, err := storeMatchers(matchers)
	if err != nil {
		return nil, err
	}
	data, err := q.st.QueryRange(ctx, sm, startMs, endMs)
	if err != nil {
		return nil, err
	}
	out := make([]promql.SeriesData, 0, len(data))
	for _, sd := range data {
		if len(sd.Samples) == 0 {
			continue
		}
		pts := make([]promql.Point, len(sd.Samples))
		for i, sm := range sd.Samples {
			pts[i] = promql.Point{T: sm.TsMs, F: sm.Value}
		}
		out = append(out, promql.SeriesData{Metric: seriesLabels(sd.Series), Points: pts})
	}
	return out, nil
}

func storeMatchers(matchers []*promql.Matcher) ([]store.Matcher, error) {
	sm := make([]store.Matcher, 0, len(matchers))
	for _, m := range matchers {
		x, err := store.NewMatcher(m.Name, store.MatchType(m.Type.String()), m.Value)
		if err != nil {
			return nil, err
		}
		sm = append(sm, x)
	}
	return sm, nil
}

func seriesLabels(s store.Series) promql.Labels {
	m := make(map[string]string, len(s.Labels)+1)
	for k, v := range s.Labels {
		m[k] = v
	}
	m[promql.MetricName] = s.Metric
	return promql.NewLabels(m)
}

// Prometheus' default time bounds for optional start/end parameters.
var (
	promMinTime = time.Unix(math.MinInt64/1000+62135596801, 0).UTC()
	promMaxTime = time.Unix(math.MaxInt64/1000-62135596801, 999999999).UTC()
)

// mount registers the endpoints on the /api/v1 sub-router.
func (p *promAPI) mount(r chi.Router) {
	getPost := func(path string, h http.HandlerFunc) {
		r.Get(path, h)
		r.Post(path, h)
	}
	getPost("/query", p.handleQuery)
	getPost("/query_range", p.handleQueryRange)
	getPost("/series", p.handleSeries)
	getPost("/labels", p.handleLabels)
	r.Get("/label/{name}/values", p.handleLabelValues)
	r.Get("/metadata", p.handleMetadata)
	r.Get("/status/buildinfo", p.handleBuildInfo)
	r.Get("/rules", p.handleRules)
	r.Get("/alerts", p.handleAlerts)
	getPost("/query_exemplars", p.handleExemplars)
}

// ---- envelope helpers ----

type promErr struct {
	status int
	typ    model.PromErrorType
	msg    string
}

func badData(format string, args ...any) *promErr {
	return &promErr{http.StatusBadRequest, "bad_data", fmt.Sprintf(format, args...)}
}

func invalidParam(name string, err error) *promErr {
	return badData("invalid parameter %q: %v", name, err)
}

func (p *promAPI) writeErr(w http.ResponseWriter, e *promErr) {
	writePromError(w, e.status, e.typ, e.msg)
}

// writeSuccess writes the success envelope with the cache class, a weak ETag
// and 304 on a matching If-None-Match.
func (p *promAPI) writeSuccess(w http.ResponseWriter, r *http.Request, cache string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writePromError(w, http.StatusInternalServerError, "internal", "internal error: "+RequestID(r.Context()))
		return
	}
	sum := sha256.Sum256(b)
	etag := `W/"` + hex.EncodeToString(sum[:8]) + `"`
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", cache)
	h.Set("ETag", etag)
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// parseForm merges the POST form body with the query string (1 MiB cap).
func (p *promAPI) parseForm(w http.ResponseWriter, r *http.Request) *promErr {
	if r.Method == http.MethodPost {
		ct := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
		if ct != "" && ct != "application/x-www-form-urlencoded" {
			return badData("invalid parameter: body must be application/x-www-form-urlencoded")
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	}
	if err := r.ParseForm(); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return &promErr{http.StatusRequestEntityTooLarge, "bad_data", "request body too large"}
		}
		return badData("error parsing form values: %v", err)
	}
	return nil
}

// parseTime is Prometheus' web/api parseTime: float seconds (rounded to ms) or RFC 3339.
func parseTime(s string) (time.Time, error) {
	if t, err := strconv.ParseFloat(s, 64); err == nil {
		sec, ns := math.Modf(t)
		ns = math.Round(ns*1000) / 1000
		return time.Unix(int64(sec), int64(ns*float64(time.Second))).UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	switch s {
	case promMinTime.Format(time.RFC3339Nano):
		return promMinTime, nil
	case promMaxTime.Format(time.RFC3339Nano):
		return promMaxTime, nil
	}
	return time.Time{}, fmt.Errorf("cannot parse %q to a valid timestamp", s)
}

func parseTimeParam(r *http.Request, name string, def time.Time) (time.Time, error) {
	val := r.FormValue(name)
	if val == "" {
		return def, nil
	}
	t, err := parseTime(val)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time value for '%s': %w", name, err)
	}
	return t, nil
}

func parseLimitParam(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, errors.New("limit must be non-negative")
	}
	return n, nil
}

// queryOpts parses timeout and lookback_delta.
func (p *promAPI) queryOpts(r *http.Request) (promql.Opts, *promErr) {
	var o promql.Opts
	if to := r.FormValue("timeout"); to != "" {
		d, err := promql.ParseAPIDuration(to)
		if err != nil {
			return o, invalidParam("timeout", err)
		}
		o.Timeout = d
	}
	if lb := r.FormValue("lookback_delta"); lb != "" {
		d, err := promql.ParseAPIDuration(lb)
		if err != nil {
			return o, badData("error parsing lookback delta duration: %v", err)
		}
		o.Lookback = d
	}
	return o, nil
}

// execErr maps engine errors to the Prometheus status/errorType table.
func (p *promAPI) execErr(r *http.Request, err error) *promErr {
	var pe *promql.ParseError
	var rte *promql.RangeTypeError
	var ee *promql.ExecError
	switch {
	case errors.As(err, &pe), errors.As(err, &rte):
		return invalidParam("query", err)
	case errors.Is(err, promql.ErrTimeout):
		return &promErr{http.StatusServiceUnavailable, "timeout", err.Error()}
	case errors.Is(err, promql.ErrCanceled):
		return &promErr{499, "canceled", err.Error()}
	case errors.As(err, &ee):
		return &promErr{http.StatusUnprocessableEntity, "execution", err.Error()}
	}
	p.s.log.Error("promql", "err", err.Error(), "req", RequestID(r.Context()))
	return &promErr{http.StatusInternalServerError, "internal", "internal error: " + RequestID(r.Context())}
}

func truncate(v promql.Value, limit int) (promql.Value, bool) {
	if limit <= 0 {
		return v, false
	}
	switch x := v.(type) {
	case promql.Vector:
		if len(x) > limit {
			return x[:limit], true
		}
	case promql.Matrix:
		if len(x) > limit {
			return x[:limit], true
		}
	}
	return v, false
}

func (p *promAPI) queryResult(v promql.Value, warnings []string) model.PromQueryResult {
	raw, _ := v.MarshalJSON()
	return model.PromQueryResult{Status: "success", Data: model.PromQueryData{ResultType: model.PromResultType(v.Type()), Result: model.PromResult(raw)}, Warnings: warnings}
}

// ---- handlers ----

func (p *promAPI) handleQuery(w http.ResponseWriter, r *http.Request) {
	if e := p.parseForm(w, r); e != nil {
		p.writeErr(w, e)
		return
	}
	limit, err := parseLimitParam(r.FormValue("limit"))
	if err != nil {
		p.writeErr(w, invalidParam("limit", err))
		return
	}
	ts, err := parseTimeParam(r, "time", p.s.now())
	if err != nil {
		p.writeErr(w, invalidParam("time", err))
		return
	}
	opts, e := p.queryOpts(r)
	if e != nil {
		p.writeErr(w, e)
		return
	}
	expr, err := promql.ParseExpr(r.FormValue("query"))
	if err != nil {
		p.writeErr(w, invalidParam("query", err))
		return
	}
	v, err := p.engine.Instant(r.Context(), expr, ts, opts)
	if err != nil {
		p.writeErr(w, p.execErr(r, err))
		return
	}
	var warnings []string
	if t, ok := truncate(v, limit); ok {
		v = t
		warnings = []string{"results truncated due to limit"}
	}
	p.writeSuccess(w, r, CacheQ15, p.queryResult(v, warnings))
}

func (p *promAPI) handleQueryRange(w http.ResponseWriter, r *http.Request) {
	if e := p.parseForm(w, r); e != nil {
		p.writeErr(w, e)
		return
	}
	limit, err := parseLimitParam(r.FormValue("limit"))
	if err != nil {
		p.writeErr(w, invalidParam("limit", err))
		return
	}
	start, err := parseTime(r.FormValue("start"))
	if err != nil {
		p.writeErr(w, invalidParam("start", err))
		return
	}
	end, err := parseTime(r.FormValue("end"))
	if err != nil {
		p.writeErr(w, invalidParam("end", err))
		return
	}
	if end.Before(start) {
		p.writeErr(w, invalidParam("end", errors.New("end timestamp must not be before start time")))
		return
	}
	step, err := promql.ParseAPIDuration(r.FormValue("step"))
	if err != nil {
		p.writeErr(w, invalidParam("step", err))
		return
	}
	if step <= 0 {
		p.writeErr(w, invalidParam("step", errors.New("zero or negative query resolution step widths are not accepted. Try a positive integer")))
		return
	}
	if end.Sub(start)/step > promql.MaxPoints {
		p.writeErr(w, badData("exceeded maximum resolution of 11,000 points per timeseries. Try decreasing the query resolution (?step=XX)"))
		return
	}
	opts, e := p.queryOpts(r)
	if e != nil {
		p.writeErr(w, e)
		return
	}
	expr, err := promql.ParseExpr(r.FormValue("query"))
	if err != nil {
		p.writeErr(w, invalidParam("query", err))
		return
	}
	m, err := p.engine.Range(r.Context(), expr, start, end, step, opts)
	if err != nil {
		p.writeErr(w, p.execErr(r, err))
		return
	}
	var v promql.Value = m
	var warnings []string
	if t, ok := truncate(v, limit); ok {
		v = t
		warnings = []string{"results truncated due to limit"}
	}
	p.writeSuccess(w, r, CacheQ15, p.queryResult(v, warnings))
}

// parseMatchers parses match[] values; wrap says whether errors carry the
// `invalid parameter "match[]"` prefix (series does, labels do not).
func parseMatchers(values []string) ([][]*promql.Matcher, error) {
	var sets [][]*promql.Matcher
	for _, s := range values {
		ms, err := promql.ParseMetricSelector(s)
		if err != nil {
			return nil, err
		}
		sets = append(sets, ms)
	}
outer:
	for _, ms := range sets {
		for _, m := range ms {
			if !m.Matches("") {
				continue outer
			}
		}
		return nil, errors.New("match[] must contain at least one non-empty matcher")
	}
	return sets, nil
}

// selectSeries returns the label sets (with __name__) of the stored and live
// series accepted by any matcher set (all series when sets is empty), with a
// sample inside [start, end] for stored series.
func (p *promAPI) selectSeries(ctx context.Context, sets [][]*promql.Matcher, start, end time.Time) ([]promql.Labels, error) {
	if len(sets) == 0 {
		sets = [][]*promql.Matcher{nil}
	}
	seen := map[string]promql.Labels{}
	add := func(l promql.Labels) { seen[l.String()] = l }
	unbounded := !start.After(promMinTime) && !end.Before(promMaxTime)
	for _, ms := range sets {
		if p.s.cfg.Store != nil {
			sm, err := storeMatchers(ms)
			if err != nil {
				return nil, err
			}
			if unbounded {
				list, err := p.s.cfg.Store.ListSeries(ctx, sm)
				if err != nil {
					return nil, err
				}
				for _, s := range list {
					add(seriesLabels(s))
				}
			} else {
				data, err := p.s.cfg.Store.QueryRange(ctx, sm, start.UnixMilli()-1, end.UnixMilli())
				if err != nil {
					return nil, err
				}
				for _, s := range data {
					if len(s.Samples) > 0 {
						add(seriesLabels(s.Series))
					}
				}
			}
		}
		if p.live != nil {
			for _, ls := range p.live.LiveSeries() {
				l := ls.Labels().WithName(ls.Metric())
				if promql.MatchLabels(ms, l) {
					add(l)
				}
			}
		}
	}
	out := make([]promql.Labels, 0, len(seen))
	for _, l := range seen {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

func (p *promAPI) handleSeries(w http.ResponseWriter, r *http.Request) {
	if e := p.parseForm(w, r); e != nil {
		p.writeErr(w, e)
		return
	}
	if len(r.Form["match[]"]) == 0 {
		p.writeErr(w, badData("no match[] parameter provided"))
		return
	}
	limit, err := parseLimitParam(r.FormValue("limit"))
	if err != nil {
		p.writeErr(w, invalidParam("limit", err))
		return
	}
	start, err := parseTimeParam(r, "start", promMinTime)
	if err != nil {
		p.writeErr(w, invalidParam("start", err))
		return
	}
	end, err := parseTimeParam(r, "end", promMaxTime)
	if err != nil {
		p.writeErr(w, invalidParam("end", err))
		return
	}
	sets, err := parseMatchers(r.Form["match[]"])
	if err != nil {
		p.writeErr(w, invalidParam("match[]", err))
		return
	}
	series, err := p.selectSeries(r.Context(), sets, start, end)
	if err != nil {
		p.writeErr(w, p.execErr(r, err))
		return
	}
	var warnings []string
	if limit > 0 && len(series) > limit {
		series = series[:limit]
		warnings = []string{"results truncated due to limit"}
	}
	data := make([]map[string]string, 0, len(series))
	for _, l := range series {
		data = append(data, l.Map())
	}
	p.writeSuccess(w, r, CacheQ15, model.PromSeriesResult{Status: "success", Data: data, Warnings: warnings})
}

func (p *promAPI) labelsRequest(w http.ResponseWriter, r *http.Request) (sets [][]*promql.Matcher, start, end time.Time, limit int, e *promErr) {
	if e := p.parseForm(w, r); e != nil {
		return nil, start, end, 0, e
	}
	limit, err := parseLimitParam(r.FormValue("limit"))
	if err != nil {
		return nil, start, end, 0, invalidParam("limit", err)
	}
	start, err = parseTimeParam(r, "start", promMinTime)
	if err != nil {
		return nil, start, end, 0, invalidParam("start", err)
	}
	end, err = parseTimeParam(r, "end", promMaxTime)
	if err != nil {
		return nil, start, end, 0, invalidParam("end", err)
	}
	sets, err = parseMatchers(r.Form["match[]"])
	if err != nil {
		return nil, start, end, 0, badData("%v", err)
	}
	return sets, start, end, limit, nil
}

func (p *promAPI) handleLabels(w http.ResponseWriter, r *http.Request) {
	sets, start, end, limit, e := p.labelsRequest(w, r)
	if e != nil {
		p.writeErr(w, e)
		return
	}
	series, err := p.selectSeries(r.Context(), sets, start, end)
	if err != nil {
		p.writeErr(w, p.execErr(r, err))
		return
	}
	set := map[string]bool{}
	for _, l := range series {
		for _, lb := range l {
			set[lb.Name] = true
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	var warnings []string
	if limit > 0 && len(names) > limit {
		names = names[:limit]
		warnings = []string{"results truncated due to limit"}
	}
	p.writeSuccess(w, r, CacheQ15, model.PromLabelsResult{Status: "success", Data: names, Warnings: warnings})
}

func (p *promAPI) handleLabelValues(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" || !utf8.ValidString(name) {
		p.writeErr(w, badData("invalid label name: %q", name))
		return
	}
	sets, start, end, limit, e := p.labelsRequest(w, r)
	if e != nil {
		p.writeErr(w, e)
		return
	}
	series, err := p.selectSeries(r.Context(), sets, start, end)
	if err != nil {
		p.writeErr(w, p.execErr(r, err))
		return
	}
	set := map[string]bool{}
	for _, l := range series {
		if l.Has(name) {
			set[l.Get(name)] = true
		}
	}
	values := make([]string, 0, len(set))
	for v := range set {
		values = append(values, v)
	}
	sort.Strings(values)
	var warnings []string
	if limit > 0 && len(values) > limit {
		values = values[:limit]
		warnings = []string{"results truncated due to limit"}
	}
	p.writeSuccess(w, r, CacheQ15, model.PromLabelsResult{Status: "success", Data: values, Warnings: warnings})
}

func (p *promAPI) handleMetadata(w http.ResponseWriter, r *http.Request) {
	limit := -1
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			p.writeErr(w, badData("limit must be a number"))
			return
		}
		limit = n
	}
	if s := r.URL.Query().Get("limit_per_metric"); s != "" {
		if _, err := strconv.Atoi(s); err != nil {
			p.writeErr(w, badData("limit_per_metric must be a number"))
			return
		}
	}
	metric := r.URL.Query().Get("metric")
	data := map[string][]model.PromMetadata{}
	for _, f := range metrics.Queryable() {
		if metric != "" && f.Name != metric {
			continue
		}
		if limit >= 0 && len(data) >= limit {
			break
		}
		data[f.Name] = []model.PromMetadata{{Type: string(f.Type), Help: f.Help, Unit: ""}}
	}
	p.writeSuccess(w, r, CacheC60, model.PromMetadataResult{Status: "success", Data: data})
}

func (p *promAPI) handleBuildInfo(w http.ResponseWriter, r *http.Request) {
	c := p.s.cfg
	p.writeSuccess(w, r, CacheC60, model.PromBuildInfoResult{Status: "success", Data: model.PromBuildInfo{
		Version: c.Version, Revision: c.Commit, Branch: c.Branch, BuildUser: c.BuildUser, BuildDate: c.BuildDate, GoVersion: runtime.Version(),
	}})
}

// zeroTime is what Prometheus prints for a rule that was never evaluated.
const zeroTime = "0001-01-01T00:00:00Z"

// RuleGroups renders content/alerts.yaml in Prometheus API shape.
func RuleGroups(c *content.Content) []model.PromRuleGroup {
	groups := make([]model.PromRuleGroup, 0, len(c.Alerts.Groups))
	for _, g := range c.Alerts.Groups {
		interval := 60.0
		if g.Interval != "" {
			if d, err := content.ParsePromDuration(g.Interval); err == nil {
				interval = d.Seconds()
			}
		}
		rules := make([]model.PromAlertingRule, 0, len(g.Rules))
		for _, ar := range g.Rules {
			query := ar.Expr
			if e, err := promql.ParseExpr(ar.Expr); err == nil {
				query = e.String()
			}
			dur := 0.0
			if ar.For != "" {
				if d, err := content.ParsePromDuration(ar.For); err == nil {
					dur = d.Seconds()
				}
			}
			labels := ar.Labels
			if labels == nil {
				labels = map[string]string{}
			}
			ann := ar.Annotations
			if ann == nil {
				ann = map[string]string{}
			}
			rules = append(rules, model.PromAlertingRule{
				State: "inactive", Name: ar.Alert, Query: query, Duration: dur, KeepFiringFor: 0,
				Labels: labels, Annotations: ann, Alerts: []model.PromAlert{}, Health: "unknown",
				EvaluationTime: 0, LastEvaluation: zeroTime, Type: "alerting",
			})
		}
		groups = append(groups, model.PromRuleGroup{Name: g.Name, File: "content/alerts.yaml", Rules: rules, Interval: interval, Limit: 0, EvaluationTime: 0, LastEvaluation: zeroTime})
	}
	return groups
}

func (p *promAPI) handleRules(w http.ResponseWriter, r *http.Request) {
	if e := p.parseForm(w, r); e != nil {
		p.writeErr(w, e)
		return
	}
	toSet := func(vs []string) map[string]bool {
		m := map[string]bool{}
		for _, v := range vs {
			m[v] = true
		}
		return m
	}
	rnSet, rgSet, fSet := toSet(r.Form["rule_name[]"]), toSet(r.Form["rule_group[]"]), toSet(r.Form["file[]"])
	if _, err := parseMatchers(r.Form["match[]"]); err != nil {
		p.writeErr(w, badData("%v", err))
		return
	}
	typ := strings.ToLower(r.URL.Query().Get("type"))
	if typ != "" && typ != "alert" && typ != "record" {
		p.writeErr(w, invalidParam("type", fmt.Errorf("not supported value %q", typ)))
		return
	}
	returnAlerts := typ == "" || typ == "alert"
	out := []model.PromRuleGroup{}
	for _, g := range RuleGroups(p.s.cfg.Content) {
		if len(rgSet) > 0 && !rgSet[g.Name] {
			continue
		}
		if len(fSet) > 0 && !fSet[g.File] {
			continue
		}
		rules := []model.PromAlertingRule{}
		if returnAlerts {
			for _, rule := range g.Rules {
				if len(rnSet) > 0 && !rnSet[rule.Name] {
					continue
				}
				rules = append(rules, rule)
			}
		}
		if len(rules) == 0 {
			continue
		}
		g.Rules = rules
		out = append(out, g)
	}
	p.writeSuccess(w, r, CacheC60, model.PromRulesResult{Status: "success", Data: model.PromRuleGroups{Groups: out}})
}

func (p *promAPI) handleAlerts(w http.ResponseWriter, r *http.Request) {
	p.writeSuccess(w, r, CacheC60, model.PromAlertsResult{Status: "success", Data: model.PromAlerts{Alerts: []model.PromAlert{}}})
}

func (p *promAPI) handleExemplars(w http.ResponseWriter, r *http.Request) {
	if e := p.parseForm(w, r); e != nil {
		p.writeErr(w, e)
		return
	}
	if _, err := parseTimeParam(r, "start", promMinTime); err != nil {
		p.writeErr(w, invalidParam("start", err))
		return
	}
	if _, err := parseTimeParam(r, "end", promMaxTime); err != nil {
		p.writeErr(w, invalidParam("end", err))
		return
	}
	if _, err := promql.ParseExpr(r.FormValue("query")); err != nil {
		p.writeErr(w, invalidParam("query", err))
		return
	}
	p.writeSuccess(w, r, CacheC60, model.PromExemplarsResult{Status: "success", Data: []json.RawMessage{}})
}
