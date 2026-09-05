package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"divy.dev/internal/collector"
	"divy.dev/internal/content"
	"divy.dev/internal/model"
	"divy.dev/internal/store"
)

var frozen = time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)

func newTestServer(t *testing.T, withStore bool) (*Server, *store.Store) {
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
	var st *store.Store
	var runner *collector.Runner
	if withStore {
		st, err = store.Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "s.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		reg := collector.NewRegistry()
		_ = reg.Register(collector.Process{})
		runner = &collector.Runner{Store: st, Registry: reg}
	}
	s, err := New(Config{Content: c, Store: st, Runner: runner, SiteFS: fstest.MapFS{}, Version: "v0.1.0-test", Commit: "abc1234", SiteOrigin: "https://example.vercel.app", CollectTokens: []string{"s3cret"}, CollectBudget: 2 * time.Second, Now: func() time.Time { return frozen }})
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func do(t *testing.T, s *Server, method, path string, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	return res, body
}

func decode(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("bad json: %v\n%s", err, body)
	}
}

func TestHealthz(t *testing.T) {
	s, _ := newTestServer(t, true)
	res, body := do(t, s, "GET", "/healthz", nil)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/json" || res.Header.Get("Cache-Control") != CacheNS {
		t.Fatalf("status=%d headers=%v", res.StatusCode, res.Header)
	}
	want := `{"status":"ok","open_to":["backend intern","infra","sre"],"tz":"Asia/Kolkata"}`
	if strings.TrimSpace(string(body)) != want {
		t.Errorf("healthz body = %s", body)
	}
	if res.Header.Get("X-Divy-Trace-Id") != "" {
		t.Error("no fake X-Divy-Trace-Id may be emitted before the OTel package lands")
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("security headers missing")
	}
	// HEAD mirrors GET (over a real connection net/http drops the body)
	srv := httptest.NewServer(s)
	defer srv.Close()
	hres, err := http.Head(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := io.ReadAll(hres.Body)
	_ = hres.Body.Close()
	if hres.StatusCode != 200 || len(hb) != 0 || hres.Header.Get("Content-Type") != "application/json" {
		t.Errorf("HEAD: status=%d len=%d", hres.StatusCode, len(hb))
	}
}

func TestReadyz(t *testing.T) {
	s, st := newTestServer(t, true)
	res, body := do(t, s, "GET", "/readyz", nil)
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	var r model.Readyz
	decode(t, body, &r)
	if r.Status != "ok" || !r.Checks.DB.OK || !r.Checks.Content.OK || r.Checks.Content.Spans != 28 || r.Checks.Content.LogLines != 11 || r.Version != "v0.1.0-test" {
		t.Errorf("readyz = %+v", r)
	}
	pc, ok := r.Checks.Collectors["process"]
	if !ok || pc.OK != nil || pc.StaleAfterS != 900 {
		t.Errorf("process collector readiness = %+v ok=%v", pc, ok)
	}
	// db down → 503 unavailable
	_ = st.Close()
	res, body = do(t, s, "GET", "/readyz", nil)
	decode(t, body, &r)
	if res.StatusCode != 503 || r.Status != "unavailable" || r.Checks.DB.OK {
		t.Errorf("after close: status=%d body=%s", res.StatusCode, body)
	}
}

func TestReadyzShuttingDownAndNoStore(t *testing.T) {
	c := content.MustLoad("../content/testdata/valid", content.Options{Now: frozen})
	down := false
	s, err := New(Config{Content: c, SiteFS: fstest.MapFS{}, ShuttingDown: func() bool { return down }})
	if err != nil {
		t.Fatal(err)
	}
	res, body := do(t, s, "GET", "/readyz", nil)
	if res.StatusCode != 503 || !strings.Contains(string(body), `"unavailable"`) {
		t.Errorf("no store: %d %s", res.StatusCode, body)
	}
	down = true
	res, body = do(t, s, "GET", "/readyz", nil)
	if res.StatusCode != 503 || !strings.Contains(string(body), `"shutting_down"`) {
		t.Errorf("shutting down: %d %s", res.StatusCode, body)
	}
}

