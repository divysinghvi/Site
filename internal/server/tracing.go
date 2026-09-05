package server

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"divy.dev/internal/promql"
	"divy.dev/internal/trace"
)

// tracedQuerier wraps the store adapter of the PromQL engine with a
// "sqlite.select" client span per Select (LogQL §L.5.5): db.system,
// db.operation.name, db.statement (summarized: the matchers and the window,
// never raw SQL with values), divy.table, divy.rows, divy.samples.
type tracedQuerier struct {
	inner promql.Storage
	tp    *trace.Provider
}

func (q tracedQuerier) Select(ctx context.Context, matchers []*promql.Matcher, startMs, endMs int64) ([]promql.SeriesData, error) {
	ctx, span := q.tp.Start(ctx, "sqlite.select", oteltrace.SpanKindClient,
		attribute.String("db.system", "sqlite"),
		attribute.String("db.operation.name", "select"),
		attribute.String("divy.table", "samples"),
		attribute.String("db.statement", summarizeSelect(matchers, startMs, endMs)))
	defer span.End()
	out, err := q.inner.Select(ctx, matchers, startMs, endMs)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "select failed")
		return nil, err
	}
	samples := 0
	for _, sd := range out {
		samples += len(sd.Points)
	}
	span.SetAttributes(attribute.Int("divy.rows", len(out)), attribute.Int("divy.samples", samples))
	return out, nil
}

// summarizeSelect renders the query the way a slow-log would: the label
// matchers and the (start, end] window in ms, capped at 512 bytes.
func summarizeSelect(matchers []*promql.Matcher, startMs, endMs int64) string {
	parts := make([]string, 0, len(matchers))
	for _, m := range matchers {
		if m == nil {
			continue
		}
		parts = append(parts, m.String())
	}
	s := fmt.Sprintf("SELECT samples WHERE {%s} AND ts_ms IN (%d, %d]", strings.Join(parts, ", "), startMs, endMs)
	if len(s) > 512 {
		s = s[:512] + "…"
	}
	return s
}
