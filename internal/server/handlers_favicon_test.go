package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"divy.dev/internal/collector"
	"divy.dev/internal/store"
)

func TestFaviconNoData(t *testing.T) {
	s, _ := newTestServer(t, true)
	res, body := do(t, s, "GET", "/favicon.svg", nil)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/svg+xml" || res.Header.Get("Cache-Control") != CacheA3600 {
		t.Fatalf("status=%d headers=%v", res.StatusCode, res.Header)
	}
	b := string(body)
	if !strings.Contains(b, "<!-- no github samples yet") || strings.Contains(b, "<polyline") || !strings.Contains(b, `viewBox="0 0 32 32"`) || !strings.Contains(b, `stroke="#5b6069"`) {
		t.Errorf("no-data body:\n%s", b)
	}
	etag := res.Header.Get("ETag")
	if len(etag) != 66 || !strings.HasPrefix(etag, `"`) {
		t.Errorf("etag = %q", etag)
	}
	res304, _ := do(t, s, "GET", "/favicon.svg", map[string]string{"If-None-Match": etag})
	if res304.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match: %d", res304.StatusCode)
	}
	// favicon.ico is a cacheable 404, never a static sparkline
	res, body = do(t, s, "GET", "/favicon.ico", nil)
	if res.StatusCode != 404 || !strings.Contains(string(body), "/favicon.svg") || !strings.Contains(res.Header.Get("Cache-Control"), "max-age=86400") {
		t.Errorf("favicon.ico: %d %v %s", res.StatusCode, res.Header, body)
	}
}

// TestFaviconSparkline seeds the LogQL §L.6.3 example (counts 3 0 5 2 7 1 4
// for 2026-08-30..2026-09-05) as a cumulative github_commits_total series
// and expects the exact polyline of the draft.
func TestFaviconSparkline(t *testing.T) {
	s, st := newTestServer(t, true)
	// the server clock is frozen at 2026-09-05T00:00:00Z; run "now" is 00:00 so the live sample sits 1ms later
	ctx := context.Background()
	day := func(d string) time.Time { x, _ := time.Parse("2006-01-02", d); return x }
	counts := collector.DailyCounts{"2026-08-30": 3, "2026-08-31": 0, "2026-09-01": 5, "2026-09-02": 2, "2026-09-03": 7, "2026-09-04": 1, "2026-09-05": 4}
	// base 10 before the window (frozen prefix) to prove differences, not totals, are drawn
	grid, live := collector.CounterSamples(counts, day("2026-08-30"), frozen.Add(time.Millisecond), 10, true)
	id, err := st.EnsureSeries(ctx, "github_commits_total", nil)
	if err != nil {
		t.Fatal(err)
	}
	prior := store.Sample{TsMs: collector.DayEnd(day("2026-08-29")), Value: 10}
	if err := st.UpsertSamples(ctx, id, append(append([]store.Sample{prior}, grid...), live)); err != nil {
		t.Fatal(err)
	}
	s.cfg.Now = func() time.Time { return frozen.Add(time.Millisecond) }
	res, body := do(t, s, "GET", "/favicon.svg", nil)
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	b := string(body)
	want := []string{
		"<!-- github commits per UTC day, 2026-08-30..2026-09-05: 3 0 5 2 7 1 4 -->",
		`<rect width="32" height="32" rx="6" fill="#0b0c0e"/>`,
		`<polyline fill="none" stroke="#73bf69" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" points="3,18.4 7.3,27 11.7,12.7 16,21.3 20.3,7 24.7,24.1 29,15.6"/>`,
		`<circle cx="29" cy="15.6" r="2" fill="#73bf69"/>`,
	}
	for _, w := range want {
		if !strings.Contains(b, w) {
			t.Errorf("missing %s in\n%s", w, b)
		}
	}
	if strings.Contains(b, "no github samples") {
		t.Error("data present but the no-data comment was emitted")
	}
	// HEAD carries the same headers without a body (through GetHead)
	res, body = do(t, s, "HEAD", "/favicon.svg", nil)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/svg+xml" {
		t.Errorf("HEAD: %d %v (%d bytes)", res.StatusCode, res.Header, len(body))
	}
}
