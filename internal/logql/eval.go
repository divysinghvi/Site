package logql

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dustin/go-humanize"

	"divy.dev/internal/promql"
)

// Limits every query obeys.
const (
	MaxSteps  = 11000
	MaxSeries = 1000
)

// Entry is one log line with its unique nanosecond timestamp.
type Entry struct {
	TS   int64
	Line string
}

// Stream is one label set and its entries in ascending time order.
type Stream struct {
	Labels  Labels
	Entries []Entry
}

// Store is the immutable in-memory log index the engine queries.
type Store struct {
	streams []*Stream
}

// NewStore indexes the streams: entries sorted by timestamp, streams by label string.
func NewStore(streams []Stream) *Store {
	s := &Store{}
	for _, in := range streams {
		st := &Stream{Labels: append(Labels(nil), in.Labels...), Entries: append([]Entry(nil), in.Entries...)}
		sort.SliceStable(st.Entries, func(i, j int) bool { return st.Entries[i].TS < st.Entries[j].TS })
		s.streams = append(s.streams, st)
	}
	sort.SliceStable(s.streams, func(i, j int) bool { return s.streams[i].Labels.String() < s.streams[j].Labels.String() })
	return s
}

// Streams returns every stream.
func (s *Store) Streams() []*Stream { return s.streams }

// Select returns the streams whose labels satisfy every matcher.
func (s *Store) Select(ms []*Matcher) []*Stream {
	var out []*Stream
	for _, st := range s.streams {
		if MatchAll(ms, st.Labels) {
			out = append(out, st)
		}
	}
	return out
}

// window returns the entries with start ≤ ts < end.
func (st *Stream) window(start, end int64) []Entry {
	lo := sort.Search(len(st.Entries), func(i int) bool { return st.Entries[i].TS >= start })
	hi := sort.Search(len(st.Entries), func(i int) bool { return st.Entries[i].TS >= end })
	if lo >= hi {
		return nil
	}
	return st.Entries[lo:hi]
}

// LabelNames lists the label names of the streams matching sel with at least one entry in [start, end).
func (s *Store) LabelNames(sel []*Matcher, start, end int64) []string {
	set := map[string]bool{}
	for _, st := range s.Select(sel) {
		if len(st.window(start, end)) == 0 {
			continue
		}
		for _, l := range st.Labels {
			set[l.Name] = true
		}
	}
	return sortedKeys(set)
}

// LabelValues lists the values of one label over the same streams.
func (s *Store) LabelValues(name string, sel []*Matcher, start, end int64) []string {
	set := map[string]bool{}
	for _, st := range s.Select(sel) {
		if len(st.window(start, end)) == 0 {
			continue
		}
		if v, ok := st.Labels.Get(name); ok {
			set[v] = true
		}
	}
	return sortedKeys(set)
}

// Series returns the label sets of the streams matching any selector with an entry in the window.
func (s *Store) Series(sels [][]*Matcher, start, end int64) []Labels {
	seen := map[string]Labels{}
	for _, sel := range sels {
		for _, st := range s.Select(sel) {
			if len(st.window(start, end)) == 0 {
				continue
			}
			seen[st.Labels.String()] = st.Labels
		}
	}
	out := make([]Labels, 0, len(seen))
	for _, k := range sortedKeys(boolKeys(seen)) {
		out = append(out, seen[k])
	}
	return out
}

// IndexStats counts streams, entries and raw bytes in the window.
type IndexStats struct {
	Streams uint64
	Entries uint64
	Bytes   uint64
}

// Stats counts the streams matching sel with entries in the window.
func (s *Store) Stats(sel []*Matcher, start, end int64) IndexStats {
	var out IndexStats
	for _, st := range s.Select(sel) {
		w := st.window(start, end)
		if len(w) == 0 {
			continue
		}
		out.Streams++
		out.Entries += uint64(len(w))
		for _, e := range w {
			out.Bytes += uint64(len(e.Line))
		}
	}
	return out
}

// VolumeEntry is the raw bytes of one series or label value.
type VolumeEntry struct {
	Labels Labels
	Bytes  uint64
}

