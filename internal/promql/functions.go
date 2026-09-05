package promql

import (
	"fmt"
	"math"
)

// evalCall evaluates a function call at ts.
func (ev *evaluator) evalCall(n *Call, ts int64) (Value, error) {
	args := make([]Value, len(n.Args))
	for i, a := range n.Args {
		v, err := ev.eval(a, ts)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	switch n.Func.Name {
	case "time":
		return Scalar{ts, float64(ts) / 1000}, nil
	case "vector":
		return Vector{{Metric: Labels{}, T: ts, F: args[0].(Scalar).V}}, nil
	case "scalar":
		v := args[0].(Vector)
		if len(v) != 1 {
			return Scalar{ts, math.NaN()}, nil
		}
		return Scalar{ts, v[0].F}, nil
	case "abs":
		return simpleFunc(args[0].(Vector), ts, math.Abs), nil
	case "ceil":
		return simpleFunc(args[0].(Vector), ts, math.Ceil), nil
	case "floor":
		return simpleFunc(args[0].(Vector), ts, math.Floor), nil
	case "round":
		toNearest := 1.0
		if len(args) >= 2 {
			toNearest = args[1].(Scalar).V
		}
		inv := 1.0 / toNearest
		return simpleFunc(args[0].(Vector), ts, func(f float64) float64 {
			return math.Floor(f*inv+0.5) / inv
		}), nil
	case "clamp_min":
		return clamp(args[0].(Vector), args[1].(Scalar).V, math.Inf(+1), ts), nil
	case "clamp_max":
		return clamp(args[0].(Vector), math.Inf(-1), args[1].(Scalar).V, ts), nil
	case "rate":
		return extrapolatedRate(args[0].(Matrix), n, ts, true, true), nil
	case "increase":
		return extrapolatedRate(args[0].(Matrix), n, ts, true, false), nil
	case "delta":
		return extrapolatedRate(args[0].(Matrix), n, ts, false, false), nil
	case "irate":
		return irate(args[0].(Matrix), ts), nil
	case "sum_over_time":
		return aggrOverTime(args[0].(Matrix), ts, func(pts []Point) float64 {
			var sum, c float64
			for _, p := range pts {
				sum, c = kahanInc(p.F, sum, c)
			}
			if math.IsInf(sum, 0) {
				return sum
			}
			return sum + c
		}), nil
	case "avg_over_time":
		return aggrOverTime(args[0].(Matrix), ts, func(pts []Point) float64 {
			sum, count := pts[0].F, 1.0
			var mean, kahanC float64
			incremental := false
			for i, p := range pts[1:] {
				count = float64(i + 2)
				if !incremental {
					newSum, newC := kahanInc(p.F, sum, kahanC)
					if !math.IsInf(newSum, 0) {
						sum, kahanC = newSum, newC
						continue
					}
					incremental = true
					mean = sum / (count - 1)
					kahanC /= count - 1
				}
				q := (count - 1) / count
				mean, kahanC = kahanInc(p.F/count, q*mean, q*kahanC)
			}
			if incremental {
				return mean + kahanC
			}
			return sum/count + kahanC/count
		}), nil
	case "min_over_time":
		return aggrOverTime(args[0].(Matrix), ts, func(pts []Point) float64 {
			v := pts[0].F
			for _, p := range pts {
				if p.F < v || math.IsNaN(v) {
					v = p.F
				}
			}
			return v
		}), nil
	case "max_over_time":
		return aggrOverTime(args[0].(Matrix), ts, func(pts []Point) float64 {
			v := pts[0].F
			for _, p := range pts {
				if p.F > v || math.IsNaN(v) {
					v = p.F
				}
			}
			return v
		}), nil
	case "count_over_time":
		return aggrOverTime(args[0].(Matrix), ts, func(pts []Point) float64 { return float64(len(pts)) }), nil
	case "last_over_time":
		m := args[0].(Matrix)
		out := make(Vector, 0, len(m))
		for _, s := range m {
			if len(s.Points) == 0 {
				continue
			}
			out = append(out, Sample{Metric: s.Metric, T: ts, F: s.Points[len(s.Points)-1].F})
		}
		return out, nil
	}
	return nil, fmt.Errorf("function %q has no implementation", n.Func.Name)
}

func simpleFunc(v Vector, ts int64, f func(float64) float64) Vector {
	out := make(Vector, 0, len(v))
	for _, s := range v {
		out = append(out, Sample{Metric: s.Metric.Without(MetricName), T: ts, F: f(s.F)})
	}
	return out
}

func clamp(v Vector, minVal, maxVal float64, ts int64) Vector {
	if maxVal < minVal {
		return Vector{}
	}
	out := make(Vector, 0, len(v))
	for _, s := range v {
		out = append(out, Sample{Metric: s.Metric.Without(MetricName), T: ts, F: math.Max(minVal, math.Min(maxVal, s.F))})
	}
	return out
}

func aggrOverTime(m Matrix, ts int64, f func([]Point) float64) Vector {
	out := make(Vector, 0, len(m))
	for _, s := range m {
		if len(s.Points) == 0 {
			continue
		}
		out = append(out, Sample{Metric: s.Metric.Without(MetricName), T: ts, F: f(s.Points)})
	}
	return out
}

// extrapolatedRate is Prometheus' extrapolatedRate for float samples without
// start timestamps: the float operations happen in the same order.
func extrapolatedRate(m Matrix, call *Call, ts int64, isCounter, isRate bool) Vector {
	ms := call.Args[0].(*MatrixSelector)
	rangeStart := ts - ms.Range.Milliseconds()
	rangeEnd := ts
	out := make(Vector, 0, len(m))
	for _, s := range m {
		pts := s.Points
		if len(pts) < 2 {
			continue
		}
		numSamplesMinusOne := len(pts) - 1
		firstT, lastT := pts[0].T, pts[numSamplesMinusOne].T
		resultFloat := pts[numSamplesMinusOne].F - pts[0].F
		if isCounter {
			for i, cur := range pts[1:] {
				prev := pts[i]
				if cur.F < prev.F {
					resultFloat += prev.F
				}
			}
		}
		durationToStart := float64(firstT-rangeStart) / 1000
		durationToEnd := float64(rangeEnd-lastT) / 1000
		sampledInterval := float64(lastT-firstT) / 1000
		averageDurationBetweenSamples := sampledInterval / float64(numSamplesMinusOne)
		extrapolationThreshold := averageDurationBetweenSamples * 1.1

		if durationToStart >= extrapolationThreshold {
			durationToStart = averageDurationBetweenSamples / 2
		}
		if isCounter {
			durationToZero := durationToStart
			if resultFloat > 0 && pts[0].F >= 0 {
				durationToZero = sampledInterval * (pts[0].F / resultFloat)
			}
			if durationToZero < durationToStart {
				durationToStart = durationToZero
			}
		}
		if durationToEnd >= extrapolationThreshold {
			durationToEnd = averageDurationBetweenSamples / 2
		}
		factor := 1.0
		if sampledInterval != 0 {
			factor = (sampledInterval + durationToStart + durationToEnd) / sampledInterval
		}
		if isRate {
			factor /= ms.Range.Seconds()
		}
		out = append(out, Sample{Metric: s.Metric.Without(MetricName), T: ts, F: resultFloat * factor})
	}
	return out
}

// irate is Prometheus' instantValue with isRate=true.
func irate(m Matrix, ts int64) Vector {
	out := make(Vector, 0, len(m))
	for _, s := range m {
		pts := s.Points
		if len(pts) < 2 {
			continue
		}
		a, b := pts[len(pts)-2], pts[len(pts)-1]
		sampledInterval := b.T - a.T
		if sampledInterval == 0 {
			continue
		}
		v := b.F
		if !(b.F < a.F) {
			v = b.F - a.F
		}
		v /= float64(sampledInterval) / 1000
		out = append(out, Sample{Metric: s.Metric.Without(MetricName), T: ts, F: v})
	}
	return out
}
