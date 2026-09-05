package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"divy.dev/internal/model"
	"divy.dev/internal/store"
)

func seedProbes(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	ms := func(d time.Duration) int64 { return frozen.Add(-d).UnixMilli() }
	lat := func(v float64) *float64 { return &v }
	errTimeout := "timeout: context deadline exceeded"
	errHTTP := "http: got 503, want 2xx or 3xx"
	rows := []store.Probe{
		// github-profile: 100 days ago (outside 90d), then an incident of 3 failed probes 2 days ago, then up
		{Target: "github-profile", TsMs: ms(100 * 24 * time.Hour), Up: true, LatencyMs: lat(100), StatusCode: 200},
		{Target: "github-profile", TsMs: ms(48*time.Hour + 15*time.Minute), Up: true, LatencyMs: lat(120), StatusCode: 200},
		{Target: "github-profile", TsMs: ms(48 * time.Hour), Up: false, LatencyMs: lat(10000), StatusCode: 0, Error: &errTimeout},
		{Target: "github-profile", TsMs: ms(48*time.Hour - 5*time.Minute), Up: false, LatencyMs: lat(10000), StatusCode: 0, Error: &errTimeout},
		{Target: "github-profile", TsMs: ms(48*time.Hour - 10*time.Minute), Up: false, LatencyMs: lat(300), StatusCode: 503, Error: &errHTTP},
		{Target: "github-profile", TsMs: ms(48*time.Hour - 15*time.Minute), Up: true, LatencyMs: lat(140), StatusCode: 200},
		// a single failed probe (a blip, not an incident), then up again
		{Target: "github-profile", TsMs: ms(3 * time.Hour), Up: false, LatencyMs: lat(9000), StatusCode: 0, Error: &errTimeout},
		{Target: "github-profile", TsMs: ms(2 * time.Hour), Up: true, LatencyMs: lat(160), StatusCode: 200},
		{Target: "github-profile", TsMs: ms(time.Hour), Up: true, LatencyMs: lat(180), StatusCode: 200},
		// self-api: ongoing incident (two failures at the tail)
		{Target: "self-api", TsMs: ms(20 * time.Minute), Up: true, LatencyMs: lat(5), StatusCode: 200},
		{Target: "self-api", TsMs: ms(10 * time.Minute), Up: false, LatencyMs: lat(5000), StatusCode: 0, Error: &errTimeout},
		{Target: "self-api", TsMs: ms(5 * time.Minute), Up: false, LatencyMs: lat(5000), StatusCode: 0, Error: &errTimeout},
	}
	if err := st.WriteProbeResults(ctx, rows); err != nil {
		t.Fatal(err)
	}
}

