package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"divy.dev/internal/content"
	"divy.dev/internal/model"
	"divy.dev/internal/store"
)

var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

const traceNotFound = "trace not found (self-traces are sampled and kept 24h; the career trace is /api/traces/career)"

// handleTrace serves /api/traces/{id}: career (alias or fixed id) from
// content, any other 32-hex id from otel_spans.
func (s *Server) handleTrace(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "career" || id == content.TraceID {
		writeJSON(w, http.StatusOK, CacheQ15, content.JaegerResponse(s.cfg.Content.JaegerTrace(s.now())))
		return
	}
	if !hex32.MatchString(id) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid trace id %q: want \"career\" or 32 hex characters", id))
		return
	}
	if s.cfg.Store == nil {
		writeError(w, http.StatusNotFound, traceNotFound)
		return
	}
	rows, err := s.cfg.Store.ReadTrace(r.Context(), id)
	if err != nil {
		s.log.Error("read trace", "err", err.Error())
		writeError(w, http.StatusInternalServerError, "internal error: "+RequestID(r.Context()))
		return
	}
	if len(rows) == 0 && s.cfg.Trace != nil {
		// The request that produced the header ended milliseconds ago; its
		// children may still be buffered behind a root that has not ended.
		fctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		_ = s.cfg.Trace.ForceFlush(fctx)
		cancel()
		if rows, err = s.cfg.Store.ReadTrace(r.Context(), id); err != nil {
			s.log.Error("read trace", "err", err.Error())
			writeError(w, http.StatusInternalServerError, "internal error: "+RequestID(r.Context()))
			return
		}
	}
	if len(rows) == 0 {
		writeError(w, http.StatusNotFound, traceNotFound)
		return
	}
	writeJSON(w, http.StatusOK, CacheNS, content.JaegerResponse(OTelTrace(rows)))
}

// handleServices serves /api/services: every content service owning a span plus the self-trace service.
func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	names := append(s.cfg.Content.ServicesWithSpans(), s.cfg.OTelServiceName)
	sort.Strings(names)
	writeJSON(w, http.StatusOK, CacheC60, model.JaegerStringsResponse{Data: names, Total: len(names), Limit: 0, Offset: 0})
}

