package manual

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"divy.dev/internal/content"
	"divy.dev/internal/model"
	"divy.dev/internal/store"
)

var frozen = time.Date(2026, 9, 5, 10, 15, 0, 0, time.UTC)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), "file:"+filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func latest(t *testing.T, st *store.Store) map[string]store.Latest {
	t.Helper()
	rows, err := st.LatestPerSeries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.Latest{}
	for _, l := range rows {
		out[l.Metric+store.CanonicalLabels(l.Labels)] = l
	}
	return out
}

func TestRunFromContent(t *testing.T) {
	c := content.MustLoad("../../content/testdata/valid", content.Options{Now: frozen})
	st := newStore(t)
	col := New(Config{Now: func() time.Time { return frozen }}, c, st)
	res, err := col.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// two gauges, both updated_at TODO → no timestamp series
	if res.Items != 2 || !strings.Contains(res.Note, "updated_at_todo=savely_active_users,lfx_applications") {
		t.Errorf("result = %+v", res)
	}
	l := latest(t, st)
	if v := l["savely_active_users{}"]; v.Value != 5000 || v.TsMs != frozen.UnixMilli() {
		t.Errorf("savely_active_users = %+v", v)
	}
	if v := l[`lfx_applications{"status":"pending"}`]; v.Value != 1 {
		t.Errorf("lfx_applications = %+v", v)
	}
	if _, ok := l[`divy_manual_metric_updated_timestamp_seconds{"metric":"savely_active_users"}`]; ok {
		t.Error("a TODO updated_at must not produce a timestamp")
	}
	// idle rerun inside the heartbeat writes nothing
	col2 := New(Config{Now: func() time.Time { return frozen.Add(10 * time.Minute) }}, c, st)
	if res, _ := col2.Run(context.Background()); res.Items != 0 {
		t.Errorf("idle rerun wrote %d", res.Items)
	}
}

func TestUpdatedAt(t *testing.T) {
	st := newStore(t)
	col := New(Config{Metrics: []model.ManualMetric{{Metric: "savely_active_users", Value: 5200, Source: "store stats", UpdatedAt: "2026-08-01"}}, Now: func() time.Time { return frozen }}, nil, st)
	res, err := col.Run(context.Background())
	if err != nil || res.Items != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	l := latest(t, st)
	want := float64(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Unix())
	if v := l[`divy_manual_metric_updated_timestamp_seconds{"metric":"savely_active_users"}`]; v.Value != want {
		t.Errorf("updated timestamp = %v, want %v", v.Value, want)
	}
	bad := New(Config{Metrics: []model.ManualMetric{{Metric: "x", Value: 1, UpdatedAt: "2026-13-40"}}}, nil, st)
	if _, err := bad.Run(context.Background()); err == nil {
		t.Error("expected an error for an invalid updated_at")
	}
}
