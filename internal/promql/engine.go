package promql

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// Engine defaults (QUERY_* env vars override them at the server).
const (
	DefaultLookback       = 26 * time.Hour // stored counters are one sample per UTC day (review finding protocol-02)
	DefaultTimeout        = 30 * time.Second
	DefaultMaxSamples     = 2_000_000
	DefaultMaxConcurrency = 20
	// MaxPoints is the Prometheus cap on (end-start)/step for a range query.
	MaxPoints = 11000
)

// Storage serves stored series to the engine.
type Storage interface {
	// Select returns every series accepted by all matchers with the samples
	// startMs < t <= endMs in ascending order. Series without samples in
	// the window may be omitted.
	Select(ctx context.Context, matchers []*Matcher, startMs, endMs int64) ([]SeriesData, error)
}

// SeriesData is one stored series; Metric includes __name__.
type SeriesData struct {
	Metric Labels
	Points []Point
}

// LiveSeries is a series that is a function of the evaluation time and is
// never stored (divy_uptime_seconds, divy_build_info, …).
type LiveSeries interface {
	Metric() string
	// Labels are the series labels without __name__.
	Labels() Labels
	// Value returns the value at t; ok=false means the series is absent at t.
	Value(t time.Time) (float64, bool)
}

// LiveProvider lists the live series the engine consults before storage.
type LiveProvider interface {
	LiveSeries() []LiveSeries
}

// Engine evaluates parsed expressions against a Storage and a LiveProvider.
type Engine struct {
	Storage Storage
	Live    LiveProvider // optional
	// Lookback is the default instant-selector lookback (QUERY_LOOKBACK_DELTA).
	Lookback time.Duration
	// Timeout caps the wall time of one query (QUERY_TIMEOUT).
	Timeout time.Duration
	// MaxSamples caps the samples loaded by one query (QUERY_MAX_SAMPLES).
	MaxSamples int
	// MaxConcurrency caps concurrent evaluations (QUERY_MAX_CONCURRENCY).
	MaxConcurrency int

	semOnce sync.Once
	sem     chan struct{}
}

// Opts are per-query overrides.
type Opts struct {
	// Lookback overrides the engine lookback (the API's lookback_delta).
	Lookback time.Duration
	// Timeout narrows the engine timeout (the API's timeout parameter).
	Timeout time.Duration
}

// Errors of the execution phase.
var (
	// ErrTimeout maps to HTTP 503 errorType "timeout".
	ErrTimeout = errors.New("query timed out in query execution")
	// ErrCanceled is returned when the caller's context was canceled.
	ErrCanceled = errors.New("query was canceled")
	// ErrTooManySamples maps to 422 "execution".
	ErrTooManySamples = &ExecError{"query processing would load too many samples into memory in query execution"}
)

// ExecError is an evaluation error (HTTP 422, errorType "execution").
type ExecError struct{ Msg string }

func (e *ExecError) Error() string { return e.Msg }

// RangeTypeError is returned by Range for expressions that are not scalar or
// instant vector (HTTP 400, wrapped as `invalid parameter "query"`).
type RangeTypeError struct{ Type ValueType }

func (e *RangeTypeError) Error() string {
	return fmt.Sprintf("invalid expression type %q for range query, must be Scalar or instant Vector", DocumentedType(e.Type))
}

func (e *Engine) lookback(o Opts) time.Duration {
	if o.Lookback > 0 {
		return o.Lookback
	}
	if e.Lookback > 0 {
		return e.Lookback
	}
	return DefaultLookback
}

func (e *Engine) timeout(o Opts) time.Duration {
	t := e.Timeout
	if t <= 0 {
		t = DefaultTimeout
	}
	if o.Timeout > 0 && o.Timeout < t {
		t = o.Timeout
	}
	return t
}

func (e *Engine) maxSamples() int {
	if e.MaxSamples > 0 {
		return e.MaxSamples
	}
	return DefaultMaxSamples
}

