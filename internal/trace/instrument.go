package trace

import (
	"context"
	"errors"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"divy.dev/internal/collector"
)

// Attributes of collector and outbound spans (LogQL §L.5.5).
const (
	AttrCollector      = attribute.Key("divy.collector")
	AttrItems          = attribute.Key("divy.items")
	AttrOK             = attribute.Key("divy.ok")
	AttrResult         = attribute.Key("divy.result")
	AttrNote           = attribute.Key("divy.note")
	AttrServerAddress  = attribute.Key("server.address")
	AttrRequestMethod  = attribute.Key("http.request.method")
	AttrResponseStatus = attribute.Key("http.response.status_code")
)

// WrapCollector returns c with one root span per run ("collector.<name>",
// attributes divy.collector, divy.items, divy.ok, divy.result); a disabled
// collector is passed through untouched (its skipped runs leave no span).
// The span is a new root linked to the caller's span (never a child of the
// /api/collect request: vercel-adaptation "never trace /api/collect
// internals beyond one span per collector").
func (p *Provider) WrapCollector(c collector.Collector) collector.Collector {
	return &tracedCollector{Collector: c, p: p}
}

type tracedCollector struct {
	collector.Collector
	p *Provider
}

// Disabled forwards the inner collector's Disabled().
func (t *tracedCollector) Disabled() bool {
	if d, ok := t.Collector.(interface{ Disabled() bool }); ok {
		return d.Disabled()
	}
	return false
}

// Unwrap returns the inner collector.
func (t *tracedCollector) Unwrap() collector.Collector { return t.Collector }

// Run implements collector.Collector.
func (t *tracedCollector) Run(ctx context.Context) (collector.Result, error) {
	if t.Disabled() {
		return t.Collector.Run(ctx)
	}
	name := t.Name()
	opts := []oteltrace.SpanStartOption{
		oteltrace.WithNewRoot(),
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
		oteltrace.WithAttributes(AttrCollector.String(name)),
	}
	if psc := oteltrace.SpanContextFromContext(ctx); psc.IsValid() {
		opts = append(opts, oteltrace.WithLinks(oteltrace.Link{SpanContext: psc}))
	}
	ctx, span := t.p.tracer.Start(ctx, "collector."+name, opts...)
	defer span.End()
	res, err := t.Collector.Run(ctx)
	result := collector.OutcomeOK
	switch {
	case errors.Is(err, collector.ErrDisabled):
		result = collector.OutcomeSkipped
	case err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)):
		result = collector.OutcomeTimeout
	case err != nil:
		result = collector.OutcomeError
	}
	span.SetAttributes(AttrItems.Int(res.Items), AttrOK.Bool(err == nil), AttrResult.String(result))
	if res.Note != "" {
		span.SetAttributes(AttrNote.String(truncate(res.Note, maxAttrBytes)))
	}
	if err != nil && result != collector.OutcomeSkipped {
		span.RecordError(err)
		span.SetStatus(codes.Error, truncate(err.Error(), maxAttrBytes))
	}
	return res, err
}

// Transport wraps base (nil = http.DefaultTransport) with a client span per
// request: "outbound <METHOD> <host>" with server.address,
// http.request.method and http.response.status_code — never url.full. No
// traceparent is sent upstream (the collectors talk to public APIs).
func (p *Provider) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base, p: p}
}

type transport struct {
	base http.RoundTripper
	p    *Provider
}

// RoundTrip implements http.RoundTripper.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host
	if host == "" {
		host = req.Host
	}
	ctx, span := t.p.tracer.Start(req.Context(), "outbound "+req.Method+" "+host,
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(AttrServerAddress.String(host), AttrRequestMethod.String(req.Method)))
	defer span.End()
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, truncate(err.Error(), maxAttrBytes))
		return nil, err
	}
	span.SetAttributes(AttrResponseStatus.Int(resp.StatusCode))
	if resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
	}
	return resp, nil
}
