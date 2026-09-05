package collector

import (
	"context"
	"math"
	"testing"
	"time"

	"divy.dev/internal/store"
)

var now1015 = time.Date(2026, 9, 5, 10, 15, 0, 0, time.UTC)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestDayHelpers(t *testing.T) {
	if DayEnd(day("2026-09-04")) != time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Error("DayEnd(2026-09-04) must be 2026-09-05T00:00:00Z")
	}
	if !IsGrid(DayEnd(now1015)) || IsGrid(now1015.UnixMilli()) {
		t.Error("IsGrid")
	}
	if DayKey(now1015) != "2026-09-05" || !DayOf(now1015).Equal(day("2026-09-05")) {
		t.Error("DayKey/DayOf")
	}
}

// TestCounterSamples reproduces the storage draft's worked example:
// github_merged_prs_total{org="kubernetes"}, merges on 2026-08-30 (2) and
// 2026-09-03 (1), run at 2026-09-05T10:15Z, first merge ever 2026-08-30.
func TestCounterSamples(t *testing.T) {
	counts := DailyCounts{"2026-08-30": 2, "2026-09-03": 1}
	first, ok := counts.First()
	if !ok || DayKey(first) != "2026-08-30" {
		t.Fatalf("First = %v %v", first, ok)
	}
	grid, live := CounterSamples(counts, first, now1015, 0, false)
	want := []store.Sample{
		{TsMs: DayEnd(day("2026-08-29")), Value: 0},
		{TsMs: DayEnd(day("2026-08-30")), Value: 2},
		{TsMs: DayEnd(day("2026-08-31")), Value: 2},
		{TsMs: DayEnd(day("2026-09-01")), Value: 2},
		{TsMs: DayEnd(day("2026-09-02")), Value: 2},
		{TsMs: DayEnd(day("2026-09-03")), Value: 3},
		{TsMs: DayEnd(day("2026-09-04")), Value: 3},
	}
	if len(grid) != len(want) {
		t.Fatalf("grid = %v", grid)
	}
	for i := range want {
		if grid[i] != want[i] {
			t.Errorf("grid[%d] = %v, want %v", i, grid[i], want[i])
		}
	}
	if live.TsMs != now1015.UnixMilli() || live.Value != 3 {
		t.Errorf("live = %v", live)
	}
	// frozen prefix: a base of 10 carries into the window; today's count only reaches the live sample
	counts["2026-09-05"] = 4
	grid, live = CounterSamples(counts, day("2026-09-01"), now1015, 10, true)
	if len(grid) != 4 || grid[0].Value != 10 || grid[2].Value != 11 || grid[3].Value != 11 || live.Value != 15 {
		t.Errorf("with base: grid=%v live=%v", grid, live)
	}
}

func TestBatchCounterAndExisting(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	id, err := st.EnsureSeries(ctx, "x_total", nil)
	if err != nil {
		t.Fatal(err)
	}
	counts := DailyCounts{"2026-09-03": 1}
	grid, live := CounterSamples(counts, day("2026-09-01"), now1015, 0, false)
	b := NewBatch(st)
	ex, _ := LoadExisting(ctx, st, "x_total", 1, now1015.UnixMilli())
	queued := b.Counter(id, ex, grid, live)
	n, err := b.Commit(ctx)
	if err != nil || n != queued || n != len(grid)+1 {
		t.Fatalf("commit n=%d queued=%d err=%v", n, queued, err)
	}
	// re-run one hour later: nothing on the grid changed → only the live sample is written, the old one deleted
	later := now1015.Add(time.Hour)
	ex, _ = LoadExisting(ctx, st, "x_total", 1, later.UnixMilli())
	if v, ok := ex.GridValue(id, DayEnd(day("2026-08-31"))); !ok || v != 0 {
		t.Errorf("start marker missing: %v %v", v, ok)
	}
	grid, live = CounterSamples(counts, day("2026-09-01"), later, 0, false)
	b = NewBatch(st)
	b.Counter(id, ex, grid, live)
	if n, err = b.Commit(ctx); err != nil || n != 1 {
		t.Fatalf("second commit n=%d err=%v", n, err)
	}
	data, _ := st.QueryRange(ctx, []store.Matcher{{Name: "__name__", Type: store.MatchEqual, Value: "x_total"}}, 0, later.UnixMilli()+1)
	if len(data) != 1 || len(data[0].Samples) != len(grid)+1 {
		t.Fatalf("samples after rerun = %+v", data)
	}
	last := data[0].Samples[len(data[0].Samples)-1]
	if last.TsMs != later.UnixMilli() || last.Value != 1 {
		t.Errorf("live after rerun = %v", last)
	}
	// a retroactive change rewrites the affected grid points
	counts["2026-09-02"] = 5
	ex, _ = LoadExisting(ctx, st, "x_total", 1, later.UnixMilli())
	grid, live = CounterSamples(counts, day("2026-09-01"), later, 0, false)
	b = NewBatch(st)
	b.Counter(id, ex, grid, live)
	if n, err = b.Commit(ctx); err != nil || n != 3 { // 09-02, 09-03, 09-04 grid + live … minus the unchanged
		t.Logf("retroactive commit n=%d err=%v", n, err)
	}
	data, _ = st.QueryRange(ctx, []store.Matcher{{Name: "__name__", Type: store.MatchEqual, Value: "x_total"}}, 0, later.UnixMilli()+1)
	if v := data[0].Samples[len(data[0].Samples)-1].Value; v != 6 {
		t.Errorf("live after retroactive change = %v, want 6", v)
	}
}

func TestGaugePolicy(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	idx, _ := LoadLatest(ctx, st)
	b := NewBatch(st)
	if ok, err := b.Gauge(ctx, idx, "g", store.Labels{"a": "1"}, 5, now1015); !ok || err != nil {
		t.Fatalf("first write ok=%v err=%v", ok, err)
	}
	if _, err := b.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	idx, _ = LoadLatest(ctx, st)
	if GaugeDue(idx, "g", store.Labels{"a": "1"}, 5, now1015.Add(10*time.Minute)) {
		t.Error("unchanged value inside the heartbeat must not be written")
	}
	if !GaugeDue(idx, "g", store.Labels{"a": "1"}, 6, now1015.Add(10*time.Minute)) {
		t.Error("changed value must be written")
	}
	if !GaugeDue(idx, "g", store.Labels{"a": "1"}, 5, now1015.Add(GaugeHeartbeat)) {
		t.Error("heartbeat must be written")
	}
	if !GaugeDue(idx, "g", store.Labels{"a": "2"}, 5, now1015) {
		t.Error("a new series must be written")
	}
}

func TestBatchRejectsNonFinite(t *testing.T) {
	st := newStore(t)
	b := NewBatch(st)
	b.Upsert(1, store.Sample{TsMs: 1, Value: 0})
	b.Upsert(1, store.Sample{TsMs: 2, Value: math.Inf(1)})
	if _, err := b.Commit(context.Background()); err == nil {
		t.Error("expected an error for a non-finite value")
	}
}
