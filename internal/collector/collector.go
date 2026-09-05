// Package collector is the collection framework: the Collector interface,
// the Registry, the in-process Scheduler used by `serve --collect`, and
// RunRound, the bounded concurrent round behind GET|POST /api/collect on
// Vercel. Real collectors (GitHub, PyPI, uptime, manual, retention) register
// here; the "process" collector is a no-op that keeps the round exercised.
package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"divy.dev/internal/model"
	"divy.dev/internal/store"
)

// Result is what one successful Run reports.
type Result struct {
	// Items is the number of samples/rows written (or deleted, for retention).
	Items int
	// Note is optional free text for the log line (never secrets).
	Note string
}

// Collector is one data source.
type Collector interface {
	// Name is the collector name used in collector_runs and metrics labels.
	Name() string
	// Interval is the cadence of the in-process scheduler.
	Interval() time.Duration
	// Run performs one complete collection; it must respect ctx.
	Run(ctx context.Context) (Result, error)
}

// ErrDisabled is returned by a collector that is registered but has no
// configuration (e.g. GitHub without a token). The run is reported as
// "skipped" and no collector_runs row is written.
var ErrDisabled = errors.New("collector disabled")

// Outcome classes recorded per run (divy_collector_runs_total{result}).
const (
	OutcomeOK      = "ok"
	OutcomeError   = "error"
	OutcomeTimeout = "timeout"
	OutcomeSkipped = "skipped"
)

// MaxRunTimeout caps a single run.
const MaxRunTimeout = 2 * time.Minute

// Registry holds the registered collectors in registration order.
type Registry struct {
	mu   sync.RWMutex
	list []Collector
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Register adds a collector; duplicate names are an error.
func (r *Registry) Register(c Collector) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := c.Name()
	if name == "" {
		return errors.New("collector: empty name")
	}
	for _, x := range r.list {
		if x.Name() == name {
			return fmt.Errorf("collector: %q registered twice", name)
		}
	}
	r.list = append(r.list, c)
	return nil
}

// Collectors returns the collectors in registration order.
func (r *Registry) Collectors() []Collector {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Collector(nil), r.list...)
}

// Get finds a collector by name.
func (r *Registry) Get(name string) (Collector, bool) {
	for _, c := range r.Collectors() {
		if c.Name() == name {
			return c, true
		}
	}
	return nil, false
}

// Names returns the registered names in order.
func (r *Registry) Names() []string {
	cs := r.Collectors()
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name())
	}
	return out
}

// StaleAfter is the freshness threshold of a collector: max(3 × interval, 15m).
func StaleAfter(interval time.Duration) time.Duration {
	if d := 3 * interval; d > 15*time.Minute {
		return d
	}
	return 15 * time.Minute
}

// RunTimeout is the per-run timeout: min(interval/2, MaxRunTimeout), at least 1s.
func RunTimeout(interval time.Duration) time.Duration {
	d := interval / 2
	if d > MaxRunTimeout {
		d = MaxRunTimeout
	}
	if d < time.Second {
		d = time.Second
	}
	return d
}

// Runner executes collectors and records their runs.
type Runner struct {
	Store    *store.Store
	Registry *Registry
	Logger   *slog.Logger
	// OnResult is called after every run with the outcome class (metrics hook).
	OnResult func(name, outcome string, d time.Duration)

	flights sync.Map // name → *sync.Mutex (single-flight)
}

func (r *Runner) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

