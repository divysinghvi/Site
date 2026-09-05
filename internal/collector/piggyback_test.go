package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestPiggybackSingleFlightAndMinInterval(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	started := 0
	release := make(chan struct{})
	p := &Piggyback{MinInterval: time.Minute, Now: func() time.Time { return now }}
	p.run = func(ctx context.Context) { mu.Lock(); started++; mu.Unlock(); <-release }

	if !p.Kick() {
		t.Fatal("first kick should start a round")
	}
	if p.Kick() {
		t.Fatal("second kick must not start while a round is running")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for p.Rounds() != 1 || func() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.running }() {
		if time.Now().After(deadline) {
			t.Fatal("round did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if p.Kick() {
		t.Fatal("kick within MinInterval must be skipped")
	}
	now = now.Add(2 * time.Minute)
	release = make(chan struct{})
	close(release)
	if !p.Kick() {
		t.Fatal("kick after MinInterval should start a round")
	}
	mu.Lock()
	defer mu.Unlock()
	if started != 2 {
		t.Fatalf("started = %d, want 2", started)
	}
}

func TestPiggybackMiddlewarePaths(t *testing.T) {
	p := &Piggyback{}
	p.run = func(ctx context.Context) {}
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, c := range []struct {
		path string
		kick bool
	}{{"/api/v1/query", true}, {"/", true}, {"/metrics", true}, {"/loki/api/v1/labels", true}, {"/api/collect", false}, {"/readyz", false}, {"/healthz", false}, {"/_app/immutable/x.js", false}, {"/dashboard", false}} {
		p = &Piggyback{}
		p.run = func(ctx context.Context) {}
		h = p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", c.path, nil))
		if got := p.Rounds() > 0; got != c.kick {
			t.Errorf("%s: kicked=%v want %v", c.path, got, c.kick)
		}
	}
}
