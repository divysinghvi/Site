// Package server is the chi router of the divy binary: health, robots, the
// ASCII negotiation on `/`, the content endpoints, the Jaeger trace endpoints,
// the collect endpoint, the Prometheus HTTP API (promapi.go), /metrics and
// the embedded static site. Loki and the OTel/CORS/cache middlewares are
// mounted by their own packages through the hooks.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"divy.dev/internal/collector"
	"divy.dev/internal/content"
	"divy.dev/internal/metrics"
	"divy.dev/internal/model"
	"divy.dev/internal/promql"
	"divy.dev/internal/store"
	"divy.dev/internal/version"
)

// Config wires the server.
type Config struct {
	Content *content.Content
	Store   *store.Store
	Runner  *collector.Runner
	Logger  *slog.Logger

	Version   string
	Commit    string
	StartedAt time.Time
	// Branch, BuildUser and BuildDate fill /api/v1/status/buildinfo (default: the version package).
	Branch    string
	BuildUser string
	BuildDate string
	// SiteFS overrides the embedded site (tests); nil = the build embedded by internal/web.
	SiteFS fs.FS
	// SiteOrigin is the absolute origin used in robots.txt, og_image and the ASCII footer (SITE_ORIGIN).
	SiteOrigin string
	// CollectTokens are the bearer tokens accepted by /api/collect (DIVY_COLLECT_TOKEN, CRON_SECRET).
	CollectTokens []string
	// CollectBudget bounds one collection round (DIVY_COLLECT_BUDGET, default 8s).
	CollectBudget time.Duration
	// OTelServiceName is the self-trace service name listed by /api/services (default divy-api).
	OTelServiceName string
	// RequestTimeout is the per-request context deadline (default 30s); /api/collect uses its own budget.
	RequestTimeout time.Duration
	// ShuttingDown makes /readyz answer 503 shutting_down once true.
	ShuttingDown func() bool
	// Now overrides the clock (tests).
	Now func() time.Time

	// PromQL engine knobs (QUERY_LOOKBACK_DELTA, QUERY_TIMEOUT, QUERY_MAX_SAMPLES, QUERY_MAX_CONCURRENCY); zero = engine defaults.
	QueryLookback       time.Duration
	QueryTimeout        time.Duration
	QueryMaxSamples     int
	QueryMaxConcurrency int
	// CollectorIntervals are the configured cadences used for the /metrics
	// staleness cut-off; registered collectors and the catalogue defaults fill the gaps.
	CollectorIntervals map[string]time.Duration
	// Metrics is the client_golang registry; nil = a new one built here.
	Metrics *metrics.Registry

	// Hooks are the slots later packages fill; see Hooks.
	Hooks Hooks
}

// Hooks are the extension points of the middleware chain and router.
//
// The chain is: recover → Outer… → security headers → request context →
// HTTP metrics → request log → GetHead → Inner… → routes. Fill Outer with the OTel
// middleware and the X-Divy-Trace-Id header writer (they must run before
// logging so the log line can carry the trace id) and Inner with client-IP
// resolution, the per-IP rate limiter, CORS and the response cache (the
// order the contract fixes: clientIP → rateLimit → cors → cache).
//
// Mount registers additional route families on the router before the static
// fallback is installed: /api/v1/*, /loki/*, /metrics, /favicon.svg, /og/*,
// /api/uptime*. Each Mount function receives the root router.
type Hooks struct {
	Outer []func(http.Handler) http.Handler
	Inner []func(http.Handler) http.Handler
	Mount []func(r chi.Router)
}

// Server is the HTTP handler.
type Server struct {
	cfg     Config
	router  chi.Router
	static  *staticHandler
	log     *slog.Logger
	metrics *metrics.Registry
	live    *metrics.Live
	prom    *promAPI
}

// Cache-Control classes (contract §K.3.2, Vercel adaptation: explicit s-maxage).
const (
	CacheQ15   = "public, max-age=15, s-maxage=15"
	CacheC60   = "public, max-age=60, s-maxage=60"
	CacheNS    = "no-store"
	CacheHTML  = "public, max-age=0, s-maxage=60, stale-while-revalidate=300"
	CacheA3600 = "public, max-age=3600, s-maxage=3600"
	CacheIMM   = "public, max-age=31536000, immutable"
)

