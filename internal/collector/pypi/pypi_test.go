package pypi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"divy.dev/internal/collector"
	"divy.dev/internal/content"
	"divy.dev/internal/store"
)

var frozen = time.Date(2026, 9, 5, 10, 15, 0, 0, time.UTC)

// overallBody follows https://pypistats.org/api/ ("overall" with mirrors=false):
// only the without_mirrors category, one row per day, newest last.
const overallBody = `{"data":[
{"category":"without_mirrors","date":"2026-08-30","downloads":12},
{"category":"without_mirrors","date":"2026-08-31","downloads":3},
{"category":"without_mirrors","date":"2026-09-02","downloads":7},
{"category":"without_mirrors","date":"2026-09-03","downloads":8}
],"package":"codemind-ci","type":"overall_downloads"}`

// infoBody is the shape of https://pypi.org/pypi/<package>/json (trimmed).
const infoBody = `{"info":{"name":"codemind-ci","version":"0.2.0","summary":"Persistent memory layer for codebases"},"releases":{"0.1.0":[{"upload_time_iso_8601":"2026-02-01T10:00:00.000000Z"}],"0.2.0":[{"upload_time_iso_8601":"2026-03-01T10:00:00.000000Z"}]}}`

type fakePyPI struct {
	statsStatus int32
	infoStatus  int32
	version     atomic.Value
	statsCalls  atomic.Int32
	infoCalls   atomic.Int32
	infoIfNone  atomic.Value
}

