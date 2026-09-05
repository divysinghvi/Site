package retention

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"divy.dev/internal/store"
)

var frozen = time.Date(2026, 9, 5, 10, 15, 0, 0, time.UTC)

func TestRun(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, "file:"+filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	old := frozen.Add(-800 * 24 * time.Hour).UnixMilli()
	mid := frozen.Add(-100 * 24 * time.Hour).UnixMilli()
	fresh := frozen.Add(-time.Hour).UnixMilli()
	if _, err := st.WriteSeries(ctx, "github_commits_total", nil, []store.Sample{{TsMs: old, Value: 1}, {TsMs: mid, Value: 2}, {TsMs: fresh, Value: 3}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WriteSeries(ctx, "probe_success", store.Labels{"target": "x"}, []store.Sample{{TsMs: mid, Value: 1}, {TsMs: fresh, Value: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WriteSeries(ctx, "orphan_total", nil, []store.Sample{{TsMs: old, Value: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteProbeResults(ctx, []store.Probe{{Target: "x", TsMs: mid, Up: true}, {Target: "x", TsMs: fresh, Up: true}}); err != nil {
		t.Fatal(err)
	}
	span := func(id string, at time.Time) store.Span {
		return store.Span{TraceID: strings.Repeat("a", 31) + id, SpanID: strings.Repeat("b", 15) + id, Name: "GET /", Service: "divy-api", StartUnixNano: at.UnixNano(), EndUnixNano: at.UnixNano() + 1}
	}
	if err := st.WriteSpans(ctx, []store.Span{span("1", frozen.Add(-30*time.Hour)), span("2", frozen.Add(-time.Hour))}); err != nil {
		t.Fatal(err)
	}
	runStart := frozen.Add(-2 * time.Hour).UnixMilli()
	if _, err := st.StartRun(ctx, "github"); err != nil { // finished_ms NULL, started now → not abandoned yet
		t.Fatal(err)
	}
	ok := true
	oldFin := frozen.Add(-40 * 24 * time.Hour).UnixMilli()
	if err := st.RecordCollectorRun(ctx, store.CollectorRun{Collector: "uptime", StartedMs: oldFin - 10, FinishedMs: &oldFin, OK: &ok}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordCollectorRun(ctx, store.CollectorRun{Collector: "pypi", StartedMs: runStart}); err != nil { // unfinished, 2h old → abandoned
		t.Fatal(err)
	}

	res, err := New(Config{Now: func() time.Time { return frozen }}, st).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// samples: 1 old commit sample + 1 old probe sample + 1 orphan sample; probe_results: 1; spans: 1; runs: 1 abandoned + 1 deleted; orphan series: 1
	if res.Items != 8 {
		t.Errorf("items = %d note=%q", res.Items, res.Note)
	}
	for _, want := range []string{"samples=2", "probe_samples=1", "probe_results=1", "spans_age=1", "runs_abandoned=1", "runs=1", "orphan_series=1"} {
		if !strings.Contains(res.Note, want) {
			t.Errorf("note %q lacks %s", res.Note, want)
		}
	}
	names, _ := st.MetricNames(ctx)
	if strings.Join(names, ",") != "github_commits_total,probe_success" {
		t.Errorf("metrics after retention = %v", names)
	}
	if rows, _ := st.ReadProbes(ctx, "x", 0); len(rows) != 1 {
		t.Errorf("probe rows = %d", len(rows))
	}
	runs, _ := st.RecentRuns(ctx, "pypi", 5)
	if len(runs) != 1 || runs[0].Error == nil || *runs[0].Error != "abandoned" {
		t.Errorf("abandoned run = %+v", runs)
	}
	// second run: nothing expired
	res, _ = New(Config{Now: func() time.Time { return frozen }}, st).Run(ctx)
	if res.Items != 0 || res.Note != "nothing expired" {
		t.Errorf("second run = %+v", res)
	}
}
