package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"divy.dev/internal/ascii"
	"divy.dev/internal/model"
)

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, CacheNS, s.cfg.Content.Healthz())
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	c := s.cfg.Content
	body := model.Readyz{
		Status:  "ok",
		Version: s.cfg.Version,
		Commit:  s.cfg.Commit,
		UptimeS: int64(now.Sub(s.cfg.StartedAt).Seconds()),
	}
	body.Checks.Content = model.ReadyzContent{OK: true, Files: c.Files, Spans: len(c.Nodes()), LogLines: len(c.Logs), Todos: len(c.Todos), LoadedAt: c.LoadedAt.UTC().Format(time.RFC3339)}
	body.Checks.Collectors = map[string]model.ReadyzCollector{}
	status := http.StatusOK
	if s.cfg.Store != nil {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		lat, err := s.cfg.Store.Ping(ctx)
		cancel()
		body.Checks.DB = model.ReadyzDB{OK: err == nil, LatencyMs: float64(lat.Microseconds()) / 1000}
		if err != nil {
			body.Checks.DB.Error = err.Error()
			body.Status = "unavailable"
			status = http.StatusServiceUnavailable
		}
	} else {
		body.Checks.DB = model.ReadyzDB{OK: false, Error: "no store configured"}
		body.Status = "unavailable"
		status = http.StatusServiceUnavailable
	}
	if s.cfg.Runner != nil {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		cols, err := s.cfg.Runner.Readiness(ctx, now)
		cancel()
		if err == nil {
			body.Checks.Collectors = cols
		} else {
			s.log.Warn("readyz: collectors", "err", err.Error())
		}
	}
	if s.cfg.ShuttingDown != nil && s.cfg.ShuttingDown() {
		body.Status = "shutting_down"
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, CacheNS, body)
}

func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	o := s.cfg.SiteOrigin
	body := "# Observability for humans: /metrics\n" +
		"# Also: /healthz  /readyz  /api/traces/career  /loki/api/v1/labels\n" +
		"# Try: curl -H 'Accept: text/plain' " + o + "/\n" +
		"User-agent: *\n" +
		"Allow: /\n" +
		"Disallow: /api/\n" +
		"Disallow: /loki/\n" +
		"Sitemap: " + o + "/sitemap.xml\n"
	writeText(w, http.StatusOK, CacheA3600, body)
}

func (s *Server) renderASCII(width int) string {
	return ascii.Render(s.cfg.Content, ascii.Options{Width: width, Now: s.now(), Origin: s.cfg.SiteOrigin})
}

func (s *Server) handleASCII(w http.ResponseWriter, r *http.Request) {
	width := 80
	if v := r.URL.Query().Get("width"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			width = ascii.ClampWidth(n)
		}
	}
	writeText(w, http.StatusOK, CacheC60, s.renderASCII(width))
}

// handleIndex negotiates `/`: text/plain (or ?format=ascii) → the ASCII
// waterfall; otherwise the embedded index.html, or the JSON hint when no site
// is embedded.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Accept")
	if r.URL.Query().Get("format") == "ascii" || wantsText(r.Header.Get("Accept")) {
		writeText(w, http.StatusOK, CacheC60, s.renderASCII(80))
		return
	}
	if s.static.hasIndex() {
		s.static.serveIndex(w, r)
		return
	}
	writeJSON(w, http.StatusNotFound, CacheNS, model.PlainError{Error: "web assets not embedded; run `make web-build` (or use the Vite dev server on :5173); try: curl -H 'Accept: text/plain' " + s.cfg.SiteOrigin + "/"})
}

// handleCollect runs one bounded collection round (GET|POST /api/collect).
func (s *Server) handleCollect(w http.ResponseWriter, r *http.Request) {
	if !s.bearerOK(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="divy collect"`)
		writeError(w, http.StatusUnauthorized, "unauthorized: send Authorization: Bearer $DIVY_COLLECT_TOKEN")
		return
	}
	if s.cfg.Runner == nil {
		writeError(w, http.StatusServiceUnavailable, "collectors are not configured")
		return
	}
	budget := s.cfg.CollectBudget
	// The round must finish inside the platform's function limit; the caller
	// cannot widen the budget, only narrow it.
	if v := r.URL.Query().Get("budget"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 && d < budget {
			budget = d
		}
	}
	summary := s.cfg.Runner.RunRound(r.Context(), budget)
	writeJSON(w, http.StatusOK, CacheNS, summary)
}

func fmtInt(n int) string { return fmt.Sprint(n) }