// New builds the router.
func New(cfg Config) (*Server, error) {
	if cfg.Content == nil {
		return nil, fmt.Errorf("server: content is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.StartedAt.IsZero() {
		cfg.StartedAt = cfg.Now()
	}
	if cfg.SiteOrigin == "" {
		cfg.SiteOrigin = "http://localhost:8080"
	}
	cfg.SiteOrigin = strings.TrimRight(cfg.SiteOrigin, "/")
	if cfg.CollectBudget <= 0 {
		cfg.CollectBudget = 8 * time.Second
	}
	if cfg.OTelServiceName == "" {
		cfg.OTelServiceName = "divy-api"
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.Commit == "" {
		cfg.Commit = "none"
	}
	if cfg.Branch == "" {
		cfg.Branch = version.Branch
	}
	if cfg.BuildUser == "" {
		cfg.BuildUser = version.BuildUser
	}
	if cfg.BuildDate == "" {
		cfg.BuildDate = version.Date
	}
	s := &Server{cfg: cfg, log: cfg.Logger}
	st, err := newStaticHandler(s)
	if err != nil {
		return nil, err
	}
	s.static = st
	s.live = metrics.NewLive(cfg.Content, cfg.StartedAt, cfg.Version, cfg.Commit)
	intervals := map[string]time.Duration{}
	for k, v := range cfg.CollectorIntervals {
		intervals[k] = v
	}
	if cfg.Runner != nil && cfg.Runner.Registry != nil {
		for _, c := range cfg.Runner.Registry.Collectors() {
			if _, ok := intervals[c.Name()]; !ok {
				intervals[c.Name()] = c.Interval()
			}
		}
	}
	s.metrics = cfg.Metrics
	if s.metrics == nil {
		s.metrics = metrics.New(metrics.Options{Store: cfg.Store, Live: s.live, Intervals: intervals, Now: cfg.Now, Logger: cfg.Logger})
	}
	if cfg.Runner != nil && cfg.Runner.OnResult == nil {
		cfg.Runner.OnResult = s.metrics.OnResult
	}
	engine := &promql.Engine{Live: s.live, Lookback: cfg.QueryLookback, Timeout: cfg.QueryTimeout, MaxSamples: cfg.QueryMaxSamples, MaxConcurrency: cfg.QueryMaxConcurrency}
	if cfg.Store != nil {
		engine.Storage = storeQuerier{st: cfg.Store}
	}
	s.prom = &promAPI{s: s, engine: engine, live: s.live}
	s.router = s.buildRouter()
	return s, nil
}

// Metrics returns the registry behind /metrics (the collector runner's OnResult hook).
func (s *Server) Metrics() *metrics.Registry { return s.metrics }

// Engine returns the PromQL engine (tests, the Loki family's shared clock).
func (s *Server) Engine() *promql.Engine { return s.prom.engine }

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.router.ServeHTTP(w, r) }

func (s *Server) now() time.Time { return s.cfg.Now().UTC() }

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(s.recoverer)
	r.Use(s.cfg.Hooks.Outer...)
	r.Use(securityHeaders)
	r.Use(requestContext)
	r.Use(s.metrics.Middleware)
	r.Use(s.requestLog)
	r.Use(middleware.GetHead)
	r.Use(s.cfg.Hooks.Inner...)

	r.NotFound(s.static.ServeHTTP)
	r.MethodNotAllowed(s.methodNotAllowed)

	timeout := middleware.Timeout(s.cfg.RequestTimeout)

	r.Group(func(r chi.Router) {
		r.Use(timeout)
		r.Get("/healthz", s.handleHealthz)
		r.Get("/readyz", s.handleReadyz)
		r.Get("/robots.txt", s.handleRobots)
		r.Get("/ascii", s.handleASCII)
		r.Get("/", s.handleIndex)

		r.Route("/api/content", func(r chi.Router) {
			r.Get("/services", s.handleContentServices)
			r.Get("/spans", s.handleContentSpans)
			r.Get("/logs", s.handleContentLogs)
			r.Get("/postmortems", s.handlePostmortems)
			r.Get("/postmortems/{id}", s.handlePostmortem)
			r.Get("/panels", s.handleContentPanels)
			r.Get("/alerts", s.handleContentAlerts)
			r.Get("/uptime", s.handleContentUptime)
			r.Get("/manual-metrics", s.handleContentManual)
			r.Get("/profile", s.handleContentProfile)
			r.Get("/todos", s.handleContentTodos)
		})
		r.Get("/api/traces", s.handleTraceSearch)
		r.Get("/api/traces/{id}", s.handleTrace)
		r.Get("/api/services", s.handleServices)
		r.Get("/api/services/{service}/operations", s.handleServiceOperations)
		r.Get("/api/operations", s.handleOperations)
	})
	r.Get("/api/collect", s.handleCollect)
	r.Post("/api/collect", s.handleCollect)
	r.Method(http.MethodGet, "/metrics", s.metrics.Handler())
	// The Prometheus family is a sub-router so that unknown paths answer the
	// Prometheus 404 envelope and wrong methods 405 (contract K-X5, K.3.4).
	r.Route("/api/v1", func(r chi.Router) {
		r.NotFound(s.promNotFound)
		r.MethodNotAllowed(s.methodNotAllowed)
		s.prom.mount(r)
	})

	for _, m := range s.cfg.Hooks.Mount {
		m(r)
	}

	// Unknown API paths never fall through to the static site (contract K-X5).
	r.HandleFunc("/loki/*", s.lokiNotFound)
	r.HandleFunc("/loki", s.lokiNotFound)
	r.HandleFunc("/api/*", s.apiNotFound)
	r.HandleFunc("/api", s.apiNotFound)
	return r
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, cacheControl string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(`{"error":"internal error: encoding"}`)
		status = http.StatusInternalServerError
		cacheControl = CacheNS
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	if cacheControl != "" {
		h.Set("Cache-Control", cacheControl)
	}
	if status >= 400 {
		h.Set("Cache-Control", CacheNS)
	}
	h.Set("Content-Length", fmt.Sprint(len(b)))
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// writeError writes the {"error": …} envelope (every JSON family outside /api/v1 and /loki).
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, CacheNS, model.PlainError{Error: msg})
}

