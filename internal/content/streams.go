package content

import (
	"sort"
	"time"
)

// LogStream is one Loki stream of content/logs.ndjson: its label set
// (service, level and component when present) and the entries in ascending
// order of their ordering timestamps.
type LogStream struct {
	// Labels are the stream labels.
	Labels map[string]string
	// Key is the canonical selector string of the labels (StreamKey).
	Key string
	// Entries are the stream's lines, ascending by TSNano.
	Entries []LogEntry
}

// LogStreams groups the loaded log entries by their stream labels, sorted by
// Key. The content is immutable per process, so callers may index the result
// once at startup.
func (c *Content) LogStreams() []LogStream {
	byKey := map[string]*LogStream{}
	for _, e := range c.Logs {
		k := StreamKey(e.Labels)
		st, ok := byKey[k]
		if !ok {
			labels := make(map[string]string, len(e.Labels))
			for name, v := range e.Labels {
				labels[name] = v
			}
			st = &LogStream{Labels: labels, Key: k}
			byKey[k] = st
		}
		st.Entries = append(st.Entries, e)
	}
	out := make([]LogStream, 0, len(byKey))
	for _, st := range byKey {
		sort.SliceStable(st.Entries, func(i, j int) bool { return st.Entries[i].TSNano < st.Entries[j].TSNano })
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// LogsStart is the default start of the Loki time window: the root span's
// resolved start (2023-01-01T00:00:00Z when spans.yaml did not load).
func (c *Content) LogsStart() time.Time {
	if r := c.Root(); r != nil && !r.Start.IsZero() {
		return r.Start.UTC()
	}
	return time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
}
