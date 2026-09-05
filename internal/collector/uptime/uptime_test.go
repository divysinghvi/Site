package uptime

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"divy.dev/internal/content"
	"divy.dev/internal/store"
)

var frozen = time.Date(2026, 9, 5, 10, 15, 0, 0, time.UTC)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func target(id, url string, mod func(*Target)) Target {
	t := Target{ID: id, Name: id, URL: url, Configured: !strings.HasPrefix(url, "TODO"), Method: "GET", Timeout: 2 * time.Second, Interval: 5 * time.Minute, FollowRedirects: true}
	if mod != nil {
		mod(&t)
	}
	return t
}

func TestTargetsFromContent(t *testing.T) {
	c := content.MustLoad("../../content/testdata/valid", content.Options{Now: frozen, SelfURL: "http://127.0.0.1:1/readyz"})
	ts := TargetsFromContent(c, 10*time.Second)
	if len(ts) != 5 {
		t.Fatalf("targets = %d", len(ts))
	}
	byID := map[string]Target{}
	for _, x := range ts {
		byID[x.ID] = x
	}
	if byID["savely-landing"].Configured || byID["savely-landing"].URL != "TODO(divy)" {
		t.Errorf("TODO target must be unconfigured: %+v", byID["savely-landing"])
	}
	if g := byID["github-profile"]; !g.Configured || g.Method != "HEAD" || g.Timeout != 10*time.Second || g.Interval != 5*time.Minute || len(g.Expected) != 0 {
		t.Errorf("github-profile = %+v", g)
	}
	if s := byID["self-api"]; s.URL != "http://127.0.0.1:1/readyz" || s.Timeout != 5*time.Second {
		t.Errorf("self-api = %+v", s)
	}
	// PROBE_TIMEOUT caps the per-target timeout
	if ts := TargetsFromContent(c, 3*time.Second); ts[3].Timeout != 3*time.Second {
		t.Errorf("timeout not capped: %v", ts[3].Timeout)
	}
}

