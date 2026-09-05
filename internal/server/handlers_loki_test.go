package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"divy.dev/internal/model"
)

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

type lokiSample struct {
	Metric map[string]string `json:"metric"`
	Value  []json.RawMessage `json:"value"`
	Values [][]json.RawMessage
}

func lokiGet(t *testing.T, s *Server, path string, params url.Values, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	return do(t, s, http.MethodGet, path, hdr)
}

func decodeLoki(t *testing.T, body []byte) (model.LokiQueryResult, []lokiStream, []lokiSample) {
	t.Helper()
	var res model.LokiQueryResult
	decode(t, body, &res)
	var streams []lokiStream
	var samples []lokiSample
	switch res.Data.ResultType {
	case "streams":
		if err := json.Unmarshal(res.Data.Result, &streams); err != nil {
			t.Fatalf("streams: %v\n%s", err, res.Data.Result)
		}
	default:
		if err := json.Unmarshal(res.Data.Result, &samples); err != nil {
			t.Fatalf("samples: %v\n%s", err, res.Data.Result)
		}
	}
	return res, streams, samples
}

func countEntries(streams []lokiStream) int {
	n := 0
	for _, s := range streams {
		n += len(s.Values)
	}
	return n
}

func wantText(t *testing.T, res *http.Response, body []byte, status int, msg string) {
	t.Helper()
	if res.StatusCode != status {
		t.Errorf("status = %d, want %d (body %s)", res.StatusCode, status, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("Cache-Control") != CacheNS {
		t.Errorf("error headers = %v", res.Header)
	}
	if string(body) != msg {
		t.Errorf("body = %q\nwant %q", body, msg)
	}
}

const nowNS = "1788566400000000000" // 2026-09-05T00:00:00Z, the test server's frozen clock

func TestLokiQueryRangeStreams(t *testing.T) {
	s, _ := newTestServer(t, false)
	// row 1: backward, limit 2 → the two newest gradr lines, one stream each
	res, body := lokiGet(t, s, "/loki/api/v1/query_range", url.Values{"query": {`{service="gradr"}`}, "limit": {"2"}, "direction": {"backward"}}, map[string]string{"X-Loki-Response-Encoding-Flags": "categorize-labels"})
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/json" || res.Header.Get("Cache-Control") != CacheQ15 {
		t.Fatalf("status=%d headers=%v body=%s", res.StatusCode, res.Header, body)
	}
	if et := res.Header.Get("ETag"); !strings.HasPrefix(et, `W/"`) {
		t.Errorf("ETag = %q", et)
	}
	out, streams, _ := decodeLoki(t, body)
	if out.Status != "success" || out.Data.ResultType != "streams" || len(streams) != 2 || countEntries(streams) != 2 {
		t.Fatalf("result = %+v %+v", out, streams)
	}
	if streams[0].Stream["component"] != "dev-proxy" || streams[1].Stream["component"] != "secrets-sidecar" {
		t.Errorf("stream order = %+v", streams)
	}
	if v := streams[0].Values[0]; len(v) != 2 || v[0] != "1772323200000000008" || !strings.HasPrefix(v[1], `{"ts":"TODO(divy)","level":"warn","service":"gradr","component":"dev-proxy"`) {
		t.Errorf("values[0] = %v", v)
	}
	if strings.Contains(string(body), "encodingFlags") {
		t.Errorf("encodingFlags must not be emitted (X-Loki-Response-Encoding-Flags is ignored)")
	}
	if out.Data.Stats.Store.TotalChunksRef != 3 || out.Data.Stats.Summary.TotalLinesProcessed != 4 || out.Data.Stats.Summary.TotalEntriesReturned != 2 || out.Data.Stats.Summary.TotalBytesProcessed == 0 {
		t.Errorf("stats = %+v", out.Data.Stats)
	}
	// ETag → 304
	res2, _ := lokiGet(t, s, "/loki/api/v1/query_range", url.Values{"query": {`{service="gradr"}`}, "limit": {"2"}, "direction": {"backward"}}, map[string]string{"If-None-Match": res.Header.Get("ETag")})
	if res2.StatusCode != 304 {
		t.Errorf("If-None-Match: status = %d", res2.StatusCode)
	}
	// forward keeps ascending order inside a stream
	_, body = lokiGet(t, s, "/loki/api/v1/query_range", url.Values{"query": {`{service="gradr", component=""}`}, "direction": {"forward"}}, nil)
	_, streams, _ = decodeLoki(t, body)
	if len(streams) != 1 || len(streams[0].Values) != 2 || streams[0].Values[0][0] >= streams[0].Values[1][0] {
		t.Errorf("forward = %+v", streams)
	}
	// the default window starts at the root span start (inclusive) and ends now
	_, body = lokiGet(t, s, "/loki/api/v1/query_range", url.Values{"query": {`{level="debug"}`}}, nil)
	if _, streams, _ = decodeLoki(t, body); countEntries(streams) != 2 {
		t.Errorf("default window debug entries = %d", countEntries(streams))
	}
	_, body = lokiGet(t, s, "/loki/api/v1/query_range", url.Values{"query": {`{service="edu"}`}}, nil)
	if _, streams, _ = decodeLoki(t, body); countEntries(streams) != 1 {
		t.Errorf("entry at the window start must be included")
	}
	// since= counts back from now
	_, body = lokiGet(t, s, "/loki/api/v1/query_range", url.Values{"query": {`{service="gradr"}`}, "since": {"1y"}}, nil)
	if _, streams, _ = decodeLoki(t, body); countEntries(streams) != 4 {
		t.Errorf("since=1y entries = %d", countEntries(streams))
	}
	_, body = lokiGet(t, s, "/loki/api/v1/query_range", url.Values{"query": {`{service="gradr"}`}, "since": {"30d"}}, nil)
	if _, streams, _ = decodeLoki(t, body); countEntries(streams) != 0 {
		t.Errorf("since=30d entries = %d", countEntries(streams))
	}
	// the json pipeline groups by the final label set
	_, body = lokiGet(t, s, "/loki/api/v1/query_range", url.Values{"query": {`{service="gradr"} | json | resolved="true"`}}, nil)
	_, streams, _ = decodeLoki(t, body)
	if len(streams) != 2 || streams[0].Stream["incident"] != "INC-002" || streams[0].Stream["containers_approx"] != "65" || streams[1].Stream["span"] != "gradr.inc-001" {
		t.Errorf("json streams = %+v", streams)
	}
	// HEAD mirrors GET
	srv := httptest.NewServer(s)
	defer srv.Close()
	hres, err := http.Head(srv.URL + "/loki/api/v1/query_range?query=" + url.QueryEscape(`{service="gradr"}`))
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := io.ReadAll(hres.Body)
	_ = hres.Body.Close()
	if hres.StatusCode != 200 || len(hb) != 0 || hres.Header.Get("Content-Type") != "application/json" {
		t.Errorf("HEAD: status=%d len=%d", hres.StatusCode, len(hb))
	}
}

func TestLokiErrors(t *testing.T) {
	s, _ := newTestServer(t, false)
	cases := []struct {
		name   string
		params url.Values
		status int
		msg    string
	}{
		{"empty-compatible selector", url.Values{"query": {`{service=~".*"}`}}, 400, "parse error : " + `queries require at least one regexp or equality matcher that does not have an empty-compatible value. For instance, app=~".*" does not meet this requirement, but app=~".+" will`},
		{"logfmt", url.Values{"query": {`{service="gradr"} | logfmt`}}, 400, `parse error at line 1, col 21: unsupported parser "logfmt" (supported: json)`},
		{"limit over max", url.Values{"query": {`{service="gradr"}`}, "limit": {"6000"}}, 400, `max entries limit per query exceeded, limit > max_entries_limit_per_query (6000 > 5000)`},
		{"limit zero", url.Values{"query": {`{service="gradr"}`}, "limit": {"0"}}, 400, `limit must be a positive value`},
		{"limit text", url.Values{"query": {`{service="gradr"}`}, "limit": {"abc"}}, 400, `invalid parameter "limit": strconv.Atoi: parsing "abc": invalid syntax`},
		{"bad start", url.Values{"query": {`{service="gradr"}`}, "start": {"abc"}}, 400, `invalid parameter "start": cannot parse "abc" as nanoseconds, float seconds or RFC3339`},
		{"end before start", url.Values{"query": {`{service="gradr"}`}, "start": {"2026-03-01T00:00:00Z"}, "end": {"2026-02-01T00:00:00Z"}}, 400, `end must be after start`},
		{"direction", url.Values{"query": {`{service="gradr"}`}, "direction": {"sideways"}}, 400, `invalid direction "sideways": want forward or backward`},
		{"missing query", url.Values{}, 400, `parse error : syntax error: unexpected $end`},
		{"bad step", url.Values{"query": {`count_over_time({service="gradr"}[1d])`}, "step": {"x"}}, 400, `invalid parameter "step": cannot parse "x" to a valid duration`},
		{"zero step", url.Values{"query": {`count_over_time({service="gradr"}[1d])`}, "step": {"0"}}, 400, `zero or negative query resolution step widths are not accepted. Try a positive integer`},
		{"too many steps", url.Values{"query": {`count_over_time({service="gradr"}[1d])`}, "start": {"0"}, "end": {nowNS}, "step": {"1"}}, 400, `too many steps (1788566400 > 11000); increase step`},
		{"bad since", url.Values{"query": {`{service="gradr"}`}, "since": {"1.5h"}}, 400, `invalid parameter "since": unknown unit "." in duration "1.5h"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, body := lokiGet(t, s, "/loki/api/v1/query_range", c.params, nil)
			wantText(t, res, body, c.status, c.msg)
		})
	}
	// unsupported endpoints, methods and paths (Loki text envelope)
	res, body := do(t, s, "GET", "/loki/api/v1/tail", nil)
	wantText(t, res, body, 404, lokiNotSupported)
	res, body = do(t, s, "POST", "/loki/api/v1/push", nil)
	wantText(t, res, body, 404, lokiNotSupported)
	res, body = do(t, s, "GET", "/loki/api/v1/query_range/", nil)
	wantText(t, res, body, 404, lokiNotSupported)
	res, body = do(t, s, "GET", "/loki/something", nil)
	wantText(t, res, body, 404, lokiNotSupported)
	res, body = do(t, s, "PUT", "/loki/api/v1/query_range", nil)
	wantText(t, res, body, 405, "method not allowed")
	if allow := res.Header.Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "POST") {
		t.Errorf("Allow = %q", allow)
	}
	res, body = do(t, s, "POST", "/loki/api/v1/status/buildinfo", nil)
	wantText(t, res, body, 405, "method not allowed")
	// a JSON body is not a form
	req := httptest.NewRequest(http.MethodPost, "/loki/api/v1/query_range", strings.NewReader(`{"query":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	wantText(t, rec.Result(), rec.Body.Bytes(), 400, "invalid parameter: body must be application/x-www-form-urlencoded")
}

func TestLokiTimestamps(t *testing.T) {
	s, _ := newTestServer(t, false)
	// review finding protocol-01: ≤10 digits = seconds, >10 = nanoseconds, RFC3339, float seconds
	forms := []url.Values{
		{"start": {"1757030400"}, "end": {nowNS}},
		{"start": {"1757030400000000000"}, "end": {nowNS}},
		{"start": {"2025-09-05T00:00:00Z"}, "end": {nowNS}},
		{"start": {"1757030400.0"}, "end": {"1788566400"}},
		{"start": {"2025-09-05T00:00:00+00:00"}, "end": {"2026-09-05T00:00:00.000Z"}},
	}
	var first string
	for i, f := range forms {
		f.Set("query", `{service="gradr"}`)
		res, body := lokiGet(t, s, "/loki/api/v1/query_range", f, nil)
		if res.StatusCode != 200 {
			t.Fatalf("form %d: %d %s", i, res.StatusCode, body)
		}
		out, streams, _ := decodeLoki(t, body)
		if countEntries(streams) != 4 {
			t.Errorf("form %d: entries = %d", i, countEntries(streams))
		}
		if i == 0 {
			first = string(out.Data.Result)
		} else if string(out.Data.Result) != first {
			t.Errorf("form %d: result differs from form 0", i)
		}
	}
	// mixed RFC3339 / float seconds
	res, body := lokiGet(t, s, "/loki/api/v1/query_range", url.Values{"query": {`{service="gradr"}`}, "start": {"2026-03-01T00:00:00Z"}, "end": {"1772323200.5"}}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("mixed forms: %d %s", res.StatusCode, body)
	}
	if _, streams, _ := decodeLoki(t, body); countEntries(streams) != 3 {
		t.Errorf("mixed forms entries = %d", countEntries(streams))
	}
	// the instant endpoint: a 10-digit `time` is seconds; the 2026-03-01 lines
	// sit a few nanoseconds after the second (ts + line index) and are excluded
	res, body = lokiGet(t, s, "/loki/api/v1/query", url.Values{"query": {`{service="gradr"}`}, "time": {"1772323200"}, "limit": {"1"}}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("instant: %d %s", res.StatusCode, body)
	}
	if _, streams, _ := decodeLoki(t, body); countEntries(streams) != 1 || streams[0].Values[0][0] != "1764547200000000005" {
		t.Errorf("instant at 2026-03-01: %+v", streams)
	}
	res, body = lokiGet(t, s, "/loki/api/v1/query", url.Values{"query": {`{service="gradr"}`}, "time": {"1772323200000000008"}, "limit": {"1"}}, nil)
	if _, streams, _ := decodeLoki(t, body); res.StatusCode != 200 || countEntries(streams) != 1 || streams[0].Values[0][0] != "1772323200000000008" {
		t.Errorf("instant at a 19-digit time: %+v", streams)
	}
}

func TestLokiInstantAndMetrics(t *testing.T) {
	s, _ := newTestServer(t, false)
	// Grafana's Save & test request (verbatim shape, review finding protocol-05)
	res, body := do(t, s, "GET", "/loki/api/v1/query?direction=backward&query=vector(1)%2Bvector(1)&time=4000000000", nil)
	if res.StatusCode != 200 || res.Header.Get("Cache-Control") != CacheQ15 {
		t.Fatalf("health check: %d %s", res.StatusCode, body)
	}
	out, _, samples := decodeLoki(t, body)
	if out.Data.ResultType != "vector" || len(samples) != 1 || len(samples[0].Metric) != 0 || string(samples[0].Value[0]) != "4000000000" || string(samples[0].Value[1]) != `"2"` {
		t.Errorf("health check result = %+v (%s)", samples, out.Data.Result)
	}
	// instant log query: entries with ts ≤ time, newest first
	_, body = lokiGet(t, s, "/loki/api/v1/query", url.Values{"query": {`{service="gradr"}`}, "limit": {"1"}}, nil)
	out, streams, _ := decodeLoki(t, body)
	if out.Data.ResultType != "streams" || countEntries(streams) != 1 || streams[0].Values[0][0] != "1772323200000000008" {
		t.Errorf("instant logs = %+v", streams)
	}
	_, body = lokiGet(t, s, "/loki/api/v1/query", url.Values{"query": {`{service="gradr"}`}, "time": {"2026-01-01T00:00:00Z"}}, nil)
	if _, streams, _ = decodeLoki(t, body); countEntries(streams) != 1 || streams[0].Values[0][0] != "1764547200000000005" {
		t.Errorf("instant logs at 2026-01-01 = %+v", streams)
	}
	// instant metric
	_, body = lokiGet(t, s, "/loki/api/v1/query", url.Values{"query": {`sum(count_over_time({service=~".+"}[1y]))`}}, nil)
	out, _, samples = decodeLoki(t, body)
	if out.Data.ResultType != "vector" || len(samples) != 1 || string(samples[0].Value[0]) != "1788566400" || string(samples[0].Value[1]) != `"5"` {
		t.Errorf("instant sum = %s", out.Data.Result)
	}
	// Grafana's range shape: UnixNano bounds, step with ms unit, no limit (metric queries ignore it anyway)
	_, body = lokiGet(t, s, "/loki/api/v1/query_range", url.Values{
		"query": {`sum by (level, detected_level) (count_over_time({service="gradr"}[30d]))`},
		"start": {"1772323200000000000"}, "end": {"1774915200000000000"}, "step": {"2592000000ms"}, "direction": {"backward"},
	}, nil)
	out, _, _ = decodeLoki(t, body)
	if out.Data.ResultType != "matrix" || string(out.Data.Result) != `[{"metric":{"level":"info"},"values":[[1774915200,"1"]]},{"metric":{"level":"warn"},"values":[[1774915200,"2"]]}]` {
		t.Errorf("matrix = %s", out.Data.Result)
	}
	if out.Data.Stats.Store.TotalChunksRef != 3 {
		t.Errorf("matrix stats = %+v", out.Data.Stats)
	}
	// step=15000ms over one hour → 241 points of a scalar
	res, body = lokiGet(t, s, "/loki/api/v1/query_range", url.Values{"query": {`vector(1)+vector(1)`}, "start": {"1772323200"}, "end": {"1772326800"}, "step": {"15000ms"}}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("step=15000ms: %d %s", res.StatusCode, body)
	}
	var raw struct {
		Data struct {
			Result []struct {
				Values [][]json.RawMessage `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	decode(t, body, &raw)
	if len(raw.Data.Result) != 1 || len(raw.Data.Result[0].Values) != 241 || string(raw.Data.Result[0].Values[1][0]) != "1772323215" {
		t.Errorf("step=15000ms points = %d", len(raw.Data.Result[0].Values))
	}
	// a metric query with limit=6000 is fine (limit only applies to log queries)
	res, body = lokiGet(t, s, "/loki/api/v1/query_range", url.Values{"query": {`count_over_time({service="gradr"}[1d])`}, "limit": {"6000"}, "step": {"1d"}}, nil)
	if res.StatusCode != 200 {
		t.Errorf("metric query with limit=6000: %d %s", res.StatusCode, body)
	}
	// default step = max(floor((end−start)/250), 1) s: 10 s over 250 s → 26 points
	res, body = lokiGet(t, s, "/loki/api/v1/query_range", url.Values{"query": {`vector(1)`}, "start": {"1772323200"}, "end": {"1772325700"}}, nil)
	decode(t, body, &raw)
	if res.StatusCode != 200 || len(raw.Data.Result[0].Values) != 251 {
		t.Errorf("default step points = %d", len(raw.Data.Result[0].Values))
	}
}

func TestLokiLabelsSeriesStats(t *testing.T) {
	s, _ := newTestServer(t, false)
	res, body := do(t, s, "GET", "/loki/api/v1/labels", nil)
	if res.StatusCode != 200 || strings.TrimSpace(string(body)) != `{"status":"success","data":["component","level","service"]}` || res.Header.Get("Cache-Control") != CacheQ15 {
		t.Errorf("labels = %d %s", res.StatusCode, body)
	}
	_, body = do(t, s, "GET", "/loki/api/v1/labels?query="+url.QueryEscape(`{level="debug"}`), nil)
	if strings.TrimSpace(string(body)) != `{"status":"success","data":["level","service"]}` {
		t.Errorf("labels for debug = %s", body)
	}
	_, body = do(t, s, "GET", "/loki/api/v1/labels?start="+nowNS+"&end="+nowNS, nil)
	if strings.TrimSpace(string(body)) != `{"status":"success","data":[]}` {
		t.Errorf("labels in an empty window = %s", body)
	}
	_, body = do(t, s, "GET", "/loki/api/v1/label/service/values?query="+url.QueryEscape(`{level="warn"}`), nil)
	if strings.TrimSpace(string(body)) != `{"status":"success","data":["gradr"]}` {
		t.Errorf("label values = %s", body)
	}
	_, body = do(t, s, "GET", "/loki/api/v1/label/service/values", nil)
	if strings.TrimSpace(string(body)) != `{"status":"success","data":["edu","ef-polymer","euro-tech","gradr","oss","quant"]}` {
		t.Errorf("all services = %s", body)
	}
	_, body = do(t, s, "GET", "/loki/api/v1/label/nope/values", nil)
	if strings.TrimSpace(string(body)) != `{"status":"success","data":[]}` {
		t.Errorf("unknown label = %s", body)
	}
	res, body = do(t, s, "GET", "/loki/api/v1/label/service/values?query="+url.QueryEscape(`{level="warn"} | json`), nil)
	wantText(t, res, body, 400, "parse error : syntax error: only a stream selector is allowed here")

	// series: GET and POST form give identical bodies
	getRes, getBody := do(t, s, "GET", "/loki/api/v1/series?match[]="+url.QueryEscape(`{service="gradr"}`), nil)
	postRes, postBody := postForm(t, s, "/loki/api/v1/series", url.Values{"match[]": {`{service="gradr"}`}}, nil)
	if getRes.StatusCode != 200 || postRes.StatusCode != 200 || string(getBody) != string(postBody) {
		t.Fatalf("series GET %d %s\nPOST %d %s", getRes.StatusCode, getBody, postRes.StatusCode, postBody)
	}
	var series model.LokiSeriesResult
	decode(t, getBody, &series)
	if len(series.Data) != 3 || series.Data[0]["component"] != "dev-proxy" || series.Data[2]["level"] != "info" {
		t.Errorf("series = %+v", series.Data)
	}
	_, body = do(t, s, "GET", "/loki/api/v1/series?match[]="+url.QueryEscape(`{service="gradr"}`)+"&match[]="+url.QueryEscape(`{level="debug"}`), nil)
	decode(t, body, &series)
	if len(series.Data) != 5 {
		t.Errorf("series union = %d", len(series.Data))
	}
	res, body = do(t, s, "GET", "/loki/api/v1/series", nil)
	wantText(t, res, body, 400, "at least one match[] selector is required")

	// index/stats
	var bytes uint64
	for _, e := range s.cfg.Content.Logs {
		if e.Line.Service == "gradr" {
			bytes += uint64(len(e.Raw))
		}
	}
	res, body = do(t, s, "GET", "/loki/api/v1/index/stats?query="+url.QueryEscape(`{service="gradr"}`)+"&start=0&end="+nowNS, nil)
	if res.StatusCode != 200 || strings.TrimSpace(string(body)) != `{"streams":3,"chunks":3,"entries":4,"bytes":`+strconv.FormatUint(bytes, 10)+`}` {
		t.Errorf("index/stats = %d %s", res.StatusCode, body)
	}
	res, body = do(t, s, "GET", "/loki/api/v1/index/stats", nil)
	wantText(t, res, body, 400, "parse error : syntax error: unexpected $end")

	// index/volume
	res, body = do(t, s, "GET", "/loki/api/v1/index/volume?query="+url.QueryEscape(`{service="gradr"}`)+"&start=0&end="+nowNS, nil)
	if res.StatusCode != 200 {
		t.Fatalf("volume: %d %s", res.StatusCode, body)
	}
	var vol model.LokiVolumeResult
	decode(t, body, &vol)
	var vs []lokiSample
	if err := json.Unmarshal(vol.Data.Result, &vs); err != nil || vol.Data.ResultType != "vector" || len(vs) != 3 || string(vs[0].Value[0]) != "1788566400" {
		t.Errorf("volume = %s (%v)", body, err)
	}
	_, body = do(t, s, "GET", "/loki/api/v1/index/volume?query="+url.QueryEscape(`{service="gradr"}`)+"&aggregateBy=labels&targetLabels=level&limit=1", nil)
	decode(t, body, &vol)
	if err := json.Unmarshal(vol.Data.Result, &vs); err != nil || len(vs) != 1 || vs[0].Metric["level"] == "" {
		t.Errorf("volume by labels = %s", body)
	}
	res, body = do(t, s, "GET", "/loki/api/v1/index/volume?query="+url.QueryEscape(`{service="gradr"}`)+"&aggregateBy=x", nil)
	wantText(t, res, body, 400, `invalid aggregateBy "x": want series or labels`)

	// buildinfo
	res, body = do(t, s, "GET", "/loki/api/v1/status/buildinfo", nil)
	var bi model.LokiBuildInfo
	decode(t, body, &bi)
	if res.StatusCode != 200 || res.Header.Get("Cache-Control") != CacheC60 || bi.Version != "v0.1.0-test" || bi.Revision != "abc1234" || bi.GoVersion != runtime.Version() || bi.Branch == "" || bi.BuildUser == "" {
		t.Errorf("buildinfo = %d %s", res.StatusCode, body)
	}
}
