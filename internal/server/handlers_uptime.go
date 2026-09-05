package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"divy.dev/internal/content"
	"divy.dev/internal/model"
	"divy.dev/internal/store"
)

// Uptime endpoint limits (contract K.1.4).
const (
	uptimeMaxDays     = 90
	uptimeHourlyMax   = 7
	uptimeMinIncident = 2 // consecutive failed probes that make an incident
)

// handleUptime is GET /api/uptime: byte-identical to /api/uptime/heartbeats?days=90&bucket=1d.
func (s *Server) handleUptime(w http.ResponseWriter, r *http.Request) {
	s.serveHeartbeats(w, r, uptimeMaxDays, "1d")
}

// handleUptimeHeartbeats is GET /api/uptime/heartbeats?days=1..90&bucket=1d|1h.
func (s *Server) handleUptimeHeartbeats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days := uptimeMaxDays
	if v := q.Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > uptimeMaxDays {
			writeError(w, http.StatusBadRequest, "days must be between 1 and 90")
			return
		}
		days = n
	}
	bucket := q.Get("bucket")
	switch bucket {
	case "":
		bucket = "1d"
	case "1d":
	case "1h":
		if days > uptimeHourlyMax {
			writeError(w, http.StatusBadRequest, "bucket=1h requires days<=7")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "bucket must be 1d or 1h")
		return
	}
	s.serveHeartbeats(w, r, days, bucket)
}

func (s *Server) serveHeartbeats(w http.ResponseWriter, r *http.Request, days int, bucket string) {
	body, err := s.Heartbeats(r.Context(), s.now(), days, bucket)
	if err != nil {
		s.log.Warn("uptime: heartbeats", "err", err.Error(), "req", RequestID(r.Context()))
		writeError(w, http.StatusInternalServerError, "internal error: "+RequestID(r.Context()))
		return
	}
	writeJSONETag(w, r, CacheC60, body)
}

// writeJSONETag writes a 200 JSON body with a weak ETag (first 16 hex of
// sha256) and answers 304 to a matching If-None-Match.
func writeJSONETag(w http.ResponseWriter, r *http.Request, cacheControl string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error: encoding")
		return
	}
	sum := sha256.Sum256(b)
	etag := `W/"` + hex.EncodeToString(sum[:8]) + `"`
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", cacheControl)
	h.Set("ETag", etag)
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// Heartbeats builds the UptimeHeartbeats body: every target of
// content/uptime.yaml in file order with its state, last probe, uptime
// ratios, per-bucket rollup (only buckets that have probes — the page paints
// the rest grey, never green) and incidents (runs of at least two
// consecutive failed probes).
func (s *Server) Heartbeats(ctx context.Context, now time.Time, days int, bucket string) (model.UptimeHeartbeats, error) {
	out := model.UptimeHeartbeats{GeneratedAt: now.Format(time.RFC3339), Days: days, Bucket: bucket, Targets: []model.HeartbeatTarget{}}
	since := now.Add(-time.Duration(days) * 24 * time.Hour)
	for _, t := range s.cfg.Content.Uptime.Targets {
		v := content.TargetView(t)
		ht := model.HeartbeatTarget{Target: v.ID, Name: v.Name, URL: v.URL, Span: v.Span, Note: v.Note, Status: "unconfigured", Buckets: []model.HeartbeatBucket{}, Incidents: []model.UptimeIncident{}}
		if !v.Configured || s.cfg.Store == nil {
			out.Targets = append(out.Targets, ht)
			continue
		}
		ht.Status = "unknown"
		last, ok, err := s.cfg.Store.LastProbe(ctx, v.ID)
		if err != nil {
			return out, err
		}
		if ok {
			ht.Last = probeLast(last)
			if last.Up {
				ht.Status = "up"
			} else {
				ht.Status = "down"
			}
		}
		rows, err := s.cfg.Store.ReadProbes(ctx, v.ID, since.UnixMilli())
		if err != nil {
			return out, err
		}
		ht.Uptime = uptimeWindows(rows, now, days)
		ht.Buckets = heartbeatBuckets(rows, bucket)
		ht.Incidents = incidents(rows, now)
		out.Targets = append(out.Targets, ht)
	}
	return out, nil
}