func (s *Server) careerOperations(service string) []string {
	var out []string
	for _, n := range s.cfg.Content.Nodes() {
		if n.Span.Service == service {
			out = append(out, n.Span.ID)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) isCareerService(service string) bool {
	for _, n := range s.cfg.Content.Nodes() {
		if n.Span.Service == service {
			return true
		}
	}
	return false
}

func (s *Server) handleServiceOperations(w http.ResponseWriter, r *http.Request) {
	service := chi.URLParam(r, "service")
	var ops []string
	switch {
	case service == s.cfg.OTelServiceName:
		if s.cfg.Store != nil {
			var err error
			ops, err = s.cfg.Store.Operations(r.Context(), service, s.now().Add(-24*time.Hour).UnixNano())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal error: "+RequestID(r.Context()))
				return
			}
		}
	case s.isCareerService(service):
		ops = s.careerOperations(service)
	default:
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	if ops == nil {
		ops = []string{}
	}
	writeJSON(w, http.StatusOK, CacheC60, model.JaegerStringsResponse{Data: ops, Total: len(ops)})
}

func (s *Server) handleOperations(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	if service == "" {
		writeError(w, http.StatusBadRequest, "parameter 'service' is required")
		return
	}
	ops := []model.JaegerOperation{}
	switch {
	case service == s.cfg.OTelServiceName:
		if s.cfg.Store != nil {
			kinds, err := s.cfg.Store.OperationKinds(r.Context(), service, s.now().Add(-24*time.Hour).UnixNano())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal error: "+RequestID(r.Context()))
				return
			}
			for _, k := range kinds {
				ops = append(ops, model.JaegerOperation{Name: k.Name, SpanKind: k.Kind})
			}
		}
	case s.isCareerService(service):
		for _, name := range s.careerOperations(service) {
			ops = append(ops, model.JaegerOperation{Name: name, SpanKind: "internal"})
		}
	default:
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	writeJSON(w, http.StatusOK, CacheC60, model.JaegerOperationsResponse{Data: ops, Total: len(ops)})
}

// handleTraceSearch serves the Jaeger search form: GET /api/traces?service=…
func (s *Server) handleTraceSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	service := q.Get("service")
	if service == "" {
		writeError(w, http.StatusBadRequest, "parameter 'service' is required")
		return
	}
	operation := q.Get("operation")
	tags := map[string]string{}
	if t := q.Get("tags"); t != "" {
		var raw map[string]any
		if err := json.Unmarshal([]byte(t), &raw); err != nil {
			writeError(w, http.StatusBadRequest, "invalid tags: want a JSON object of string values")
			return
		}
		for k, v := range raw {
			sv, ok := v.(string)
			if !ok {
				writeError(w, http.StatusBadRequest, "invalid tags: want a JSON object of string values")
				return
			}
			tags[k] = sv
		}
	}
	var minDur, maxDur time.Duration
	for _, p := range []struct {
		name string
		dst  *time.Duration
	}{{"minDuration", &minDur}, {"maxDuration", &maxDur}} {
		if v := q.Get(p.name); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid parameter %q: want a Go duration such as 1s", p.name))
				return
			}
			*p.dst = d
		}
	}
	limit := 20
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid parameter \"limit\": want an integer 1..100")
			return
		}
		if n > 100 {
			n = 100
		}
		limit = n
	}
	now := s.now()
	startUs, endUs := now.Add(-time.Hour).UnixMicro(), now.UnixMicro()
	for _, p := range []struct {
		name string
		dst  *int64
	}{{"start", &startUs}, {"end", &endUs}} {
		if v := q.Get(p.name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid parameter %q: want microseconds since epoch", p.name))
				return
			}
			*p.dst = n
		}
	}

	var traces []model.JaegerTrace
	switch {
	case s.isCareerService(service):
		tr := s.cfg.Content.JaegerTrace(now)
		if traceMatches(tr, service, operation, tags, minDur, maxDur) {
			traces = append(traces, tr)
		}
	case service == s.cfg.OTelServiceName && s.cfg.Store != nil:
		ids, err := s.cfg.Store.SearchTraces(r.Context(), service, operation, startUs*1000, endUs*1000, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error: "+RequestID(r.Context()))
			return
		}
		for _, id := range ids {
			rows, err := s.cfg.Store.ReadTrace(r.Context(), id)
			if err != nil || len(rows) == 0 {
				continue
			}
			tr := OTelTrace(rows)
			if traceMatches(tr, service, operation, tags, minDur, maxDur) {
				traces = append(traces, tr)
			}
		}
	}
	if len(traces) > limit {
		traces = traces[:limit]
	}
	resp := content.JaegerResponse(traces...)
	resp.Total, resp.Limit = len(traces), len(traces)
	writeJSON(w, http.StatusOK, CacheQ15, resp)
}

// traceMatches applies the Jaeger search filters to one trace: at least one
// span of the service matches the operation, every tag and the duration bounds.
func traceMatches(tr model.JaegerTrace, service, operation string, tags map[string]string, minDur, maxDur time.Duration) bool {
	for _, sp := range tr.Spans {
		p, ok := tr.Processes[sp.ProcessID]
		if !ok || p.ServiceName != service {
			continue
		}
		if operation != "" && sp.OperationName != operation {
			continue
		}
		d := time.Duration(sp.Duration) * time.Microsecond
		if minDur > 0 && d < minDur {
			continue
		}
		if maxDur > 0 && d > maxDur {
			continue
		}
		okTags := true
		for k, v := range tags {
			got, has := content.SpanTagValue(sp, k)
			if !has || got != v {
				okTags = false
				break
			}
		}
		if okTags {
			return true
		}
	}
	return false
}