// Volume sums raw bytes per stream (restricted to targetLabels when given) or,
// with byLabels, per label name/value pair; the top limit by bytes are returned.
func (s *Store) Volume(sel []*Matcher, start, end int64, targetLabels []string, byLabels bool, limit int) []VolumeEntry {
	acc := map[string]*VolumeEntry{}
	add := func(ls Labels, b uint64) {
		k := ls.String()
		if v, ok := acc[k]; ok {
			v.Bytes += b
			return
		}
		acc[k] = &VolumeEntry{Labels: ls, Bytes: b}
	}
	for _, st := range s.Select(sel) {
		var b uint64
		for _, e := range st.window(start, end) {
			b += uint64(len(e.Line))
		}
		if b == 0 {
			continue
		}
		ls := st.Labels
		if len(targetLabels) > 0 {
			ls = ls.Keep(targetLabels)
		}
		if !byLabels {
			add(ls, b)
			continue
		}
		for _, l := range ls {
			add(Labels{l}, b)
		}
	}
	out := make([]VolumeEntry, 0, len(acc))
	for _, v := range acc {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Labels.String() < out[j].Labels.String()
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func boolKeys(m map[string]Labels) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// ---- errors ----

// QueryError is a request-level evaluation error (HTTP 400).
type QueryError struct{ Msg string }

func (e *QueryError) Error() string { return e.Msg }

// ---- pipeline ----

// Process runs the stages over one entry: line filters on the raw line,
// `| json` extraction, label filters. It returns the final label set and
// whether the entry survives.
func (q *LogQuery) Process(stream Labels, line string) (Labels, bool) {
	ls := stream
	for _, st := range q.Stages {
		switch s := st.(type) {
		case *LineFilter:
			if !s.Matches(line) {
				return ls, false
			}
		case *JSONParser:
			ls = extractJSON(stream, ls, line)
		case *LabelFilter:
			var ok bool
			ls, ok = evalLF(s.Expr, ls)
			if !ok {
				return ls, false
			}
		}
	}
	return ls, true
}

func evalLF(e LFExpr, ls Labels) (Labels, bool) {
	switch x := e.(type) {
	case *LFAnd:
		ls, ok := evalLF(x.L, ls)
		if !ok {
			return ls, false
		}
		return evalLF(x.R, ls)
	case *LFOr:
		ls, ok := evalLF(x.L, ls)
		if ok {
			return ls, true
		}
		return evalLF(x.R, ls)
	case *LFString:
		v, _ := ls.Get(x.Name)
		switch x.Op {
		case OpEq:
			return ls, v == x.Value
		case OpNeq:
			return ls, v != x.Value
		case OpRe:
			return ls, x.re.MatchString(v)
		case OpNre:
			return ls, !x.re.MatchString(v)
		}
	case *LFNumber:
		if ls.Has(ErrorLabel) {
			// an errored entry passes numeric filters; only string filters drop it (Loki)
			return ls, true
		}
		v, ok := ls.Get(x.Name)
		if !ok {
			return ls, false
		}
		f, err := parseNumeric(v, x.Kind)
		if err != nil {
			return ls.With(ErrorLabel, ErrLabelFilter).With(ErrorDetailsLabel, err.Error()), true
		}
		switch x.Op {
		case CmpEq:
			return ls, f == x.Value
		case CmpNeq:
			return ls, f != x.Value
		case CmpGt:
			return ls, f > x.Value
		case CmpGte:
			return ls, f >= x.Value
		case CmpLt:
			return ls, f < x.Value
		case CmpLte:
			return ls, f <= x.Value
		}
	}
	return ls, false
}

// parseNumeric parses a label value for a numeric filter: a float, a
// duration in seconds (Prometheus or Go syntax; a bare number is seconds)
// or a bytes literal.
func parseNumeric(v string, kind NumKind) (float64, error) {
	switch kind {
	case NumDuration:
		if d, err := promql.ParseDuration(v); err == nil {
			return d.Seconds(), nil
		}
		if d, err := time.ParseDuration(v); err == nil {
			return d.Seconds(), nil
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, nil
		}
		return 0, fmt.Errorf("not a valid duration string: %q", v)
	case NumBytes:
		b, err := humanize.ParseBytes(v)
		if err != nil {
			return 0, err
		}
		return float64(b), nil
	}
	return strconv.ParseFloat(v, 64)
}

// ---- engine ----

// Engine evaluates queries over a Store.
type Engine struct {
	Store *Store
}

// Stats describes the work a query did.
type Stats struct {
	Streams int   // streams selected
	Lines   int   // entries scanned
	Bytes   int64 // their raw bytes
	Entries int   // entries returned (log queries)
	Exec    time.Duration
}

// LogOptions bound a log query.
type LogOptions struct {
	Start, End int64 // ns, window start ≤ ts < end
	Limit      int   // 0 = unlimited
	Forward    bool  // false = newest first
}

// ResultStream is one output stream: the final label set and its entries.
type ResultStream struct {
	Labels  Labels
	Entries []Entry
}

// Streams is the result of a log query, sorted by label string.
type Streams []ResultStream

type resultEntry struct {
	labels Labels
	key    string
	ts     int64
	line   string
}

// scan runs the pipeline over every selected entry in [start, end).
func (e *Engine) scan(ctx context.Context, q *LogQuery, start, end int64, stats *Stats) ([]resultEntry, error) {
	var out []resultEntry
	for _, st := range e.Store.Select(q.Selector) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		stats.Streams++
		for _, en := range st.window(start, end) {
			stats.Lines++
			stats.Bytes += int64(len(en.Line))
			ls, ok := q.Process(st.Labels, en.Line)
			if !ok {
				continue
			}
			out = append(out, resultEntry{labels: ls, key: ls.String(), ts: en.TS, line: en.Line})
		}
	}
	return out, nil
}

// Logs evaluates a log query.
func (e *Engine) Logs(ctx context.Context, q *LogQuery, opt LogOptions) (Streams, Stats, error) {
	t0 := time.Now()
	var stats Stats
	entries, err := e.scan(ctx, q, opt.Start, opt.End, &stats)
	if err != nil {
		return nil, stats, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.ts != b.ts {
			if opt.Forward {
				return a.ts < b.ts
			}
			return a.ts > b.ts
		}
		return a.key < b.key
	})
	if opt.Limit > 0 && len(entries) > opt.Limit {
		entries = entries[:opt.Limit]
	}
	stats.Entries = len(entries)
	byKey := map[string]*ResultStream{}
	for _, en := range entries {
		rs, ok := byKey[en.key]
		if !ok {
			rs = &ResultStream{Labels: en.labels}
			byKey[en.key] = rs
		}
		rs.Entries = append(rs.Entries, Entry{TS: en.ts, Line: en.line})
	}
	out := make(Streams, 0, len(byKey))
	for _, k := range sortedKeys(boolKeysRS(byKey)) {
		out = append(out, *byKey[k])
	}
	stats.Exec = time.Since(t0)
	return out, stats, nil
}