func TestRobots(t *testing.T) {
	s, _ := newTestServer(t, false)
	res, body := do(t, s, "GET", "/robots.txt", nil)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("status=%d ct=%s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	for _, want := range []string{"# Observability for humans: /metrics\n", "Sitemap: https://example.vercel.app/sitemap.xml\n", "Disallow: /api/\n", "User-agent: *\n"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("robots missing %q:\n%s", want, body)
		}
	}
}

func TestIndexNegotiation(t *testing.T) {
	s, _ := newTestServer(t, false)
	cases := []struct {
		accept   string
		path     string
		wantText bool
	}{
		{"text/plain", "/", true},
		{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", "/", false},
		{"*/*", "/", false},
		{"", "/", false},
		{"text/*", "/", false},
		{"text/plain, */*", "/", false},
		{"text/plain;q=0.9, text/html;q=0.5", "/", true},
		{"text/html", "/?format=ascii", true},
		{"application/json", "/ascii", true},
		{"application/json", "/ascii?width=100", true},
	}
	for _, c := range cases {
		res, body := do(t, s, "GET", c.path, map[string]string{"Accept": c.accept})
		isText := strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain; charset=utf-8") && res.StatusCode == 200
		if isText != c.wantText {
			t.Errorf("Accept=%q path=%s: text=%v (status %d, ct %s)", c.accept, c.path, isText, res.StatusCode, res.Header.Get("Content-Type"))
			continue
		}
		if c.wantText {
			if !strings.HasPrefix(string(body), "divy.career · "+content.TraceID) {
				t.Errorf("ascii body wrong: %q", body[:60])
			}
			if c.path == "/" && res.Header.Get("Vary") != "Accept" {
				t.Errorf("Vary missing on /: %v", res.Header)
			}
		} else {
			// no embedded site in tests → the JSON hint
			if res.StatusCode != 404 || !strings.Contains(string(body), "web assets not embedded") {
				t.Errorf("html branch: status=%d body=%s", res.StatusCode, body)
			}
		}
	}
	res, _ := do(t, s, "HEAD", "/", map[string]string{"Accept": "text/plain"})
	if res.StatusCode != 200 {
		t.Errorf("HEAD / = %d", res.StatusCode)
	}
}

func TestContentEndpoints(t *testing.T) {
	s, _ := newTestServer(t, false)
	type check struct {
		path   string
		ct     string
		assert func(body []byte)
	}
	checks := []check{
		{"/api/content/services", "application/json", func(b []byte) {
			var v model.ContentServices
			decode(t, b, &v)
			if len(v.Services) != 10 || v.Services[0].ID != "divy" || v.Services[0].Color != "#73bf69" {
				t.Errorf("services = %+v", v)
			}
		}},
		{"/api/content/spans", "application/json", func(b []byte) {
			var v model.SpansFile
			decode(t, b, &v)
			if v.Version != 1 || v.Trace.ID != "divy.career" || string(v.Trace.Start) != "2023" {
				t.Errorf("spans = %+v", v.Trace.ID)
			}
		}},
		{"/api/content/logs", "application/x-ndjson", func(b []byte) {
			if lines := strings.Count(strings.TrimSpace(string(b)), "\n") + 1; lines != 11 {
				t.Errorf("logs lines = %d", lines)
			}
		}},
		{"/api/content/postmortems", "application/json", func(b []byte) {
			var v model.ContentPostmortemList
			decode(t, b, &v)
			if len(v.Items) != 4 || v.Items[0].ID != "INC-001" || v.Items[0].OgImage != "https://example.vercel.app/og/postmortems/INC-001.png" || v.Items[0].TodoCount == 0 {
				t.Errorf("postmortems = %+v", v.Items)
			}
		}},
		{"/api/content/postmortems/INC-002", "application/json", func(b []byte) {
			var v model.ContentPostmortem
			decode(t, b, &v)
			if v.ID != "INC-002" || v.SpanURL != "/#trace?span=gradr.inc-002" || !strings.Contains(v.HTML, `<h2 id="timeline-utc">`) || len(v.TOC) != 8 || !strings.HasPrefix(v.Markdown, "---\n") {
				t.Errorf("postmortem = id=%s span_url=%s toc=%d", v.ID, v.SpanURL, len(v.TOC))
			}
		}},
		{"/api/content/postmortems/INC-002.md", "text/markdown; charset=utf-8", func(b []byte) {
			if !strings.HasPrefix(string(b), "---\nid: INC-002") {
				t.Errorf("markdown = %q", b[:30])
			}
		}},
		{"/api/content/panels", "application/json", func(b []byte) {
			var v model.PanelsFile
			decode(t, b, &v)
			if len(v.Panels) != 5 || v.Dashboard.Time.Default != "1y" {
				t.Errorf("panels = %d", len(v.Panels))
			}
		}},
		{"/api/content/alerts", "application/json", func(b []byte) {
			var v model.AlertsFile
			decode(t, b, &v)
			if len(v.Groups) != 1 || len(v.Groups[0].Rules) != 3 || v.Groups[0].Rules[0].Alert != "DivyAvailableForHire" {
				t.Errorf("alerts = %+v", v)
			}
		}},
		{"/api/content/uptime", "application/json", func(b []byte) {
			var v model.ContentUptime
			decode(t, b, &v)
			if len(v.Targets) != 5 || v.Targets[0].Configured || v.Targets[3].Method != "HEAD" || v.Targets[3].Timeout != "10s" || v.Targets[3].ExpectedStatus[0] != 200 || !v.Targets[3].Configured || v.Targets[4].Note == nil {
				t.Errorf("uptime = %+v", v.Targets)
			}
		}},
		{"/api/content/manual-metrics", "application/json", func(b []byte) {
			var v model.ContentManualMetrics
			decode(t, b, &v)
			if len(v.Metrics) != 2 || v.Metrics[0].Metric != "savely_active_users" || v.Metrics[0].Value != 5000 {
				t.Errorf("manual = %+v", v.Metrics)
			}
		}},
		{"/api/content/profile", "application/json", func(b []byte) {
			var v model.ContentProfile
			decode(t, b, &v)
			if len(v.Pods) != 4 || v.Pods[0].Restarts != 4 || v.Pods[0].AgeS <= 0 || v.Pods[1].Restarts != 0 || v.TZ != "Asia/Kolkata" {
				t.Errorf("profile pods = %+v", v.Pods)
			}
		}},
		{"/api/content/todos", "application/json", func(b []byte) {
			var v model.ContentTodos
			decode(t, b, &v)
			if v.Count == 0 || v.Count != len(v.Items) || v.ByFile["content/spans.yaml"] == 0 || v.GeneratedAt != "2026-09-05T00:00:00Z" {
				t.Errorf("todos = count=%d files=%v", v.Count, v.ByFile)
			}
		}},
	}
	for _, c := range checks {
		res, body := do(t, s, "GET", c.path, nil)
		if res.StatusCode != 200 {
			t.Errorf("%s: status %d body %s", c.path, res.StatusCode, body)
			continue
		}
		if ct := res.Header.Get("Content-Type"); ct != c.ct {
			t.Errorf("%s: content-type %q", c.path, ct)
		}
		if cc := res.Header.Get("Cache-Control"); cc != CacheC60 {
			t.Errorf("%s: cache-control %q", c.path, cc)
		}
		c.assert(body)
	}
	res, body := do(t, s, "GET", "/api/content/postmortems/INC-999", nil)
	if res.StatusCode != 404 || strings.TrimSpace(string(body)) != `{"error":"postmortem not found"}` || res.Header.Get("Cache-Control") != CacheNS {
		t.Errorf("404 = %d %s", res.StatusCode, body)
	}
	res, body = do(t, s, "GET", "/api/nope", nil)
	if res.StatusCode != 404 || strings.TrimSpace(string(body)) != `{"error":"not found"}` {
		t.Errorf("api 404 = %d %s", res.StatusCode, body)
	}
	res, body = do(t, s, "GET", "/api/v1/nope", nil)
	if res.StatusCode != 404 || !strings.Contains(string(body), `"errorType":"not_found"`) {
		t.Errorf("prom 404 = %d %s", res.StatusCode, body)
	}
	res, body = do(t, s, "GET", "/loki/api/v1/tail", nil)
	if res.StatusCode != 404 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") || !strings.HasPrefix(string(body), "not supported by divy.dev") {
		t.Errorf("loki 404 = %d %s", res.StatusCode, body)
	}
	res, body = do(t, s, "POST", "/api/content/profile", nil)
	if res.StatusCode != 405 || !strings.Contains(res.Header.Get("Allow"), "GET") || strings.TrimSpace(string(body)) != `{"error":"method not allowed"}` {
		t.Errorf("405 = %d allow=%q body=%s", res.StatusCode, res.Header.Get("Allow"), body)
	}
	res, body = do(t, s, "GET", "/nope", nil)
	if res.StatusCode != 404 || strings.TrimSpace(string(body)) != `{"error":"not found"}` {
		t.Errorf("static 404 without a site = %d %s", res.StatusCode, body)
	}
}

