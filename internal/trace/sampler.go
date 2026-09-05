package trace

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
)

// AttrScrape marks a request span as a Prometheus scrape of /metrics; the
// sampler never records those (a scraper polling every 15 s would otherwise
// fill a quarter of the 24 h span budget with identical traces).
const AttrScrape = attribute.Key("divy.scrape")

// Sampling decisions counted in divy_otel_spans_total{decision}.
const (
	DecisionSampled = "sampled"
	DecisionDropped = "dropped"
	DecisionScrape  = "scrape"
)

// rateSampler records every root span until the bucket is empty, then
// drops. Children follow their parent (ParentBased), so the bucket counts
// traces per second, not spans.
type rateSampler struct {
	lim     *rate.Limiter
	desc    string
	metrics Metrics
}

func newRateSampler(rps float64, burst int, m Metrics) *rateSampler {
	return &rateSampler{lim: rate.NewLimiter(rate.Limit(rps), burst), desc: fmt.Sprintf("divy-ratelimit{%g/s,burst=%d}", rps, burst), metrics: m}
}

func (s *rateSampler) count(decision string) {
	if s.metrics != nil {
		s.metrics.SpanDecision(decision)
	}
}

// ShouldSample implements sdktrace.Sampler.
func (s *rateSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	psc := oteltrace.SpanContextFromContext(p.ParentContext)
	for _, kv := range p.Attributes {
		if kv.Key == AttrScrape && kv.Value.AsBool() {
			s.count(DecisionScrape)
			return sdktrace.SamplingResult{Decision: sdktrace.Drop, Tracestate: psc.TraceState()}
		}
	}
	if s.lim.Allow() {
		s.count(DecisionSampled)
		return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample, Tracestate: psc.TraceState()}
	}
	s.count(DecisionDropped)
	return sdktrace.SamplingResult{Decision: sdktrace.Drop, Tracestate: psc.TraceState()}
}

// Description implements sdktrace.Sampler.
func (s *rateSampler) Description() string { return s.desc }
