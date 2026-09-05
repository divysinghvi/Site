package server

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"divy.dev/internal/promql"
	"divy.dev/internal/store"
)

// fixtureBase is day 0 of the promql fixture in the SQLite store (samples must have ts > 0).
var fixtureBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const promFixture = `
github_commits_total                         0 3 3 7 12 12 20
pypi_downloads_total{package="codemind-ci"}  _ _ _ 100 130 190
reset_total                                  10 14 3 9
github_merged_prs_total{org="kubernetes"}    5 6 6 7
github_merged_prs_total{org="kubeflow"}      1 1 2 2
github_merged_prs_total{org="gradr"}         0 0 3 3
github_stars{repo="codemind"}                12 12 13 13
github_stars{repo="savely"}                  40 41 41 42
probe_success{target="pypi"}                 1 0 1 1
probe_duration_seconds{target="pypi"}        0.2 0.2 0.2 0.2
`

// seedFixture writes the fixture (day i = fixtureBase + i days) plus a few
// recent samples relative to the frozen clock so /metrics has fresh series.
func seedFixture(t *testing.T, st *store.Store) {
	t.Helper()
	data, err := promql.LoadFixture(promFixture, 86400*1000, fixtureBase.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, sd := range data {
		labels := store.Labels{}
		metric := ""
		for _, l := range sd.Metric {
			if l.Name == promql.MetricName {
				metric = l.Value
			} else {
				labels[l.Name] = l.Value
			}
		}
		samples := make([]store.Sample, 0, len(sd.Points))
		for _, p := range sd.Points {
			if math.IsNaN(p.F) {
				continue
			}
			samples = append(samples, store.Sample{TsMs: p.T, Value: p.F})
		}
		if _, err := st.WriteSeries(ctx, metric, labels, samples); err != nil {
			t.Fatal(err)
		}
	}
	recent := frozen.Add(-time.Minute).UnixMilli()
	for _, s := range []struct {
		metric string
		labels store.Labels
		ts     int64
		v      float64
	}{
		{"github_stars", store.Labels{"repo": "savely"}, recent, 42},
		{"github_stars", store.Labels{"repo": "codemind"}, recent, 13},
		{"probe_success", store.Labels{"target": "pypi"}, recent, 1},
		{"pypi_downloads_total", store.Labels{"package": "codemind-ci"}, frozen.Add(-30 * time.Minute).UnixMilli(), 200},
		{"github_followers", store.Labels{}, frozen.Add(-2 * time.Hour).UnixMilli(), 7}, // stale: github cadence 15m → 45m cut-off
		{"reset_total", store.Labels{}, recent, 9},                                      // not in the catalogue: queryable, never exposed
	} {
		if _, err := st.WriteSeries(ctx, s.metric, s.labels, []store.Sample{{TsMs: s.ts, Value: s.v}}); err != nil {
			t.Fatal(err)
		}
	}
	ok := true
	fin := frozen.Add(-3 * time.Minute).UnixMilli()
	if err := st.RecordCollectorRun(ctx, store.CollectorRun{Collector: "uptime", StartedMs: fin - 1000, FinishedMs: &fin, OK: &ok, Items: 3}); err != nil {
		t.Fatal(err)
	}
}

func newPromServer(t *testing.T) *Server {
	t.Helper()
	s, st := newTestServer(t, true)
	seedFixture(t, st)
	return s
}

func day(i float64) string {
	return fixtureBase.Add(time.Duration(i * 24 * float64(time.Hour))).Format(time.RFC3339)
}

func postForm(t *testing.T, s *Server, path string, form url.Values, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	return res, body
}

func TestPromAPI(t *testing.T) {
	s := newPromServer(t)
	cases := []struct {
		name   string
		method string
		path   string
		form   url.Values // POST form body when method is POST
		status int
		body   string // exact body, or a prefix when it ends with "…"
	}{
		{"H1 empty vector", "GET", "/api/v1/query?query=up", nil, 200, `{"status":"success","data":{"resultType":"vector","result":[]}}`},
		{"H2 grafana health check", "POST", "/api/v1/query", url.Values{"query": {"1+1"}, "time": {"4"}}, 200, `{"status":"success","data":{"resultType":"scalar","result":[4,"2"]}}`},
		{"H3 missing query", "GET", "/api/v1/query", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": unknown position: parse error: no expression found in input"}`},
		{"H4 parse error", "GET", "/api/v1/query?query=sum(", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": 1:5: parse error: unclosed left parenthesis"}`},
		{"H5 offset", "GET", "/api/v1/query?query=x%20offset%201d", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": 1:3: parse error: offset modifier is not supported"}`},
		{"H6 bad time", "GET", "/api/v1/query?query=up&time=yesterday", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"time\": invalid time value for 'time': cannot parse \"yesterday\" to a valid timestamp"}`},
		{"H7 end before start", "GET", "/api/v1/query_range?query=up&start=10&end=5&step=1", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"end\": end timestamp must not be before start time"}`},
		{"H8a zero step", "GET", "/api/v1/query_range?query=up&start=0&end=10&step=0", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"step\": zero or negative query resolution step widths are not accepted. Try a positive integer"}`},
		{"H8b bad step", "GET", "/api/v1/query_range?query=up&start=0&end=10&step=abc", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"step\": cannot parse \"abc\" to a valid duration"}`},
		{"H8c missing start", "GET", "/api/v1/query_range?query=up&end=10&step=1", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"start\": cannot parse \"\" to a valid timestamp"}`},
		{"H9a too many points", "GET", "/api/v1/query_range?query=up&start=0&end=100000&step=1", nil, 400, `{"status":"error","errorType":"bad_data","error":"exceeded maximum resolution of 11,000 points per timeseries. Try decreasing the query resolution (?step=XX)"}`},
		{"H9b 11001 points pass", "GET", "/api/v1/query_range?query=up&start=0&end=11000&step=1", nil, 200, `{"status":"success","data":{"resultType":"matrix","result":[]}}`},
		{"H9c 11002 points fail", "GET", "/api/v1/query_range?query=up&start=0&end=11001&step=1", nil, 400, `{"status":"error","errorType":"bad_data","error":"exceeded maximum resolution…`},
		{"H10 range vector in range query", "GET", "/api/v1/query_range?query=up%5B5m%5D&start=0&end=60&step=15", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": invalid expression type \"range vector\" for range query, must be Scalar or instant Vector"}`},
		{"H11 mixed time formats", "GET", "/api/v1/query_range?query=github_commits_total&start=" + day(1) + "&end=" + fmtUnix(fixtureBase.Add(48*time.Hour+500*time.Millisecond)) + "&step=1d", nil, 200,
			`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"github_commits_total"},"values":[[` + fmtUnix(fixtureBase.Add(24*time.Hour)) + `,"3"],[` + fmtUnix(fixtureBase.Add(48*time.Hour)) + `,"3"]]}]}}`},
		{"H12a no match", "GET", "/api/v1/series", nil, 400, `{"status":"error","errorType":"bad_data","error":"no match[] parameter provided"}`},
		{"H12b empty matcher", "GET", "/api/v1/series?match[]=" + url.QueryEscape(`{job=~".*"}`), nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"match[]\": match[] must contain at least one non-empty matcher"}`},
		{"H12c bad selector", "GET", "/api/v1/series?match[]=" + url.QueryEscape(`github_stars{`), nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"match[]\": 1:14: parse error: unexpected end of input inside braces"}`},
		{"H12d series union", "GET", "/api/v1/series?match[]=github_stars&match[]=" + url.QueryEscape(`probe_success{target="pypi"}`), nil, 200, `{"status":"success","data":[{"__name__":"github_stars","repo":"codemind"},{"__name__":"github_stars","repo":"savely"},{"__name__":"probe_success","target":"pypi"}]}`},
		{"H12e series window", "GET", "/api/v1/series?match[]=" + url.QueryEscape(`{__name__=~"pypi.*"}`) + "&start=" + day(0) + "&end=" + day(2), nil, 200, `{"status":"success","data":[]}`},
		{"H12f series POST", "POST", "/api/v1/series", url.Values{"match[]": {`{__name__="divy_build_info"}`}, "start": {""}, "end": {""}, "limit": {"40000"}}, 200, `{"status":"success","data":[{"__name__":"divy_build_info","commit":"abc1234","go_version":"…`},
		{"H13a utf8 label name is legal", "GET", "/api/v1/label/1bad/values", nil, 200, `{"status":"success","data":[]}`},
		{"H13b invalid utf8 label name", "GET", "/api/v1/label/%FF/values", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid label name: \"\\xff\""}`},
		{"H13c values of org", "GET", "/api/v1/label/org/values?match[]=github_merged_prs_total", nil, 200, `{"status":"success","data":["gradr","kubeflow","kubernetes"]}`},
		{"H13d labels bad matcher", "GET", "/api/v1/labels?match[]=" + url.QueryEscape(`{job=~".*"}`), nil, 400, `{"status":"error","errorType":"bad_data","error":"match[] must contain at least one non-empty matcher"}`},
		{"H16 buildinfo", "GET", "/api/v1/status/buildinfo", nil, 200, `{"status":"success","data":{"version":"v0.1.0-test","revision":"abc1234","branch":"…`},
		{"H17a rules record", "GET", "/api/v1/rules?type=record", nil, 200, `{"status":"success","data":{"groups":[]}}`},
		{"H17b rules bad type", "GET", "/api/v1/rules?type=x", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"type\": not supported value \"x\""}`},
		{"H18 alerts", "GET", "/api/v1/alerts", nil, 200, `{"status":"success","data":{"alerts":[]}}`},
		{"H19 exemplars", "GET", "/api/v1/query_exemplars?query=test&start=1788600600000&end=1788602400000", nil, 200, `{"status":"success","data":[]}`},
		{"H19b exemplars bad query", "GET", "/api/v1/query_exemplars?query=sum(&start=1&end=2", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": 1:5: parse error: unclosed left parenthesis"}`},
		{"H20a unknown path", "GET", "/api/v1/nope", nil, 404, `{"status":"error","errorType":"not_found","error":"path not found"}`},
		{"H20b trailing slash", "GET", "/api/v1/query/", nil, 404, `{"status":"error","errorType":"not_found","error":"path not found"}`},
		{"H20c wrong method", "DELETE", "/api/v1/query", nil, 405, `{"status":"error","errorType":"bad_data","error":"method not allowed"}`},
		{"H22a lookback ok", "GET", "/api/v1/query?query=up&lookback_delta=5m", nil, 200, `{"status":"success","data":{"resultType":"vector","result":[]}}`},
		{"H22b lookback bad", "GET", "/api/v1/query?query=up&lookback_delta=x", nil, 400, `{"status":"error","errorType":"bad_data","error":"error parsing lookback delta duration: cannot parse \"x\" to a valid duration"}`},
		{"X1 vector at day 3", "GET", "/api/v1/query?query=" + url.QueryEscape(`sum by (org) (github_merged_prs_total)`) + "&time=" + day(3), nil, 200, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"org":"gradr"},"value":[` + fmtUnix(fixtureBase.Add(72*time.Hour)) + `,"3"]},{"metric":{"org":"kubeflow"},"value":[` + fmtUnix(fixtureBase.Add(72*time.Hour)) + `,"2"]},{"metric":{"org":"kubernetes"},"value":[` + fmtUnix(fixtureBase.Add(72*time.Hour)) + `,"7"]}]}}`},
		{"X2 string result", "GET", "/api/v1/query?query=%22hello%22&time=4", nil, 200, `{"status":"success","data":{"resultType":"string","result":[4,"hello"]}}`},
		{"X3 range selector instant", "GET", "/api/v1/query?query=" + url.QueryEscape(`probe_success[2d]`) + "&time=" + day(2), nil, 200, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"probe_success","target":"pypi"},"values":[[` + fmtUnix(fixtureBase.Add(24*time.Hour)) + `,"0"],[` + fmtUnix(fixtureBase.Add(48*time.Hour)) + `,"1"]]}]}}`},
		{"X4 execution error", "GET", "/api/v1/query?query=" + url.QueryEscape(`{target="pypi"} + probe_success{target="pypi"}`) + "&time=" + day(3), nil, 422, `{"status":"error","errorType":"execution","error":"multiple matches for labels: many-to-one matching must be explicit (group_left/group_right)"}`},
		{"X5 limit truncation", "GET", "/api/v1/query?query=github_stars&time=" + day(3) + "&limit=1", nil, 200, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"github_stars","repo":"codemind"},"value":[` + fmtUnix(fixtureBase.Add(72*time.Hour)) + `,"13"]}]},"warnings":["results truncated due to limit"]}`},
		{"X6 bad limit", "GET", "/api/v1/query?query=up&limit=-1", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"limit\": limit must be non-negative"}`},
		{"X7 live series", "GET", "/api/v1/query?query=" + url.QueryEscape(`divy_open_to_work == 1`), nil, 200, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"divy_open_to_work"},"value":[` + fmtUnix(frozen) + `,"1"]}]}}`},
		{"X8 metadata one", "GET", "/api/v1/metadata?metric=github_stars", nil, 200, `{"status":"success","data":{"github_stars":[{"type":"gauge","help":"Current stargazer count of a repository.","unit":""}]}}`},
		{"X9 metadata bad limit", "GET", "/api/v1/metadata?limit=x", nil, 400, `{"status":"error","errorType":"bad_data","error":"limit must be a number"}`},
		{"X10 json body rejected", "POST", "/api/v1/query", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter: body must be application/x-www-form-urlencoded"}`},
		{"X11 range POST panel expression", "POST", "/api/v1/query_range", url.Values{"query": {"sum(increase(github_commits_total[7d]))"}, "start": {day(6)}, "end": {day(6)}, "step": {"3600"}}, 200, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[` + fmtUnix(fixtureBase.Add(6*24*time.Hour)) + `,"20"]]}]}}`},
		{"X12 timeout parameter", "GET", "/api/v1/query?query=up&timeout=x", nil, 400, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"timeout\": cannot parse \"x\" to a valid duration"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var res *http.Response
			var body []byte
			switch {
			case tc.method == "POST" && tc.form != nil:
				res, body = postForm(t, s, tc.path, tc.form, nil)
			case tc.method == "POST":
				req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"query":"up"}`))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				s.ServeHTTP(rec, req)
				res = rec.Result()
				body, _ = io.ReadAll(res.Body)
			default:
				res, body = do(t, s, tc.method, tc.path, nil)
			}
			got := strings.TrimSpace(string(body))
			if res.StatusCode != tc.status {
				t.Fatalf("status %d, want %d; body %s", res.StatusCode, tc.status, got)
			}
			if strings.HasSuffix(tc.body, "…") {
				if !strings.HasPrefix(got, strings.TrimSuffix(tc.body, "…")) {
					t.Fatalf("body prefix mismatch\n want: %s\n  got: %s", tc.body, got)
				}
			} else if got != tc.body {
				t.Fatalf("body mismatch\n want: %s\n  got: %s", tc.body, got)
			}
			if ct := res.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("content-type %q", ct)
			}
			if res.StatusCode >= 400 && res.Header.Get("Cache-Control") != CacheNS {
				t.Errorf("error cache-control %q", res.Header.Get("Cache-Control"))
			}
			if res.StatusCode == 405 && !strings.Contains(res.Header.Get("Allow"), "GET") {
				t.Errorf("405 without Allow: %q", res.Header.Get("Allow"))
			}
		})
	}
}