func boolKeysRS(m map[string]*ResultStream) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// Point is one metric sample (milliseconds, value).
type Point struct {
	T int64
	V float64
}

// Series is one metric series.
type Series struct {
	Metric Labels
	Points []Point
}

// Matrix is a range-query result sorted by label string.
type Matrix []Series

// Sample is one instant-query sample.
type Sample struct {
	Metric Labels
	T      int64 // ms
	V      float64
}

// Vector is an instant-query result sorted by label string.
type Vector []Sample

// Range evaluates a metric or scalar query at start, start+step, … ≤ end (nanoseconds).
func (e *Engine) Range(ctx context.Context, q Query, start, end int64, step time.Duration) (Matrix, Stats, error) {
	t0 := time.Now()
	var stats Stats
	if step <= 0 {
		return nil, stats, &QueryError{Msg: "zero or negative query resolution step widths are not accepted. Try a positive integer"}
	}
	if end < start {
		return nil, stats, &QueryError{Msg: "end must be after start"}
	}
	n := int((end-start)/int64(step)) + 1
	if n-1 > MaxSteps {
		return nil, stats, &QueryError{Msg: fmt.Sprintf("too many steps (%d > %d); increase step", n-1, MaxSteps)}
	}
	steps := make([]int64, 0, n)
	for t := start; t <= end; t += int64(step) {
		steps = append(steps, t)
	}
	switch x := q.(type) {
	case *ScalarQuery:
		pts := make([]Point, len(steps))
		for i, t := range steps {
			pts[i] = Point{T: t / int64(time.Millisecond), V: x.Value}
		}
		stats.Exec = time.Since(t0)
		return Matrix{{Metric: Labels{}, Points: pts}}, stats, nil
	case *MetricQuery:
		m, err := e.evalMetric(ctx, x, steps, &stats)
		stats.Exec = time.Since(t0)
		return m, stats, err
	}
	return nil, stats, &QueryError{Msg: "log queries have no metric result; use /loki/api/v1/query_range with a log selector for streams"}
}