func probeLast(p store.Probe) *model.ProbeLast {
	l := &model.ProbeLast{TS: time.UnixMilli(p.TsMs).UTC().Format(time.RFC3339), Up: p.Up, StatusCode: p.StatusCode, Error: p.Error}
	if p.LatencyMs != nil {
		l.LatencyMs = *p.LatencyMs
	}
	return l
}

// uptimeWindows computes sum(up)/count over the trailing windows; a window
// longer than the requested days, or without probes, is null.
func uptimeWindows(rows []store.Probe, now time.Time, days int) model.UptimeWindows {
	ratio := func(d time.Duration) *float64 {
		if d > time.Duration(days)*24*time.Hour {
			return nil
		}
		cut := now.Add(-d).UnixMilli()
		n, up := 0, 0
		for _, p := range rows {
			if p.TsMs < cut {
				continue
			}
			n++
			if p.Up {
				up++
			}
		}
		if n == 0 {
			return nil
		}
		r := float64(up) / float64(n)
		return &r
	}
	return model.UptimeWindows{H24: ratio(24 * time.Hour), D7: ratio(7 * 24 * time.Hour), D30: ratio(30 * 24 * time.Hour), D90: ratio(90 * 24 * time.Hour)}
}

// heartbeatBuckets groups probes by UTC day (or hour), ascending; only
// buckets with probes are returned.
func heartbeatBuckets(rows []store.Probe, bucket string) []model.HeartbeatBucket {
	size := int64(86_400_000)
	if bucket == "1h" {
		size = 3_600_000
	}
	out := []model.HeartbeatBucket{}
	var cur *model.HeartbeatBucket
	var curTs int64 = -1
	var latSum float64
	var latN int
	flush := func() {
		if cur != nil {
			if latN > 0 {
				cur.AvgLatencyMs = latSum / float64(latN)
			}
			out = append(out, *cur)
		}
	}
	for _, p := range rows {
		ts := (p.TsMs / size) * size
		if ts != curTs {
			flush()
			curTs = ts
			cur = &model.HeartbeatBucket{TS: time.UnixMilli(ts).UTC().Format(time.RFC3339)}
			latSum, latN = 0, 0
		}
		cur.Samples++
		if p.Up {
			cur.UpRatio++
		}
		if p.LatencyMs != nil {
			latSum += *p.LatencyMs
			latN++
			if *p.LatencyMs > cur.MaxLatencyMs {
				cur.MaxLatencyMs = *p.LatencyMs
			}
		}
	}
	flush()
	for i := range out {
		out[i].UpRatio /= float64(out[i].Samples)
	}
	return out
}

// incidents returns the maximal runs of consecutive failed probes with at
// least uptimeMinIncident probes, newest first; an ongoing run has ended_at
// null and its duration measured to now.
func incidents(rows []store.Probe, now time.Time) []model.UptimeIncident {
	var runs []model.UptimeIncident
	start, count := -1, 0
	flush := func(endTs int64, ongoing bool) {
		if start < 0 || count < uptimeMinIncident {
			return
		}
		first := rows[start]
		inc := model.UptimeIncident{StartedAt: time.UnixMilli(first.TsMs).UTC().Format(time.RFC3339), Probes: count}
		if first.Error != nil {
			inc.FirstError = *first.Error
		}
		if ongoing {
			inc.DurationS = int64(now.Sub(time.UnixMilli(first.TsMs)).Seconds())
		} else {
			e := time.UnixMilli(endTs).UTC().Format(time.RFC3339)
			inc.EndedAt = &e
			inc.DurationS = (endTs - first.TsMs) / 1000
		}
		if inc.DurationS < 0 {
			inc.DurationS = 0
		}
		runs = append(runs, inc)
	}
	for i, p := range rows {
		if !p.Up {
			if start < 0 {
				start = i
			}
			count++
			continue
		}
		flush(p.TsMs, false)
		start, count = -1, 0
	}
	flush(0, true)
	out := make([]model.UptimeIncident, 0, len(runs))
	for i := len(runs) - 1; i >= 0; i-- {
		out = append(out, runs[i])
	}
	return out
}
