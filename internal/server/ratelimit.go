package server

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"divy.dev/internal/model"
	"divy.dev/internal/trace"
)

// RateLimitConfig configures the token buckets (contract §K.3.5). RPS <= 0
// disables rate limiting altogether.
type RateLimitConfig struct {
	// RPS / Burst: one bucket per client IP (RATE_LIMIT_RPS, RATE_LIMIT_BURST).
	RPS   float64
	Burst int
	// GlobalRPS / GlobalBurst: one bucket shared by every client for
	// /healthz, /readyz and /metrics (defaults 50 / 200).
	GlobalRPS   float64
	GlobalBurst int
	// Now overrides the limiter clock (tests).
	Now func() time.Time
}

// Rate-limit classes.
const (
	rlClassIP     = "ip"
	rlClassGlobal = "global"
	rlClassNone   = "none"
)

const (
	rlIdleAfter  = 10 * time.Minute
	rlSweepEvery = time.Minute
	rlMaxClients = 10000
)

type rateLimiter struct {
	cfg    RateLimitConfig
	global *rate.Limiter
	now    func() time.Time

	mu        sync.Mutex
	clients   map[string]*clientBucket
	lastSweep time.Time
}

type clientBucket struct {
	lim  *rate.Limiter
	seen time.Time
}

func newRateLimiter(cfg RateLimitConfig) *rateLimiter {
	if cfg.Burst <= 0 {
		cfg.Burst = 100
	}
	if cfg.GlobalRPS <= 0 {
		cfg.GlobalRPS = 50
	}
	if cfg.GlobalBurst <= 0 {
		cfg.GlobalBurst = 200
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &rateLimiter{cfg: cfg, global: rate.NewLimiter(rate.Limit(cfg.GlobalRPS), cfg.GlobalBurst), now: cfg.Now, clients: map[string]*clientBucket{}, lastSweep: cfg.Now()}
}

// rateLimitClass maps a path to its class: immutable assets are free, the
// health and scrape endpoints share one global bucket, everything else is
// per client IP.
func rateLimitClass(p string) string {
	switch {
	case strings.HasPrefix(p, "/_app/"), strings.HasPrefix(p, "/fonts/"):
		return rlClassNone
	case p == "/healthz", p == "/readyz", p == "/metrics":
		return rlClassGlobal
	default:
		return rlClassIP
	}
}

func (l *rateLimiter) bucket(ip string) *rate.Limiter {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) >= rlSweepEvery || len(l.clients) > rlMaxClients {
		for k, b := range l.clients {
			if now.Sub(b.seen) > rlIdleAfter {
				delete(l.clients, k)
			}
		}
		l.lastSweep = now
	}
	b := l.clients[ip]
	if b == nil {
		b = &clientBucket{lim: rate.NewLimiter(rate.Limit(l.cfg.RPS), l.cfg.Burst)}
		l.clients[ip] = b
	}
	b.seen = now
	return b.lim
}

// take consumes one token or returns the delay until one is available.
func (l *rateLimiter) take(lim *rate.Limiter) (ok bool, retry time.Duration) {
	now := l.now()
	res := lim.ReserveN(now, 1)
	if !res.OK() {
		return false, time.Second
	}
	if d := res.DelayFrom(now); d > 0 {
		res.CancelAt(now)
		return false, d
	}
	return true, 0
}

// middleware answers 429 in the family envelope with Retry-After once a
// bucket is empty (contract §K.3.4/§K.3.5). Cached hits still consume tokens
// (the cache runs after this middleware).
func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		var retry time.Duration
		var msg string
		switch rateLimitClass(r.URL.Path) {
		case rlClassNone:
			next.ServeHTTP(w, r)
			return
		case rlClassGlobal:
			ok, retry = l.take(l.global)
			msg = fmt.Sprintf("rate limit exceeded: %g req/s shared by /healthz, /readyz and /metrics, burst %d", l.cfg.GlobalRPS, l.cfg.GlobalBurst)
		default:
			ok, retry = l.take(l.bucket(ClientIP(r.Context())))
			msg = fmt.Sprintf("rate limit exceeded: %g req/s per client, burst %d", l.cfg.RPS, l.cfg.Burst)
		}
		if ok {
			next.ServeHTTP(w, r)
			return
		}
		secs := int(math.Ceil(retry.Seconds()))
		if secs < 1 {
			secs = 1
		}
		trace.SetAttributes(r.Context(), trace.AttrRateLimited.Bool(true))
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		msg += fmt.Sprintf("; retry after %ds", secs)
		writeFamilyError(w, r, http.StatusTooManyRequests, msg)
	})
}

// writeFamilyError writes an error in the envelope of the path's family:
// Prometheus JSON under /api/v1/, Loki text under /loki/, {"error"} elsewhere.
func writeFamilyError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/v1/"):
		typ := "bad_data"
		switch status {
		case http.StatusTooManyRequests:
			typ = "unavailable"
		case http.StatusInternalServerError:
			typ = "internal"
		case http.StatusNotFound:
			typ = "not_found"
		}
		writePromError(w, status, model.PromErrorType(typ), msg)
	case strings.HasPrefix(p, "/loki/"):
		writeText(w, status, CacheNS, msg)
	default:
		writeError(w, status, msg)
	}
}