// Instant evaluates a metric or scalar query at t (nanoseconds).
func (e *Engine) Instant(ctx context.Context, q Query, t int64) (Vector, Stats, error) {
	t0 := time.Now()
	var stats Stats
	switch x := q.(type) {
	case *ScalarQuery:
		stats.Exec = time.Since(t0)
		return Vector{{Metric: Labels{}, T: t / int64(time.Millisecond), V: x.Value}}, stats, nil
	case *MetricQuery:
		m, err := e.evalMetric(ctx, x, []int64{t}, &stats)
		stats.Exec = time.Since(t0)
		if err != nil {
			return nil, stats, err
		}
		out := make(Vector, 0, len(m))
		for _, s := range m {
			for _, p := range s.Points {
				out = append(out, Sample{Metric: s.Metric, T: p.T, V: p.V})
			}
		}
		return out, stats, nil
	}
	return nil, stats, &QueryError{Msg: "log queries have no vector result"}
}

// evalMetric computes the range aggregation at every step and applies the
// optional vector aggregation.
func (e *Engine) evalMetric(ctx context.Context, q *MetricQuery, steps []int64, stats *Stats) (Matrix, error) {
	r := q.Range
	if len(steps) == 0 {
		return Matrix{}, nil
	}
	rng := int64(r.Range)
	lo, hi := steps[0]-rng+1, steps[len(steps)-1]+1
	entries, err := e.scan(ctx, r.Log, lo, hi, stats)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].ts < entries[j].ts })
	type group struct {
		labels Labels
		points []Point
	}
	series := map[string]*group{}
	for _, t := range steps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// entries with t − R < ts ≤ t
		from := sort.Search(len(entries), func(i int) bool { return entries[i].ts > t-rng })
		to := sort.Search(len(entries), func(i int) bool { return entries[i].ts > t })
		counts := map[string]*group{}
		var order []string
		for _, en := range entries[from:to] {
			g, ok := counts[en.key]
			if !ok {
				g = &group{labels: en.labels}
				counts[en.key] = g
				order = append(order, en.key)
			}
			if len(g.points) == 0 {
				g.points = []Point{{V: 0}}
			}
			g.points[0].V++
		}
		if len(counts) == 0 {
			continue
		}
		// range aggregation values per final label set
		values := make([]Sample, 0, len(counts))
		for _, k := range order {
			g := counts[k]
			v := g.points[0].V
			if r.Fn == "rate" {
				v /= r.Range.Seconds()
			}
			values = append(values, Sample{Metric: g.labels, V: v})
		}
		if q.Agg != nil {
			values = aggregate(q.Agg, values)
		}
		for _, s := range values {
			k := s.Metric.String()
			g, ok := series[k]
			if !ok {
				if len(series) >= MaxSeries {
					return nil, &QueryError{Msg: fmt.Sprintf("query produced too many series (%d > %d); add a by() clause", len(series)+1, MaxSeries)}
				}
				g = &group{labels: s.Metric}
				series[k] = g
			}
			g.points = append(g.points, Point{T: t / int64(time.Millisecond), V: s.V})
		}
	}
	keys := make([]string, 0, len(series))
	for k := range series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(Matrix, 0, len(keys))
	for _, k := range keys {
		out = append(out, Series{Metric: series[k].labels, Points: series[k].points})
	}
	return out, nil
}

