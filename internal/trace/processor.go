package trace

import (
	"context"
	"log/slog"
	"sync"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Buffer bounds of the processor.
const (
	maxChildrenPerTrace = 512
	maxBufferedSpans    = 8192
	exportTimeout       = 5 * time.Second
)

// processor is the span pipeline (vercel-adaptation "OTel"): a root span
// (no parent, or a remote parent) is exported synchronously when it ends,
// together with every child of its trace that ended before it; children
// are buffered per trace until then. Children that outlive their root (a
// goroutine finishing after the response) are written by the sweeper once
// they are older than OrphanAfter, or by ForceFlush.
type processor struct {
	exp     *exporter
	log     *slog.Logger
	metrics Metrics
	now     func() time.Time

	mu       sync.Mutex
	pending  map[oteltrace.TraceID]*pendingTrace
	buffered int
	stopped  bool

	sweepEvery  time.Duration
	orphanAfter time.Duration
	stop        chan struct{}
	done        chan struct{}

	errMu   sync.Mutex
	lastErr time.Time
}

type pendingTrace struct {
	first time.Time
	spans []sdktrace.ReadOnlySpan
}

func newProcessor(exp *exporter, log *slog.Logger, m Metrics, now func() time.Time, sweepEvery, orphanAfter time.Duration) *processor {
	if sweepEvery <= 0 {
		sweepEvery = 5 * time.Second
	}
	if orphanAfter <= 0 {
		orphanAfter = 30 * time.Second
	}
	p := &processor{exp: exp, log: log, metrics: m, now: now, pending: map[oteltrace.TraceID]*pendingTrace{}, sweepEvery: sweepEvery, orphanAfter: orphanAfter, stop: make(chan struct{}), done: make(chan struct{})}
	go p.sweep()
	return p
}

// OnStart implements sdktrace.SpanProcessor.
func (p *processor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

// OnEnd implements sdktrace.SpanProcessor.
func (p *processor) OnEnd(s sdktrace.ReadOnlySpan) {
	sc := s.SpanContext()
	if !sc.IsSampled() {
		return
	}
	parent := s.Parent()
	isRoot := !parent.IsValid() || parent.IsRemote()
	tid := sc.TraceID()
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	if !isRoot {
		pt := p.pending[tid]
		if pt == nil {
			pt = &pendingTrace{first: p.now()}
			p.pending[tid] = pt
		}
		if len(pt.spans) >= maxChildrenPerTrace || p.buffered >= maxBufferedSpans {
			p.mu.Unlock()
			return // bounded: extra children are dropped, the root still resolves
		}
		pt.spans = append(pt.spans, s)
		p.buffered++
		p.mu.Unlock()
		return
	}
	batch := p.takeLocked(tid)
	p.mu.Unlock()
	batch = append(batch, s) // children first, the root last
	p.export(batch)
}

// takeLocked removes and returns the buffered children of a trace.
func (p *processor) takeLocked(tid oteltrace.TraceID) []sdktrace.ReadOnlySpan {
	pt := p.pending[tid]
	if pt == nil {
		return nil
	}
	delete(p.pending, tid)
	p.buffered -= len(pt.spans)
	return pt.spans
}

func (p *processor) export(batch []sdktrace.ReadOnlySpan) {
	if len(batch) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
	defer cancel()
	if err := p.exp.ExportSpans(ctx, batch); err != nil {
		p.warn(err, len(batch))
	}
}

// warn logs an export failure at most once per minute.
func (p *processor) warn(err error, n int) {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	if now := p.now(); now.Sub(p.lastErr) >= time.Minute {
		p.lastErr = now
		p.log.Warn("otel: span export failed", "spans", n, "err", err.Error())
	}
}

// ForceFlush implements sdktrace.SpanProcessor: every buffered child is written now.
func (p *processor) ForceFlush(ctx context.Context) error {
	p.mu.Lock()
	var batch []sdktrace.ReadOnlySpan
	for tid := range p.pending {
		batch = append(batch, p.takeLocked(tid)...)
	}
	p.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	if err := p.exp.ExportSpans(ctx, batch); err != nil {
		p.warn(err, len(batch))
		return err
	}
	return nil
}

// Shutdown implements sdktrace.SpanProcessor.
func (p *processor) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return nil
	}
	p.stopped = true
	close(p.stop)
	p.mu.Unlock()
	<-p.done
	p.mu.Lock()
	var batch []sdktrace.ReadOnlySpan
	for tid := range p.pending {
		batch = append(batch, p.takeLocked(tid)...)
	}
	p.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	return p.exp.ExportSpans(ctx, batch)
}

// sweep writes children whose root never ended (or ended unsampled).
func (p *processor) sweep() {
	defer close(p.done)
	t := time.NewTicker(p.sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.sweepOnce()
		}
	}
}

func (p *processor) sweepOnce() {
	cutoff := p.now().Add(-p.orphanAfter)
	p.mu.Lock()
	var batch []sdktrace.ReadOnlySpan
	for tid, pt := range p.pending {
		if pt.first.Before(cutoff) {
			batch = append(batch, p.takeLocked(tid)...)
		}
	}
	p.mu.Unlock()
	p.export(batch)
}

// Buffered reports the number of child spans waiting for their root (tests, readyz).
func (p *processor) Buffered() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buffered
}
