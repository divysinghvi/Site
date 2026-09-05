package trace

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Response headers carrying the request's own trace (contract §K.3.1).
const (
	HeaderTraceID = "X-Divy-Trace-Id"
	HeaderSampled = "X-Divy-Trace-Sampled"
)

// Span attributes of the request span (the task's names; no IPs, no user agent).
const (
	AttrHTTPMethod  = attribute.Key("http.method")
	AttrHTTPRoute   = attribute.Key("http.route")
	AttrHTTPStatus  = attribute.Key("http.status_code")
	AttrURLPath     = attribute.Key("url.path")
	AttrURLQuery    = attribute.Key("url.query")
	AttrBodySize    = attribute.Key("http.response.body.size")
	AttrCache       = attribute.Key("divy.cache")
	AttrRateLimited = attribute.Key("divy.ratelimited")
)

// Middleware starts one server span per request, named "HTTP <METHOD> <chi
// route pattern>" once the route is known, and sets X-Divy-Trace-Id and
// X-Divy-Trace-Sampled before the handler runs so that every response —
// 304, 404, 405, 429, 500, static files, /metrics — carries them. An
// inbound W3C traceparent is joined (the response header is then the
// caller's trace id); sampling stays ours. The response is flushed before
// the root span is exported so the client never waits on the write.
func (p *Provider) Middleware(next http.Handler) http.Handler {
	prop := propagation.TraceContext{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		attrs := []attribute.KeyValue{
			AttrHTTPMethod.String(r.Method),
			AttrURLPath.String(truncate(r.URL.Path, maxAttrBytes)),
		}
		if r.URL.Path == "/metrics" && strings.Contains(r.UserAgent(), "Prometheus") {
			attrs = append(attrs, AttrScrape.Bool(true))
		}
		ctx, span := p.tracer.Start(ctx, "HTTP "+r.Method, oteltrace.WithSpanKind(oteltrace.SpanKindServer), oteltrace.WithAttributes(attrs...))
		sc := span.SpanContext()
		if sc.HasTraceID() {
			h := w.Header()
			h.Set(HeaderTraceID, sc.TraceID().String())
			if sc.IsSampled() {
				h.Set(HeaderSampled, "1")
			} else {
				h.Set(HeaderSampled, "0")
			}
		}
		ww, ok := w.(middleware.WrapResponseWriter)
		if !ok {
			ww = middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		}
		r = r.WithContext(ctx)
		defer func() {
			if rec := recover(); rec != nil {
				span.SetStatus(codes.Error, "panic")
				span.RecordError(fmt.Errorf("panic: %v", rec))
				span.SetAttributes(AttrHTTPStatus.Int(http.StatusInternalServerError))
				p.finish(span, r)
				panic(rec)
			}
		}()
		next.ServeHTTP(ww, r)
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		span.SetAttributes(AttrHTTPStatus.Int(status), AttrBodySize.Int(ww.BytesWritten()))
		if status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(status))
		}
		p.finish(span, r)
		// The body is complete: hand it to the client before the export.
		if f, ok := ww.(http.Flusher); ok {
			f.Flush()
		}
		span.End()
	})
}

// finish names the span after the matched route and records the query of
// API routes (never of static paths).
func (p *Provider) finish(span oteltrace.Span, r *http.Request) {
	route := ""
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		route = rctx.RoutePattern()
	}
	if route != "" {
		span.SetName("HTTP " + r.Method + " " + route)
		span.SetAttributes(AttrHTTPRoute.String(route))
	}
	if q := r.URL.RawQuery; q != "" && (strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/loki/")) {
		span.SetAttributes(AttrURLQuery.String(truncate(q, maxQueryBytes)))
	}
}
