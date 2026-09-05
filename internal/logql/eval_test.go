package logql

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fixtureLines are the 11 lines of internal/content/testdata/valid/logs.ndjson
// with the ordering timestamps the content loader assigns (ts, or the span /
// root start for TODO(divy), plus the line index in nanoseconds).
var fixtureLines = []struct {
	ts   int64
	line string
}{
	{1672531200000000000, `{"ts":"2023-01-01T00:00:00Z","precision":"year","level":"info","service":"edu","span":"edu.btech-ece","msg":"enrolled: B.Tech Electronics & Communication Engineering, CTAE Udaipur","expected_graduation":"2027"}`},
	{1714521600000000001, `{"ts":"2024-05-01T00:00:00Z","precision":"month","level":"info","service":"ef-polymer","span":"ef-polymer.swe-intern","msg":"joined EF Polymer Ltd. as Software Engineering Intern","sector":"agritech","company_country":"JP"}`},
	{1751328000000000002, `{"ts":"2025-07-01T00:00:00Z","precision":"month","level":"info","service":"ef-polymer","span":"ef-polymer.swe-intern","msg":"internship complete: Sales & Warehouse Management System deployed across multiple warehouses","team":"japan","months_with_team":12}`},
	{1754006400000000003, `{"ts":"2025-08-01T00:00:00Z","precision":"month","level":"info","service":"euro-tech","span":"euro-tech.go-iam-intern","msg":"joined Euro Technologies as Go/IAM Intern","lang":"go"}`},
	{1761955200000000004, `{"ts":"2025-11-01T00:00:00Z","precision":"month","level":"info","service":"euro-tech","span":"euro-tech.go-iam-intern","msg":"shipped Euro-IAM: multi-tenant OIDC, WebAuthn, TOTP, magic links, SSO","lang":"go","stack":"gin,gorm,postgres,redis,asynq"}`},
	{1764547200000000005, `{"ts":"2025-12-01T00:00:00Z","precision":"month","level":"info","service":"gradr","span":"gradr.intern","msg":"joined Gradr as Intern","company":"gradr.se","country":"SE"}`},
	{1772323200000000006, `{"ts":"2026-03-01T00:00:00Z","precision":"month","level":"info","service":"gradr","span":"gradr.product-engineer","msg":"promoted to Product Engineer","from":"intern"}`},
	{1772323200000000007, `{"ts":"TODO(divy)","level":"warn","service":"gradr","component":"secrets-sidecar","span":"gradr.inc-001","msg":"post-reboot race: secrets sidecar wrote .env after app containers started; Supabase-backed service down","incident":"INC-001","resolved":true}`},
	{1772323200000000008, `{"ts":"TODO(divy)","level":"warn","service":"gradr","component":"dev-proxy","span":"gradr.inc-002","msg":"cascading memory exhaustion: sentry containers saturating swap","incident":"INC-002","containers_approx":65,"resolved":true}`},
	{1672531200000000009, `{"ts":"TODO(divy)","level":"debug","service":"oss","span":"oss.wasmedge-prep","msg":"first x86-64 asm routine of the 128-bit arithmetic library compiles","lang":"asm"}`},
	{1672531200000000010, `{"ts":"TODO(divy)","level":"debug","service":"quant","msg":"alpha submitted on WorldQuant BRAIN","platform":"brain"}`},
}

// fixtureStore indexes the fixture lines (plus any extra streams) by stream labels.
func fixtureStore(extra ...Stream) *Store {
	byKey := map[string]*Stream{}
	var order []string
	for _, l := range fixtureLines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l.line), &m); err != nil {
			panic(err)
		}
		labels := map[string]string{"service": m["service"].(string), "level": m["level"].(string)}
		if c, ok := m["component"].(string); ok {
			labels["component"] = c
		}
		ls := NewLabels(labels)
		k := ls.String()
		st, ok := byKey[k]
		if !ok {
			st = &Stream{Labels: ls}
			byKey[k] = st
			order = append(order, k)
		}
		st.Entries = append(st.Entries, Entry{TS: l.ts, Line: l.line})
	}
	var streams []Stream
	for _, k := range order {
		streams = append(streams, *byKey[k])
	}
	return NewStore(append(streams, extra...))
}