func TestProbesAndClasses(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "divy-uptime/1.0 (+https://example.vercel.app)") {
			t.Errorf("user agent = %q", r.Header.Get("User-Agent"))
		}
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(200)
			_, _ = w.Write([]byte("ok"))
		case "/redir":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/teapot":
			w.WriteHeader(418)
		case "/slow":
			time.Sleep(400 * time.Millisecond)
			w.WriteHeader(200)
		case "/head-only":
			if r.Method != http.MethodHead {
				w.WriteHeader(405)
				return
			}
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()
	// a closed port for the conn class
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closedAddr := ln.Addr().String()
	_ = ln.Close()

	st := newStore(t)
	targets := []Target{
		target("ok", srv.URL+"/ok", nil),
		target("redir", srv.URL+"/redir", nil),
		target("noredir", srv.URL+"/redir", func(x *Target) { x.FollowRedirects = false }),
		target("noredir-listed", srv.URL+"/redir", func(x *Target) { x.FollowRedirects = false; x.Expected = []int{302} }),
		target("loop", srv.URL+"/loop", nil),
		target("teapot", srv.URL+"/teapot", nil),
		target("teapot-ok", srv.URL+"/teapot", func(x *Target) { x.Expected = []int{418} }),
		target("slow", srv.URL+"/slow?token=secret", func(x *Target) { x.Timeout = 100 * time.Millisecond }),
		target("head", srv.URL+"/head-only", func(x *Target) { x.Method = "HEAD" }),
		target("conn", "http://"+closedAddr+"/", nil),
		target("dns", "http://nonexistent.invalid/", nil),
		target("todo", "TODO(divy)", nil),
	}
	c := New(Config{Targets: targets, UserAgent: UserAgent("https://example.vercel.app"), Now: func() time.Time { return frozen }}, st)
	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Note, "probed=11") || !strings.Contains(res.Note, "unconfigured=1") || res.Items != 11*4 {
		t.Errorf("result = %+v", res)
	}
	want := map[string]struct {
		up     bool
		status int
		class  string
	}{
		"ok":             {true, 200, ""},
		"redir":          {true, 200, ""},
		"noredir":        {true, 302, ""}, // 3xx is accepted when no expected_status is listed
		"noredir-listed": {true, 302, ""},
		"loop":           {false, 0, ClassRedirect},
		"teapot":         {false, 418, ClassHTTP},
		"teapot-ok":      {true, 418, ""},
		"slow":           {false, 0, ClassTimeout},
		"head":           {true, 200, ""},
		"conn":           {false, 0, ClassConn},
		"dns":            {false, 0, ClassDNS},
	}
	for id, w := range want {
		p, ok, err := st.LastProbe(context.Background(), id)
		if err != nil || !ok {
			t.Errorf("%s: no probe row (%v)", id, err)
			continue
		}
		if p.Up != w.up || p.StatusCode != w.status || p.TsMs != frozen.UnixMilli() {
			t.Errorf("%s: up=%v status=%d ts=%d, want up=%v status=%d", id, p.Up, p.StatusCode, p.TsMs, w.up, w.status)
		}
		if w.class == "" {
			if p.Error != nil {
				t.Errorf("%s: unexpected error %q", id, *p.Error)
			}
		} else if p.Error == nil || !strings.HasPrefix(*p.Error, w.class+": ") {
			t.Errorf("%s: error = %v, want class %s", id, p.Error, w.class)
		}
		if p.LatencyMs == nil || *p.LatencyMs < 0 {
			t.Errorf("%s: latency = %v", id, p.LatencyMs)
		}
		if p.Error != nil && strings.Contains(*p.Error, "token=secret") {
			t.Errorf("%s: query string leaked into the error: %s", id, *p.Error)
		}
	}
	if _, ok, _ := st.LastProbe(context.Background(), "todo"); ok {
		t.Error("an unconfigured target must never be probed")
	}
	mu.Lock()
	if hits["/head-only"] != 1 || hits["/loop"] < 5 {
		t.Errorf("hits = %v", hits)
	}
	mu.Unlock()
	// the three gauges per probed target
	series, _ := st.ListSeries(context.Background(), nil)
	if len(series) != 11*3 {
		t.Errorf("probe series = %d, want 33", len(series))
	}
	for _, s := range series {
		if !strings.HasPrefix(s.Metric, "probe_") || s.Labels["target"] == "" {
			t.Errorf("unexpected series %s%s", s.Metric, store.CanonicalLabels(s.Labels))
		}
	}

	// per-target interval: a tick inside the interval probes nothing
	ua := UserAgent("https://example.vercel.app")
	c2 := New(Config{Targets: targets, UserAgent: ua, Now: func() time.Time { return frozen.Add(2 * time.Minute) }}, st)
	res, _ = c2.Run(context.Background())
	if !strings.Contains(res.Note, "probed=0") || !strings.Contains(res.Note, "not_due=11") {
		t.Errorf("inside the interval: %+v", res)
	}
	c3 := New(Config{Targets: targets[:1], UserAgent: ua, Now: func() time.Time { return frozen.Add(5 * time.Minute) }}, st)
	res, _ = c3.Run(context.Background())
	if !strings.Contains(res.Note, "probed=1 up=1") {
		t.Errorf("after the interval: %+v", res)
	}
}

func TestBudgetCutRecordsNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	st := newStore(t)
	c := New(Config{Targets: []Target{target("slow", srv.URL, func(x *Target) { x.Timeout = 5 * time.Second })}, Now: func() time.Time { return frozen }}, st)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res, err := c.Run(ctx)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(res.Note, "cut_by_budget=1") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, ok, _ := st.LastProbe(context.Background(), "slow"); ok {
		t.Error("a probe cut short by the run budget must not be recorded as down")
	}
}

func TestClassify(t *testing.T) {
	if Classify(&net.DNSError{Err: "no such host"}) != ClassDNS {
		t.Error("dns")
	}
	if Classify(context.DeadlineExceeded) != ClassTimeout {
		t.Error("timeout")
	}
	if Classify(errTooManyRedirects) != ClassRedirect {
		t.Error("redirect")
	}
	if Classify(errors.New("x509: certificate signed by unknown authority")) != ClassTLS {
		t.Error("tls")
	}
	if Classify(errors.New("something else")) != ClassOther {
		t.Error("other")
	}
	if got := sanitize(`Get "https://u:p@example.com/x?token=1": dial tcp: connection refused`, "https://u:p@example.com/x?token=1"); got != "dial tcp: connection refused" {
		t.Errorf("sanitize = %q", got)
	}
}
