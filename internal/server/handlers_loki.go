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
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"divy.dev/internal/logql"
	"divy.dev/internal/model"
	"divy.dev/internal/promql"
)

// Loki family limits (docs/logql-subset.md §3).
const (
	lokiDefaultLimit = 100
	lokiMaxEntries   = 5000
	lokiTimeout      = 5 * time.Second
	lokiNotSupported = "not supported by divy.dev; see /loki/api/v1/status/buildinfo"
)

// lokiAPI serves the Loki HTTP API under /loki/api/v1 over the in-memory
// log streams of content/logs.ndjson (docs/logql-subset.md).
type lokiAPI struct {
	s      *Server
	engine *logql.Engine
	// start is the default window start: the root span's resolved start (L-X2).
	start time.Time
}

func newLokiAPI(s *Server) *lokiAPI {
	c := s.cfg.Content
	var streams []logql.Stream
	for _, st := range c.LogStreams() {
		entries := make([]logql.Entry, len(st.Entries))
		for i, e := range st.Entries {
			entries[i] = logql.Entry{TS: e.TSNano, Line: e.Raw}
		}
		streams = append(streams, logql.Stream{Labels: logql.NewLabels(st.Labels), Entries: entries})
	}
	return &lokiAPI{s: s, engine: &logql.Engine{Store: logql.NewStore(streams)}, start: c.LogsStart()}
}

// mountLoki registers the family as a sub-router so unknown paths answer the
// Loki text 404 and wrong methods 405 with Allow (contract K-X5, K.3.4).
func (s *Server) mountLoki(r chi.Router, timeout func(http.Handler) http.Handler) {
	api := newLokiAPI(s)
	r.Route("/loki/api/v1", func(r chi.Router) {
		r.Use(timeout)
		r.NotFound(s.lokiNotFound)
		r.MethodNotAllowed(s.methodNotAllowed)
		getPost := func(path string, h http.HandlerFunc) {
			r.Get(path, h)
			r.Post(path, h)
		}
		getPost("/query_range", api.handleQueryRange)
		getPost("/query", api.handleQuery)
		getPost("/labels", api.handleLabels)
		getPost("/label/{name}/values", api.handleLabelValues)
		getPost("/series", api.handleSeries)
		getPost("/index/stats", api.handleIndexStats)
		r.Get("/index/volume", api.handleVolume)
		r.Get("/status/buildinfo", api.handleBuildInfo)
	})
}

// ---- errors and envelopes ----

// lokiErr is a request error: plain text body, the status given (L-X1).
type lokiErr struct {
	status int
	msg    string
}