// OTelTrace converts stored otel_spans rows into Jaeger JSON (LogQL §L.4.3).
func OTelTrace(rows []store.Span) model.JaegerTrace {
	tr := model.JaegerTrace{Spans: []model.JaegerSpan{}, Processes: map[string]model.JaegerProcess{}}
	if len(rows) == 0 {
		return tr
	}
	tr.TraceID = rows[0].TraceID
	pids := map[string]string{}
	for _, row := range rows {
		pid, ok := pids[row.Service]
		if !ok {
			pid = "p" + strconv.Itoa(len(pids)+1)
			pids[row.Service] = pid
			tr.Processes[pid] = model.JaegerProcess{ServiceName: row.Service, Tags: []model.JaegerKeyValue{}}
		}
		sp := model.JaegerSpan{
			TraceID:       row.TraceID,
			SpanID:        row.SpanID,
			OperationName: row.Name,
			References:    []model.JaegerReference{},
			StartTime:     row.StartUnixNano / 1000,
			Duration:      (row.EndUnixNano - row.StartUnixNano) / 1000,
			Tags:          []model.JaegerKeyValue{},
			Logs:          []model.JaegerLog{},
			ProcessID:     pid,
		}
		if row.ParentSpanID != nil && *row.ParentSpanID != "" {
			sp.References = append(sp.References, model.JaegerReference{RefType: "CHILD_OF", TraceID: row.TraceID, SpanID: *row.ParentSpanID})
		}
		attrs := decodeAttrs(row.Attributes)
		keys := make([]string, 0, len(attrs))
		for k := range attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sp.Tags = append(sp.Tags, attrKV(k, attrs[k]))
		}
		switch row.StatusCode {
		case 1:
			sp.Tags = append(sp.Tags, model.JaegerKeyValue{Key: "otel.status_code", Type: "string", Value: "OK"})
		case 2:
			sp.Tags = append(sp.Tags, model.JaegerKeyValue{Key: "otel.status_code", Type: "string", Value: "ERROR"}, model.JaegerKeyValue{Key: "error", Type: "bool", Value: true})
			if row.StatusMsg != nil && *row.StatusMsg != "" {
				sp.Tags = append(sp.Tags, model.JaegerKeyValue{Key: "otel.status_description", Type: "string", Value: *row.StatusMsg})
			}
		}
		var events []struct {
			TimeUnixNano int64          `json:"time_unix_nano"`
			Name         string         `json:"name"`
			Attributes   map[string]any `json:"attributes"`
		}
		if len(row.Events) > 0 {
			_ = json.Unmarshal(row.Events, &events)
		}
		for _, ev := range events {
			fields := []model.JaegerKeyValue{{Key: "event", Type: "string", Value: ev.Name}}
			ekeys := make([]string, 0, len(ev.Attributes))
			for k := range ev.Attributes {
				ekeys = append(ekeys, k)
			}
			sort.Strings(ekeys)
			for _, k := range ekeys {
				fields = append(fields, attrKV(k, ev.Attributes[k]))
			}
			sp.Logs = append(sp.Logs, model.JaegerLog{Timestamp: ev.TimeUnixNano / 1000, Fields: fields})
		}
		tr.Spans = append(tr.Spans, sp)
	}
	return tr
}

func decodeAttrs(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	_ = dec.Decode(&out)
	return out
}

// attrKV types a JSON attribute value the Jaeger way: string, bool, integral → int64, other numbers → float64, arrays/objects → JSON text.
func attrKV(key string, v any) model.JaegerKeyValue {
	switch t := v.(type) {
	case string:
		return model.JaegerKeyValue{Key: key, Type: "string", Value: t}
	case bool:
		return model.JaegerKeyValue{Key: key, Type: "bool", Value: t}
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return model.JaegerKeyValue{Key: key, Type: "int64", Value: i}
		}
		f, _ := t.Float64()
		return model.JaegerKeyValue{Key: key, Type: "float64", Value: f}
	case float64:
		if t == float64(int64(t)) {
			return model.JaegerKeyValue{Key: key, Type: "int64", Value: int64(t)}
		}
		return model.JaegerKeyValue{Key: key, Type: "float64", Value: t}
	case int64:
		return model.JaegerKeyValue{Key: key, Type: "int64", Value: t}
	case int:
		return model.JaegerKeyValue{Key: key, Type: "int64", Value: int64(t)}
	case nil:
		return model.JaegerKeyValue{Key: key, Type: "string", Value: ""}
	default:
		b, _ := json.Marshal(t)
		return model.JaegerKeyValue{Key: key, Type: "string", Value: string(b)}
	}
}
