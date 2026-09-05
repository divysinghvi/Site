package promql

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

const day = 86400 * 1000

// fixture is the promqltest-style series table of docs/drafts/promql.md
// §P.10.2 (plus probe_duration_seconds for the matching-error case);
// sample i of a series sits at i × 1d in milliseconds.
const fixture = `
github_commits_total                         0 3 3 7 12 12 20
pypi_downloads_total{package="codemind-ci"}  _ _ _ 100 130 190
reset_total                                  10 14 3 9
github_merged_prs_total{org="kubernetes"}    5 6 6 7
github_merged_prs_total{org="kubeflow"}      1 1 2 2
github_merged_prs_total{org="gradr"}         0 0 3 3
github_stars{repo="codemind"}                12 12 13 13
github_stars{repo="savely"}                  40 41 41 42
gauge_neg                                    -2.5 1.25 NaN 3.75
divy_open_to_work                            1 1 1 1
lfx_applications{status="pending"}           1 1 1 1
probe_success{target="pypi"}                 1 0 1 1
probe_duration_seconds{target="pypi"}        0.2 0.2 0.2 0.2
`

// memStorage is an in-memory Storage for tests.
type memStorage struct {
	series []SeriesData
	delay  time.Duration // optional per-Select sleep (timeout tests)
}

func (m *memStorage) Select(ctx context.Context, matchers []*Matcher, startMs, endMs int64) ([]SeriesData, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	var out []SeriesData
	for _, s := range m.series {
		if !MatchLabels(matchers, s.Metric) {
			continue
		}
		var pts []Point
		for _, p := range s.Points {
			if p.T > startMs && p.T <= endMs {
				pts = append(pts, p)
			}
		}
		if len(pts) > 0 {
			out = append(out, SeriesData{Metric: s.Metric, Points: pts})
		}
	}
	return out, nil
}

