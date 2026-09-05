package trace

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"

	"divy.dev/internal/store"
)

// Size guards of stored attribute values (LogQL §L.5.3).
const (
	maxAttrBytes  = 1024
	maxQueryBytes = 512
)

// scrubbed attributes are never stored, whatever set them (contract: no IPs,
// no user agents, no full URLs, no headers).
var scrubbed = map[string]struct{}{
	"client.address":       {},
	"http.client_ip":       {},
	"net.peer.ip":          {},
	"net.sock.peer.addr":   {},
	"network.peer.address": {},
	"network.peer.port":    {},
	"user_agent.original":  {},
	"http.user_agent":      {},
	"url.full":             {},
	"http.url":             {},
}

func isScrubbed(key string) bool {
	if _, ok := scrubbed[key]; ok {
		return true
	}
	return strings.HasPrefix(key, "http.request.header.") || strings.HasPrefix(key, "http.response.header.")
}

// exporter writes ended spans to otel_spans, one transaction per batch.
type exporter struct {
	st      *store.Store
	service string
	metrics Metrics
}

// ExportSpans implements sdktrace.SpanExporter.
func (e *exporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	rows := make([]store.Span, 0, len(spans))
	for _, s := range spans {
		rows = append(rows, Row(s, e.service))
	}
	if e.st == nil {
		return errors.New("trace: no store configured")
	}
	if err := e.st.WriteSpans(ctx, rows); err != nil {
		if e.metrics != nil {
			e.metrics.ExportError()
		}
		return err
	}
	if e.metrics != nil {
		e.metrics.SpansExported(len(rows))
	}
	return nil
}

// Shutdown implements sdktrace.SpanExporter (the store owns the database).
func (e *exporter) Shutdown(context.Context) error { return nil }

// Row maps an SDK span to an otel_spans row (LogQL §L.4.3 / §L.5.3):
// attributes after scrubbing plus span.kind, otel.scope.name,
// otel.scope.version and otel.dropped_attributes_count; slices become the
// JSON text of the slice (the Jaeger array rule); strings are truncated to
// 1024 bytes. The service is the resource's service.name, or fallback.
func Row(s sdktrace.ReadOnlySpan, fallbackService string) store.Span {
	sc := s.SpanContext()
	row := store.Span{
		TraceID:       sc.TraceID().String(),
		SpanID:        sc.SpanID().String(),
		Name:          s.Name(),
		Service:       fallbackService,
		StartUnixNano: s.StartTime().UnixNano(),
		EndUnixNano:   s.EndTime().UnixNano(),
	}
	if p := s.Parent(); p.HasSpanID() {
		id := p.SpanID().String()
		row.ParentSpanID = &id
	}
	if res := s.Resource(); res != nil {
		for _, kv := range res.Attributes() {
			if kv.Key == semconv.ServiceNameKey {
				row.Service = kv.Value.AsString()
			}
		}
	}
	attrs := map[string]any{}
	for _, kv := range s.Attributes() {
		k := string(kv.Key)
		if isScrubbed(k) {
			continue
		}
		attrs[k] = encodeValue(kv.Value)
	}
	attrs["span.kind"] = s.SpanKind().String()
	if scope := s.InstrumentationScope(); scope.Name != "" {
		attrs["otel.scope.name"] = scope.Name
		if scope.Version != "" {
			attrs["otel.scope.version"] = scope.Version
		}
	}
	if n := s.DroppedAttributes(); n > 0 {
		attrs["otel.dropped_attributes_count"] = n
	}
	row.Attributes, _ = json.Marshal(attrs)

	type event struct {
		TimeUnixNano int64          `json:"time_unix_nano"`
		Name         string         `json:"name"`
		Attributes   map[string]any `json:"attributes"`
	}
	events := make([]event, 0, len(s.Events()))
	for _, ev := range s.Events() {
		ea := map[string]any{}
		for _, kv := range ev.Attributes {
			if isScrubbed(string(kv.Key)) {
				continue
			}
			ea[string(kv.Key)] = encodeValue(kv.Value)
		}
		events = append(events, event{TimeUnixNano: ev.Time.UnixNano(), Name: ev.Name, Attributes: ea})
	}
	row.Events, _ = json.Marshal(events)

	switch s.Status().Code {
	case codes.Ok:
		row.StatusCode = 1
	case codes.Error:
		row.StatusCode = 2
		if d := s.Status().Description; d != "" {
			d = truncate(d, maxAttrBytes)
			row.StatusMsg = &d
		}
	}
	return row
}

// encodeValue turns an attribute value into its JSON form: bool, integer,
// number, string; slices → the JSON text of the slice as a string.
func encodeValue(v attribute.Value) any {
	switch v.Type() {
	case attribute.BOOL:
		return v.AsBool()
	case attribute.INT64:
		return v.AsInt64()
	case attribute.FLOAT64:
		return v.AsFloat64()
	case attribute.STRING:
		return truncate(v.AsString(), maxAttrBytes)
	case attribute.BOOLSLICE, attribute.INT64SLICE, attribute.FLOAT64SLICE, attribute.STRINGSLICE:
		b, _ := json.Marshal(v.AsInterface())
		return truncate(string(b), maxAttrBytes)
	default:
		return truncate(v.Emit(), maxAttrBytes)
	}
}

// truncate cuts s to at most n bytes on a rune boundary and appends "…".
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