func mustParse(t *testing.T, q string) Query {
	t.Helper()
	p, err := Parse(q)
	if err != nil {
		t.Fatalf("parse %q: %v", q, err)
	}
	return p
}

// lineNums maps the returned entries to their 1-based fixture line numbers, in result order.
func lineNums(streams Streams) []int {
	var out []int
	for _, s := range streams {
		for _, e := range s.Entries {
			for i, l := range fixtureLines {
				if l.ts == e.TS {
					out = append(out, i+1)
				}
			}
		}
	}
	return out
}

func joinInts(xs []int) string {
	var b []string
	for _, x := range xs {
		b = append(b, string(rune('0'+x/10))+string(rune('0'+x%10)))
	}
	return strings.Join(b, " ")
}

var (
	allStart = int64(0)
	allEnd   = time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC).UnixNano()
)

func TestLogs(t *testing.T) {
	eng := &Engine{Store: fixtureStore()}
	ctx := context.Background()
	cases := []struct {
		name    string
		query   string
		opt     LogOptions
		streams int
		lines   string // fixture line numbers in result order (streams sorted by label string)
	}{
		{"1 backward", `{service="gradr"}`, LogOptions{Start: allStart, End: allEnd, Limit: 100}, 3, "09 08 07 06"},
		{"2 backward limit 2", `{service="gradr"}`, LogOptions{Start: allStart, End: allEnd, Limit: 2}, 2, "09 08"},
		{"3 forward limit 2", `{service="gradr"}`, LogOptions{Start: allStart, End: allEnd, Limit: 2, Forward: true}, 1, "06 07"},
		{"4 contains", `{service="gradr"} |= "promoted"`, LogOptions{Start: allStart, End: allEnd, Limit: 100}, 1, "07"},
		{"5 not contains json filter", `{service="gradr"} != "promoted" | json | resolved="true"`, LogOptions{Start: allStart, End: allEnd, Limit: 100}, 2, "09 08"},
		{"6 numeric filter", `{service="gradr"} | json | containers_approx > 60`, LogOptions{Start: allStart, End: allEnd, Limit: 100}, 1, "09"},
		{"7 regex selector and line", `{service=~"euro-tech|ef-polymer"} |~ "(?i)shipped|deployed"`, LogOptions{Start: allStart, End: allEnd, Limit: 100}, 2, "03 05"},
		{"9 eq number", `{service="ef-polymer"} | json | months_with_team == 12`, LogOptions{Start: allStart, End: allEnd, Limit: 100}, 1, "03"},
		{"10 or", `{service="gradr"} | json | containers_approx > 60 or from="intern"`, LogOptions{Start: allStart, End: allEnd, Limit: 100}, 2, "09 07"},
		{"11 window", `{level="debug"}`, LogOptions{Start: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano(), End: time.Date(2023, 1, 1, 0, 0, 1, 0, time.UTC).UnixNano(), Limit: 100}, 2, "10 11"},
		{"11b window excludes", `{level="debug"}`, LogOptions{Start: time.Date(2023, 1, 1, 0, 0, 1, 0, time.UTC).UnixNano(), End: allEnd, Limit: 100}, 0, ""},
		{"12 label filter error kept", `{service="gradr"} | json | incident > 1`, LogOptions{Start: allStart, End: allEnd, Limit: 100}, 2, "08 09"}, // __error_details__ (INC-001 < INC-002) sorts the streams
		{"12b error dropped", `{service="gradr"} | json | incident > 1 | __error__=""`, LogOptions{Start: allStart, End: allEnd, Limit: 100}, 0, ""},
		{"line filter after json sees raw line", `{service="gradr"} | json |= "\"incident\":\"INC-002\""`, LogOptions{Start: allStart, End: allEnd, Limit: 100}, 1, "09"},
		{"duration filter", `{service="gradr"} | json | ts_age > 5m`, LogOptions{Start: allStart, End: allEnd, Limit: 100}, 0, ""},
		{"missing component matches empty", `{service="gradr", component=""}`, LogOptions{Start: allStart, End: allEnd, Limit: 100}, 1, "07 06"},
		{"nre component", `{service="gradr", component!~"dev.*"}`, LogOptions{Start: allStart, End: allEnd, Limit: 100}, 2, "08 07 06"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := mustParse(t, c.query).(*LogQuery)
			res, stats, err := eng.Logs(ctx, q, c.opt)
			if err != nil {
				t.Fatal(err)
			}
			if len(res) != c.streams {
				t.Errorf("streams = %d, want %d: %+v", len(res), c.streams, res)
			}
			if got := joinInts(lineNums(res)); got != c.lines {
				t.Errorf("lines = %q, want %q", got, c.lines)
			}
			if stats.Streams == 0 && c.streams > 0 {
				t.Errorf("stats.Streams = 0")
			}
			for i := 1; i < len(res); i++ {
				if res[i-1].Labels.String() >= res[i].Labels.String() {
					t.Errorf("streams not sorted: %s >= %s", res[i-1].Labels, res[i].Labels)
				}
			}
		})
	}

	// row 1 stream order and per-stream entry order
	res, _, _ := eng.Logs(ctx, mustParse(t, `{service="gradr"}`).(*LogQuery), LogOptions{End: allEnd, Limit: 100})
	wantKeys := []string{`{component="dev-proxy", level="warn", service="gradr"}`, `{component="secrets-sidecar", level="warn", service="gradr"}`, `{level="info", service="gradr"}`}
	for i, k := range wantKeys {
		if res[i].Labels.String() != k {
			t.Errorf("stream %d = %s, want %s", i, res[i].Labels, k)
		}
	}
	if res[2].Entries[0].TS != fixtureLines[6].ts || res[2].Entries[1].TS != fixtureLines[5].ts {
		t.Errorf("backward order inside a stream wrong: %+v", res[2].Entries)
	}

	// row 8: `| json` on the edu line: extracted labels, no *_extracted keys
	res, _, _ = eng.Logs(ctx, mustParse(t, `{service="edu"} | json`).(*LogQuery), LogOptions{End: allEnd, Limit: 100})
	if len(res) != 1 {
		t.Fatalf("edu streams = %d", len(res))
	}
	got := res[0].Labels.String()
	want := `{expected_graduation="2027", level="info", msg="enrolled: B.Tech Electronics & Communication Engineering, CTAE Udaipur", precision="year", service="edu", span="edu.btech-ece", ts="2023-01-01T00:00:00Z"}`
	if got != want {
		t.Errorf("edu labels = %s\nwant %s", got, want)
	}

	// row 12: error labels on the kept entries
	res, _, _ = eng.Logs(ctx, mustParse(t, `{service="gradr"} | json | incident > 1`).(*LogQuery), LogOptions{End: allEnd, Limit: 100})
	for _, s := range res {
		if v, _ := s.Labels.Get(ErrorLabel); v != ErrLabelFilter {
			t.Errorf("__error__ = %q on %s", v, s.Labels)
		}
		if v, _ := s.Labels.Get(ErrorDetailsLabel); !strings.HasPrefix(v, "strconv.ParseFloat: parsing \"INC-00") {
			t.Errorf("__error_details__ = %q", v)
		}
	}

	// row 21: `| json` makes one stream per distinct extracted set
	m, _, err := eng.Range(ctx, mustParse(t, `count_over_time({service="gradr"} | json [1y])`), allEnd, allEnd, time.Second)
	if err != nil || len(m) != 4 {
		t.Errorf("json count_over_time series = %d err=%v", len(m), err)
	}
	for _, s := range m {
		if len(s.Points) != 1 || s.Points[0].V != 1 {
			t.Errorf("series %s points = %+v", s.Metric, s.Points)
		}
	}
}