func loadFixture(t testing.TB) []SeriesData {
	t.Helper()
	data, err := LoadFixture(fixture, day, 0)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func newTestEngine(t testing.TB) *Engine {
	return &Engine{Storage: &memStorage{series: loadFixture(t)}}
}

func mustParse(t testing.TB, q string) Expr {
	t.Helper()
	e, err := ParseExpr(q)
	if err != nil {
		t.Fatalf("parse %q: %v", q, err)
	}
	return e
}

func at(d float64) time.Time { return time.UnixMilli(int64(d * day)).UTC() }

func TestInstantQueries(t *testing.T) {
	cases := []struct {
		name     string
		q        string
		t        time.Time
		lookback time.Duration
		want     string // Value.String(); "" = empty vector; "error: …" for errors
	}{
		{"E1 newest sample", `github_commits_total`, at(6), 5 * time.Minute, `{__name__="github_commits_total"} => 20 @[518400000]`},
		{"E2a inside lookback", `github_commits_total`, at(6).Add(4 * time.Minute), 5 * time.Minute, `{__name__="github_commits_total"} => 20 @[518640000]`},
		{"E2b lookback boundary excluded", `github_commits_total`, at(6).Add(5 * time.Minute), 5 * time.Minute, ``},
		{"E2c default lookback", `github_commits_total`, at(6).Add(5 * time.Minute), 0, `{__name__="github_commits_total"} => 20 @[518700000]`},
		{"E3 range left-open", `github_commits_total[2d]`, at(2), 0, "{__name__=\"github_commits_total\"} =>\n3 @[86400000]\n3 @[172800000]"},
		{"E4 count_over_time", `count_over_time(github_commits_total[2d])`, at(2), 0, `{} => 2 @[172800000]`},
		{"E5 increase zero clamp", `increase(github_commits_total[7d])`, at(6), 0, `{} => 20 @[518400000]`},
		{"E6 rate", `rate(github_commits_total[7d])`, at(6), 0, `{} => 0.00003306878306878307 @[518400000]`},
		{"E7 increase extrapolation", `increase(pypi_downloads_total[3d])`, at(5), 0, `{package="codemind-ci"} => 135 @[432000000]`},
		{"E8 rate extrapolation", `rate(pypi_downloads_total[3d])`, at(5), 0, `{package="codemind-ci"} => 0.0005208333333333333 @[432000000]`},
		{"E9 counter reset", `increase(reset_total[3d])`, at(3), 0, `{} => 13.5 @[259200000]`},
		{"E11a one sample", `increase(reset_total[1d])`, at(3), 0, ``},
		{"E11b one sample delta", `delta(github_stars[1d])`, at(3), 0, ``},
		{"E11c half day", `increase(github_commits_total[36h])`, at(1.5), 0, ``},
		{"E12a irate", `irate(reset_total[3d])`, at(3), 0, `{} => 0.00006944444444444444 @[259200000]`},
		{"E12b irate reset", `irate(reset_total[2d])`, at(2), 0, `{} => 0.00003472222222222222 @[172800000]`},
		{"E13 delta gauge", `delta(gauge_neg[3d])`, at(3), 0, `{} => 3.75 @[259200000]`},
		{"E14a sum", `sum(github_merged_prs_total)`, at(3), 0, `{} => 12 @[259200000]`},
		{"E14b sum by", `sum by (org) (github_merged_prs_total)`, at(3), 0, "{org=\"gradr\"} => 3 @[259200000]\n{org=\"kubeflow\"} => 2 @[259200000]\n{org=\"kubernetes\"} => 7 @[259200000]"},
		{"E14c sum without", `sum without (org) (github_merged_prs_total)`, at(3), 0, `{} => 12 @[259200000]`},
		{"E15a min", `min(github_stars)`, at(3), 0, `{} => 13 @[259200000]`},
		{"E15b max", `max(github_stars)`, at(3), 0, `{} => 42 @[259200000]`},
		{"E15c avg", `avg(github_stars)`, at(3), 0, `{} => 27.5 @[259200000]`},
		{"E15d count", `count(github_stars)`, at(3), 0, `{} => 2 @[259200000]`},
		{"E16a max NaN", `max(gauge_neg)`, at(2), 0, `{} => NaN @[172800000]`},
		{"E16b sum NaN", `sum(gauge_neg)`, at(2), 0, `{} => NaN @[172800000]`},
		{"E17a vector*scalar", `github_stars * 2`, at(3), 0, "{repo=\"codemind\"} => 26 @[259200000]\n{repo=\"savely\"} => 84 @[259200000]"},
		{"E17b scalar*vector", `2 * github_stars`, at(3), 0, "{repo=\"codemind\"} => 26 @[259200000]\n{repo=\"savely\"} => 84 @[259200000]"},
		{"E17c vector+vector", `github_stars + github_stars`, at(3), 0, "{repo=\"codemind\"} => 26 @[259200000]\n{repo=\"savely\"} => 84 @[259200000]"},
		{"E18a filter", `github_stars > 20`, at(3), 0, `{__name__="github_stars", repo="savely"} => 42 @[259200000]`},
		{"E18b filter swapped", `20 < github_stars`, at(3), 0, `{__name__="github_stars", repo="savely"} => 42 @[259200000]`},
		{"E19 bool", `github_stars > bool 20`, at(3), 0, "{repo=\"codemind\"} => 0 @[259200000]\n{repo=\"savely\"} => 1 @[259200000]"},
		{"E20a scalar bool", `1 == bool 1`, at(3), 0, `scalar: 1 @[259200000]`},
		{"E20b precedence", `3 + 4 * 2`, at(3), 0, `scalar: 11 @[259200000]`},
		{"E20c pow right assoc", `2 ^ 3 ^ 2`, at(3), 0, `scalar: 512 @[259200000]`},
		{"E20d mod", `7 % 3`, at(3), 0, `scalar: 1 @[259200000]`},
		{"E20e div zero", `1 / 0`, at(3), 0, `scalar: +Inf @[259200000]`},
		{"E20f neg div zero", `-1 / 0`, at(3), 0, `scalar: -Inf @[259200000]`},
		{"E20g nan", `0 / 0`, at(3), 0, `scalar: NaN @[259200000]`},
		{"E20h unary pow", `-2 ^ 2`, at(3), 0, `scalar: -4 @[259200000]`},
		{"E21a alert open", `divy_open_to_work == 1`, at(3), 0, `{__name__="divy_open_to_work"} => 1 @[259200000]`},
		{"E21b alert lfx", `lfx_applications{status="pending"} > 0`, at(3), 0, `{__name__="lfx_applications", status="pending"} => 1 @[259200000]`},
		{"E22a alert threshold not met", `sum(increase(github_commits_total[7d])) > 20`, at(6), 0, ``},
		{"E22b alert threshold met", `sum(increase(github_commits_total[7d])) > 19`, at(6), 0, `{} => 20 @[518400000]`},
		{"E23a abs", `abs(gauge_neg)`, at(0), 0, `{} => 2.5 @[0]`},
		{"E23b ceil", `ceil(gauge_neg)`, at(1), 0, `{} => 2 @[86400000]`},
		{"E23c floor", `floor(gauge_neg)`, at(1), 0, `{} => 1 @[86400000]`},
		{"E23d round", `round(gauge_neg)`, at(1), 0, `{} => 1 @[86400000]`},
		{"E23e round nearest", `round(gauge_neg, 0.5)`, at(3), 0, `{} => 4 @[259200000]`},
		{"E23f round hundredth", `round(github_stars / 3, 0.01)`, at(3), 0, "{repo=\"codemind\"} => 4.33 @[259200000]\n{repo=\"savely\"} => 14 @[259200000]"},
		{"E24a clamp_min", `clamp_min(gauge_neg, 0)`, at(0), 0, `{} => 0 @[0]`},
		{"E24b clamp_max", `clamp_max(gauge_neg, 1)`, at(1), 0, `{} => 1 @[86400000]`},
		{"E25a time", `time()`, at(3), 0, `scalar: 259200 @[259200000]`},
		{"E25b vector", `vector(1)`, at(3), 0, `{} => 1 @[259200000]`},
		{"E25c scalar one", `scalar(divy_open_to_work)`, at(3), 0, `scalar: 1 @[259200000]`},
		{"E25d scalar two", `scalar(github_stars)`, at(3), 0, `scalar: NaN @[259200000]`},
		{"E25e scalar none", `scalar(nonexistent)`, at(3), 0, `scalar: NaN @[259200000]`},
		{"E26a sum_over_time", `sum_over_time(probe_success[3d])`, at(3), 0, `{target="pypi"} => 2 @[259200000]`},
		{"E26b avg_over_time", `avg_over_time(probe_success[3d])`, at(3), 0, `{target="pypi"} => 0.6666666666666666 @[259200000]`},
		{"E26c min_over_time", `min_over_time(probe_success[3d])`, at(3), 0, `{target="pypi"} => 0 @[259200000]`},
		{"E26d max_over_time", `max_over_time(probe_success[3d])`, at(3), 0, `{target="pypi"} => 1 @[259200000]`},
		{"E26e count_over_time", `count_over_time(probe_success[3d])`, at(3), 0, `{target="pypi"} => 3 @[259200000]`},
		{"E26f last_over_time keeps name", `last_over_time(probe_success[3d])`, at(3), 0, `{__name__="probe_success", target="pypi"} => 1 @[259200000]`},
		{"E27a avg_over_time NaN", `avg_over_time(gauge_neg[3d])`, at(3), 0, `{} => NaN @[259200000]`},
		{"E27b max_over_time skips NaN", `max_over_time(gauge_neg[3d])`, at(3), 0, `{} => 3.75 @[259200000]`},
		{"E28a nonexistent", `nonexistent`, at(3), 0, ``},
		{"E28b sum nonexistent", `sum(nonexistent)`, at(3), 0, ``},
		{"E28c count nonexistent", `count(nonexistent)`, at(3), 0, ``},
		{"E28d rate nonexistent", `rate(nonexistent[1d])`, at(3), 0, ``},
		{"E28e unmatched binop", `github_stars + github_followers_nonexistent`, at(3), 0, ``},
		{"E28f filtered out", `github_stars > 100`, at(3), 0, ``},
		{"E29 scalar-like vectors", `sum(github_stars) + sum(github_merged_prs_total)`, at(3), 0, `{} => 67 @[259200000]`},
		{"E30a many-to-many", `{target="pypi"} + {target="pypi"}`, at(3), 0, `error: found duplicate series for the match group {target="pypi"} on the right hand-side of the operation: [{__name__="probe_success", target="pypi"}, {__name__="probe_duration_seconds", target="pypi"}];many-to-many matching not allowed: matching labels must be unique on one side`},
		{"E30b many-to-one", `{target="pypi"} + probe_success{target="pypi"}`, at(3), 0, `error: multiple matches for labels: many-to-one matching must be explicit (group_left/group_right)`},
		{"X1 unary vector", `-github_stars`, at(3), 0, "{repo=\"codemind\"} => -13 @[259200000]\n{repo=\"savely\"} => -42 @[259200000]"},
		{"X2 by __name__", `sum by (__name__) (github_stars)`, at(3), 0, `{__name__="github_stars"} => 55 @[259200000]`},
		{"X3 string", `"hello"`, at(3), 0, `hello`},
		{"X4 regex matcher", `github_stars{repo=~"code.*"}`, at(3), 0, `{__name__="github_stars", repo="codemind"} => 13 @[259200000]`},
		{"X5 name regex", `{__name__=~"github_merged.*",org!="gradr"}`, at(0), 0, "{__name__=\"github_merged_prs_total\", org=\"kubeflow\"} => 1 @[0]\n{__name__=\"github_merged_prs_total\", org=\"kubernetes\"} => 5 @[0]"},
		{"X6 missing label matches empty", `github_stars{status=""}`, at(3), 0, "{__name__=\"github_stars\", repo=\"codemind\"} => 13 @[259200000]\n{__name__=\"github_stars\", repo=\"savely\"} => 42 @[259200000]"},
		{"X7 NaN comparison", `gauge_neg != 0`, at(2), 0, `{__name__="gauge_neg"} => NaN @[172800000]`},
		{"X8 NaN filtered", `gauge_neg > 0`, at(2), 0, ``},
		{"X9 scalar vector cmp bool swap", `1 == bool probe_success`, at(1), 0, `{target="pypi"} => 0 @[86400000]`},
		{"X10 rate over gauge dip", `rate(probe_success[3d])`, at(3), 0, `{target="pypi"} => 0.000003858024691358025 @[259200000]`},
	}
	eng := newTestEngine(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := eng.Instant(context.Background(), mustParse(t, tc.q), tc.t, Opts{Lookback: tc.lookback})
			if strings.HasPrefix(tc.want, "error: ") {
				if err == nil {
					t.Fatalf("want error %q, got %s", tc.want, v.String())
				}
				var ee *ExecError
				if !errors.As(err, &ee) || "error: "+err.Error() != tc.want {
					t.Fatalf("error mismatch\n want: %s\n  got: error: %v", tc.want, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := v.String(); got != tc.want {
				t.Fatalf("result mismatch\n want: %s\n  got: %s", tc.want, got)
			}
		})
	}
}

func TestRangeQueries(t *testing.T) {
	cases := []struct {
		name       string
		q          string
		start, end time.Time
		step       time.Duration
		lookback   time.Duration
		want       string
	}{
		{"E10 increase steps", `increase(github_commits_total[2d])`, at(1), at(6), 24 * time.Hour, 0,
			"{} =>\n3 @[86400000]\n0 @[172800000]\n7 @[259200000]\n10 @[345600000]\n0 @[432000000]\n16 @[518400000]"},
		{"E31 short lookback", `github_commits_total`, at(0), at(3), 12 * time.Hour, 5 * time.Minute,
			"{__name__=\"github_commits_total\"} =>\n0 @[0]\n3 @[86400000]\n3 @[172800000]\n7 @[259200000]"},
		{"E32 grafana health shape", `1+1`, time.UnixMilli(1000), time.UnixMilli(4000), time.Second, 0,
			"{} =>\n2 @[1000]\n2 @[2000]\n2 @[3000]\n2 @[4000]"},
		{"E33 filter", `github_stars > 40`, at(0), at(3), 24 * time.Hour, 0,
			"{__name__=\"github_stars\", repo=\"savely\"} =>\n41 @[86400000]\n41 @[172800000]\n42 @[259200000]"},
		{"E34 time", `time()`, at(0), at(1), 12 * time.Hour, 0,
			"{} =>\n0 @[0]\n43200 @[43200000]\n86400 @[86400000]"},
		{"X11 daily counter step function", `github_merged_prs_total{org="gradr"}`, at(2), at(3), 6 * time.Hour, 0,
			"{__name__=\"github_merged_prs_total\", org=\"gradr\"} =>\n3 @[172800000]\n3 @[194400000]\n3 @[216000000]\n3 @[237600000]\n3 @[259200000]"},
		{"X12 empty matrix", `nonexistent`, at(0), at(1), time.Hour, 0, ""},
		{"X13 pypi panel expression", `rate(pypi_downloads_total{package="codemind-ci"}[2d]) * 86400`, at(4), at(5), 24 * time.Hour, 0,
			"{package=\"codemind-ci\"} =>\n29.999999999999996 @[345600000]\n59.99999999999999 @[432000000]"},
	}
	eng := newTestEngine(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := eng.Range(context.Background(), mustParse(t, tc.q), tc.start, tc.end, tc.step, Opts{Lookback: tc.lookback})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := m.String(); got != tc.want {
				t.Fatalf("result mismatch\n want: %s\n  got: %s", tc.want, got)
			}
		})
	}
}

