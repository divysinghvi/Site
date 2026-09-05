package collector

import (
	"context"
	"time"
)

// Process is a no-op collector that keeps the collection round exercised
// before the real collectors land. It writes nothing and reports 0 items.
type Process struct{}

// Name is "process".
func (Process) Name() string { return "process" }

// Interval is 5 minutes.
func (Process) Interval() time.Duration { return 5 * time.Minute }

// Run does nothing (honestly): the process metrics are live series, never stored.
func (Process) Run(ctx context.Context) (Result, error) {
	return Result{Items: 0, Note: "no-op"}, ctx.Err()
}