func TestTraces(t *testing.T) {
	s, st := newTestServer(t, true)
	for _, id := range []string{"career", content.TraceID} {
		res, body := do(t, s, "GET", "/api/traces/"+id, nil)
		if res.StatusCode != 200 || res.Header.Get("Cache-Control") != CacheQ15 {
			t.Fatalf("%s: status=%d cc=%s", id, res.StatusCode, res.Header.Get("Cache-Control"))
		}
		var v model.JaegerTraceResponse
		decode(t, body, &v)
		if len(v.Data) != 1 || v.Data[0].TraceID != content.TraceID || len(v.Data[0].Spans) != 28 || v.Data[0].Spans[0].OperationName == "" || v.Total != 0 || v.Errors != nil {
			t.Fatalf("career trace = data=%d", len(v.Data))
		}
		if !strings.Contains(string(body), `"errors":null`) || !strings.Contains(string(body), `"warnings":null`) {
			t.Error("errors/warnings must be null")
		}
		root := v.Data[0].Spans[0]
		if root.OperationName != "divy.career" || root.SpanID != content.SpanHexID("divy.career") || len(root.References) != 0 || root.ProcessID != "p-divy" {
			t.Errorf("root span = %+v", root)
		}
		if p, ok := v.Data[0].Processes["p-gradr"]; !ok || p.ServiceName != "gradr" {
			t.Errorf("processes = %+v", v.Data[0].Processes)
		}
	}
	res, body := do(t, s, "GET", "/api/traces/zzz", nil)
	if res.StatusCode != 400 || !strings.Contains(string(body), `invalid trace id \"zzz\": want \"career\" or 32 hex characters`) {
		t.Errorf("bad id = %d %s", res.StatusCode, body)
	}
	res, body = do(t, s, "GET", "/api/traces/00000000000000000000000000000000", nil)
	if res.StatusCode != 404 || !strings.Contains(string(body), "trace not found (self-traces are sampled and kept 24h") || res.Header.Get("Cache-Control") != CacheNS {
		t.Errorf("unknown otel id = %d %s", res.StatusCode, body)
	}
	// a stored self-trace resolves
	parent := "00f067aa0ba902b7"
	msg := "panic"
	rows := []store.Span{
		{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: parent, Name: "HTTP GET /api/v1/query_range", Service: "divy-api", StartUnixNano: frozen.UnixNano(), EndUnixNano: frozen.UnixNano() + 8012345, Attributes: json.RawMessage(`{"http.request.method":"GET","http.response.status_code":200,"span.kind":"server","ratio":0.25,"hit":true,"stack":["go","sqlite"]}`)},
		{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "53ce929d0e0e4736", ParentSpanID: &parent, Name: "sqlite.select", Service: "divy-api", StartUnixNano: frozen.UnixNano() + 1000, EndUnixNano: frozen.UnixNano() + 2000, StatusCode: 2, StatusMsg: &msg, Events: json.RawMessage(`[{"time_unix_nano":` + itoa64(frozen.UnixNano()+1200000) + `,"name":"cache","attributes":{"key":"abc"}}]`)},
	}
	if err := st.WriteSpans(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
	res, body = do(t, s, "GET", "/api/traces/4bf92f3577b34da6a3ce929d0e0e4736", nil)
	if res.StatusCode != 200 || res.Header.Get("Cache-Control") != CacheNS {
		t.Fatalf("otel trace = %d %s", res.StatusCode, body)
	}
	var v model.JaegerTraceResponse
	decode(t, body, &v)
	tr := v.Data[0]
	if len(tr.Spans) != 2 || tr.Spans[0].ProcessID != "p1" || tr.Processes["p1"].ServiceName != "divy-api" || tr.Spans[0].Duration != 8012 {
		t.Fatalf("otel trace shape = %+v", tr)
	}
	tags := map[string]model.JaegerKeyValue{}
	for _, kv := range tr.Spans[0].Tags {
		tags[kv.Key] = kv
	}
	if tags["http.response.status_code"].Type != "int64" || tags["ratio"].Type != "float64" || tags["hit"].Type != "bool" || tags["stack"].Value != `["go","sqlite"]` || tags["span.kind"].Value != "server" {
		t.Errorf("tag typing = %+v", tags)
	}
	child := tr.Spans[1]
	if len(child.References) != 1 || child.References[0].SpanID != parent || len(child.Logs) != 1 || child.Logs[0].Fields[0].Value != "cache" || child.Logs[0].Fields[1].Key != "key" {
		t.Errorf("child = %+v", child)
	}
	ctags := map[string]any{}
	for _, kv := range child.Tags {
		ctags[kv.Key] = kv.Value
	}
	if ctags["otel.status_code"] != "ERROR" || ctags["error"] != true || ctags["otel.status_description"] != "panic" {
		t.Errorf("status tags = %v", ctags)
	}
	// services and operations
	res, body = do(t, s, "GET", "/api/services", nil)
	var sv model.JaegerStringsResponse
	decode(t, body, &sv)
	if res.StatusCode != 200 || strings.Join(sv.Data, ",") != "divy,divy-api,edu,ef-polymer,euro-tech,freelance,gradr,oss,project,quant" || sv.Total != 10 {
		t.Errorf("services = %+v", sv)
	}
	res, body = do(t, s, "GET", "/api/services/gradr/operations", nil)
	decode(t, body, &sv)
	if res.StatusCode != 200 || len(sv.Data) != 8 || sv.Data[0] != "gradr.inc-001" {
		t.Errorf("gradr operations = %+v", sv)
	}
	res, body = do(t, s, "GET", "/api/services/divy-api/operations", nil)
	decode(t, body, &sv)
	if res.StatusCode != 200 || len(sv.Data) != 2 {
		t.Errorf("divy-api operations = %+v", sv)
	}
	res, body = do(t, s, "GET", "/api/services/nope/operations", nil)
	if res.StatusCode != 404 || !strings.Contains(string(body), "service not found") {
		t.Errorf("unknown service = %d %s", res.StatusCode, body)
	}
	res, body = do(t, s, "GET", "/api/operations?service=gradr", nil)
	var ov model.JaegerOperationsResponse
	decode(t, body, &ov)
	if res.StatusCode != 200 || len(ov.Data) != 8 || ov.Data[0].SpanKind != "internal" {
		t.Errorf("operations = %+v", ov)
	}
	res, body = do(t, s, "GET", "/api/operations", nil)
	if res.StatusCode != 400 || !strings.Contains(string(body), "parameter 'service' is required") {
		t.Errorf("operations without service = %d %s", res.StatusCode, body)
	}
	// search
	res, body = do(t, s, "GET", "/api/traces?service=gradr", nil)
	decode(t, body, &v)
	if res.StatusCode != 200 || len(v.Data) != 1 || v.Total != 1 || v.Limit != 1 || res.Header.Get("Cache-Control") != CacheQ15 {
		t.Errorf("search gradr = %d total=%d", res.StatusCode, v.Total)
	}
	res, body = do(t, s, "GET", `/api/traces?service=gradr&operation=gradr.inc-002&tags={"component":"dev-proxy"}`, nil)
	decode(t, body, &v)
	if res.StatusCode != 200 || len(v.Data) != 1 {
		t.Errorf("search with tags = %d %s", res.StatusCode, body[:80])
	}
	res, body = do(t, s, "GET", `/api/traces?service=gradr&tags={"component":"nope"}`, nil)
	decode(t, body, &v)
	if res.StatusCode != 200 || len(v.Data) != 0 || v.Total != 0 {
		t.Errorf("search no match = %d %d", res.StatusCode, len(v.Data))
	}
	res, body = do(t, s, "GET", `/api/traces?service=divy-api&start=`+itoa64(frozen.Add(-time.Hour).UnixMicro())+`&end=`+itoa64(frozen.Add(time.Hour).UnixMicro()), nil)
	decode(t, body, &v)
	if res.StatusCode != 200 || len(v.Data) != 1 || v.Data[0].TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("search divy-api = %d %d", res.StatusCode, len(v.Data))
	}
	res, body = do(t, s, "GET", "/api/traces", nil)
	if res.StatusCode != 400 || !strings.Contains(string(body), "parameter 'service' is required") {
		t.Errorf("search without service = %d %s", res.StatusCode, body)
	}
	res, body = do(t, s, "GET", `/api/traces?service=gradr&tags={"a":1}`, nil)
	if res.StatusCode != 400 || !strings.Contains(string(body), "invalid tags") {
		t.Errorf("bad tags = %d %s", res.StatusCode, body)
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

func TestCollect(t *testing.T) {
	s, st := newTestServer(t, true)
	for _, hdr := range []map[string]string{nil, {"Authorization": "Bearer wrong"}, {"Authorization": "Basic s3cret"}} {
		res, body := do(t, s, "POST", "/api/collect", hdr)
		if res.StatusCode != 401 || !strings.Contains(string(body), `"error":"unauthorized`) || res.Header.Get("WWW-Authenticate") == "" {
			t.Errorf("hdr=%v: status=%d body=%s", hdr, res.StatusCode, body)
		}
	}
	// first round runs; a second round inside the collector's interval is
	// reported as skipped (not due); force=1 runs it regardless
	for _, c := range []struct {
		method, path string
		ok           bool
	}{{"POST", "/api/collect", true}, {"GET", "/api/collect", false}, {"GET", "/api/collect?force=1", true}} {
		res, body := do(t, s, c.method, c.path, map[string]string{"Authorization": "Bearer s3cret"})
		if res.StatusCode != 200 || res.Header.Get("Cache-Control") != CacheNS {
			t.Fatalf("%s %s: status=%d body=%s", c.method, c.path, res.StatusCode, body)
		}
		var sum model.CollectSummary
		decode(t, body, &sum)
		if len(sum.Collectors) != 1 || sum.Collectors[0].Name != "process" || sum.Collectors[0].OK != c.ok || sum.BudgetMs != 2000 || sum.Truncated {
			t.Errorf("%s %s: summary = %+v", c.method, c.path, sum)
		}
		if !c.ok && !strings.HasPrefix(sum.Collectors[0].Error, "skipped: not due") {
			t.Errorf("%s %s: error = %q", c.method, c.path, sum.Collectors[0].Error)
		}
	}
	runs, err := st.RecentRuns(context.Background(), "process", 10)
	if err != nil || len(runs) != 2 {
		t.Errorf("runs = %d err=%v", len(runs), err)
	}
	// readyz now reports a fresh process collector
	res, body := do(t, s, "GET", "/readyz", nil)
	var r model.Readyz
	decode(t, body, &r)
	if res.StatusCode != 200 || r.Checks.Collectors["process"].OK == nil {
		t.Errorf("readyz after collect = %s", body)
	}
	// narrower budget accepted, wider ignored
	res, body = do(t, s, "GET", "/api/collect?budget=500ms", map[string]string{"Authorization": "Bearer s3cret"})
	var sum model.CollectSummary
	decode(t, body, &sum)
	if res.StatusCode != 200 || sum.BudgetMs != 500 {
		t.Errorf("budget param = %+v", sum)
	}
	_, body = do(t, s, "GET", "/api/collect?budget=1h", map[string]string{"Authorization": "Bearer s3cret"})
	decode(t, body, &sum)
	if sum.BudgetMs != 2000 {
		t.Errorf("budget must not widen: %+v", sum)
	}
}

func TestWantsText(t *testing.T) {
	cases := map[string]bool{
		"text/plain":                           true,
		"text/plain;q=0.8":                     true,
		"text/html":                            false,
		"text/*":                               false,
		"*/*":                                  false,
		"":                                     false,
		"text/plain, */*":                      false,
		"text/plain;q=0.9, text/html;q=0.8":    true,
		"text/html;q=0.9, text/plain;q=0.9":    false,
		"text/html;q=0, text/plain":            true,
		"text/plain;q=0":                       false,
		"application/json, text/plain, */*":    false,
		"text/plain, text/html;q=0.5, */*;q=0": true,
	}
	for accept, want := range cases {
		if got := wantsText(accept); got != want {
			t.Errorf("wantsText(%q) = %v, want %v", accept, got, want)
		}
	}
}