func TestRangeTypeErrors(t *testing.T) {
	eng := newTestEngine(t)
	for q, want := range map[string]string{
		`github_stars[1d]`: `invalid expression type "range vector" for range query, must be Scalar or instant Vector`,
		`"x"`:              `invalid expression type "string" for range query, must be Scalar or instant Vector`,
	} {
		_, err := eng.Range(context.Background(), mustParse(t, q), at(0), at(1), time.Hour, Opts{})
		var rte *RangeTypeError
		if !errors.As(err, &rte) || err.Error() != want {
			t.Errorf("%s: want %q, got %v", q, want, err)
		}
	}
}

func TestGuards(t *testing.T) {
	eng := newTestEngine(t)
	eng.MaxSamples = 5
	_, err := eng.Instant(context.Background(), mustParse(t, `count_over_time(github_commits_total[7d])`), at(6), Opts{})
	if !errors.Is(err, ErrTooManySamples) {
		t.Errorf("max samples: want ErrTooManySamples, got %v", err)
	}
	eng.MaxSamples = 0
	if _, err := eng.Instant(context.Background(), mustParse(t, `count_over_time(github_commits_total[7d])`), at(6), Opts{}); err != nil {
		t.Errorf("default max samples: %v", err)
	}
	slow := &Engine{Storage: &memStorage{series: loadFixture(t), delay: 200 * time.Millisecond}}
	_, err = slow.Instant(context.Background(), mustParse(t, `github_stars`), at(3), Opts{Timeout: time.Millisecond})
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("timeout: want ErrTimeout, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = slow.Instant(ctx, mustParse(t, `github_stars`), at(3), Opts{})
	if !errors.Is(err, ErrCanceled) {
		t.Errorf("cancel: want ErrCanceled, got %v", err)
	}
}

// liveGauge is a LiveSeries for tests.
type liveGauge struct {
	name   string
	labels Labels
	f      func(t time.Time) (float64, bool)
}

func (l liveGauge) Metric() string                    { return l.name }
func (l liveGauge) Labels() Labels                    { return l.labels }
func (l liveGauge) Value(t time.Time) (float64, bool) { return l.f(t) }

type liveList struct{ series []LiveSeries }

func (l *liveList) LiveSeries() []LiveSeries { return l.series }

func newLiveList(series ...LiveSeries) *liveList { return &liveList{series: series} }

func TestLiveSeries(t *testing.T) {
	start := at(2)
	uptime := liveGauge{name: "divy_uptime_seconds", labels: Labels{}, f: func(tm time.Time) (float64, bool) {
		if tm.Before(start) {
			return 0, false
		}
		return tm.Sub(start).Seconds(), true
	}}
	build := liveGauge{name: "divy_build_info", labels: NewLabels(map[string]string{"version": "v1", "commit": "abc"}), f: func(time.Time) (float64, bool) { return 1, true }}
	eng := &Engine{Storage: &memStorage{series: loadFixture(t)}, Live: newLiveList(uptime, build)}
	cases := []struct {
		q    string
		t    time.Time
		want string
	}{
		{`divy_uptime_seconds`, at(3), `{__name__="divy_uptime_seconds"} => 86400 @[259200000]`},
		{`divy_uptime_seconds`, at(1), ``},
		{`divy_build_info{version="v1"}`, at(0), `{__name__="divy_build_info", commit="abc", version="v1"} => 1 @[0]`},
		{`{__name__=~"divy_.*"}`, at(3), "{__name__=\"divy_build_info\", commit=\"abc\", version=\"v1\"} => 1 @[259200000]\n{__name__=\"divy_open_to_work\"} => 1 @[259200000]\n{__name__=\"divy_uptime_seconds\"} => 86400 @[259200000]"},
		{`sum(divy_build_info)`, at(3), `{} => 1 @[259200000]`},
		{`last_over_time(divy_uptime_seconds[1h])`, at(3), `{__name__="divy_uptime_seconds"} => 86400 @[259200000]`},
		{`rate(divy_uptime_seconds[1h])`, at(3), ``},
	}
	for _, tc := range cases {
		v, err := eng.Instant(context.Background(), mustParse(t, tc.q), tc.t, Opts{})
		if err != nil {
			t.Fatalf("%s: %v", tc.q, err)
		}
		if got := v.String(); got != tc.want {
			t.Errorf("%s @%v:\n want: %s\n  got: %s", tc.q, tc.t, tc.want, got)
		}
	}
	m, err := eng.Range(context.Background(), mustParse(t, `divy_uptime_seconds`), at(2), at(3), 12*time.Hour, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{__name__=\"divy_uptime_seconds\"} =>\n0 @[172800000]\n43200 @[216000000]\n86400 @[259200000]"; m.String() != want {
		t.Errorf("range live:\n want: %s\n  got: %s", want, m.String())
	}
}

func TestJSONShapes(t *testing.T) {
	eng := newTestEngine(t)
	for _, tc := range []struct{ q, want string }{
		{`github_stars`, `[{"metric":{"__name__":"github_stars","repo":"codemind"},"value":[259200,"13"]},{"metric":{"__name__":"github_stars","repo":"savely"},"value":[259200,"42"]}]`},
		{`1+1`, `[259200,"2"]`},
		{`"hi"`, `[259200,"hi"]`},
		{`0/0`, `[259200,"NaN"]`},
		{`1/0`, `[259200,"+Inf"]`},
		{`1e22`, `[259200,"1e+22"]`},
		{`1e-7`, `[259200,"1e-07"]`},
		{`nonexistent`, `[]`},
	} {
		v, err := eng.Instant(context.Background(), mustParse(t, tc.q), at(3), Opts{})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := v.MarshalJSON()
		if string(b) != tc.want {
			t.Errorf("%s: want %s got %s", tc.q, tc.want, b)
		}
	}
	m, err := eng.Range(context.Background(), mustParse(t, `github_stars{repo="savely"}`), time.UnixMilli(259200500), time.UnixMilli(259201500), time.Second, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := m.MarshalJSON()
	if want := `[{"metric":{"__name__":"github_stars","repo":"savely"},"values":[[259200.500,"42"],[259201.500,"42"]]}]`; string(b) != want {
		t.Errorf("matrix json: want %s got %s", want, b)
	}
	if v := FormatValue(math.Copysign(0, -1)); v != "-0" {
		t.Errorf("negative zero: %s", v)
	}
}