func (r *Runner) lock(name string) *sync.Mutex {
	m, _ := r.flights.LoadOrStore(name, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// RunOne runs a collector once with a timeout, records collector_runs and
// returns the API result. A collector already running (single-flight) is
// reported as skipped.
func (r *Runner) RunOne(ctx context.Context, c Collector, timeout time.Duration) model.CollectorResult {
	name := c.Name()
	res := model.CollectorResult{Name: name}
	mu := r.lock(name)
	if !mu.TryLock() {
		res.Error = "skipped: previous run still in progress"
		r.report(name, OutcomeSkipped, 0)
		return res
	}
	defer mu.Unlock()
	start := time.Now()
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var runID int64
	disabled := false
	if d, ok := c.(interface{ Disabled() bool }); ok && d.Disabled() {
		disabled = true
	}
	if r.Store != nil && !disabled {
		id, err := r.Store.StartRun(rctx, name)
		if err != nil {
			r.logger().Warn("collector: start run", "collector", name, "err", err.Error())
		} else {
			runID = id
		}
	}
	out, err := c.Run(rctx)
	d := time.Since(start)
	res.DurationMs = d.Milliseconds()
	res.Items = out.Items
	outcome := OutcomeOK
	switch {
	case errors.Is(err, ErrDisabled):
		outcome = OutcomeSkipped
		res.Error = "skipped: " + strings.TrimPrefix(err.Error(), ErrDisabled.Error()+": ")
		if res.Error == "skipped: "+ErrDisabled.Error() {
			res.Error = "skipped: disabled"
		}
	case err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(rctx.Err(), context.DeadlineExceeded)):
		outcome = OutcomeTimeout
		res.Error = "timeout: " + err.Error()
	case err != nil:
		outcome = OutcomeError
		res.Error = err.Error()
	default:
		res.OK = true
	}
	if runID != 0 {
		// use a fresh context: the run context may already be expired
		fctx, fcancel := context.WithTimeout(context.Background(), 5*time.Second)
		var ferr error
		if outcome == OutcomeSkipped {
			ferr = r.Store.DeleteRun(fctx, runID) // disabled collectors leave no row
		} else {
			ferr = r.Store.FinishRun(fctx, runID, res.OK, res.Error, res.Items)
		}
		if ferr != nil {
			r.logger().Warn("collector: finish run", "collector", name, "err", ferr.Error())
		}
		fcancel()
	}
	r.report(name, outcome, d)
	attrs := []any{"collector", name, "ok", res.OK, "items", res.Items, "dur", d.String()}
	if res.Error != "" {
		attrs = append(attrs, "err", res.Error)
	}
	if out.Note != "" {
		attrs = append(attrs, "note", out.Note)
	}
	r.logger().Info("collector run", attrs...)
	return res
}

func (r *Runner) report(name, outcome string, d time.Duration) {
	if r.OnResult != nil {
		r.OnResult(name, outcome, d)
	}
}

// RoundOptions tune one collection round.
type RoundOptions struct {
	// Budget bounds the whole round (default 8s).
	Budget time.Duration
	// Names selects collectors (empty = all registered).
	Names []string
	// OnlyDue skips a collector whose last successful run is younger than its
	// interval (minus a small slack): the collect endpoint is called every five
	// minutes but GitHub, PyPI, manual and retention keep their own cadences.
	OnlyDue bool
	// Now overrides the clock (tests).
	Now func() time.Time
}

// RunRound runs the named collectors (all when names is empty) concurrently
// within budget and returns the /api/collect summary. Each collector gets
// min(RunTimeout(interval), budget); Truncated is true when the budget
// expired before every collector finished.
func (r *Runner) RunRound(ctx context.Context, budget time.Duration, names ...string) model.CollectSummary {
	return r.RunRoundOpts(ctx, RoundOptions{Budget: budget, Names: names})
}

// DueSlack is how early a collector may run before its interval has fully
// elapsed: min(interval/10, 1m) — a cron that fires a little late must not
// push every run to the next tick.
func DueSlack(interval time.Duration) time.Duration {
	if d := interval / 10; d < time.Minute {
		return d
	}
	return time.Minute
}

// RunRoundOpts is RunRound with options.
func (r *Runner) RunRoundOpts(ctx context.Context, o RoundOptions) model.CollectSummary {
	budget := o.Budget
	if budget <= 0 {
		budget = 8 * time.Second
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	rctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var selected []Collector
	for _, c := range r.Registry.Collectors() {
		if len(o.Names) == 0 || containsStr(o.Names, c.Name()) {
			selected = append(selected, c)
		}
	}
	var last map[string]int64
	if o.OnlyDue && r.Store != nil {
		var err error
		if last, err = r.Store.LastSuccess(rctx); err != nil {
			r.logger().Warn("collector: last success", "err", err.Error())
			last = nil
		}
	}
	results := make([]model.CollectorResult, len(selected))
	var wg sync.WaitGroup
	for i, c := range selected {
		if o.OnlyDue {
			if ms, ok := last[c.Name()]; ok {
				age := now().Sub(time.UnixMilli(ms))
				if age < 0 {
					age = 0 // another instance's clock is ahead of ours
				}
				if wait := c.Interval() - DueSlack(c.Interval()) - age; wait > 0 {
					results[i] = model.CollectorResult{Name: c.Name(), Error: fmt.Sprintf("skipped: not due (last success %s ago, interval %s)", age.Truncate(time.Second), c.Interval())}
					r.report(c.Name(), OutcomeSkipped, 0)
					continue
				}
			}
		}
		wg.Add(1)
		go func(i int, c Collector) {
			defer wg.Done()
			timeout := RunTimeout(c.Interval())
			if timeout > budget {
				timeout = budget
			}
			results[i] = r.RunOne(rctx, c, timeout)
		}(i, c)
	}
	wg.Wait()
	summary := model.CollectSummary{Collectors: results, BudgetMs: budget.Milliseconds(), Truncated: errors.Is(rctx.Err(), context.DeadlineExceeded)}
	if summary.Collectors == nil {
		summary.Collectors = []model.CollectorResult{}
	}
	return summary
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Scheduler runs every registered collector on its cadence (serve --collect).
type Scheduler struct {
	Runner *Runner
	// InitialDelay overrides the jittered start delay (tests); nil = i×5s + rand(0,10s).
	InitialDelay func(i int, c Collector) time.Duration
	// Rand is the jitter source; nil = math/rand.
	Rand *rand.Rand

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Start launches one goroutine per collector.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	for i, c := range s.Runner.Registry.Collectors() {
		s.wg.Add(1)
		go s.loop(ctx, i, c)
	}
}

func (s *Scheduler) rnd() *rand.Rand {
	if s.Rand != nil {
		return s.Rand
	}
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

func (s *Scheduler) loop(ctx context.Context, i int, c Collector) {
	defer s.wg.Done()
	rnd := s.rnd()
	delay := time.Duration(i)*5*time.Second + time.Duration(rnd.Int63n(int64(10*time.Second)))
	if c.Name() == "retention" {
		delay = 60 * time.Second
	}
	if s.InitialDelay != nil {
		delay = s.InitialDelay(i, c)
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.Runner.RunOne(ctx, c, RunTimeout(c.Interval()))
		if ctx.Err() != nil {
			return
		}
		jitter := 0.9 + 0.2*rnd.Float64()
		t.Reset(time.Duration(float64(c.Interval()) * jitter))
	}
}

// Stop cancels the loops and waits for in-flight runs (bounded by ctx).
func (s *Scheduler) Stop(ctx context.Context) {
	if s.cancel != nil {
		s.cancel()
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Freshness describes a collector for /readyz.
type Freshness struct {
	Name        string
	Interval    time.Duration
	LastSuccess *time.Time
}

// Readiness builds the /readyz collectors map from collector_runs.
func (r *Runner) Readiness(ctx context.Context, now time.Time) (map[string]model.ReadyzCollector, error) {
	out := map[string]model.ReadyzCollector{}
	var last map[string]int64
	if r.Store != nil {
		var err error
		if last, err = r.Store.LastSuccess(ctx); err != nil {
			return nil, err
		}
	}
	for _, c := range r.Registry.Collectors() {
		stale := StaleAfter(c.Interval())
		rc := model.ReadyzCollector{StaleAfterS: int64(stale.Seconds())}
		if d, ok := c.(interface{ Disabled() bool }); ok && d.Disabled() {
			rc.Disabled = true
			out[c.Name()] = rc
			continue
		}
		if ms, ok := last[c.Name()]; ok {
			t := time.UnixMilli(ms).UTC()
			age := int64(now.Sub(t).Seconds())
			okv := age <= int64(stale.Seconds())
			ts := t.Format(time.RFC3339)
			rc.OK, rc.LastSuccess, rc.AgeS = &okv, &ts, &age
		}
		out[c.Name()] = rc
	}
	return out, nil
}

// SortedNames returns collector names sorted (deterministic JSON).
func SortedNames(m map[string]model.ReadyzCollector) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// disabled wraps a collector that is switched off by configuration
// (COLLECT_DISABLED): every run is reported as skipped with the reason.
type disabled struct {
	Collector
	reason string
}

// WrapDisabled returns a collector that never runs and reports reason.
func WrapDisabled(c Collector, reason string) Collector {
	return disabled{Collector: c, reason: reason}
}

// Disabled is true.
func (disabled) Disabled() bool { return true }

// Run returns ErrDisabled with the reason.
func (d disabled) Run(context.Context) (Result, error) {
	return Result{}, fmt.Errorf("%w: %s", ErrDisabled, d.reason)
}

// Unwrap exposes the wrapped collector (tests).
func (d disabled) Unwrap() Collector { return d.Collector }
