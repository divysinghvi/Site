package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(context.Background(), "file:"+filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestResolveURL(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{nil, DefaultURL},
		{map[string]string{"DIVY_DB_URL": "file:/tmp/x.db"}, "file:/tmp/x.db"},
		{map[string]string{"DIVY_DB_URL": "file:/tmp/x.db", "TURSO_DATABASE_URL": "libsql://db.turso.io", "TURSO_AUTH_TOKEN": "t k"}, "libsql://db.turso.io?authToken=t+k"},
		{map[string]string{"TURSO_DATABASE_URL": "libsql://db.turso.io"}, "libsql://db.turso.io"},
	}
	for _, c := range cases {
		got := ResolveURL(func(k string) string { return c.env[k] })
		if got != c.want {
			t.Errorf("ResolveURL(%v) = %q, want %q", c.env, got, c.want)
		}
	}
	if m, _ := ModeOf("libsql://x"); m != ModeRemote {
		t.Error("libsql should be remote")
	}
	if m, _ := ModeOf("https://x"); m != ModeRemote {
		t.Error("https should be remote")
	}
	if m, _ := ModeOf("file:./data/divy.db"); m != ModeFile {
		t.Error("file should be file")
	}
	if _, err := ModeOf("postgres://x"); err == nil {
		t.Error("postgres should be rejected")
	}
}

func TestMigrateIdempotent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	url := "file:" + filepath.Join(dir, "m.db")
	s, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st) == 0 || !st[0].Applied || st[0].Version != 1 || st[0].Name != "init" {
		t.Fatalf("status = %+v", st)
	}
	applied, err := s.Migrate(ctx)
	if err != nil || len(applied) != 0 {
		t.Fatalf("second migrate: applied=%v err=%v", applied, err)
	}
	_ = s.Close()
	// reopen: nothing pending, tables present
	s2, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	for _, table := range []string{"series", "samples", "probe_results", "otel_spans", "collector_runs", "collector_state", "schema_migrations"} {
		var n int
		if err := s2.Reader().QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d err=%v)", table, n, err)
		}
	}
	if _, err := s2.MigrateTo(ctx, 0); err == nil {
		t.Error("down migration should be rejected")
	}
	// a DB newer than the binary
	if _, err := s2.Reader().Exec("INSERT INTO schema_migrations(version,name,applied_ms) VALUES (9999,'future',1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Migrate(ctx); err == nil {
		t.Error("newer database should be rejected")
	}
}

