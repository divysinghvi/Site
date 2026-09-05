package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"divy.dev/internal/collector"
	"divy.dev/internal/store"
)

// faviconDays is the number of daily commit counts drawn (today included).
const faviconDays = 7

// Favicon colours (brief §5 palette).
const (
	faviconBG     = "#0b0c0e"
	faviconStroke = "#73bf69"
	faviconFlat   = "#5b6069"
)

// handleFavicon is GET /favicon.svg: a 32×32 sparkline of the last seven
// UTC days of github_commits_total (daily differences of the stored grid;
// today from the live sample). With no github_commits_total samples at all
// it draws a flat grey baseline and says so in a comment — never a fake
// curve.
func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	counts, have, err := s.commitCounts(r.Context(), now)
	if err != nil {
		s.log.Warn("favicon: commit counts", "err", err.Error(), "req", RequestID(r.Context()))
		have = false
	}
	body := faviconSVG(counts, have, collector.DayOf(now).AddDate(0, 0, -(faviconDays-1)), collector.DayOf(now))
	sum := sha256.Sum256([]byte(body))
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	h := w.Header()
	h.Set("Content-Type", "image/svg+xml")
	h.Set("Cache-Control", CacheA3600)
	h.Set("ETag", etag)
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// handleFaviconICO is GET /favicon.ico: 404. The only favicon is the live
// SVG; a committed .ico would be a static sparkline (review coverage-17).
// The 404 is cacheable for a day so browsers stop asking.
func (s *Server) handleFaviconICO(w http.ResponseWriter, r *http.Request) {
	body := `{"error":"not found: the favicon is the live sparkline at /favicon.svg"}`
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "public, max-age=86400, s-maxage=86400")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(body))
}

// commitCounts derives the commits of the last faviconDays UTC days from
// the github_commits_total series: count(d) = grid(dayEnd(d)) − grid(dayEnd(d−1))
// for complete days, count(today) = live − grid(dayEnd(yesterday)); a
// missing grid point counts as 0. have is false when the series has no
// samples at all.
func (s *Server) commitCounts(ctx context.Context, now time.Time) ([faviconDays]float64, bool, error) {
	var counts [faviconDays]float64
	if s.cfg.Store == nil {
		return counts, false, nil
	}
	today := collector.DayOf(now)
	first := today.AddDate(0, 0, -faviconDays) // the grid point before the first drawn day
	data, err := s.cfg.Store.QueryRange(ctx, []store.Matcher{{Name: "__name__", Type: store.MatchEqual, Value: "github_commits_total"}}, collector.DayEnd(first)-1, now.UnixMilli()+1)
	if err != nil {
		return counts, false, err
	}
	grid := map[int64]float64{}
	var live *store.Sample
	n := 0
	for _, sd := range data {
		for _, sm := range sd.Samples {
			n++
			if collector.IsGrid(sm.TsMs) {
				grid[sm.TsMs] = sm.Value
			} else if live == nil || sm.TsMs > live.TsMs {
				v := sm
				live = &v
			}
		}
	}
	if n == 0 {
		return counts, false, nil
	}
	for i := 0; i < faviconDays; i++ {
		d := today.AddDate(0, 0, i-(faviconDays-1))
		prev, okPrev := grid[collector.DayEnd(d.AddDate(0, 0, -1))]
		var cur float64
		okCur := false
		if i == faviconDays-1 {
			if live != nil {
				cur, okCur = live.Value, true
			}
		} else {
			cur, okCur = grid[collector.DayEnd(d)]
		}
		if okPrev && okCur && cur >= prev {
			counts[i] = cur - prev
		}
	}
	return counts, true, nil
}

// faviconSVG renders the sparkline (LogQL §L.6.3 geometry: x_i = 3 + i×26/6,
// y_i = 27 − v_i/max(1, max v) × 20, one decimal).
func faviconSVG(counts [faviconDays]float64, have bool, from, to time.Time) string {
	var sb strings.Builder
	sb.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" width="32" height="32">` + "\n")
	if !have {
		sb.WriteString("<!-- no github samples yet: github_commits_total has no series (GitHub collector disabled or never run) -->\n")
		sb.WriteString(`<rect width="32" height="32" rx="6" fill="` + faviconBG + `"/>` + "\n")
		sb.WriteString(`<line x1="3" y1="27" x2="29" y2="27" stroke="` + faviconFlat + `" stroke-width="2.5" stroke-linecap="round"/>` + "\n")
		sb.WriteString("</svg>\n")
		return sb.String()
	}
	maxV := 1.0
	parts := make([]string, 0, faviconDays)
	for _, v := range counts {
		if v > maxV {
			maxV = v
		}
		parts = append(parts, strconv.FormatFloat(v, 'f', -1, 64))
	}
	sb.WriteString(fmt.Sprintf("<!-- github commits per UTC day, %s..%s: %s -->\n", from.Format("2006-01-02"), to.Format("2006-01-02"), strings.Join(parts, " ")))
	sb.WriteString(`<rect width="32" height="32" rx="6" fill="` + faviconBG + `"/>` + "\n")
	points := make([]string, 0, faviconDays)
	var lastX, lastY string
	for i, v := range counts {
		x := 3 + float64(i)*26/6
		y := 27 - v/maxV*20
		lastX, lastY = fmtCoord(x), fmtCoord(y)
		points = append(points, lastX+","+lastY)
	}
	sb.WriteString(`<polyline fill="none" stroke="` + faviconStroke + `" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" points="` + strings.Join(points, " ") + `"/>` + "\n")
	sb.WriteString(`<circle cx="` + lastX + `" cy="` + lastY + `" r="2" fill="` + faviconStroke + `"/>` + "\n")
	sb.WriteString("</svg>\n")
	return sb.String()
}

// fmtCoord prints a coordinate with at most one decimal (27 not 27.0, 18.4).
func fmtCoord(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}