// aggregate applies sum/count/min/max/avg with by/without grouping.
func aggregate(a *Aggregation, in []Sample) []Sample {
	type acc struct {
		labels Labels
		sum    float64
		n      int
		minV   float64
		maxV   float64
	}
	groups := map[string]*acc{}
	var order []string
	for _, s := range in {
		var ls Labels
		switch {
		case !a.Grouping:
			ls = Labels{}
		case a.Without:
			ls = s.Metric.Without(a.Labels)
		default:
			ls = s.Metric.Keep(a.Labels)
		}
		k := ls.String()
		g, ok := groups[k]
		if !ok {
			g = &acc{labels: ls, minV: math.Inf(1), maxV: math.Inf(-1)}
			groups[k] = g
			order = append(order, k)
		}
		g.sum += s.V
		g.n++
		g.minV = math.Min(g.minV, s.V)
		g.maxV = math.Max(g.maxV, s.V)
	}
	out := make([]Sample, 0, len(groups))
	for _, k := range order {
		g := groups[k]
		var v float64
		switch a.Op {
		case "sum":
			v = g.sum
		case "count":
			v = float64(g.n)
		case "min":
			v = g.minV
		case "max":
			v = g.maxV
		case "avg":
			v = g.sum / float64(g.n)
		}
		out = append(out, Sample{Metric: g.labels, V: v})
	}
	return out
}

// FormatValue renders a sample value the way Loki and Prometheus do.
func FormatValue(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// FormatTime renders milliseconds as JSON seconds with a fraction only when needed.
func FormatTime(ms int64) string {
	return strconv.FormatFloat(float64(ms)/1000, 'f', -1, 64)
}

// ---- JSON ----

// MarshalJSON writes Loki's streams result: [{"stream":{…},"values":[["<ns>","<line>"],…]}].
func (s Streams) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('[')
	for i, rs := range s {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"stream":`)
		b.WriteString(marshalLabels(rs.Labels))
		b.WriteString(`,"values":[`)
		for j, en := range rs.Entries {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`["`)
			b.WriteString(strconv.FormatInt(en.TS, 10))
			b.WriteString(`",`)
			b.WriteString(jsonString(en.Line))
			b.WriteByte(']')
		}
		b.WriteString("]}")
	}
	b.WriteByte(']')
	return []byte(b.String()), nil
}

// MarshalJSON writes [{"metric":{…},"values":[[<s>,"<v>"],…]}].
func (m Matrix) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range m {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"metric":`)
		b.WriteString(marshalLabels(s.Metric))
		b.WriteString(`,"values":[`)
		for j, p := range s.Points {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('[')
			b.WriteString(FormatTime(p.T))
			b.WriteString(`,"`)
			b.WriteString(FormatValue(p.V))
			b.WriteString(`"]`)
		}
		b.WriteString("]}")
	}
	b.WriteByte(']')
	return []byte(b.String()), nil
}

// MarshalJSON writes [{"metric":{…},"value":[<s>,"<v>"]}].
func (v Vector) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"metric":`)
		b.WriteString(marshalLabels(s.Metric))
		b.WriteString(`,"value":[`)
		b.WriteString(FormatTime(s.T))
		b.WriteString(`,"`)
		b.WriteString(FormatValue(s.V))
		b.WriteString(`"]}`)
	}
	b.WriteByte(']')
	return []byte(b.String()), nil
}

func marshalLabels(ls Labels) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, l := range ls {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jsonString(l.Name))
		b.WriteByte(':')
		b.WriteString(jsonString(l.Value))
	}
	b.WriteByte('}')
	return b.String()
}

// jsonString quotes s as a JSON string without HTML escaping.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
