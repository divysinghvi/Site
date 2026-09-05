package content

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"divy.dev/internal/model"
)

// loadLogs parses content/logs.ndjson line by line: schema, decode, labels.
func (l *loader) loadLogs() {
	raw, ok := l.read("logs.ndjson")
	if !ok {
		return
	}
	rel := l.rel("logs.ndjson")
	l.c.LogsRaw = raw
	sch := l.opts.Schemas["logs"]
	lines := bytes.Split(raw, []byte("\n"))
	idx := 0
	failed := false
	for i, ln := range lines {
		ln = bytes.TrimRight(ln, "\r")
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		fileLn := i + 1
		errs, err := validateJSON(sch, ln)
		if err != nil {
			l.c.Report.errorf(rel, fileLn, 1, "logs.ndjson", "", "line does not parse as JSON: %v", err)
			failed = true
			continue
		}
		bad := false
		for _, e := range errs {
			l.c.Report.errorf(rel, fileLn, 1, "logs.ndjson", jsonPath(e.ptr), "%s", e.msg)
			bad = true
		}
		var line model.LogLine
		if err := json.Unmarshal(ln, &line); err != nil {
			l.c.Report.errorf(rel, fileLn, 1, "logs.ndjson", "", "%v", err)
			bad = true
		}
		if bad {
			failed = true
			continue
		}
		e := LogEntry{Line: line, Raw: string(ln), Index: idx, FileLn: fileLn, Labels: map[string]string{"service": line.Service, "level": string(line.Level)}}
		if line.Component != "" {
			e.Labels["component"] = line.Component
		}
		l.c.Logs = append(l.c.Logs, e)
		l.collectLogTodos(rel, fileLn, ln, line)
		idx++
	}
	if !failed {
		l.c.loaded["logs.ndjson"] = true
	}
}

// orderLogs assigns ordering timestamps (Content §C.4.1) and sorts the entries.
func (c *Content) orderLogs() {
	rootStart := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	if r := c.Root(); r != nil && !r.Start.IsZero() {
		rootStart = r.Start
	}
	for i := range c.Logs {
		e := &c.Logs[i]
		var base time.Time
		if t, err := time.Parse(time.RFC3339Nano, e.Line.TS); err == nil {
			base = t.UTC()
		} else if n, ok := c.byID[e.Line.Span]; ok && e.Line.Span != "" {
			base = n.Start
		} else {
			base = rootStart
		}
		e.TSNano = base.UnixNano() + int64(e.Index)
	}
	sort.SliceStable(c.Logs, func(i, j int) bool { return c.Logs[i].TSNano < c.Logs[j].TSNano })
}

// LogsByService returns the entries of one service in order.
func (c *Content) LogsByService(service string) []LogEntry {
	var out []LogEntry
	for _, e := range c.Logs {
		if e.Line.Service == service {
			out = append(out, e)
		}
	}
	return out
}

// StreamKey renders labels as a canonical Loki stream selector string.
func StreamKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(k + "=\"" + labels[k] + "\"")
	}
	sb.WriteString("}")
	return sb.String()
}
