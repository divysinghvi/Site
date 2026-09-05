package content

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"divy.dev/internal/model"
)

// JaegerTrace renders the career trace in Jaeger JSON shape (Content §C.3.5)
// as of now: open spans without a planned end run until now.
func (c *Content) JaegerTrace(now time.Time) model.JaegerTrace {
	now = now.UTC()
	tr := model.JaegerTrace{TraceID: TraceID, Spans: []model.JaegerSpan{}, Processes: map[string]model.JaegerProcess{}}
	for _, n := range c.nodes {
		tr.Spans = append(tr.Spans, c.jaegerSpan(n, now))
		pid := "p-" + n.Span.Service
		if _, ok := tr.Processes[pid]; !ok {
			svc := c.services[n.Span.Service]
			tags := []model.JaegerKeyValue{kv("divy.title", svc.Title), kv("divy.color", svc.Color)}
			if svc.CountsAsExperience {
				tags = append(tags, kv("divy.counts_as_experience", true))
			}
			tr.Processes[pid] = model.JaegerProcess{ServiceName: n.Span.Service, Tags: tags}
		}
	}
	return tr
}

// JaegerResponse wraps one trace in the /api/traces/{id} envelope.
func JaegerResponse(traces ...model.JaegerTrace) model.JaegerTraceResponse {
	if traces == nil {
		traces = []model.JaegerTrace{}
	}
	return model.JaegerTraceResponse{Data: traces, Total: 0, Limit: 0, Offset: 0, Errors: nil}
}

func (c *Content) jaegerSpan(n *Node, now time.Time) model.JaegerSpan {
	sp := n.Span
	start := n.Start
	end := n.AxisEnd(now)
	if n.Span.Open && (n.End.IsZero() || !n.End.After(now)) {
		end = now
	}
	s := model.JaegerSpan{
		TraceID:       TraceID,
		SpanID:        SpanHexID(sp.ID),
		OperationName: sp.ID,
		References:    []model.JaegerReference{},
		StartTime:     start.UnixMicro(),
		Duration:      end.Sub(start).Microseconds(),
		Logs:          []model.JaegerLog{},
		ProcessID:     "p-" + sp.Service,
	}
	if n.Parent != nil {
		s.References = append(s.References, model.JaegerReference{RefType: "CHILD_OF", TraceID: TraceID, SpanID: SpanHexID(n.Parent.Span.ID)})
	}
	tags := []model.JaegerKeyValue{kv("divy.id", sp.ID)}
	if sp.Title != "" {
		tags = append(tags, kv("divy.title", sp.Title))
	}
	tags = append(tags, kv("divy.start", string(sp.Start)), kv("divy.start_precision", string(n.StartPrecision)))
	if !sp.Open || sp.End != "" {
		tags = append(tags, kv("divy.end", string(sp.End)))
	}
	tags = append(tags, kv("divy.end_precision", string(n.EndPrecision)))
	if sp.Open {
		tags = append(tags, kv("divy.open", true))
		if sp.End != "" {
			tags = append(tags, kv("divy.end_planned", string(sp.End)))
		}
	}
	switch sp.Status {
	case "error":
		tags = append(tags, kv("otel.status_code", "ERROR"), kv("error", true))
	case "ok":
		tags = append(tags, kv("otel.status_code", "OK"))
	}
	keys := make([]string, 0, len(sp.Tags))
	for k := range sp.Tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		tags = append(tags, kv(k, sp.Tags[k].Value))
	}
	if len(sp.Links) > 0 {
		b, _ := json.Marshal(sp.Links)
		tags = append(tags, kv("divy.links", string(b)))
		var pms []string
		for _, lk := range sp.Links {
			if lk.Kind == "postmortem" {
				pms = append(pms, lk.Ref)
			}
		}
		if len(pms) > 0 {
			sort.Strings(pms)
			tags = append(tags, kv("divy.postmortems", strings.Join(pms, ",")))
		}
	}
	if len(sp.Todo) > 0 {
		b, _ := json.Marshal(sp.Todo)
		tags = append(tags, kv("divy.todo", string(b)))
	}
	tags = append(tags, kv("divy.depth", int64(n.Depth)), kv("divy.todo_count", int64(len(sp.Todo))))
	s.Tags = tags

	for _, ev := range sp.Events {
		t, prec, err := ParseDate(string(ev.TS))
		fields := []model.JaegerKeyValue{}
		if _, hasEvent := ev.Attrs["event"]; !hasEvent {
			fields = append(fields, kv("event", ev.Name))
		}
		akeys := make([]string, 0, len(ev.Attrs))
		for k := range ev.Attrs {
			akeys = append(akeys, k)
		}
		sort.Strings(akeys)
		for _, k := range akeys {
			fields = append(fields, kv(k, ev.Attrs[k].Value))
		}
		ts := t
		if prec == PrecisionTodo || err != nil {
			ts = start
			fields = append(fields, kv("divy.ts_precision", "todo"))
		}
		s.Logs = append(s.Logs, model.JaegerLog{Timestamp: ts.UnixMicro(), Fields: fields})
	}
	return s
}

// kv builds a typed Jaeger key/value: string, bool, int64, float64; slices become JSON text.
func kv(key string, v any) model.JaegerKeyValue {
	switch t := v.(type) {
	case string:
		return model.JaegerKeyValue{Key: key, Type: "string", Value: t}
	case bool:
		return model.JaegerKeyValue{Key: key, Type: "bool", Value: t}
	case int:
		return model.JaegerKeyValue{Key: key, Type: "int64", Value: int64(t)}
	case int64:
		return model.JaegerKeyValue{Key: key, Type: "int64", Value: t}
	case float64:
		if t == float64(int64(t)) {
			return model.JaegerKeyValue{Key: key, Type: "int64", Value: int64(t)}
		}
		return model.JaegerKeyValue{Key: key, Type: "float64", Value: t}
	case []string:
		b, _ := json.Marshal(t)
		return model.JaegerKeyValue{Key: key, Type: "string", Value: string(b)}
	case nil:
		return model.JaegerKeyValue{Key: key, Type: "string", Value: ""}
	default:
		b, _ := json.Marshal(t)
		return model.JaegerKeyValue{Key: key, Type: "string", Value: string(b)}
	}
}

// SpanTagValue returns the string form of a Jaeger tag on a span (for search matching).
func SpanTagValue(s model.JaegerSpan, key string) (string, bool) {
	for _, t := range s.Tags {
		if t.Key == key {
			switch v := t.Value.(type) {
			case string:
				return v, true
			default:
				b, _ := json.Marshal(v)
				return string(b), true
			}
		}
	}
	return "", false
}
