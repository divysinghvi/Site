package collector

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"divy.dev/internal/store"
)

type fake struct {
	name     string
	interval time.Duration
	run      func(ctx context.Context) (Result, error)
}

func (f fake) Name() string            { return f.name }
func (f fake) Interval() time.Duration { return f.interval }
func (f fake) Run(ctx context.Context) (Result, error) {
	return f.run(ctx)
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Process{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Process{}); err == nil {
		t.Error("duplicate registration should fail")
	}
	if _, ok := r.Get("process"); !ok || len(r.Names()) != 1 {
		t.Error("registry lookup")
	}
	if StaleAfter(time.Minute) != 15*time.Minute || StaleAfter(time.Hour) != 3*time.Hour {
		t.Error("StaleAfter")
	}
	if RunTimeout(time.Minute) != 30*time.Second || RunTimeout(15*time.Minute) != MaxRunTimeout || RunTimeout(time.Second) != time.Second {
		t.Error("RunTimeout")
	}
}

func TestRunRound(t *testing.T) {
	st := newStore(t)
	reg := NewRegistry()
	_ = reg.Register(Process{})
	_ = reg.Register(fake{name: "ok", interval: time.Minute, run: func(context.Context) (Result, error) { return Result{Items: 3}, nil }})
	_ = reg.Register(fake{name: "bad", interval: time.Minute, run: func(context.Context) (Result, error) { return Result{}, errors.New("boom") }})
	_ = reg.Register(fake{name: "off", interval: time.Minute, run: func(context.Context) (Result, error) { return Result{}, ErrDisabled }})
	_ = reg.Register(fake{name: "slow", interval: time.Minute, run: func(ctx context.Context) (Result, error) {
		<-ctx.Done()
		return Result{}, ctx.Err()
	}})
	var mu sync.Mutex
	var outcomes []string
	r := &Runner{Store: st, Registry: reg, OnResult: func(name, outcome string, _ time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		outcomes = append(outcomes, name+"="+outcome)
	}}
	start := time.Now()
	sum := r.RunRound(context.Background(), 300*time.Millisecond)
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("round took %v, budget was 300ms", el)
	}
	if sum.BudgetMs != 300 || !sum.Truncated {
		t.Errorf("summary = %+v", sum)
	}
	got := map[string]struct {
		ok  bool
		err string
		n   int
	}{}
	for _, c := range sum.Collectors {
		got[c.Name] = struct {
			ok  bool
			err string
			n   int
		}{c.OK, c.Error, c.Items}
	}
	if !got["process"].ok || !got["ok"].ok || got["ok"].n != 3 {
		t.Errorf("ok collectors: %+v", got)
	}
	if got["bad"].ok || got["bad"].err != "boom" {
		t.Errorf("bad: %+v", got["bad"])
	}
	if got["off"].ok || got["off"].err != "skipped: disabled" {
		t.Errorf("off: %+v", got["off"])
	}
	if got["slow"].ok || got["slow"].err == "" || got["slow"].err[:8] != "timeout:" {
		t.Errorf("slow: %+v", got["slow"])
	}
	// collector_runs rows: ok, bad, slow, process (not off)
	runs, err := st.RecentRuns(context.Background(), "", 10)
	if err != nil || len(runs) != 4 {
		t.Fatalf("runs = %+v err=%v", runs, err)
	}
	for _, run := range runs {
		if run.FinishedMs == nil || run.OK == nil {
			t.Errorf("run not finished: %+v", run)
		}
		if run.Collector == "off" {
			t.Error("disabled collector must not write a row")
		}
	}
	ls, _ := st.LastSuccess(context.Background())
	if _, ok := ls["ok"]; !ok {
		t.Error("last success missing")
	}
	if _, ok := ls["bad"]; ok {
		t.Error("failed run must not count as success")
	}
	// readiness
	ready, err := r.Readiness(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rc := ready["ok"]; rc.OK == nil || !*rc.OK || rc.StaleAfterS != 900 {
		t.Errorf("ready ok = %+v", rc)
	}
	if rc := ready["bad"]; rc.OK != nil {
		t.Errorf("never-succeeded collector should have ok=null: %+v", rc)
	}
	// subset by name
	sub := r.RunRound(context.Background(), time.Second, "ok")
	if len(sub.Collectors) != 1 || sub.Collectors[0].Name != "ok" || sub.Truncated {
		t.Errorf("subset = %+v", sub)
	}
	mu.Lock()
	n := len(outcomes)
	mu.Unlock()
	if n == 0 {
		t.Error("OnResult hook not called")
	}
}

func TestSingleFlight(t *testing.T) {
	reg := NewRegistry()
	release := make(chan struct{})
	_ = reg.Register(fake{name: "hold", interval: time.Minute, run: func(ctx context.Context) (Result, error) {
		<-release
		return Result{}, nil
	}})
	r := &Runner{Registry: reg}
	c, _ := reg.Get("hold")
	go r.RunOne(context.Background(), c, time.Second)
	time.Sleep(50 * time.Millisecond)
	res := r.RunOne(context.Background(), c, time.Second)
	close(release)
	if res.OK || res.Error != "skipped: previous run still in progress" {
		t.Errorf("second run = %+v", res)
	}
}

func TestScheduler(t *testing.T) {
	reg := NewRegistry()
	runs := make(chan string, 10)
	_ = reg.Register(fake{name: "tick", interval: 50 * time.Millisecond, run: func(context.Context) (Result, error) { runs <- "tick"; return Result{}, nil }})
	s := &Scheduler{Runner: &Runner{Registry: reg}, InitialDelay: func(int, Collector) time.Duration { return 0 }}
	s.Start(context.Background())
	select {
	case <-runs:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler never ran the collector")
	}
	select {
	case <-runs:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not repeat")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.Stop(ctx)
}