func TestUptimeHeartbeats(t *testing.T) {
	s, st := newTestServer(t, true)
	seedProbes(t, st)
	res, body := do(t, s, "GET", "/api/uptime", nil)
	if res.StatusCode != 200 || res.Header.Get("Cache-Control") != CacheC60 || !strings.HasPrefix(res.Header.Get("ETag"), `W/"`) {
		t.Fatalf("status=%d headers=%v", res.StatusCode, res.Header)
	}
	// the alias is byte-identical to the explicit query
	_, body2 := do(t, s, "GET", "/api/uptime/heartbeats?days=90&bucket=1d", nil)
	if string(body) != string(body2) {
		t.Errorf("alias body differs:\n%s\n%s", body, body2)
	}
	var hb model.UptimeHeartbeats
	decode(t, body, &hb)
	if hb.Days != 90 || hb.Bucket != "1d" || hb.GeneratedAt != "2026-09-05T00:00:00Z" || len(hb.Targets) != 5 {
		t.Fatalf("envelope = %+v", hb)
	}
	byID := map[string]model.HeartbeatTarget{}
	for _, tg := range hb.Targets {
		byID[tg.Target] = tg
	}
	// unconfigured target: never green, TODO url verbatim, empty arrays
	sv := byID["savely-landing"]
	if sv.Status != "unconfigured" || sv.URL != "TODO(divy)" || sv.Last != nil || sv.Uptime.D90 != nil || len(sv.Buckets) != 0 || len(sv.Incidents) != 0 || sv.Span == nil || *sv.Span != "project.savely" {
		t.Errorf("savely-landing = %+v", sv)
	}
	// configured target with no probes: unknown, ratios null
	if p := byID["pypi-codemind"]; p.Status != "unknown" || p.Last != nil || p.Uptime.H24 != nil || len(p.Buckets) != 0 {
		t.Errorf("pypi-codemind = %+v", p)
	}
	gh := byID["github-profile"]
	if gh.Status != "up" || gh.Last == nil || !gh.Last.Up || gh.Last.LatencyMs != 180 || gh.Last.TS != "2026-09-04T23:00:00Z" || gh.Last.Error != nil {
		t.Errorf("github-profile last = %+v (%+v)", gh.Last, gh)
	}
	// windows: 24h = 2/3 up; 7d, 30d and 90d = 4/8 (the 100-day-old row is outside the window)
	if gh.Uptime.H24 == nil || *gh.Uptime.H24 < 0.66 || *gh.Uptime.H24 > 0.67 || gh.Uptime.D90 == nil || *gh.Uptime.D90 != 0.5 || *gh.Uptime.D7 != 0.5 || *gh.Uptime.D30 != 0.5 {
		t.Errorf("github-profile uptime = %v %v %v %v", *gh.Uptime.H24, *gh.Uptime.D7, *gh.Uptime.D30, *gh.Uptime.D90)
	}
	// buckets: only days with probes (3 of 90), ascending; the 09-03 day holds the incident
	if len(gh.Buckets) != 3 || gh.Buckets[0].TS != "2026-09-02T00:00:00Z" || gh.Buckets[0].Samples != 1 || gh.Buckets[1].TS != "2026-09-03T00:00:00Z" || gh.Buckets[1].Samples != 4 || gh.Buckets[1].UpRatio != 0.25 || gh.Buckets[1].MaxLatencyMs != 10000 || gh.Buckets[1].AvgLatencyMs != 5110 {
		t.Errorf("github-profile buckets = %+v", gh.Buckets)
	}
	if gh.Buckets[2].TS != "2026-09-04T00:00:00Z" || gh.Buckets[2].Samples != 3 {
		t.Errorf("github-profile bucket[2] = %+v", gh.Buckets[2])
	}
	// incidents: the 3-probe run only (the single failed probe is a blip)
	if len(gh.Incidents) != 1 {
		t.Fatalf("github-profile incidents = %+v", gh.Incidents)
	}
	inc := gh.Incidents[0]
	if inc.StartedAt != "2026-09-03T00:00:00Z" || inc.EndedAt == nil || *inc.EndedAt != "2026-09-03T00:15:00Z" || inc.DurationS != 900 || inc.Probes != 3 || inc.FirstError != "timeout: context deadline exceeded" {
		t.Errorf("incident = %+v", inc)
	}
	// ongoing incident on self-api: ended_at null, duration to now, status down, note passed through
	self := byID["self-api"]
	if self.Status != "down" || len(self.Incidents) != 1 || self.Incidents[0].EndedAt != nil || self.Incidents[0].DurationS != 600 || self.Incidents[0].Probes != 2 {
		t.Errorf("self-api = %+v incidents=%+v", self, self.Incidents)
	}
	if self.Note == nil || !strings.Contains(*self.Note, "probed from the same function") {
		t.Errorf("self-api note = %v", self.Note)
	}

	// ETag → 304
	res304, _ := do(t, s, "GET", "/api/uptime", map[string]string{"If-None-Match": res.Header.Get("ETag")})
	if res304.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match: status=%d", res304.StatusCode)
	}

	// hourly buckets over 2 days; 30d/90d windows are null when days < window
	_, body = do(t, s, "GET", "/api/uptime/heartbeats?days=2&bucket=1h", nil)
	decode(t, body, &hb)
	for _, tg := range hb.Targets {
		if tg.Target == "github-profile" {
			if tg.Uptime.D30 != nil || tg.Uptime.D90 != nil || tg.Uptime.D7 != nil || tg.Uptime.H24 == nil {
				t.Errorf("2-day windows = %+v", tg.Uptime)
			}
			if len(tg.Buckets) != 4 || tg.Buckets[0].TS != "2026-09-03T00:00:00Z" || tg.Buckets[0].Samples != 4 || tg.Buckets[1].TS != "2026-09-04T21:00:00Z" {
				t.Errorf("hourly buckets = %+v", tg.Buckets)
			}
		}
	}

	// parameter validation
	for _, c := range []struct{ q, want string }{
		{"days=0", "days must be between 1 and 90"},
		{"days=91", "days must be between 1 and 90"},
		{"days=x", "days must be between 1 and 90"},
		{"days=8&bucket=1h", "bucket=1h requires days<=7"},
		{"bucket=1w", "bucket must be 1d or 1h"},
	} {
		res, body := do(t, s, "GET", "/api/uptime/heartbeats?"+c.q, nil)
		var pe model.PlainError
		decode(t, body, &pe)
		if res.StatusCode != 400 || pe.Error != c.want || res.Header.Get("Cache-Control") != CacheNS {
			t.Errorf("%s: status=%d body=%s", c.q, res.StatusCode, body)
		}
	}
}

func TestReadyzStorageAndCollectors(t *testing.T) {
	s, _ := newTestServer(t, true)
	s.cfg.Storage = "ephemeral"
	_, body := do(t, s, "GET", "/readyz", nil)
	var r model.Readyz
	decode(t, body, &r)
	if r.Checks.DB.Storage != "ephemeral" {
		t.Errorf("storage = %q", r.Checks.DB.Storage)
	}
}