func TestJSONStages(t *testing.T) {
	ctx := context.Background()
	st := fixtureStore(
		Stream{Labels: NewLabels(map[string]string{"level": "info", "service": "test"}), Entries: []Entry{
			{TS: 100, Line: `not json`},
			{TS: 200, Line: `{"a":{"b":1},"c":[1,2],"d":null,"e":true,"f":1.50,"weird key":"x","service":"other","level":"info","1st":"one","__error__":"ignored","g":{"h":{"i":"deep"}}}`},
			{TS: 300, Line: `["an","array"]`},
		}},
	)
	eng := &Engine{Store: st}
	// row 13: invalid JSON keeps the entry with error labels; the idiom drops it
	res, _, _ := eng.Logs(ctx, mustParse(t, `{service="test"} | json`).(*LogQuery), LogOptions{End: allEnd, Limit: 100, Forward: true})
	if len(res) != 3 {
		t.Fatalf("streams = %d: %+v", len(res), res)
	}
	var notJSON, parsed, array ResultStream
	for _, s := range res {
		switch s.Entries[0].TS {
		case 100:
			notJSON = s
		case 200:
			parsed = s
		case 300:
			array = s
		}
	}
	if v, _ := notJSON.Labels.Get(ErrorLabel); v != ErrJSONParser {
		t.Errorf("not json: __error__ = %q", v)
	}
	if v, _ := notJSON.Labels.Get(ErrorDetailsLabel); v == "" {
		t.Errorf("not json: __error_details__ empty")
	}
	if v, _ := array.Labels.Get(ErrorLabel); v != ErrJSONParser {
		t.Errorf("array: __error__ = %q", v)
	}
	res, _, _ = eng.Logs(ctx, mustParse(t, `{service="test"} | json | __error__=""`).(*LogQuery), LogOptions{End: allEnd, Limit: 100})
	if len(res) != 1 || res[0].Entries[0].TS != 200 {
		t.Errorf("__error__=\"\" idiom kept %+v", res)
	}
	// row 14: flattening, arrays/nulls skipped, literal numbers, key sanitizing, collision rule
	want := `{_1st="one", a_b="1", e="true", f="1.50", g_h_i="deep", level="info", service="test", service_extracted="other", weird_key="x"}`
	if got := parsed.Labels.String(); got != want {
		t.Errorf("parsed labels = %s\nwant %s", got, want)
	}
	// a second `| json` is idempotent
	res, _, _ = eng.Logs(ctx, mustParse(t, `{service="test"} | json | json | e="true"`).(*LogQuery), LogOptions{End: allEnd, Limit: 100})
	if len(res) != 1 || res[0].Labels.String() != want {
		t.Errorf("double json = %+v", res)
	}
	// numeric filter on an errored entry passes it through; a string filter can still drop it
	res, _, _ = eng.Logs(ctx, mustParse(t, `{service="test"} | json | a_b > 0`).(*LogQuery), LogOptions{End: allEnd, Limit: 100})
	if len(res) != 3 {
		t.Errorf("numeric filter over errored entries = %d streams", len(res))
	}
	// bytes and duration filters
	st2 := NewStore([]Stream{{Labels: NewLabels(map[string]string{"level": "info", "service": "b"}), Entries: []Entry{
		{TS: 1, Line: `{"size":"3 MiB","took":"1h30m","secs":"90","raw":"1.5s"}`},
		{TS: 2, Line: `{"size":"512KB","took":"5m","secs":"x"}`},
	}},
	})
	eng2 := &Engine{Store: st2}
	check := func(q string, wantTS ...int64) {
		t.Helper()
		res, _, err := eng2.Logs(ctx, mustParse(t, q).(*LogQuery), LogOptions{End: allEnd, Limit: 100, Forward: true})
		if err != nil {
			t.Fatal(err)
		}
		var got []int64
		for _, s := range res {
			for _, e := range s.Entries {
				got = append(got, e.TS)
			}
		}
		if len(got) != len(wantTS) {
			t.Errorf("%s → %v, want %v", q, got, wantTS)
			return
		}
		for i := range got {
			if got[i] != wantTS[i] {
				t.Errorf("%s → %v, want %v", q, got, wantTS)
			}
		}
	}
	check(`{service="b"} | json | size > 1MB`, 1)
	check(`{service="b"} | json | size < 1MiB`, 2)
	check(`{service="b"} | json | took > 1h`, 1)
	check(`{service="b"} | json | took <= 5m`, 2)
	check(`{service="b"} | json | secs == 90s`, 2, 1) // "x" is kept with LabelFilterErr and its stream sorts first
	check(`{service="b"} | json | raw > 1s`, 1)
	check(`{service="b"} | json | secs > 1s | __error__=""`, 1)
	res, _, _ = eng2.Logs(ctx, mustParse(t, `{service="b"} | json | secs > 1s | __error__="LabelFilterErr"`).(*LogQuery), LogOptions{End: allEnd, Limit: 100})
	if len(res) != 1 || res[0].Entries[0].TS != 2 {
		t.Errorf("duration parse error not labelled: %+v", res)
	}
}

