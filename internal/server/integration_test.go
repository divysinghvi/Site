package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"divy.dev/internal/collector"
	"divy.dev/internal/content"
	"divy.dev/internal/model"
	"divy.dev/internal/store"
	"divy.dev/internal/trace"
)

var hexTraceID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// newIntegrationServer builds the server the way cmd/api does: a file store,
// the OTel provider (spans → otel_spans), a wrapped collector, the per-IP
// limiter, CORS and the response cache. rl zero = no rate limiting.
func newIntegrationServer(t *testing.T, rl RateLimitConfig) (*Server, *store.Store, *trace.Provider) {
	t.Helper()
	c, err := content.Load("../content/testdata/valid", content.Options{Now: frozen, SiteOrigin: "https://example.vercel.app"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Report.HasErrors(false) {
		var sb strings.Builder
		c.Report.Write(&sb, false)
		t.Fatal(sb.String())
	}
	st, err := store.Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	tp, err := trace.New(trace.Config{ServiceName: "divy-api", Version: "v0.1.0-test", Store: st, SweepInterval: 50 * time.Millisecond, OrphanAfter: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	reg := collector.NewRegistry()
	_ = reg.Register(tp.WrapCollector(collector.Process{}))
	runner := &collector.Runner{Store: st, Registry: reg}
	s, err := New(Config{
		Content: c, Store: st, Runner: runner, Trace: tp,
		SiteFS: fstest.MapFS{}, Version: "v0.1.0-test", Commit: "abc1234", SiteOrigin: "https://example.vercel.app",
		CollectTokens: []string{"s3cret"}, CollectBudget: 2 * time.Second,
		RateLimit: rl, CORSOrigins: []string{"https://grafana.example"}, CacheEntries: 100,
		Now: func() time.Time { return frozen },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, st, tp
}

func doForm(t *testing.T, s *Server, method, path, form string, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(form))
	if form != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	res := rec.Result()
	body := rec.Body.Bytes()
	return res, body
}

func assertTraceHeaders(t *testing.T, res *http.Response) string {
	t.Helper()
	id := res.Header.Get(trace.HeaderTraceID)
	if !hexTraceID.MatchString(id) {
		t.Errorf("%s = %q, want 32 hex", trace.HeaderTraceID, id)
	}
	if got := res.Header.Get(trace.HeaderSampled); got != "1" {
		t.Errorf("%s = %q, want 1", trace.HeaderSampled, got)
	}
	return id
}

// TestEndpointFamilies is the one table over every endpoint family: status,
// content type and the trace headers on every response, including 4xx.
func TestEndpointFamilies(t *testing.T) {
	s, _, _ := newIntegrationServer(t, RateLimitConfig{})
	const (
		ctJSON = "application/json"
		ctText = "text/plain"
		ctPNG  = "image/png"
		ctSVG  = "image/svg+xml"
	)
	bearer := map[string]string{"Authorization": "Bearer s3cret"}
	start := itoa64(frozen.Add(-time.Hour).Unix())
	end := itoa64(frozen.Unix())
	cases := []struct {
		family, method, path, form string
		hdr                        map[string]string
		status                     int
		ct                         string // prefix; "" = not asserted
	}{
		// Prometheus HTTP API
		{"prom", "GET", "/api/v1/status/buildinfo", "", nil, 200, ctJSON},
		{"prom", "GET", "/api/v1/query?query=1%2B1", "", nil, 200, ctJSON},
		{"prom", "POST", "/api/v1/query", "query=divy_uptime_seconds", nil, 200, ctJSON},
		{"prom", "GET", "/api/v1/query_range?query=divy_uptime_seconds&start=" + start + "&end=" + end + "&step=60", "", nil, 200, ctJSON},
		{"prom", "GET", "/api/v1/series?match[]=divy_uptime_seconds", "", nil, 200, ctJSON},
		{"prom", "GET", "/api/v1/labels", "", nil, 200, ctJSON},
		{"prom", "GET", "/api/v1/label/__name__/values", "", nil, 200, ctJSON},
		{"prom", "GET", "/api/v1/metadata", "", nil, 200, ctJSON},
		{"prom", "GET", "/api/v1/rules", "", nil, 200, ctJSON},
		{"prom", "GET", "/api/v1/alerts", "", nil, 200, ctJSON},
		{"prom", "GET", "/api/v1/query", "", nil, 400, ctJSON},
		{"prom", "GET", "/api/v1/nope", "", nil, 404, ctJSON},
		{"prom", "DELETE", "/api/v1/query", "", nil, 405, ctJSON},
		// Loki HTTP API
		{"loki", "GET", "/loki/api/v1/labels", "", nil, 200, ctJSON},
		{"loki", "GET", "/loki/api/v1/label/service/values", "", nil, 200, ctJSON},
		{"loki", "GET", "/loki/api/v1/query_range?query=" + `{service=~".%2B"}`, "", nil, 200, ctJSON},
		{"loki", "GET", "/loki/api/v1/query?query=" + `{service=~".%2B"}`, "", nil, 200, ctJSON},
		{"loki", "GET", "/loki/api/v1/series?match[]=" + `{service=~".%2B"}`, "", nil, 200, ctJSON},
		{"loki", "GET", "/loki/api/v1/status/buildinfo", "", nil, 200, ctJSON},
		{"loki", "GET", "/loki/api/v1/query_range", "", nil, 400, ctText},
		{"loki", "GET", "/loki/api/v1/nope", "", nil, 404, ctText},
		{"loki", "GET", "/loki/nope", "", nil, 404, ctText},
		// Traces (Jaeger shape)
		{"traces", "GET", "/api/traces/career", "", nil, 200, ctJSON},
		{"traces", "GET", "/api/traces/" + content.TraceID, "", nil, 200, ctJSON},
		{"traces", "GET", "/api/traces/zzz", "", nil, 400, ctJSON},
		{"traces", "GET", "/api/traces/00000000000000000000000000000000", "", nil, 404, ctJSON},
		{"traces", "GET", "/api/traces?service=divy-api", "", nil, 200, ctJSON},
		{"traces", "GET", "/api/traces", "", nil, 400, ctJSON},
		{"traces", "GET", "/api/services", "", nil, 200, ctJSON},
		{"traces", "GET", "/api/services/divy-api/operations", "", nil, 200, ctJSON},
		{"traces", "GET", "/api/operations?service=divy-api", "", nil, 200, ctJSON},
		// Content
		{"content", "GET", "/api/content/services", "", nil, 200, ctJSON},
		{"content", "GET", "/api/content/spans", "", nil, 200, ctJSON},
		{"content", "GET", "/api/content/logs", "", nil, 200, "application/x-ndjson"},
		{"content", "GET", "/api/content/postmortems", "", nil, 200, ctJSON},
		{"content", "GET", "/api/content/postmortems/INC-001", "", nil, 200, ctJSON},
		{"content", "GET", "/api/content/postmortems/NOPE", "", nil, 404, ctJSON},
		{"content", "GET", "/api/content/panels", "", nil, 200, ctJSON},
		{"content", "GET", "/api/content/alerts", "", nil, 200, ctJSON},
		{"content", "GET", "/api/content/uptime", "", nil, 200, ctJSON},
		{"content", "GET", "/api/content/manual-metrics", "", nil, 200, ctJSON},
		{"content", "GET", "/api/content/profile", "", nil, 200, ctJSON},
		{"content", "GET", "/api/content/todos", "", nil, 200, ctJSON},
		{"content", "GET", "/api/nope", "", nil, 404, ctJSON},
		{"content", "DELETE", "/api/content/profile", "", nil, 405, ctJSON},
		// Uptime
		{"uptime", "GET", "/api/uptime", "", nil, 200, ctJSON},
		{"uptime", "GET", "/api/uptime/heartbeats", "", nil, 200, ctJSON},
		// Aux
		{"aux", "GET", "/healthz", "", nil, 200, ctJSON},
		{"aux", "GET", "/readyz", "", nil, 200, ctJSON},
		{"aux", "GET", "/robots.txt", "", nil, 200, ctText},
		{"aux", "GET", "/ascii", "", nil, 200, ctText},
		{"aux", "GET", "/", "", map[string]string{"Accept": "text/plain"}, 200, ctText},
		{"aux", "GET", "/", "", map[string]string{"Accept": "text/html"}, 404, ctJSON}, // no embedded site in tests: the JSON hint
		{"aux", "GET", "/favicon.svg", "", nil, 200, ctSVG},
		{"aux", "GET", "/favicon.ico", "", nil, 404, ""},
		{"aux", "GET", "/metrics", "", nil, 200, ctText},
		{"aux", "GET", "/no/such/page", "", nil, 404, ""},
		{"aux", "HEAD", "/healthz", "", nil, 200, ctJSON},
		// OG images
		{"og", "GET", "/og/default.png", "", nil, 200, ctPNG},
		{"og", "GET", "/og/postmortems/INC-001.png", "", nil, 200, ctPNG},
		{"og", "GET", "/og/INC-001.png", "", nil, 200, ctPNG},
		{"og", "GET", "/og/postmortems/NOPE.png", "", nil, 404, ""},
		// Collect
		{"collect", "POST", "/api/collect", "", nil, 401, ctJSON},
		{"collect", "POST", "/api/collect?force=1", "", bearer, 200, ctJSON},
		{"collect", "GET", "/api/collect?force=1", "", bearer, 200, ctJSON},
	}
	for _, tc := range cases {
		name := tc.family + " " + tc.method + " " + tc.path
		t.Run(name, func(t *testing.T) {
			res, body := doForm(t, s, tc.method, tc.path, tc.form, tc.hdr)
			if res.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d\n%s", res.StatusCode, tc.status, truncateBody(body))
			}
			if tc.ct != "" && !strings.HasPrefix(res.Header.Get("Content-Type"), tc.ct) {
				t.Errorf("content-type = %q, want prefix %q", res.Header.Get("Content-Type"), tc.ct)
			}
			assertTraceHeaders(t, res)
			if res.StatusCode >= 400 && res.Header.Get("Cache-Control") != CacheNS && res.Header.Get("Cache-Control") != "public, max-age=86400, s-maxage=86400" && tc.path != "/favicon.ico" {
				t.Errorf("error response Cache-Control = %q, want %q", res.Header.Get("Cache-Control"), CacheNS)
			}
			if res.StatusCode == 405 && res.Header.Get("Allow") == "" {
				t.Error("405 without Allow")
			}
		})
	}
}

func truncateBody(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}

// TestTraceHeaderResolves is the end-to-end self-tracing check: the id in
// X-Divy-Trace-Id resolves immediately at /api/traces/{id} to the request's
// own span tree (root server span + the sqlite.select child of the query).
func TestTraceHeaderResolves(t *testing.T) {
	s, _, _ := newIntegrationServer(t, RateLimitConfig{})
	res, _ := do(t, s, "GET", "/api/v1/query?query=divy_uptime_seconds", nil)
	if res.StatusCode != 200 {
		t.Fatalf("query status = %d", res.StatusCode)
	}
	id := assertTraceHeaders(t, res)
	res, body := do(t, s, "GET", "/api/traces/"+id, nil)
	if res.StatusCode != 200 {
		t.Fatalf("/api/traces/%s = %d %s", id, res.StatusCode, body)
	}
	if res.Header.Get("Cache-Control") != CacheNS {
		t.Errorf("self-trace Cache-Control = %q, want no-store", res.Header.Get("Cache-Control"))
	}
	var v model.JaegerTraceResponse
	decode(t, body, &v)
	if len(v.Data) != 1 || v.Data[0].TraceID != id || len(v.Data[0].Spans) < 1 {
		t.Fatalf("trace = %+v", v)
	}
	tr := v.Data[0]
	var root, child *model.JaegerSpan
	for i := range tr.Spans {
		sp := &tr.Spans[i]
		if len(sp.References) == 0 {
			root = sp
		} else if sp.OperationName == "sqlite.select" {
			child = sp
		}
	}
	if root == nil || root.OperationName != "HTTP GET /api/v1/query" {
		t.Fatalf("root span = %+v", root)
	}
	if child == nil || child.References[0].SpanID != root.SpanID {
		t.Fatalf("expected a sqlite.select child of the root, spans = %d", len(tr.Spans))
	}
	tags := map[string]any{}
	for _, kv := range root.Tags {
		tags[kv.Key] = kv.Value
	}
	if tags["http.route"] != "/api/v1/query" || tags["http.status_code"] != float64(200) {
		t.Errorf("root tags = %v", tags)
	}
	if p, ok := tr.Processes[root.ProcessID]; !ok || p.ServiceName != "divy-api" {
		t.Errorf("process = %+v", tr.Processes)
	}
	// The trace lookup itself is traced too.
	assertTraceHeaders(t, res)
	// A 404 carries a resolvable id as well.
	res, _ = do(t, s, "GET", "/api/nope", nil)
	id404 := assertTraceHeaders(t, res)
	if res, _ = do(t, s, "GET", "/api/traces/"+id404, nil); res.StatusCode != 200 {
		t.Errorf("404's trace = %d", res.StatusCode)
	}
}

// TestCollectorSpans: one root span per collector run, searchable by service + operation.
func TestCollectorSpans(t *testing.T) {
	s, _, _ := newIntegrationServer(t, RateLimitConfig{})
	res, body := do(t, s, "POST", "/api/collect?force=1", map[string]string{"Authorization": "Bearer s3cret"})
	if res.StatusCode != 200 {
		t.Fatalf("collect = %d %s", res.StatusCode, body)
	}
	// The server clock is frozen; the spans carry wall-clock times, so pass a window.
	win := "&start=" + itoa64(time.Now().Add(-time.Hour).UnixMicro()) + "&end=" + itoa64(time.Now().Add(time.Hour).UnixMicro())
	res, body = do(t, s, "GET", "/api/traces?service=divy-api&operation=collector.process"+win, nil)
	if res.StatusCode != 200 {
		t.Fatalf("search = %d %s", res.StatusCode, body)
	}
	var v model.JaegerTraceResponse
	decode(t, body, &v)
	if len(v.Data) < 1 || len(v.Data[0].Spans) < 1 || v.Data[0].Spans[0].OperationName != "collector.process" {
		t.Fatalf("collector traces = %+v", v.Data)
	}
}

// TestGrafanaSaveAndTest replays the requests Grafana's "Save & test" makes.
func TestGrafanaSaveAndTest(t *testing.T) {
	s, _, _ := newIntegrationServer(t, RateLimitConfig{})
	t.Run("prometheus", func(t *testing.T) {
		res, body := do(t, s, "GET", "/api/v1/status/buildinfo", nil)
		if res.StatusCode != 200 {
			t.Fatalf("buildinfo = %d %s", res.StatusCode, body)
		}
		var bi struct {
			Status string `json:"status"`
			Data   struct {
				Version string `json:"version"`
			} `json:"data"`
		}
		decode(t, body, &bi)
		if bi.Status != "success" || bi.Data.Version == "" {
			t.Fatalf("buildinfo = %s", body)
		}
		res, body = do(t, s, "GET", "/api/v1/query?query=1%2B1", nil)
		if res.StatusCode != 200 {
			t.Fatalf("query = %d %s", res.StatusCode, body)
		}
		var q struct {
			Status string `json:"status"`
			Data   struct {
				ResultType string `json:"resultType"`
				Result     []any  `json:"result"`
			} `json:"data"`
		}
		decode(t, body, &q)
		if q.Status != "success" || q.Data.ResultType != "scalar" || len(q.Data.Result) != 2 || q.Data.Result[1] != "2" {
			t.Fatalf("1+1 = %s", body)
		}
		// Grafana also POSTs form bodies.
		res, body = doForm(t, s, "POST", "/api/v1/query", "query=1%2B1", nil)
		if res.StatusCode != 200 || !strings.Contains(string(body), `"2"`) {
			t.Fatalf("POST query = %d %s", res.StatusCode, body)
		}
	})
	t.Run("loki", func(t *testing.T) {
		res, body := do(t, s, "GET", "/loki/api/v1/labels", nil)
		if res.StatusCode != 200 {
			t.Fatalf("labels = %d %s", res.StatusCode, body)
		}
		var l struct {
			Status string   `json:"status"`
			Data   []string `json:"data"`
		}
		decode(t, body, &l)
		found := false
		for _, n := range l.Data {
			if n == "service" {
				found = true
			}
		}
		if l.Status != "success" || !found {
			t.Fatalf("labels = %s", body)
		}
		if res, body = do(t, s, "GET", "/loki/api/v1/status/buildinfo", nil); res.StatusCode != 200 || !strings.Contains(string(body), `"version"`) {
			t.Fatalf("buildinfo = %d %s", res.StatusCode, body)
		}
	})
}

// TestResponseCacheAndETag: MISS then HIT, equal ETags, If-None-Match → 304 — all traced.
func TestResponseCacheAndETag(t *testing.T) {
	s, _, _ := newIntegrationServer(t, RateLimitConfig{})
	res1, body1 := do(t, s, "GET", "/api/content/profile", nil)
	res2, body2 := do(t, s, "GET", "/api/content/profile", nil)
	if res1.StatusCode != 200 || res2.StatusCode != 200 {
		t.Fatalf("status = %d %d", res1.StatusCode, res2.StatusCode)
	}
	if res1.Header.Get("X-Cache") != "MISS" || res2.Header.Get("X-Cache") != "HIT" {
		t.Fatalf("X-Cache = %q then %q", res1.Header.Get("X-Cache"), res2.Header.Get("X-Cache"))
	}
	etag := res1.Header.Get("ETag")
	if etag == "" || etag != res2.Header.Get("ETag") || string(body1) != string(body2) {
		t.Fatalf("etag = %q / %q", etag, res2.Header.Get("ETag"))
	}
	if assertTraceHeaders(t, res1) == assertTraceHeaders(t, res2) {
		t.Error("a cache HIT must still get its own trace id")
	}
	res3, _ := do(t, s, "GET", "/api/content/profile", map[string]string{"If-None-Match": etag})
	if res3.StatusCode != 304 {
		t.Fatalf("If-None-Match = %d", res3.StatusCode)
	}
	assertTraceHeaders(t, res3)
	// The collect endpoint is never cached.
	res4, _ := do(t, s, "POST", "/api/collect", nil)
	if res4.Header.Get("X-Cache") != "" {
		t.Error("/api/collect must bypass the cache")
	}
}

// TestRateLimit429: the per-IP bucket answers 429 in the family envelope with
// Retry-After and the trace headers; /healthz, /readyz and /metrics are exempt.
func TestRateLimit429(t *testing.T) {
	s, _, _ := newIntegrationServer(t, RateLimitConfig{RPS: 1, Burst: 2})
	var res *http.Response
	var body []byte
	for i := 0; i < 3; i++ {
		res, body = do(t, s, "GET", "/api/content/profile", nil)
	}
	if res.StatusCode != 429 || res.Header.Get("Retry-After") == "" || !strings.Contains(string(body), `"error":"rate limit exceeded`) {
		t.Fatalf("third request = %d %v %s", res.StatusCode, res.Header, body)
	}
	if res.Header.Get("Content-Type") != "application/json" || res.Header.Get("Cache-Control") != CacheNS {
		t.Errorf("429 headers = %v", res.Header)
	}
	assertTraceHeaders(t, res)
	// Prometheus envelope on the Prometheus family.
	res, body = do(t, s, "GET", "/api/v1/query?query=1%2B1", nil)
	if res.StatusCode != 429 || !strings.Contains(string(body), `"errorType":"unavailable"`) {
		t.Errorf("prom 429 = %d %s", res.StatusCode, body)
	}
	// Loki family: text.
	res, _ = do(t, s, "GET", "/loki/api/v1/labels", nil)
	if res.StatusCode != 429 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") {
		t.Errorf("loki 429 = %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	for _, p := range []string{"/healthz", "/readyz", "/metrics"} {
		if res, _ = do(t, s, "GET", p, nil); res.StatusCode != 200 {
			t.Errorf("%s = %d, must be exempt from the per-IP bucket", p, res.StatusCode)
		}
	}
	// Another client has its own bucket.
	req := httptest.NewRequest("GET", "/api/content/profile", nil)
	req.RemoteAddr = "198.51.100.7:4242"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("other client = %d", rec.Code)
	}
}

// TestCORS: exact-origin allow-list, preflight 204, exposed trace headers.
func TestCORS(t *testing.T) {
	s, _, _ := newIntegrationServer(t, RateLimitConfig{})
	res, _ := do(t, s, "OPTIONS", "/api/v1/labels", map[string]string{"Origin": "https://grafana.example", "Access-Control-Request-Method": "GET"})
	if res.StatusCode != 204 || res.Header.Get("Access-Control-Allow-Origin") != "https://grafana.example" || res.Header.Get("Access-Control-Allow-Methods") == "" {
		t.Fatalf("preflight = %d %v", res.StatusCode, res.Header)
	}
	assertTraceHeaders(t, res)
	res, _ = do(t, s, "GET", "/api/v1/labels", map[string]string{"Origin": "https://grafana.example"})
	if res.Header.Get("Access-Control-Allow-Origin") != "https://grafana.example" || !strings.Contains(res.Header.Get("Access-Control-Expose-Headers"), trace.HeaderTraceID) {
		t.Errorf("simple request headers = %v", res.Header)
	}
	res, _ = do(t, s, "GET", "/api/v1/labels", map[string]string{"Origin": "https://evil.example"})
	if res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("unknown origin must get no CORS headers: %v", res.Header)
	}
}
