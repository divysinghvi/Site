package collector

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Piggyback runs due collection rounds in the background, triggered by
// ordinary traffic. It is the scheduler for platforms without one (Vercel
// functions have no long-running process): a request to the API kicks a
// round at most once per MinInterval per instance, rounds are single-flight,
// and OnlyDue keeps every collector on its own cadence, so a warm instance
// does one cheap LastSuccess lookup per minute and real work only when due.
type Piggyback struct {
	Runner      *Runner
	Budget      time.Duration
	MinInterval time.Duration // default 1m
	Logger      *slog.Logger
	// Now overrides the clock (tests).
	Now func() time.Time
	// run overrides the round (tests).
	run func(ctx context.Context)

	mu      sync.Mutex
	running bool
	last    time.Time
	rounds  int
}

// Middleware kicks a round for API traffic (not for health probes, static
// assets or /api/collect, which runs a round itself).
func (p *Piggyback) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.wants(r.URL.Path) {
			p.Kick()
		}
		next.ServeHTTP(w, r)
	})
}

func (p *Piggyback) wants(path string) bool {
	switch {
	case path == "/api/collect", path == "/healthz", path == "/readyz":
		return false
	case path == "/" || path == "/metrics" || strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/loki/"):
		return true
	}
	return false
}

func (p *Piggyback) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Kick starts a due-only round unless one is running or one started less
// than MinInterval ago. It returns true when a round was started.
func (p *Piggyback) Kick() bool {
	min := p.MinInterval
	if min <= 0 {
		min = time.Minute
	}
	p.mu.Lock()
	if p.running || (!p.last.IsZero() && p.now().Sub(p.last) < min) {
		p.mu.Unlock()
		return false
	}
	p.running = true
	p.last = p.now()
	p.rounds++
	p.mu.Unlock()
	go p.round()
	return true
}

// Rounds reports how many rounds were started (tests, /readyz).
func (p *Piggyback) Rounds() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rounds
}

func (p *Piggyback) round() {
	defer func() {
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
	}()
	budget := p.Budget
	if budget <= 0 {
		budget = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget+2*time.Second)
	defer cancel()
	if p.run != nil {
		p.run(ctx)
		return
	}
	sum := p.Runner.RunRoundOpts(ctx, RoundOptions{Budget: budget, OnlyDue: true})
	if p.Logger != nil {
		ran, failed := 0, 0
		for _, c := range sum.Collectors {
			if c.OK {
				ran++
			} else if !strings.HasPrefix(c.Error, "skipped") {
				failed++
			}
		}
		if ran > 0 || failed > 0 {
			p.Logger.Info("piggyback collection round", "ran", ran, "failed", failed, "truncated", sum.Truncated)
		}
	}
}
