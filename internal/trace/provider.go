// Package trace is the OpenTelemetry self-tracing of the divy binary: a
// TracerProvider whose spans are written to the otel_spans table so that the
// X-Divy-Trace-Id of any response resolves at /api/traces/{id}.
//
// Root spans (one per HTTP request, one per collector run) are exported
// synchronously when they end; child spans (store reads, outbound HTTP
// calls) are buffered per trace and written in the same transaction as
// their root, so a trace is either absent or complete. Sampling is
// "always on" behind a token bucket (OTEL_SAMPLE_RPS / OTEL_SAMPLE_BURST
// root spans per second) and never records Prometheus scrapes of /metrics.
package trace

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
	oteltrace "go.opentelemetry.io/otel/trace"

	"divy.dev/internal/store"
)

// ScopeName is the instrumentation scope of every span this package starts.
const ScopeName = "divy.dev/internal/trace"

// Default sampler settings (OTEL_SAMPLE_RPS, OTEL_SAMPLE_BURST).
const (
	DefaultSampleRPS   = 100
	DefaultSampleBurst = 200
)

// Metrics receives the sampler and exporter counters
// (divy_otel_spans_total{decision}, divy_otel_exported_spans_total,
// divy_otel_export_errors_total); internal/metrics.Registry implements it.
type Metrics interface {
	SpanDecision(decision string)
	SpansExported(n int)
	ExportError()
}

// Config configures the provider.
type Config struct {
	// ServiceName is the resource service.name (OTEL_SERVICE_NAME, default divy-api).
	ServiceName string
	// Version is the resource service.version (the build's version.Version).
	Version string
	// SampleRPS / SampleBurst bound the root spans recorded per second; zero = defaults.
	SampleRPS   float64
	SampleBurst int
	// Store receives the spans; nil = spans are sampled and counted but never written.
	Store *store.Store
	// Metrics is optional.
	Metrics Metrics
	Logger  *slog.Logger
	// Now overrides the clock of the orphan sweeper (tests).
	Now func() time.Time
	// SweepInterval / OrphanAfter tune the buffered-children sweeper (tests); zero = 5s / 30s.
	SweepInterval time.Duration
	OrphanAfter   time.Duration
}

// Provider is the configured TracerProvider plus the helpers that
// instrument the request path, the store and the collectors.
type Provider struct {
	tp      *sdktrace.TracerProvider
	proc    *processor
	sampler *rateSampler
	tracer  oteltrace.Tracer
	service string
	log     *slog.Logger
}

// New builds the provider. The resource is resource.Default() merged with
// service.name/service.version at the semconv schema the SDK itself uses
// (review finding protocol-08: a schema URL conflict is an error, not a
// silent downgrade).
func New(cfg Config) (*Provider, error) {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "divy-api"
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.SampleRPS <= 0 {
		cfg.SampleRPS = DefaultSampleRPS
	}
	if cfg.SampleBurst <= 0 {
		cfg.SampleBurst = DefaultSampleBurst
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.Version)))
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w (the semconv version differs from the SDK's; align the pins)", err)
	}
	sampler := newRateSampler(cfg.SampleRPS, cfg.SampleBurst, cfg.Metrics)
	exp := &exporter{st: cfg.Store, service: cfg.ServiceName, metrics: cfg.Metrics}
	proc := newProcessor(exp, cfg.Logger, cfg.Metrics, cfg.Now, cfg.SweepInterval, cfg.OrphanAfter)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		// Inbound W3C traceparent headers are joined (the trace id is the
		// caller's) but the sampling decision is always ours: the bucket
		// applies to remote parents exactly as to fresh roots, so a flood of
		// pre-sampled traceparents cannot bypass the cap.
		sdktrace.WithSampler(sdktrace.ParentBased(sampler,
			sdktrace.WithRemoteParentSampled(sampler),
			sdktrace.WithRemoteParentNotSampled(sampler))),
		sdktrace.WithSpanProcessor(proc),
	)
	p := &Provider{tp: tp, proc: proc, sampler: sampler, service: cfg.ServiceName, log: cfg.Logger}
	p.tracer = tp.Tracer(ScopeName)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return p, nil
}

// Tracer returns the tracer of this package's scope.
func (p *Provider) Tracer() oteltrace.Tracer { return p.tracer }

// TracerProvider exposes the SDK provider (tests).
func (p *Provider) TracerProvider() *sdktrace.TracerProvider { return p.tp }

// ServiceName is the resource service.name.
func (p *Provider) ServiceName() string { return p.service }

// SamplerDescription is the sampler's Description() (documentation, readyz).
func (p *Provider) SamplerDescription() string { return p.sampler.Description() }

// ForceFlush writes every buffered child span (the request's own root span
// is written at its end already).
func (p *Provider) ForceFlush(ctx context.Context) error { return p.tp.ForceFlush(ctx) }

// Shutdown flushes and stops the provider; call it before closing the store.
func (p *Provider) Shutdown(ctx context.Context) error { return p.tp.Shutdown(ctx) }

// Start begins a child span of ctx (an internal span unless kind says
// otherwise); the caller must End it.
func (p *Provider) Start(ctx context.Context, name string, kind oteltrace.SpanKind, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return p.tracer.Start(ctx, name, oteltrace.WithSpanKind(kind), oteltrace.WithAttributes(attrs...))
}

// TraceIDFromContext returns the 32-hex trace id of the span in ctx, or "".
func TraceIDFromContext(ctx context.Context) string {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.HasTraceID() {
		return ""
	}
	return sc.TraceID().String()
}

// SetAttributes adds attributes to the span in ctx (a no-op without one).
func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	oteltrace.SpanFromContext(ctx).SetAttributes(attrs...)
}

// HTTPClient wraps a client's transport with client spans
// ("outbound <METHOD> <host>"); see Transport.
func (p *Provider) HTTPClient(c *http.Client) *http.Client {
	if c == nil {
		c = &http.Client{}
	}
	out := *c
	out.Transport = p.Transport(c.Transport)
	return &out
}