func lokiBad(format string, args ...any) *lokiErr {
	return &lokiErr{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

func (l *lokiAPI) writeErr(w http.ResponseWriter, e *lokiErr) {
	writeText(w, e.status, CacheNS, e.msg)
}

// writeJSON writes a success body with the cache class, a weak ETag and 304
// on a matching If-None-Match (same rules as the Prometheus family). The
// ETag hashes etagOf when given (the query result without its timing stats)
// and the whole body otherwise.
func (l *lokiAPI) writeJSON(w http.ResponseWriter, r *http.Request, cache string, v any, etagOf []byte) {
	b, err := json.Marshal(v)
	if err != nil {
		writeText(w, http.StatusInternalServerError, CacheNS, "internal error; trace id "+RequestID(r.Context()))
		return
	}
	if etagOf == nil {
		etagOf = b
	}
	sum := sha256.Sum256(etagOf)
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

// execErr maps evaluation errors to Loki statuses.
func (l *lokiAPI) execErr(r *http.Request, err error) *lokiErr {
	var qe *logql.QueryError
	var pe *logql.ParseError
	switch {
	case errors.As(err, &pe), errors.As(err, &qe):
		return lokiBad("%s", err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return &lokiErr{http.StatusGatewayTimeout, fmt.Sprintf("query timed out after %s", lokiTimeout)}
	case errors.Is(err, context.Canceled):
		return &lokiErr{499, ""}
	}
	l.s.log.Error("logql", "err", err.Error(), "req", RequestID(r.Context()))
	return &lokiErr{http.StatusInternalServerError, "internal error; trace id " + RequestID(r.Context())}
}

// ---- parameters ----

// parseForm merges a POST form body with the query string (1 MiB cap).
func (l *lokiAPI) parseForm(w http.ResponseWriter, r *http.Request) *lokiErr {
	if r.Method == http.MethodPost {
		ct := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
		if ct != "" && ct != "application/x-www-form-urlencoded" {
			return lokiBad("invalid parameter: body must be application/x-www-form-urlencoded")
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	}
	if err := r.ParseForm(); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return &lokiErr{http.StatusRequestEntityTooLarge, "request body too large"}
		}
		return lokiBad("error parsing form values: %v", err)
	}
	return nil
}

// parseLokiTime is Loki's parseTimestamp (review finding protocol-01): a
// value with a `.` is float seconds (rounded to ms); an integer with at most
// 10 digits is Unix seconds, longer is Unix nanoseconds; else RFC 3339.
func parseLokiTime(v string) (time.Time, error) {
	if strings.Contains(v, ".") {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			sec, frac := math.Modf(f)
			frac = math.Round(frac*1000) / 1000
			return time.Unix(int64(sec), int64(frac*float64(time.Second))).UTC(), nil
		}
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		if len(strings.TrimPrefix(v, "-")) <= 10 {
			return time.Unix(n, 0).UTC(), nil
		}
		return time.Unix(0, n).UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("cannot parse %q as nanoseconds, float seconds or RFC3339", v)
}

func (l *lokiAPI) timeParam(r *http.Request, name string, def time.Time) (time.Time, *lokiErr) {
	v := r.FormValue(name)
	if v == "" {
		return def, nil
	}
	t, err := parseLokiTime(v)
	if err != nil {
		return time.Time{}, lokiBad("invalid parameter %q: %v", name, err)
	}
	return t, nil
}

// bounds resolves start/end/since: end defaults to now, start to `end −
// since` or the root span start (L-X2); end must not precede start.
func (l *lokiAPI) bounds(r *http.Request) (start, end time.Time, e *lokiErr) {
	now := l.s.now()
	end, e = l.timeParam(r, "end", now)
	if e != nil {
		return start, end, e
	}
	if r.FormValue("start") != "" {
		start, e = l.timeParam(r, "start", now)
		if e != nil {
			return start, end, e
		}
	} else if since := r.FormValue("since"); since != "" {
		d, err := promql.ParseDuration(since)
		if err != nil {
			return start, end, lokiBad("invalid parameter %q: %v", "since", err)
		}
		base := end
		if now.Before(base) {
			base = now
		}
		start = base.Add(-d)
	} else {
		start = l.start
	}
	if end.Before(start) {
		return start, end, lokiBad("end must be after start")
	}
	return start, end, nil
}

// limitParam parses limit: default 100, must be positive (Loki's text); the
// 5000 cap is applied by the log-query handlers.
func limitParam(r *http.Request) (int, *lokiErr) {
	v := r.FormValue("limit")
	if v == "" {
		return lokiDefaultLimit, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, lokiBad("invalid parameter %q: %v", "limit", err)
	}
	if n <= 0 {
		return 0, lokiBad("limit must be a positive value")
	}
	return n, nil
}

func maxEntries(limit int) *lokiErr {
	if limit > lokiMaxEntries {
		return lokiBad("max entries limit per query exceeded, limit > max_entries_limit_per_query (%d > %d)", limit, lokiMaxEntries)
	}
	return nil
}

func directionParam(r *http.Request) (forward bool, e *lokiErr) {
	switch v := strings.ToLower(r.FormValue("direction")); v {
	case "", "backward":
		return false, nil
	case "forward":
		return true, nil
	default:
		return false, lokiBad("invalid direction %q: want forward or backward", r.FormValue("direction"))
	}
}

// stepParam parses step as float seconds or a Prometheus duration (Grafana
// sends `15000ms`); default max(⌊(end−start)/250⌋, 1) seconds (Loki's formula).
func stepParam(r *http.Request, start, end time.Time) (time.Duration, *lokiErr) {
	v := r.FormValue("step")
	if v == "" {
		secs := math.Max(math.Floor(end.Sub(start).Seconds()/250), 1)
		return time.Duration(secs) * time.Second, nil
	}
	d, err := promql.ParseAPIDuration(v)
	if err != nil {
		return 0, lokiBad("invalid parameter %q: %v", "step", err)
	}
	if d <= 0 {
		return 0, lokiBad("zero or negative query resolution step widths are not accepted. Try a positive integer")
	}
	return d, nil
}

// queryParam returns the query text; absent or blank is Loki's `unexpected $end`.
func queryParam(r *http.Request) (string, *lokiErr) {
	q := strings.TrimSpace(r.FormValue("query"))
	if q == "" {
		return "", lokiBad("parse error : syntax error: unexpected $end")
	}
	return q, nil
}

// selectorParam parses an optional selector-only query parameter.
func selectorParam(r *http.Request, name string, required bool) ([]*logql.Matcher, *lokiErr) {
	v := strings.TrimSpace(r.FormValue(name))
	if v == "" {
		if required {
			return nil, lokiBad("parse error : syntax error: unexpected $end")
		}
		return nil, nil
	}
	sel, err := logql.ParseSelector(v)
	if err != nil {
		return nil, lokiBad("%s", err.Error())
	}
	return sel, nil
}

func lokiStats(st logql.Stats) model.LokiStats {
	exec := st.Exec.Seconds()
	var bps, lps int64
	if exec > 0 {
		bps = int64(float64(st.Bytes) / exec)
		lps = int64(float64(st.Lines) / exec)
	}
	return model.LokiStats{
		Store: model.LokiStoreStats{TotalChunksRef: int64(st.Streams)},
		Summary: model.LokiSummaryStats{
			BytesProcessedPerSecond: bps, ExecTime: exec, LinesProcessedPerSecond: lps,
			TotalBytesProcessed: st.Bytes, TotalLinesProcessed: int64(st.Lines), TotalEntriesReturned: int64(st.Entries),
		},
	}
}

// writeQuery writes a query result; the ETag covers resultType + result so
// repeated identical queries share it despite the changing execTime.
func (l *lokiAPI) writeQuery(w http.ResponseWriter, r *http.Request, typ model.LokiResultType, v json.Marshaler, st logql.Stats) {
	raw, _ := v.MarshalJSON()
	res := model.LokiQueryResult{Status: "success", Data: model.LokiQueryData{ResultType: typ, Result: model.LokiResult(raw), Stats: lokiStats(st)}}
	l.writeJSON(w, r, CacheQ15, res, append([]byte(typ+":"), raw...))
}

// ---- handlers ----

func (l *lokiAPI) handleQueryRange(w http.ResponseWriter, r *http.Request) {
	if e := l.parseForm(w, r); e != nil {
		l.writeErr(w, e)
		return
	}
	qs, e := queryParam(r)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	start, end, e := l.bounds(r)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	limit, e := limitParam(r)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	forward, e := directionParam(r)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	q, err := logql.Parse(qs)
	if err != nil {
		l.writeErr(w, lokiBad("%s", err.Error()))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lokiTimeout)
	defer cancel()
	if lq, ok := q.(*logql.LogQuery); ok {
		if e := maxEntries(limit); e != nil {
			l.writeErr(w, e)
			return
		}
		streams, st, err := l.engine.Logs(ctx, lq, logql.LogOptions{Start: start.UnixNano(), End: end.UnixNano(), Limit: limit, Forward: forward})
		if err != nil {
			l.writeErr(w, l.execErr(r, err))
			return
		}
		l.writeQuery(w, r, "streams", streams, st)
		return
	}
	step, e := stepParam(r, start, end)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	m, st, err := l.engine.Range(ctx, q, start.UnixNano(), end.UnixNano(), step)
	if err != nil {
		l.writeErr(w, l.execErr(r, err))
		return
	}
	l.writeQuery(w, r, "matrix", m, st)
}

func (l *lokiAPI) handleQuery(w http.ResponseWriter, r *http.Request) {
	if e := l.parseForm(w, r); e != nil {
		l.writeErr(w, e)
		return
	}
	qs, e := queryParam(r)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	ts, e := l.timeParam(r, "time", l.s.now())
	if e != nil {
		l.writeErr(w, e)
		return
	}
	limit, e := limitParam(r)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	forward, e := directionParam(r)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	q, err := logql.Parse(qs)
	if err != nil {
		l.writeErr(w, lokiBad("%s", err.Error()))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lokiTimeout)
	defer cancel()
	if lq, ok := q.(*logql.LogQuery); ok {
		if e := maxEntries(limit); e != nil {
			l.writeErr(w, e)
			return
		}
		// instant log query: every entry with ts ≤ time, newest first (§L.2.3)
		streams, st, err := l.engine.Logs(ctx, lq, logql.LogOptions{Start: 0, End: ts.UnixNano() + 1, Limit: limit, Forward: forward})
		if err != nil {
			l.writeErr(w, l.execErr(r, err))
			return
		}
		l.writeQuery(w, r, "streams", streams, st)
		return
	}
	v, st, err := l.engine.Instant(ctx, q, ts.UnixNano())
	if err != nil {
		l.writeErr(w, l.execErr(r, err))
		return
	}
	l.writeQuery(w, r, "vector", v, st)
}

// windowRequest parses the shared start/end/since/query parameters of the label endpoints.
func (l *lokiAPI) windowRequest(w http.ResponseWriter, r *http.Request) (sel []*logql.Matcher, start, end int64, e *lokiErr) {
	if e := l.parseForm(w, r); e != nil {
		return nil, 0, 0, e
	}
	s, en, e := l.bounds(r)
	if e != nil {
		return nil, 0, 0, e
	}
	sel, e = selectorParam(r, "query", false)
	if e != nil {
		return nil, 0, 0, e
	}
	return sel, s.UnixNano(), en.UnixNano(), nil
}

func (l *lokiAPI) handleLabels(w http.ResponseWriter, r *http.Request) {
	sel, start, end, e := l.windowRequest(w, r)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	names := l.engine.Store.LabelNames(sel, start, end)
	if names == nil {
		names = []string{}
	}
	l.writeJSON(w, r, CacheQ15, model.LokiLabelsResult{Status: "success", Data: names}, nil)
}

func (l *lokiAPI) handleLabelValues(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	sel, start, end, e := l.windowRequest(w, r)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	values := l.engine.Store.LabelValues(name, sel, start, end)
	if values == nil {
		values = []string{}
	}
	l.writeJSON(w, r, CacheQ15, model.LokiLabelsResult{Status: "success", Data: values}, nil)
}

func (l *lokiAPI) handleSeries(w http.ResponseWriter, r *http.Request) {
	if e := l.parseForm(w, r); e != nil {
		l.writeErr(w, e)
		return
	}
	start, end, e := l.bounds(r)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	raw := append(r.Form["match[]"], r.Form["match"]...)
	var sels [][]*logql.Matcher
	for _, v := range raw {
		if strings.TrimSpace(v) == "" {
			continue
		}
		sel, err := logql.ParseSelector(v)
		if err != nil {
			l.writeErr(w, lokiBad("%s", err.Error()))
			return
		}
		sels = append(sels, sel)
	}
	if len(sels) == 0 {
		l.writeErr(w, lokiBad("at least one match[] selector is required"))
		return
	}
	series := l.engine.Store.Series(sels, start.UnixNano(), end.UnixNano())
	data := make([]map[string]string, 0, len(series))
	for _, ls := range series {
		data = append(data, ls.Map())
	}
	l.writeJSON(w, r, CacheQ15, model.LokiSeriesResult{Status: "success", Data: data}, nil)
}

func (l *lokiAPI) handleIndexStats(w http.ResponseWriter, r *http.Request) {
	if e := l.parseForm(w, r); e != nil {
		l.writeErr(w, e)
		return
	}
	sel, e := selectorParam(r, "query", true)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	start, end, e := l.bounds(r)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	st := l.engine.Store.Stats(sel, start.UnixNano(), end.UnixNano())
	l.writeJSON(w, r, CacheQ15, model.LokiIndexStats{Streams: st.Streams, Chunks: st.Streams, Entries: st.Entries, Bytes: st.Bytes}, nil)
}

func (l *lokiAPI) handleVolume(w http.ResponseWriter, r *http.Request) {
	if e := l.parseForm(w, r); e != nil {
		l.writeErr(w, e)
		return
	}
	sel, e := selectorParam(r, "query", true)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	start, end, e := l.bounds(r)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	limit, e := limitParam(r)
	if e != nil {
		l.writeErr(w, e)
		return
	}
	var target []string
	for _, t := range strings.Split(r.FormValue("targetLabels"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			target = append(target, t)
		}
	}
	byLabels := false
	switch v := r.FormValue("aggregateBy"); v {
	case "", "series":
	case "labels":
		byLabels = true
	default:
		l.writeErr(w, lokiBad("invalid aggregateBy %q: want series or labels", v))
		return
	}
	entries := l.engine.Store.Volume(sel, start.UnixNano(), end.UnixNano(), target, byLabels, limit)
	v := make(logql.Vector, 0, len(entries))
	for _, en := range entries {
		v = append(v, logql.Sample{Metric: en.Labels, T: end.UnixMilli(), V: float64(en.Bytes)})
	}
	raw, _ := v.MarshalJSON()
	l.writeJSON(w, r, CacheQ15, model.LokiVolumeResult{Status: "success", Data: model.LokiVolumeData{ResultType: "vector", Result: model.LokiResult(raw)}}, nil)
}

func (l *lokiAPI) handleBuildInfo(w http.ResponseWriter, r *http.Request) {
	c := l.s.cfg
	l.writeJSON(w, r, CacheC60, model.LokiBuildInfo{
		Version: c.Version, Revision: c.Commit, Branch: c.Branch, BuildUser: c.BuildUser, BuildDate: c.BuildDate, GoVersion: runtime.Version(),
	}, nil)
}