func TestSamplesUpsertAndRange(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	id, err := s.EnsureSeries(ctx, "github_merged_prs_total", Labels{"org": "kubernetes"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.EnsureSeries(ctx, "github_merged_prs_total", Labels{"org": "kubernetes"})
	if err != nil || id2 != id {
		t.Fatalf("EnsureSeries not idempotent: %d %d %v", id, id2, err)
	}
	gen := s.Generation()
	day := int64(86400000)
	samples := []Sample{{TsMs: 1 * day, Value: 0}, {TsMs: 2 * day, Value: 2}, {TsMs: 3 * day, Value: 3}, {TsMs: 3*day + 5000, Value: 3}}
	if err := s.UpsertSamples(ctx, id, samples); err != nil {
		t.Fatal(err)
	}
	if s.Generation() == gen {
		t.Error("generation should bump on sample write")
	}
	// overwrite: same instant, new value
	if err := s.UpsertSamples(ctx, id, []Sample{{TsMs: 2 * day, Value: 5}}); err != nil {
		t.Fatal(err)
	}
	m, _ := NewMatcher("__name__", MatchEqual, "github_merged_prs_total")
	// bounds: from < ts <= to
	got, err := s.QueryRange(ctx, []Matcher{m}, 1*day, 3*day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Samples) != 2 || got[0].Samples[0].TsMs != 2*day || got[0].Samples[0].Value != 5 || got[0].Samples[1].TsMs != 3*day {
		t.Fatalf("range = %+v", got)
	}
	if got[0].Labels["org"] != "kubernetes" {
		t.Errorf("labels = %v", got[0].Labels)
	}
	// regex matcher and negative matcher
	re, _ := NewMatcher("org", MatchRegexp, "kube.*")
	if got, _ := s.QueryRange(ctx, []Matcher{m, re}, 0, 10*day); len(got) != 1 {
		t.Errorf("regex matcher failed: %+v", got)
	}
	ne, _ := NewMatcher("org", MatchNotEqual, "kubernetes")
	if got, _ := s.QueryRange(ctx, []Matcher{m, ne}, 0, 10*day); len(got) != 0 {
		t.Errorf("!= matcher failed: %+v", got)
	}
	// latest
	latest, err := s.LatestPerSeries(ctx)
	if err != nil || len(latest) != 1 || latest[0].TsMs != 3*day+5000 || latest[0].Value != 3 {
		t.Fatalf("latest = %+v err=%v", latest, err)
	}
	// off-grid delete
	if n, err := s.DeleteOffGrid(ctx, id); err != nil || n != 1 {
		t.Fatalf("DeleteOffGrid n=%d err=%v", n, err)
	}
	// NaN rejected
	if err := s.UpsertSamples(ctx, id, []Sample{{TsMs: day, Value: nan()}}); err == nil {
		t.Error("NaN should be rejected")
	}
	// canonical labels
	if CanonicalLabels(Labels{"b": "2", "a": "<1>"}) != `{"a":"<1>","b":"2"}` || CanonicalLabels(nil) != "{}" {
		t.Error("canonical labels")
	}
	// delete series where
	if n, err := s.DeleteSeriesWhere(ctx, "github_merged_prs_total", func(l Labels) bool { return l["org"] == "kubernetes" }); err != nil || n != 1 {
		t.Fatalf("DeleteSeriesWhere n=%d err=%v", n, err)
	}
	if got, _ := s.ListSeries(ctx, nil); len(got) != 0 {
		t.Errorf("series should be gone: %+v", got)
	}
}

func nan() float64 { z := 0.0; return z / z }

func TestProbesSpansRunsState(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	lat := 12.5
	errText := "timeout: context deadline exceeded"
	probes := []Probe{{Target: "gh", TsMs: 1000, Up: true, LatencyMs: &lat, StatusCode: 200}, {Target: "gh", TsMs: 2000, Up: false, StatusCode: 0, Error: &errText}}
	if err := s.WriteProbeResults(ctx, probes); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteProbeResults(ctx, []Probe{{Target: "gh", TsMs: 2000, Up: true, StatusCode: 200}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadProbes(ctx, "gh", 1500)
	if err != nil || len(got) != 1 || !got[0].Up || got[0].Error != nil {
		t.Fatalf("probes = %+v err=%v", got, err)
	}
	last, ok, err := s.LastProbe(ctx, "gh")
	if err != nil || !ok || last.TsMs != 2000 {
		t.Fatalf("last = %+v ok=%v err=%v", last, ok, err)
	}
	if _, ok, _ := s.LastProbe(ctx, "none"); ok {
		t.Error("unknown target should have no probe")
	}
	// spans
	parent := "00f067aa0ba902b7"
	msg := "panic"
	spans := []Span{
		{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: parent, Name: "HTTP GET /x", Service: "divy-api", StartUnixNano: 100, EndUnixNano: 200, Attributes: json.RawMessage(`{"http.route":"/x"}`)},
		{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "53ce929d0e0e4736", ParentSpanID: &parent, Name: "sqlite.select", Service: "divy-api", StartUnixNano: 120, EndUnixNano: 150, StatusCode: 2, StatusMsg: &msg},
	}
	if err := s.WriteSpans(ctx, spans); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSpans(ctx, spans[:1]); err != nil {
		t.Fatal("duplicate span insert should be ignored:", err)
	}
	tr, err := s.ReadTrace(ctx, "4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil || len(tr) != 2 || tr[0].SpanID != parent || tr[1].ParentSpanID == nil || *tr[1].StatusMsg != "panic" {
		t.Fatalf("trace = %+v err=%v", tr, err)
	}
	ids, err := s.SearchTraces(ctx, "divy-api", "", 0, 1000, 10)
	if err != nil || len(ids) != 1 {
		t.Fatalf("search = %v err=%v", ids, err)
	}
	ops, err := s.Operations(ctx, "divy-api", 0)
	if err != nil || len(ops) != 2 || ops[0] != "HTTP GET /x" {
		t.Fatalf("ops = %v err=%v", ops, err)
	}
	if n, err := s.CapSpans(ctx, 1); err != nil || n != 1 {
		t.Fatalf("CapSpans n=%d err=%v", n, err)
	}
	if n, err := s.DeleteSpansBefore(ctx, 1000); err != nil || n != 1 {
		t.Fatalf("DeleteSpansBefore n=%d err=%v", n, err)
	}
	// runs
	id, err := s.StartRun(ctx, "process")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, id, true, "", 3); err != nil {
		t.Fatal(err)
	}
	ls, err := s.LastSuccess(ctx)
	if err != nil || ls["process"] == 0 {
		t.Fatalf("last success = %v err=%v", ls, err)
	}
	ok2 := false
	e := "boom"
	if err := s.RecordCollectorRun(ctx, CollectorRun{Collector: "github", StartedMs: 1, FinishedMs: ptr(int64(2)), OK: &ok2, Error: &e}); err != nil {
		t.Fatal(err)
	}
	runs, err := s.RecentRuns(ctx, "", 10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %+v err=%v", runs, err)
	}
	if _, err := s.StartRun(ctx, "stale"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.MarkAbandonedRuns(ctx, time.Now().Add(time.Hour).UnixMilli()); err != nil || n != 1 {
		t.Fatalf("abandoned n=%d err=%v", n, err)
	}
	// state
	if _, ok, _ := s.GetState(ctx, "cursor"); ok {
		t.Error("state should be empty")
	}
	if err := s.SetState(ctx, "cursor", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(ctx, "cursor", "def"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.GetState(ctx, "cursor"); !ok || v != "def" {
		t.Errorf("state = %q ok=%v", v, ok)
	}
	// retention helpers
	if _, err := s.WriteSeries(ctx, "probe_success", Labels{"target": "gh"}, []Sample{{TsMs: 1000, Value: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteSeries(ctx, "github_stars", Labels{"repo": "x"}, []Sample{{TsMs: 1000, Value: 1}}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeleteSamplesBefore(ctx, 5000, "probe_"); err != nil || n != 1 {
		t.Fatalf("delete probe samples n=%d err=%v", n, err)
	}
	if n, err := s.DeleteSamplesBefore(ctx, 5000, "!probe_"); err != nil || n != 1 {
		t.Fatalf("delete non-probe samples n=%d err=%v", n, err)
	}
	if n, err := s.DeleteOrphanSeries(ctx); err != nil || n != 2 {
		t.Fatalf("orphans n=%d err=%v", n, err)
	}
	if n, err := s.DeleteProbesBefore(ctx, 1500); err != nil || n != 1 {
		t.Fatalf("delete probes n=%d err=%v", n, err)
	}
	if lat, err := s.Ping(ctx); err != nil || lat <= 0 {
		t.Errorf("ping lat=%v err=%v", lat, err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestWriteAfterClose(t *testing.T) {
	s := openTemp(t)
	_ = s.Close()
	if _, err := s.EnsureSeries(context.Background(), "x", nil); err == nil {
		t.Error("write after close should fail")
	}
}