func writePromError(w http.ResponseWriter, status int, typ model.PromErrorType, msg string) {
	writeJSON(w, status, CacheNS, model.PromError{Status: "error", ErrorType: typ, Error: msg})
}

func writeText(w http.ResponseWriter, status int, cacheControl, body string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	if cacheControl != "" {
		h.Set("Cache-Control", cacheControl)
	}
	if status >= 400 {
		h.Set("Cache-Control", CacheNS)
	}
	h.Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (s *Server) apiNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) promNotFound(w http.ResponseWriter, r *http.Request) {
	writePromError(w, http.StatusNotFound, "not_found", "path not found")
}

func (s *Server) lokiNotFound(w http.ResponseWriter, r *http.Request) {
	writeText(w, http.StatusNotFound, CacheNS, "not supported by divy.dev; see /loki/api/v1/status/buildinfo")
}

var allowProbe = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}

func (s *Server) methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	var allow []string
	for _, m := range allowProbe {
		rctx := chi.NewRouteContext()
		if s.router.Match(rctx, m, r.URL.Path) {
			allow = append(allow, m)
		}
	}
	if len(allow) > 0 {
		w.Header().Set("Allow", strings.Join(allow, ", "))
	}
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/v1/"):
		writePromError(w, http.StatusMethodNotAllowed, "bad_data", "method not allowed")
	case strings.HasPrefix(p, "/loki/"):
		writeText(w, http.StatusMethodNotAllowed, CacheNS, "method not allowed")
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ---- middleware ----

type ctxKey int

const ctxRequestID ctxKey = iota

// RequestID returns the request-scoped id set by the request context middleware.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxRequestID).(string)
	return id
}

func requestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := middleware.GetReqID(r.Context())
		if id == "" {
			id = fmt.Sprintf("%d", time.Now().UnixNano())
		}
		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// requestLog logs one line per request: method, route pattern, status, bytes,
// duration, request id. Never the client IP, user agent or query values.
func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		route := "?"
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			if p := rctx.RoutePattern(); p != "" {
				route = p
			}
		}
		if route == "?" {
			route = "/*"
		}
		s.log.Info("http", "method", r.Method, "route", route, "status", ww.Status(), "bytes", ww.BytesWritten(), "dur_ms", time.Since(start).Milliseconds(), "req", RequestID(r.Context()))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				s.log.Error("panic", "err", fmt.Sprint(rec), "path", r.URL.Path, "req", RequestID(r.Context()))
				writeError(w, http.StatusInternalServerError, "internal error: "+RequestID(r.Context()))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// bearerOK compares the Authorization bearer token against the configured tokens in constant time.
func (s *Server) bearerOK(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return false
	}
	got := strings.TrimSpace(auth[len("bearer "):])
	if got == "" {
		return false
	}
	for _, t := range s.cfg.CollectTokens {
		if t != "" && subtle.ConstantTimeCompare([]byte(got), []byte(t)) == 1 {
			return true
		}
	}
	return false
}
