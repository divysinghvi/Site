package server

import (
	"net/http"
	"strings"
)

// CORS values (contract §K.3.3).
const (
	corsAllowMethods  = "GET, POST, HEAD, OPTIONS"
	corsAllowHeaders  = "Accept, Content-Type, If-None-Match, X-Requested-With"
	corsExposeHeaders = "X-Divy-Trace-Id, X-Divy-Trace-Sampled, ETag, X-Cache, Retry-After"
	corsMaxAge        = "600"
)

// corsPath reports whether OPTIONS on p is the CORS preflight (204) rather
// than a static-handler 405.
func corsPath(p string) bool {
	return strings.HasPrefix(p, "/api/") || p == "/api" || strings.HasPrefix(p, "/loki/") || p == "/loki" || p == "/metrics" || p == "/healthz" || p == "/readyz"
}

// cors is the allow-list middleware: exact origin matches only, no
// credentials. Preflights answer 204 without reaching a handler; other
// requests from an allowed origin get the allow and expose headers plus
// Vary: Origin. It runs after the rate limiter and before the cache, so
// per-origin headers are never cached (contract K-X1).
func (s *Server) cors(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range s.cfg.CORSOrigins {
		if o = strings.TrimSpace(strings.TrimRight(o, "/")); o != "" {
			allowed[strings.ToLower(o)] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		ok := origin != "" && allowed[strings.ToLower(strings.TrimRight(origin, "/"))]
		if r.Method == http.MethodOptions && corsPath(r.URL.Path) {
			h := w.Header()
			h.Add("Vary", "Origin")
			if ok {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Methods", corsAllowMethods)
				h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
				h.Set("Access-Control-Max-Age", corsMaxAge)
			}
			h.Set("Cache-Control", CacheNS)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if ok {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Expose-Headers", corsExposeHeaders)
			h.Add("Vary", "Origin")
		}
		next.ServeHTTP(w, r)
	})
}