func (e *Engine) acquire(ctx context.Context) error {
	e.semOnce.Do(func() {
		n := e.MaxConcurrency
		if n <= 0 {
			n = DefaultMaxConcurrency
		}
		e.sem = make(chan struct{}, n)
	})
	select {
	case e.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) release() { <-e.sem }

// Instant evaluates expr at t.
func (e *Engine) Instant(ctx context.Context, expr Expr, t time.Time, opts Opts) (Value, error) {
	ms := t.UnixMilli()
	var out Value
	err := e.run(ctx, opts, func(ev *evaluator) error {
		if err := ev.prefetch(expr, ms, ms); err != nil {
			return err
		}
		v, err := ev.eval(expr, ms)
		if err != nil {
			return err
		}
		switch x := v.(type) {
		case Vector:
			sortVector(x)
		case Matrix:
			sortMatrix(x)
		}
		out = v
		return nil
	})
	return out, err
}

// Range evaluates expr at start, start+step, … ≤ end and returns a matrix.
func (e *Engine) Range(ctx context.Context, expr Expr, start, end time.Time, step time.Duration, opts Opts) (Matrix, error) {
	if t := expr.Type(); t != ValueTypeScalar && t != ValueTypeVector {
		return nil, &RangeTypeError{Type: t}
	}
	startMs, endMs, stepMs := start.UnixMilli(), end.UnixMilli(), step.Milliseconds()
	if stepMs <= 0 {
		return nil, &ExecError{"zero or negative query resolution step widths are not accepted. Try a positive integer"}
	}
	var out Matrix
	err := e.run(ctx, opts, func(ev *evaluator) error {
		if err := ev.prefetch(expr, startMs, endMs); err != nil {
			return err
		}
		byKey := map[string]int{}
		var series Matrix
		for ts := startMs; ts <= endMs; ts += stepMs {
			if err := ev.check(); err != nil {
				return err
			}
			v, err := ev.eval(expr, ts)
			if err != nil {
				return err
			}
			switch x := v.(type) {
			case Scalar:
				if len(series) == 0 {
					series = append(series, Series{Metric: Labels{}})
					byKey[""] = 0
				}
				series[0].Points = append(series[0].Points, Point{ts, x.V})
			case Vector:
				for _, s := range x {
					k := s.Metric.key()
					idx, ok := byKey[k]
					if !ok {
						idx = len(series)
						byKey[k] = idx
						series = append(series, Series{Metric: s.Metric})
					}
					series[idx].Points = append(series[idx].Points, Point{ts, s.F})
				}
			}
		}
		sortMatrix(series)
		if series == nil {
			series = Matrix{}
		}
		out = series
		return nil
	})
	return out, err
}

// run acquires the concurrency slot, applies the timeout and maps context errors.
func (e *Engine) run(ctx context.Context, opts Opts, f func(*evaluator) error) error {
	qctx, cancel := context.WithTimeout(ctx, e.timeout(opts))
	defer cancel()
	mapErr := func(err error) error {
		if err == nil {
			return nil
		}
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
			return ErrCanceled
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(qctx.Err(), context.DeadlineExceeded) {
			return ErrTimeout
		}
		if errors.Is(err, context.Canceled) {
			return ErrCanceled
		}
		return err
	}
	if err := e.acquire(qctx); err != nil {
		return mapErr(err)
	}
	defer e.release()
	ev := &evaluator{ctx: qctx, eng: e, lookbackMs: e.lookback(opts).Milliseconds(), sel: map[*VectorSelector][]SeriesData{}}
	return mapErr(f(ev))
}

type evaluator struct {
	ctx        context.Context
	eng        *Engine
	lookbackMs int64
	sel        map[*VectorSelector][]SeriesData
	samples    int
}

func (ev *evaluator) check() error { return ev.ctx.Err() }

// prefetch loads every selector's samples once for the whole [start, end] window.
func (ev *evaluator) prefetch(expr Expr, startMs, endMs int64) error {
	var walkErr error
	fetch := func(vs *VectorSelector, before int64) {
		if walkErr != nil {
			return
		}
		if _, ok := ev.sel[vs]; ok {
			return
		}
		if ev.eng.Storage == nil {
			ev.sel[vs] = nil
			return
		}
		data, err := ev.eng.Storage.Select(ev.ctx, vs.Matchers, startMs-before, endMs)
		if err != nil {
			walkErr = err
			return
		}
		for _, s := range data {
			ev.samples += len(s.Points)
		}
		if ev.samples > ev.eng.maxSamples() {
			walkErr = ErrTooManySamples
			return
		}
		sort.SliceStable(data, func(i, j int) bool { return data[i].Metric.key() < data[j].Metric.key() })
		ev.sel[vs] = data
	}
	Walk(expr, func(n Expr) bool {
		switch x := n.(type) {
		case *MatrixSelector:
			fetch(x.VS, x.Range.Milliseconds())
			return false
		case *VectorSelector:
			fetch(x, ev.lookbackMs)
		}
		return walkErr == nil
	})
	return walkErr
}

func (ev *evaluator) liveSeries() []LiveSeries {
	if ev.eng.Live == nil {
		return nil
	}
	return ev.eng.Live.LiveSeries()
}

// eval evaluates one node at ts.
func (ev *evaluator) eval(expr Expr, ts int64) (Value, error) {
	switch n := expr.(type) {
	case *NumberLiteral:
		return Scalar{ts, n.Val}, nil
	case *StringLiteral:
		return String{ts, n.Val}, nil
	case *ParenExpr:
		return ev.eval(n.Expr, ts)
	case *VectorSelector:
		return ev.evalVectorSelector(n, ts), nil
	case *MatrixSelector:
		return ev.evalMatrixSelector(n, ts), nil
	case *UnaryExpr:
		v, err := ev.eval(n.Expr, ts)
		if err != nil {
			return nil, err
		}
		if n.Op == tAdd {
			return v, nil
		}
		switch x := v.(type) {
		case Scalar:
			return Scalar{ts, -x.V}, nil
		case Vector:
			out := make(Vector, 0, len(x))
			for _, s := range x {
				out = append(out, Sample{Metric: s.Metric.Without(MetricName), T: ts, F: -s.F})
			}
			return out, nil
		}
		return v, nil
	case *BinaryExpr:
		return ev.evalBinary(n, ts)
	case *AggregateExpr:
		v, err := ev.eval(n.Expr, ts)
		if err != nil {
			return nil, err
		}
		return ev.aggregate(n, v.(Vector), ts), nil
	case *Call:
		return ev.evalCall(n, ts)
	}
	return nil, fmt.Errorf("unknown node type %T", expr)
}

func (ev *evaluator) evalVectorSelector(n *VectorSelector, ts int64) Vector {
	var out Vector
	for _, s := range ev.sel[n] {
		pts := s.Points
		idx := sort.Search(len(pts), func(i int) bool { return pts[i].T > ts }) - 1
		if idx < 0 || pts[idx].T <= ts-ev.lookbackMs {
			continue
		}
		out = append(out, Sample{Metric: s.Metric, T: ts, F: pts[idx].F})
	}
	for _, ls := range ev.liveSeries() {
		lbls := ls.Labels().WithName(ls.Metric())
		if !MatchLabels(n.Matchers, lbls) {
			continue
		}
		if v, ok := ls.Value(time.UnixMilli(ts).UTC()); ok {
			out = append(out, Sample{Metric: lbls, T: ts, F: v})
		}
	}
	sortVector(out)
	return out
}

func (ev *evaluator) evalMatrixSelector(n *MatrixSelector, ts int64) Matrix {
	rangeMs := n.Range.Milliseconds()
	var out Matrix
	for _, s := range ev.sel[n.VS] {
		pts := s.Points
		lo := sort.Search(len(pts), func(i int) bool { return pts[i].T > ts-rangeMs })
		hi := sort.Search(len(pts), func(i int) bool { return pts[i].T > ts })
		if lo >= hi {
			continue
		}
		out = append(out, Series{Metric: s.Metric, Points: pts[lo:hi]})
	}
	for _, ls := range ev.liveSeries() {
		lbls := ls.Labels().WithName(ls.Metric())
		if !MatchLabels(n.VS.Matchers, lbls) {
			continue
		}
		if v, ok := ls.Value(time.UnixMilli(ts).UTC()); ok {
			out = append(out, Series{Metric: lbls, Points: []Point{{ts, v}}})
		}
	}
	sortMatrix(out)
	return out
}

// ---- binary operators ----

func (ev *evaluator) evalBinary(n *BinaryExpr, ts int64) (Value, error) {
	lv, err := ev.eval(n.LHS, ts)
	if err != nil {
		return nil, err
	}
	rv, err := ev.eval(n.RHS, ts)
	if err != nil {
		return nil, err
	}
	switch l := lv.(type) {
	case Scalar:
		switch r := rv.(type) {
		case Scalar:
			val, keep := elemBinop(n.Op, l.V, r.V)
			if n.ReturnBool {
				val = boolFloat(keep)
			}
			return Scalar{ts, val}, nil
		case Vector:
			return vectorScalarBinop(n.Op, r, l.V, true, n.ReturnBool, ts), nil
		}
	case Vector:
		switch r := rv.(type) {
		case Scalar:
			return vectorScalarBinop(n.Op, l, r.V, false, n.ReturnBool, ts), nil
		case Vector:
			return vectorBinop(n.Op, l, r, n.ReturnBool, ts)
		}
	}
	return nil, &ExecError{"binary expression must contain only scalar and instant vector types"}
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// changesMetricSchema reports whether the operator drops __name__ (arithmetic).
func changesMetricSchema(op tokenType) bool {
	switch op {
	case tAdd, tSub, tDiv, tMul, tPow, tMod, tAtan2:
		return true
	}
	return false
}

// elemBinop is Prometheus' vectorElemBinop for floats: the value and whether
// a comparison keeps the sample.
func elemBinop(op tokenType, lhs, rhs float64) (float64, bool) {
	switch op {
	case tAdd:
		return lhs + rhs, true
	case tSub:
		return lhs - rhs, true
	case tMul:
		return lhs * rhs, true
	case tDiv:
		return lhs / rhs, true
	case tPow:
		return math.Pow(lhs, rhs), true
	case tMod:
		return math.Mod(lhs, rhs), true
	case tEqlc:
		return lhs, lhs == rhs
	case tNeq:
		return lhs, lhs != rhs
	case tGtr:
		return lhs, lhs > rhs
	case tLss:
		return lhs, lhs < rhs
	case tGte:
		return lhs, lhs >= rhs
	case tLte:
		return lhs, lhs <= rhs
	}
	return math.NaN(), false
}

func vectorScalarBinop(op tokenType, vec Vector, scalar float64, swap, returnBool bool, ts int64) Vector {
	out := make(Vector, 0, len(vec))
	for _, s := range vec {
		lf, rf := s.F, scalar
		if swap {
			lf, rf = rf, lf
		}
		val, keep := elemBinop(op, lf, rf)
		if op.isComparison() && swap {
			val = rf
		}
		if returnBool {
			val = boolFloat(keep)
			keep = true
		}
		if !keep {
			continue
		}
		metric := s.Metric
		if changesMetricSchema(op) || returnBool {
			metric = metric.Without(MetricName)
		}
		out = append(out, Sample{Metric: metric, T: ts, F: val})
	}
	return out
}

func vectorBinop(op tokenType, lhs, rhs Vector, returnBool bool, ts int64) (Vector, error) {
	if len(lhs) == 0 || len(rhs) == 0 {
		return Vector{}, nil
	}
	rightSigs := map[string]Sample{}
	for _, rs := range rhs {
		sig := rs.Metric.Without(MetricName)
		key := sig.key()
		if dup, ok := rightSigs[key]; ok {
			return nil, &ExecError{fmt.Sprintf("found duplicate series for the match group %s on the right hand-side of the operation: [%s, %s];many-to-many matching not allowed: matching labels must be unique on one side", sig.String(), rs.Metric.String(), dup.Metric.String())}
		}
		rightSigs[key] = rs
	}
	matched := map[string]bool{}
	out := make(Vector, 0, len(lhs))
	for _, ls := range lhs {
		key := ls.Metric.Without(MetricName).key()
		rs, ok := rightSigs[key]
		if !ok {
			continue
		}
		val, keep := elemBinop(op, ls.F, rs.F)
		if returnBool {
			val = boolFloat(keep)
		}
		if matched[key] {
			return nil, &ExecError{"multiple matches for labels: many-to-one matching must be explicit (group_left/group_right)"}
		}
		matched[key] = true
		if !keep && !returnBool {
			continue
		}
		metric := ls.Metric
		if returnBool || changesMetricSchema(op) {
			metric = metric.Without(MetricName)
		}
		out = append(out, Sample{Metric: metric, T: ts, F: val})
	}
	return out, nil
}

// ---- aggregations ----

type group struct {
	labels     Labels
	floatValue float64
	floatMean  float64
	kahanC     float64
	count      float64
	incMean    bool
}

func (ev *evaluator) aggregate(n *AggregateExpr, in Vector, ts int64) Vector {
	groups := map[string]*group{}
	var order []*group
	for _, s := range in {
		var lbls Labels
		if n.Without {
			lbls = s.Metric.Without(append([]string{MetricName}, n.Grouping...)...)
		} else {
			lbls = s.Metric.Keep(n.Grouping...)
		}
		key := lbls.key()
		g, ok := groups[key]
		if !ok {
			g = &group{labels: lbls, floatValue: s.F, floatMean: s.F, count: 1}
			groups[key] = g
			order = append(order, g)
			continue
		}
		f := s.F
		switch n.Op {
		case tSum:
			g.floatValue, g.kahanC = kahanInc(f, g.floatValue, g.kahanC)
		case tAvg:
			g.count++
			if !g.incMean {
				newV, newC := kahanInc(f, g.floatValue, g.kahanC)
				if !math.IsInf(newV, 0) {
					g.floatValue, g.kahanC = newV, newC
					break
				}
				g.incMean = true
				g.floatMean = g.floatValue / (g.count - 1)
				g.kahanC /= g.count - 1
			}
			q := (g.count - 1) / g.count
			g.floatMean, g.kahanC = kahanInc(f/g.count, q*g.floatMean, q*g.kahanC)
		case tMax:
			if g.floatValue < f || math.IsNaN(g.floatValue) {
				g.floatValue = f
			}
		case tMin:
			if g.floatValue > f || math.IsNaN(g.floatValue) {
				g.floatValue = f
			}
		case tCount:
			g.count++
		}
	}
	out := make(Vector, 0, len(order))
	for _, g := range order {
		v := g.floatValue
		switch n.Op {
		case tAvg:
			if g.incMean {
				v = g.floatMean + g.kahanC
			} else {
				v = g.floatValue/g.count + g.kahanC/g.count
			}
		case tCount:
			v = g.count
		case tSum:
			v += g.kahanC
		}
		out = append(out, Sample{Metric: g.labels, T: ts, F: v})
	}
	sortVector(out)
	return out
}

// kahanInc is Prometheus' kahansum.Inc (Neumaier variant); the explicit
// float64 conversions keep the compiler from fusing the operations.
func kahanInc(inc, sum, c float64) (float64, float64) {
	inc = float64(inc)
	sum = float64(sum)
	c = float64(c)
	t := sum + inc
	switch {
	case math.IsInf(t, 0):
		c = 0
	case math.Abs(sum) >= math.Abs(inc):
		c += (sum - t) + inc
	default:
		c += (inc - t) + sum
	}
	t = float64(t)
	c = float64(c)
	return t, c
}
