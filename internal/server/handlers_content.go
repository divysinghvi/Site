package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleContentServices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, CacheC60, s.cfg.Content.ServicesView())
}

func (s *Server) handleContentSpans(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, CacheC60, s.cfg.Content.Spans)
}

func (s *Server) handleContentLogs(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "application/x-ndjson")
	h.Set("Cache-Control", CacheC60)
	h.Set("Content-Length", fmtInt(len(s.cfg.Content.LogsRaw)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.cfg.Content.LogsRaw)
}

func (s *Server) handlePostmortems(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, CacheC60, s.cfg.Content.PostmortemList(s.cfg.SiteOrigin))
}

func (s *Server) handlePostmortem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if strings.HasSuffix(id, ".md") {
		pm, ok := s.cfg.Content.Postmortem(strings.TrimSuffix(id, ".md"))
		if !ok {
			writeError(w, http.StatusNotFound, "postmortem not found")
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/markdown; charset=utf-8")
		h.Set("Cache-Control", CacheC60)
		h.Set("Content-Length", fmtInt(len(pm.Markdown)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(pm.Markdown))
		return
	}
	v, ok := s.cfg.Content.PostmortemView(id, s.cfg.SiteOrigin)
	if !ok {
		writeError(w, http.StatusNotFound, "postmortem not found")
		return
	}
	writeJSON(w, http.StatusOK, CacheC60, v)
}

func (s *Server) handleContentPanels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, CacheC60, s.cfg.Content.Panels)
}

func (s *Server) handleContentAlerts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, CacheC60, s.cfg.Content.Alerts)
}

func (s *Server) handleContentUptime(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, CacheC60, s.cfg.Content.UptimeView())
}

func (s *Server) handleContentManual(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, CacheC60, s.cfg.Content.ManualView())
}

func (s *Server) handleContentProfile(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, CacheC60, s.cfg.Content.ProfileView(s.now()))
}

func (s *Server) handleContentTodos(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, CacheC60, s.cfg.Content.TodosView(s.now().Format(time.RFC3339)))
}