func newFake(t *testing.T) (*fakePyPI, *httptest.Server) {
	f := &fakePyPI{statsStatus: 200, infoStatus: 200}
	f.version.Store("0.2.0")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/packages/", func(w http.ResponseWriter, r *http.Request) {
		f.statsCalls.Add(1)
		if r.URL.Query().Get("mirrors") != "false" || !strings.HasPrefix(r.Header.Get("User-Agent"), "divy.dev-collector/") {
			t.Errorf("bad pypistats request: %s %v", r.URL, r.Header)
		}
		if s := atomic.LoadInt32(&f.statsStatus); s != 200 {
			w.WriteHeader(int(s))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(overallBody))
	})
	mux.HandleFunc("/pypi/", func(w http.ResponseWriter, r *http.Request) {
		f.infoCalls.Add(1)
		f.infoIfNone.Store(r.Header.Get("If-None-Match"))
		if s := atomic.LoadInt32(&f.infoStatus); s != 200 {
			w.WriteHeader(int(s))
			return
		}
		if r.Header.Get("If-None-Match") == `"etag-1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"etag-1"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.Replace(infoBody, `"version":"0.2.0"`, fmt.Sprintf(`"version":%q`, f.version.Load()), 1)))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newCollector(st *store.Store, srv *httptest.Server) *Collector {
	return New(Config{Packages: []string{"codemind-ci"}, StatsBase: srv.URL, PyPIBase: srv.URL, UserAgent: collector.UserAgent("https://example.vercel.app"), Now: func() time.Time { return frozen }}, st)
}

func samples(t *testing.T, st *store.Store, metric string, labels store.Labels) map[int64]float64 {
	t.Helper()
	ms := []store.Matcher{{Name: "__name__", Type: store.MatchEqual, Value: metric}}
	for k, v := range labels {
		ms = append(ms, store.Matcher{Name: k, Type: store.MatchEqual, Value: v})
	}
	data, err := st.QueryRange(context.Background(), ms, 0, frozen.Add(time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 1 {
		return nil
	}
	out := map[int64]float64{}
	for _, s := range data[0].Samples {
		out[s.TsMs] = s.Value
	}
	return out
}

func dayEnd(date string) int64 {
	d, _ := time.Parse("2006-01-02", date)
	return collector.DayEnd(d)
}

func TestPackagesFromContent(t *testing.T) {
	c := content.MustLoad("../../content/testdata/valid", content.Options{Now: frozen})
	if got := PackagesFromContent(c); len(got) != 1 || got[0] != "codemind-ci" {
		t.Errorf("PackagesFromContent = %v", got)
	}
	if got := MergePackages([]string{"B", " a "}, []string{"a"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("MergePackages = %v", got)
	}
	if packageFromURL("https://pypi.org/project/Foo-Bar/") != "foo-bar" || packageFromURL("https://example.com/project/x/") != "" {
		t.Error("packageFromURL")
	}
}

func TestRun(t *testing.T) {
	f, srv := newFake(t)
	st := newStore(t)
	c := newCollector(st, srv)
	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v (%+v)", err, res)
	}
	if !strings.Contains(res.Note, "30 downloads over 4 days (first 2026-08-30)") || !strings.Contains(res.Note, "version 0.2.0") {
		t.Errorf("note = %q", res.Note)
	}
	dl := samples(t, st, MetricDownloads, store.Labels{"package": "codemind-ci"})
	for ts, want := range map[int64]float64{
		dayEnd("2026-08-29"): 0,  // start marker
		dayEnd("2026-08-30"): 12, // through 08-30
		dayEnd("2026-08-31"): 15,
		dayEnd("2026-09-01"): 15, // no row → carried forward
		dayEnd("2026-09-02"): 22,
		dayEnd("2026-09-03"): 30,
		dayEnd("2026-09-04"): 30, // pypistats lags a day: carried forward
		frozen.UnixMilli():   30, // live
	} {
		if got, ok := dl[ts]; !ok || got != want {
			t.Errorf("downloads @%d = %v %v, want %v", ts, got, ok, want)
		}
	}
	if len(dl) != 8 {
		t.Errorf("download samples = %d, want 8", len(dl))
	}
	info := samples(t, st, MetricInfo, store.Labels{"package": "codemind-ci", "version": "0.2.0"})
	if v := info[frozen.UnixMilli()]; v != 1 {
		t.Errorf("pypi_package_info = %v", info)
	}
	if v, ok, _ := st.GetState(context.Background(), "pypi.etag.codemind-ci"); !ok || v != `"etag-1"` {
		t.Errorf("etag state = %q %v", v, ok)
	}

	// second run: 304 on pypi.org (If-None-Match sent), version reused, one series still
	if _, err := c.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.infoIfNone.Load() != `"etag-1"` || f.infoCalls.Load() != 2 {
		t.Errorf("If-None-Match not sent on the second run: %v calls=%d", f.infoIfNone.Load(), f.infoCalls.Load())
	}

	// a new release replaces the version series: exactly one pypi_package_info per package
	f.version.Store("0.3.0")
	c2 := New(Config{Packages: []string{"codemind-ci"}, StatsBase: srv.URL, PyPIBase: srv.URL, Now: func() time.Time { return frozen.Add(2 * time.Hour) }}, st)
	_ = st.DeleteState(context.Background(), "pypi.etag.codemind-ci") // pretend the cache expired
	if _, err := c2.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	series, _ := st.ListSeries(context.Background(), []store.Matcher{{Name: "__name__", Type: store.MatchEqual, Value: MetricInfo}})
	if len(series) != 1 || series[0].Labels["version"] != "0.3.0" {
		t.Errorf("info series after a release = %+v", series)
	}
}

func TestPartialFailureRecordsBothOutcomes(t *testing.T) {
	f, srv := newFake(t)
	atomic.StoreInt32(&f.statsStatus, 429)
	st := newStore(t)
	res, err := newCollector(st, srv).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "codemind-ci downloads: pypistats: rate limited (429)") {
		t.Fatalf("err = %v", err)
	}
	if res.Items != 1 || !strings.Contains(res.Note, "version 0.2.0") {
		t.Errorf("the pypi.org half must still be written: %+v", res)
	}
	if samples(t, st, MetricDownloads, store.Labels{"package": "codemind-ci"}) != nil {
		t.Error("downloads written despite the 429")
	}
	// unknown package on both sides
	atomic.StoreInt32(&f.statsStatus, 404)
	atomic.StoreInt32(&f.infoStatus, 404)
	_, err = newCollector(st, srv).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), `unknown (404)`) {
		t.Fatalf("err = %v", err)
	}
}

func TestDisabledWithoutPackages(t *testing.T) {
	st := newStore(t)
	c := New(Config{}, st)
	_, err := c.Run(context.Background())
	if !c.Disabled() || !errors.Is(err, collector.ErrDisabled) {
		t.Fatalf("disabled=%v err=%v", c.Disabled(), err)
	}
}