func TestMetrics(t *testing.T) {
	eng := &Engine{Store: fixtureStore()}
	ctx := context.Background()
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).UnixNano()
	end := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC).UnixNano()
	step := 2592000 * time.Second
	render := func(m Matrix) string {
		b, _ := m.MarshalJSON()
		return string(b)
	}
	// row 15
	m, stats, err := eng.Range(ctx, mustParse(t, `count_over_time({service="gradr"}[30d])`), start, end, step)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 {
		t.Fatalf("series = %d", len(m))
	}
	for _, s := range m {
		if len(s.Points) != 1 || s.Points[0].T != 1774915200000 || s.Points[0].V != 1 {
			t.Errorf("series %s points = %+v", s.Metric, s.Points)
		}
	}
	if stats.Streams != 3 {
		t.Errorf("stats = %+v", stats)
	}
	// row 16
	m, _, _ = eng.Range(ctx, mustParse(t, `sum by (level) (count_over_time({service="gradr"}[30d]))`), start, end, step)
	if got, want := render(m), `[{"metric":{"level":"info"},"values":[[1774915200,"1"]]},{"metric":{"level":"warn"},"values":[[1774915200,"2"]]}]`; got != want {
		t.Errorf("sum by level = %s\nwant %s", got, want)
	}
	// row 17
	m, _, _ = eng.Range(ctx, mustParse(t, `rate({service="gradr"}[30d])`), start, end, step)
	for _, s := range m {
		if FormatValue(s.Points[0].V) != "0.00000038580246913580245" {
			t.Errorf("rate = %s", FormatValue(s.Points[0].V))
		}
	}
	// row 18
	v, _, err := eng.Instant(ctx, mustParse(t, `sum(count_over_time({service=~".+"}[1y]))`), allEnd)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := v.MarshalJSON(); string(b) != `[{"metric":{},"value":[1788566400,"5"]}]` {
		t.Errorf("instant sum = %s", b)
	}
	// row 19
	v, _, _ = eng.Instant(ctx, mustParse(t, `vector(1)+vector(1)`), 1757030400123000000)
	if b, _ := v.MarshalJSON(); string(b) != `[{"metric":{},"value":[1757030400.123,"2"]}]` {
		t.Errorf("vector = %s", b)
	}
	m, _, _ = eng.Range(ctx, mustParse(t, `vector(1)+vector(1)`), start, start+2*int64(time.Second), time.Second)
	if got := render(m); got != `[{"metric":{},"values":[[1772323200,"2"],[1772323201,"2"],[1772323202,"2"]]}]` {
		t.Errorf("vector range = %s", got)
	}
	// row 20: unknown grouping label ignored
	v, _, _ = eng.Instant(ctx, mustParse(t, `sum by (level, detected_level) (count_over_time({service="gradr"}[1y]))`), allEnd)
	if b, _ := v.MarshalJSON(); string(b) != `[{"metric":{"level":"info"},"value":[1788566400,"2"]},{"metric":{"level":"warn"},"value":[1788566400,"2"]}]` {
		t.Errorf("sum by level, detected_level = %s", b)
	}
	// count / min / max / avg / without
	for q, want := range map[string]string{
		`count(count_over_time({service="gradr"}[1y]))`:                    `[{"metric":{},"value":[1788566400,"3"]}]`,
		`min(count_over_time({service="gradr"}[1y]))`:                      `[{"metric":{},"value":[1788566400,"1"]}]`,
		`max(count_over_time({service="gradr"}[1y]))`:                      `[{"metric":{},"value":[1788566400,"2"]}]`,
		`avg(count_over_time({service="gradr"}[1y]))`:                      `[{"metric":{},"value":[1788566400,"1.3333333333333333"]}]`,
		`sum without (component) (count_over_time({service="gradr"}[1y]))`: `[{"metric":{"level":"info","service":"gradr"},"value":[1788566400,"2"]},{"metric":{"level":"warn","service":"gradr"},"value":[1788566400,"2"]}]`,
		`sum by () (count_over_time({service="gradr"}[1y]))`:               `[{"metric":{},"value":[1788566400,"4"]}]`,
		`count_over_time({service="nope"}[1y])`:                            `[]`,
	} {
		v, _, err := eng.Instant(ctx, mustParse(t, q), allEnd)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := v.MarshalJSON(); string(b) != want {
			t.Errorf("%s = %s\nwant %s", q, b, want)
		}
	}
	// guards
	if _, _, err := eng.Range(ctx, mustParse(t, `count_over_time({service="gradr"}[1d])`), start, end, time.Second); err == nil || !strings.HasPrefix(err.Error(), "too many steps (2592000 > 11000); increase step") {
		t.Errorf("steps guard: %v", err)
	}
	if _, _, err := eng.Range(ctx, mustParse(t, `count_over_time({service="gradr"}[1d])`), start, end, 0); err == nil {
		t.Errorf("zero step accepted")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := eng.Range(cctx, mustParse(t, `count_over_time({service="gradr"}[1d])`), start, end, step); err != context.Canceled {
		t.Errorf("cancel: %v", err)
	}
	// row 22: labels, values, series, stats
	st := eng.Store
	if got := strings.Join(st.LabelNames(nil, 0, allEnd), " "); got != "component level service" {
		t.Errorf("label names = %q", got)
	}
	sel, _ := ParseSelector(`{level="warn"}`)
	if got := strings.Join(st.LabelValues("service", sel, 0, allEnd), " "); got != "gradr" {
		t.Errorf("label values = %q", got)
	}
	if got := strings.Join(st.LabelValues("service", nil, 0, allEnd), " "); got != "edu ef-polymer euro-tech gradr oss quant" {
		t.Errorf("all services = %q", got)
	}
	if got := st.LabelValues("nope", nil, 0, allEnd); len(got) != 0 {
		t.Errorf("unknown label values = %v", got)
	}
	gsel, _ := ParseSelector(`{service="gradr"}`)
	if got := st.Series([][]*Matcher{gsel}, 0, allEnd); len(got) != 3 {
		t.Errorf("series = %d", len(got))
	}
	if got := st.Series([][]*Matcher{gsel, sel}, 0, allEnd); len(got) != 3 {
		t.Errorf("series union = %d", len(got))
	}
	ist := st.Stats(gsel, 0, allEnd)
	if ist.Streams != 3 || ist.Entries != 4 || ist.Bytes == 0 {
		t.Errorf("stats = %+v", ist)
	}
	vol := st.Volume(gsel, 0, allEnd, nil, false, 100)
	if len(vol) != 3 || vol[0].Bytes < vol[1].Bytes {
		t.Errorf("volume = %+v", vol)
	}
	vol = st.Volume(gsel, 0, allEnd, []string{"level"}, true, 1)
	if len(vol) != 1 || vol[0].Labels.String() != `{level="warn"}` && vol[0].Labels.String() != `{level="info"}` {
		t.Errorf("volume by labels = %+v", vol)
	}
}

func TestStreamsJSON(t *testing.T) {
	s := Streams{{Labels: NewLabels(map[string]string{"level": "warn", "service": "gradr"}), Entries: []Entry{{TS: 1772323200000000008, Line: `{"a":"b\"c"}`}}}}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"stream":{"level":"warn","service":"gradr"},"values":[["1772323200000000008","{\"a\":\"b\\\"c\"}"]]}]`
	if string(b) != want {
		t.Errorf("streams json = %s\nwant %s", b, want)
	}
	var back []struct {
		Stream map[string]string `json:"stream"`
		Values [][]string        `json:"values"`
	}
	if err := json.Unmarshal(b, &back); err != nil || back[0].Values[0][1] != `{"a":"b\"c"}` {
		t.Errorf("round trip: %v %+v", err, back)
	}
	if got, _ := json.Marshal(Streams{}); string(got) != "[]" {
		t.Errorf("empty streams = %s", got)
	}
	if got, _ := json.Marshal(Matrix{}); string(got) != "[]" {
		t.Errorf("empty matrix = %s", got)
	}
}