func fmtUnix(t time.Time) string {
	ms := t.UnixMilli()
	if frac := ms % 1000; frac != 0 {
		return fmt.Sprintf("%d.%03d", ms/1000, frac)
	}
	return strconv.FormatInt(ms/1000, 10)
}

func TestPromAPIHeadersAndCaching(t *testing.T) {
	s := newPromServer(t)
	res1, body1 := do(t, s, "GET", "/api/v1/query?query=github_stars&time="+day(3), nil)
	res2, body2 := do(t, s, "GET", "/api/v1/query?query=github_stars&time="+day(3), nil)
	if string(body1) != string(body2) || res1.Header.Get("ETag") == "" || res1.Header.Get("ETag") != res2.Header.Get("ETag") {
		t.Fatalf("etags: %q %q", res1.Header.Get("ETag"), res2.Header.Get("ETag"))
	}
	if cc := res1.Header.Get("Cache-Control"); cc != CacheQ15 {
		t.Errorf("cache-control %q", cc)
	}
	res3, body3 := do(t, s, "GET", "/api/v1/query?query=github_stars&time="+day(3), map[string]string{"If-None-Match": res1.Header.Get("ETag")})
	if res3.StatusCode != 304 || len(body3) != 0 || res3.Header.Get("ETag") != res1.Header.Get("ETag") {
		t.Errorf("304: status=%d len=%d etag=%q", res3.StatusCode, len(body3), res3.Header.Get("ETag"))
	}
	res4, _ := do(t, s, "GET", "/api/v1/rules", nil)
	if cc := res4.Header.Get("Cache-Control"); cc != CacheC60 {
		t.Errorf("rules cache-control %q", cc)
	}
	// HEAD mirrors GET
	res5, body5 := do(t, s, "HEAD", "/api/v1/status/buildinfo", nil)
	if res5.StatusCode != 200 || res5.Header.Get("Content-Type") != "application/json" {
		t.Errorf("HEAD buildinfo: %d %s", res5.StatusCode, body5)
	}
	// oversized POST body
	big := strings.NewReader("query=" + strings.Repeat("a", 1<<20+10))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query", big)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 413 || !strings.Contains(rec.Body.String(), "request body too large") {
		t.Errorf("413: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPromLabelsAndSeriesEndpoints(t *testing.T) {
	s := newPromServer(t)
	res, body := do(t, s, "GET", "/api/v1/labels", nil)
	var lr struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	decode(t, body, &lr)
	if res.StatusCode != 200 || lr.Status != "success" {
		t.Fatalf("labels: %d %s", res.StatusCode, body)
	}
	want := []string{"__name__", "commit", "go_version", "org", "package", "repo", "target", "version"}
	if strings.Join(lr.Data, ",") != strings.Join(want, ",") {
		t.Errorf("labels = %v, want %v", lr.Data, want)
	}
	res, body = postForm(t, s, "/api/v1/labels", url.Values{"match[]": {`{__name__="github_stars"}`}, "limit": {"40000"}, "start": {""}, "end": {""}}, nil)
	decode(t, body, &lr)
	if res.StatusCode != 200 || strings.Join(lr.Data, ",") != "__name__,repo" {
		t.Errorf("labels match: %d %v", res.StatusCode, lr.Data)
	}
	_, body = do(t, s, "GET", "/api/v1/label/__name__/values?limit=40000&start=&end=", nil)
	decode(t, body, &lr)
	names := strings.Join(lr.Data, ",")
	for _, n := range []string{"divy_build_info", "divy_experience_years", "divy_open_to_work", "divy_uptime_seconds", "github_commits_total", "github_stars", "reset_total"} {
		if !strings.Contains(","+names+",", ","+n+",") {
			t.Errorf("__name__ values missing %s: %s", n, names)
		}
	}
	_, body = do(t, s, "GET", "/api/v1/label/__name__/values?limit=2", nil)
	var lw struct {
		Data     []string `json:"data"`
		Warnings []string `json:"warnings"`
	}
	decode(t, body, &lw)
	if len(lw.Data) != 2 || len(lw.Warnings) != 1 || lw.Warnings[0] != "results truncated due to limit" {
		t.Errorf("limit: %s", body)
	}
	// series in a window that has samples
	res, body = do(t, s, "GET", "/api/v1/series?match[]=github_commits_total&start="+day(5)+"&end="+day(6), nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"__name__":"github_commits_total"`) {
		t.Errorf("series window: %d %s", res.StatusCode, body)
	}
	// rules: the full document
	res, body = do(t, s, "GET", "/api/v1/rules", nil)
	var rr struct {
		Data struct {
			Groups []struct {
				Name     string  `json:"name"`
				File     string  `json:"file"`
				Interval float64 `json:"interval"`
				Rules    []struct {
					State, Name, Query, Health, Type string
					Duration                         float64
					Labels                           map[string]string
					Alerts                           []any
				} `json:"rules"`
			} `json:"groups"`
		} `json:"data"`
	}
	decode(t, body, &rr)
	if res.StatusCode != 200 || len(rr.Data.Groups) != 1 || rr.Data.Groups[0].Name != "divy" || rr.Data.Groups[0].Interval != 15 || len(rr.Data.Groups[0].Rules) != 3 {
		t.Fatalf("rules: %d %s", res.StatusCode, body)
	}
	r0 := rr.Data.Groups[0].Rules[0]
	if r0.Name != "DivyAvailableForHire" || r0.State != "inactive" || r0.Health != "unknown" || r0.Type != "alerting" || r0.Duration != 30 || r0.Query != "divy_open_to_work == 1" || r0.Labels["severity"] != "page" || len(r0.Alerts) != 0 {
		t.Errorf("rule 0 = %+v", r0)
	}
	if q := rr.Data.Groups[0].Rules[1].Query; q != "sum(increase(github_commits_total[1w])) > 20" {
		t.Errorf("canonical query = %q", q)
	}
	res, body = do(t, s, "GET", "/api/v1/rules?rule_name[]=LFXApplicationPending", nil)
	decode(t, body, &rr)
	if res.StatusCode != 200 || len(rr.Data.Groups) != 1 || len(rr.Data.Groups[0].Rules) != 1 || rr.Data.Groups[0].Rules[0].Name != "LFXApplicationPending" {
		t.Errorf("rule_name filter: %s", body)
	}
	res, body = do(t, s, "GET", "/api/v1/rules?rule_group[]=nope", nil)
	if res.StatusCode != 200 || strings.TrimSpace(string(body)) != `{"status":"success","data":{"groups":[]}}` {
		t.Errorf("rule_group filter: %s", body)
	}
	res, body = do(t, s, "POST", "/api/v1/rules", nil)
	if res.StatusCode != 405 || !strings.Contains(res.Header.Get("Allow"), "GET") {
		t.Errorf("POST rules: %d %s", res.StatusCode, body)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	s := newPromServer(t)
	// generate an HTTP request first so divy_http_requests_total has a sample
	do(t, s, "GET", "/healthz", nil)
	res, body := do(t, s, "GET", "/metrics", nil)
	if res.StatusCode != 200 {
		t.Fatalf("metrics: %d %s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4; charset=utf-8") {
		t.Errorf("content-type %q", ct)
	}
	if cc := res.Header.Get("Cache-Control"); cc != CacheNS {
		t.Errorf("cache-control %q", cc)
	}
	text := string(body)
	for _, want := range []string{
		"# HELP github_stars Current stargazer count of a repository.\n# TYPE github_stars gauge\ngithub_stars{repo=\"codemind\"} 13\ngithub_stars{repo=\"savely\"} 42\n",
		"probe_success{target=\"pypi\"} 1\n",
		"pypi_downloads_total{package=\"codemind-ci\"} 200\n",
		"divy_open_to_work 1\n",
		"divy_build_info{commit=\"abc1234\",go_version=\"",
		"divy_collector_last_success_timestamp_seconds{collector=\"uptime\"} " + strconv.FormatFloat(float64(frozen.Add(-3*time.Minute).Unix()), 'g', -1, 64) + "\n",
		"divy_http_requests_total{code=\"200\",method=\"GET\",route=\"/healthz\"} 1\n",
		"# TYPE go_goroutines gauge\n",
		"# TYPE process_cpu_seconds_total counter\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("exposition lacks %q", want)
		}
	}
	for _, absent := range []string{"github_followers", "reset_total"} {
		if strings.Contains(text, absent) {
			t.Errorf("exposition must not contain %q (stale or outside the catalogue)", absent)
		}
	}
	if res, _ := do(t, s, "POST", "/metrics", nil); res.StatusCode != 405 {
		t.Errorf("POST /metrics = %d", res.StatusCode)
	}
}
