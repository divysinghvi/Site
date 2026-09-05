package metrics

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil/promlint"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"divy.dev/internal/content"
	"divy.dev/internal/store"
)

var update = flag.Bool("update", false, "rewrite testdata/exposition.golden")

var frozen = time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	st, err := store.Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	c := content.MustLoad("../content/testdata/valid", content.Options{Now: frozen})
	ctx := context.Background()
	fresh := frozen.Add(-time.Minute).UnixMilli()
	// one recent sample per stored catalogue family (every label set filled in)
	for _, f := range Catalogue {
		if f.Source != SourceStored || f.Name == "github_followers" {
			continue // github_followers is seeded stale below
		}
		labels := store.Labels{}
		for _, l := range f.Labels {
			labels[l] = "x-" + l
		}
		if _, err := st.WriteSeries(ctx, f.Name, labels, []store.Sample{{TsMs: fresh - 86400000, Value: 1}, {TsMs: fresh, Value: 2}}); err != nil {
			t.Fatal(err)
		}
	}
	// a second series of a labelled family, a stale series and a non-catalogue series
	for _, s := range []struct {
		metric string
		labels store.Labels
		ts     int64
		v      float64
	}{
		{"github_stars", store.Labels{"repo": "savely"}, fresh, 42},
		{"github_followers", store.Labels{}, frozen.Add(-2 * time.Hour).UnixMilli(), 7},
		{"not_in_catalogue_total", store.Labels{}, fresh, 1},
	} {
		if _, err := st.WriteSeries(ctx, s.metric, s.labels, []store.Sample{{TsMs: s.ts, Value: s.v}}); err != nil {
			t.Fatal(err)
		}
	}
	ok := true
	fin := frozen.Add(-2 * time.Minute).UnixMilli()
	for _, name := range []string{"uptime", "github"} {
		if err := st.RecordCollectorRun(ctx, store.CollectorRun{Collector: name, StartedMs: fin - 500, FinishedMs: &fin, OK: &ok}); err != nil {
			t.Fatal(err)
		}
	}
	live := NewLive(c, frozen.Add(-90*time.Second), "v0.1.0-test", "abc1234")
	r := New(Options{Store: st, Live: live, Now: func() time.Time { return frozen }})
	r.OnResult("uptime", "ok", 120*time.Millisecond)
	r.OnResult("github", "skipped", 0)
	// one request through the middleware so the HTTP families have a sample
	rec := httptest.NewRecorder()
	r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	return r
}

func gatherText(t *testing.T, r *Registry) string {
	t.Helper()
	mfs, err := r.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := expfmt.NewEncoder(&buf, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, mf := range mfs {
		if err := enc.Encode(mf); err != nil {
			t.Fatal(err)
		}
	}
	return buf.String()
}

func TestPromlint(t *testing.T) {
	r := newTestRegistry(t)
	text := gatherText(t, r)
	mfs, err := r.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	// the same nine rules promtool check metrics applies to the text exposition
	problems, err := promlint.NewWithMetricFamilies(mfs).Lint()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Errorf("promlint: %s: %s", p.Metric, p.Text)
	}
	// every catalogue family that is not a process-only family from another lane must be present
	for _, f := range Catalogue {
		if strings.HasPrefix(f.Name, "divy_otel_") || f.Name == "github_followers" {
			continue // registered by the trace package / deliberately stale above
		}
		if !strings.Contains(text, "# TYPE "+f.Name+" "+string(f.Type)+"\n") {
			t.Errorf("family %s (%s) missing from the exposition", f.Name, f.Type)
		}
	}
}

// TestExpositionGolden compares the deterministic part of the exposition
// (HELP/TYPE of every family; samples of the catalogue families) with
// testdata/exposition.golden. Regenerate with `go test ./internal/metrics -update`.
func TestExpositionGolden(t *testing.T) {
	text := gatherText(t, newTestRegistry(t))
	parser := expfmt.NewTextParser(model.UTF8Validation)
	mfs, err := parser.TextToMetricFamilies(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(mfs))
	for n := range mfs {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		mf := mfs[n]
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", n, mf.GetHelp(), n, strings.ToLower(mf.GetType().String()))
		fam, ok := Lookup(n)
		if !ok || fam.Source == SourceProcess {
			continue // go_*/process_* and timing-dependent in-process values vary
		}
		for _, m := range mf.GetMetric() {
			var lbls []string
			for _, lp := range m.GetLabel() {
				lbls = append(lbls, lp.GetName()+"="+fmt.Sprintf("%q", lp.GetValue()))
			}
			v := 0.0
			switch {
			case m.GetGauge() != nil:
				v = m.GetGauge().GetValue()
			case m.GetCounter() != nil:
				v = m.GetCounter().GetValue()
			}
			fmt.Fprintf(&b, "%s{%s} %g\n", n, strings.Join(lbls, ","), v)
		}
	}
	got := b.String()
	// the Go version label changes with the toolchain
	got = strings.ReplaceAll(got, fmt.Sprintf("go_version=%q", goVersion()), `go_version="<go>"`)
	path := filepath.Join("testdata", "exposition.golden")
	if *update || os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if string(want) != got {
		t.Errorf("exposition differs from %s (run with -update):\n%s", path, got)
	}
	for _, absent := range []string{"github_followers{", "not_in_catalogue_total"} {
		if strings.Contains(got, absent) {
			t.Errorf("stale or non-catalogue series exposed: %s", absent)
		}
	}
}

func TestLiveSeries(t *testing.T) {
	c := content.MustLoad("../content/testdata/valid", content.Options{Now: frozen})
	start := frozen.Add(-10 * time.Second)
	l := NewLive(c, start, "v1", "c1")
	byName := map[string]LiveSeries{}
	for _, s := range l.All() {
		byName[s.Metric()] = s
	}
	if v, ok := byName["divy_uptime_seconds"].Value(frozen); !ok || v != 10 {
		t.Errorf("uptime = %v %v", v, ok)
	}
	if _, ok := byName["divy_uptime_seconds"].Value(start.Add(-time.Second)); ok {
		t.Error("uptime present before start")
	}
	if v, ok := byName["divy_open_to_work"].Value(frozen); !ok || v != 1 {
		t.Errorf("open_to_work = %v %v", v, ok)
	}
	if v, ok := byName["divy_experience_years"].Value(frozen); !ok || v <= 0 {
		t.Errorf("experience_years = %v %v", v, ok)
	}
	if lbls := byName["divy_build_info"].Labels(); lbls.Get("version") != "v1" || lbls.Get("commit") != "c1" || lbls.Get("go_version") == "" {
		t.Errorf("build_info labels = %v", lbls)
	}
	if StaleAfter(5*time.Minute) != 15*time.Minute || StaleAfter(time.Hour) != 3*time.Hour {
		t.Error("StaleAfter")
	}
	if len(Queryable()) == 0 || Queryable()[0].Name != "divy_build_info" {
		t.Errorf("Queryable = %v", Queryable())
	}
}

func goVersion() string {
	for _, s := range NewLive(nil, frozen, "v", "c").All() {
		if s.Metric() == "divy_build_info" {
			return s.Labels().Get("go_version")
		}
	}
	return ""
}
