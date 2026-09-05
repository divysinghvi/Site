package content

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"divy.dev/internal/model"
)

var frozen = time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)

func loadValid(t *testing.T) *Content {
	t.Helper()
	c, err := Load("testdata/valid", Options{Now: frozen, SiteOrigin: "https://example.vercel.app"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Report.HasErrors(false) {
		var sb strings.Builder
		c.Report.Write(&sb, false)
		t.Fatal(sb.String())
	}
	return c
}

func TestValidFixture(t *testing.T) {
	c := loadValid(t)
	if c.Files != 11 || len(c.Nodes()) != 28 || len(c.Logs) != 11 || len(c.Postmortems) != 4 {
		t.Errorf("files=%d nodes=%d logs=%d pms=%d", c.Files, len(c.Nodes()), len(c.Logs), len(c.Postmortems))
	}
	rules := map[string]int{}
	for _, w := range c.Report.Warnings {
		rules[w.Rule]++
	}
	if rules["logs.count"] != 1 || rules["logs.coverage"] != 1 || len(c.Report.Warnings) != 2 {
		t.Errorf("warnings = %+v", c.Report.Warnings)
	}
	if len(c.Todos) == 0 || c.Todos[0].File != "content/spans.yaml" {
		t.Errorf("todos = %+v", c.Todos[:1])
	}
	// TODO inventory covers values, comments, log lines and markdown
	kinds := map[string]bool{}
	for _, td := range c.Todos {
		switch {
		case strings.HasSuffix(td.Context, "(comment)"):
			kinds["comment"] = true
		case td.File == "content/logs.ndjson" && td.Path == "$.ts":
			kinds["log"] = true
		case strings.HasPrefix(td.File, "content/postmortems/") && strings.Contains(td.Context, " › "):
			kinds["markdown"] = true
		case td.Path == "$.trace.children[1].start":
			kinds["value"] = true
			if td.Context != "freelance.web-dev" || td.Text != "TODO(divy)" || td.Line == 0 {
				t.Errorf("todo item = %+v", td)
			}
		}
	}
	for _, k := range []string{"comment", "log", "markdown", "value"} {
		if !kinds[k] {
			t.Errorf("no %s TODO found", k)
		}
	}
	// log ordering: ts, span fallback, root fallback, +index tiebreak
	first := c.Logs[0]
	if first.Line.Service != "edu" || first.TSNano != time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano() {
		t.Errorf("first log = %+v", first)
	}
	var brain, inc2 LogEntry
	for _, e := range c.Logs {
		if e.Line.Msg == "alpha submitted on WorldQuant BRAIN" {
			brain = e
		}
		if e.Line.Span == "gradr.inc-002" {
			inc2 = e
		}
	}
	if brain.TSNano != time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()+int64(brain.Index) {
		t.Errorf("TODO ts without span must land on the root start (+index): %+v", brain)
	}
	if n, _ := c.Node("gradr.inc-002"); inc2.TSNano != n.Start.UnixNano()+int64(inc2.Index) || inc2.Labels["component"] != "dev-proxy" {
		t.Errorf("TODO ts with span must land on the span start: %+v", inc2)
	}
	// experience start = earliest dated span whose service counts
	if es, ok := c.ExperienceStart(); !ok || !es.Equal(time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("experience start = %v %v", es, ok)
	}
	if y, ok := c.ExperienceYears(frozen); !ok || y < 2.3 || y > 2.4 {
		t.Errorf("experience years = %v", y)
	}
	// uptime view: $SITE_ORIGIN expansion + SelfURL override
	c2, _ := Load("testdata/valid", Options{Now: frozen, SelfURL: "http://127.0.0.1:18080/readyz"})
	if v := c2.UptimeView(); v.Targets[4].URL != "http://127.0.0.1:18080/readyz" || !v.Targets[4].Configured {
		t.Errorf("self url override = %+v", v.Targets[4])
	}
}

func TestTreeResolution(t *testing.T) {
	c := loadValid(t)
	root := c.Root()
	if root.Span.ID != "divy.career" || root.StartPrecision != PrecisionYear || root.EndPrecision != PrecisionOpen || !root.Start.Equal(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("root = %+v", root)
	}
	ef, _ := c.Node("ef-polymer.swe-intern")
	if ef.StartPrecision != PrecisionMonth || !ef.Start.Equal(time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)) || !ef.End.Equal(time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)) || ef.Depth != 1 {
		t.Errorf("ef-polymer = start=%v end=%v", ef.Start, ef.End)
	}
	japan, _ := c.Node("japan.onsite")
	if japan.StartPrecision != PrecisionTodo || !japan.Start.Equal(ef.Start) || japan.EndPrecision != PrecisionTodo || !japan.End.Equal(ef.End) || japan.Depth != 2 {
		t.Errorf("TODO child must inherit the parent interval: %+v", japan)
	}
	inc2, _ := c.Node("gradr.inc-002")
	if !inc2.Start.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) || !inc2.End.Equal(frozen) || inc2.Depth != 3 || inc2.Path != "$.trace.children[5].children[0].children[1]" {
		t.Errorf("inc-002 = start=%v end=%v depth=%d path=%s", inc2.Start, inc2.End, inc2.Depth, inc2.Path)
	}
	edu, _ := c.Node("edu.btech-ece")
	if !edu.Open() || edu.EndPrecision != PrecisionYear || !edu.AxisEnd(frozen).Equal(time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("edu = %+v", edu)
	}
	// DFS order: children sorted by (start, id); TODO children share the parent's start
	ids := make([]string, 0, len(c.Nodes()))
	for _, n := range c.Nodes() {
		ids = append(ids, n.Span.ID)
	}
	if ids[0] != "divy.career" || ids[1] != "edu.btech-ece" || ids[2] != "freelance.web-dev" || ids[len(ids)-1] != "gradr.product-features" {
		t.Errorf("order = %v", ids)
	}
	if pm := c.PostmortemsFor("gradr.observability"); len(pm) != 4 || pm[0] != "INC-001" {
		t.Errorf("postmortems under observability = %v", pm)
	}
	if pm := c.PostmortemsFor("gradr.inc-003"); len(pm) != 1 {
		t.Errorf("postmortems for inc-003 = %v", pm)
	}
	if svcs := c.ServicesWithSpans(); strings.Join(svcs, ",") != "divy,edu,ef-polymer,euro-tech,freelance,gradr,oss,project,quant" {
		t.Errorf("services with spans = %v", svcs)
	}
}

func TestJaeger(t *testing.T) {
	c := loadValid(t)
	if TraceID != "9f3a0703b53d5b0aae2fb3bdacea0ff6" {
		t.Errorf("trace id = %s", TraceID)
	}
	for id, want := range map[string]string{"divy.career": "9f3a0703b53d5b0a", "edu.btech-ece": "4e76e10ea3071d79", "gradr.inc-002": "ef53e50f70cc9d38", "gradr.product-engineer": "da42f4e70b8baf7c"} {
		if got := SpanHexID(id); got != want {
			t.Errorf("SpanHexID(%s) = %s, want %s", id, got, want)
		}
	}
	tr := c.JaegerTrace(frozen)
	if tr.TraceID != TraceID || len(tr.Spans) != 28 || len(tr.Processes) != 9 {
		t.Fatalf("trace: spans=%d processes=%d", len(tr.Spans), len(tr.Processes))
	}
	byOp := map[string]model.JaegerSpan{}
	for _, s := range tr.Spans {
		byOp[s.OperationName] = s
		if s.TraceID != TraceID || s.ProcessID != "p-"+strings.SplitN(s.ProcessID, "-", 2)[1] || s.Logs == nil || s.References == nil {
			t.Errorf("span %s malformed", s.OperationName)
		}
	}
	tagOf := func(s model.JaegerSpan, key string) (model.JaegerKeyValue, bool) {
		for _, kv := range s.Tags {
			if kv.Key == key {
				return kv, true
			}
		}
		return model.JaegerKeyValue{}, false
	}
	root := byOp["divy.career"]
	if root.StartTime != 1672531200000000 || root.Duration != frozen.Sub(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)).Microseconds() || len(root.References) != 0 {
		t.Errorf("root = start=%d dur=%d", root.StartTime, root.Duration)
	}
	if kv, _ := tagOf(root, "divy.open"); kv.Type != "bool" || kv.Value != true {
		t.Errorf("divy.open = %+v", kv)
	}
	if kv, _ := tagOf(root, "divy.end_precision"); kv.Value != "open" {
		t.Errorf("root end_precision = %+v", kv)
	}
	if _, has := tagOf(root, "divy.end"); has {
		t.Error("root must not carry divy.end")
	}
	if kv, _ := tagOf(root, "divy.depth"); kv.Type != "int64" || kv.Value != int64(0) {
		t.Errorf("depth = %+v", kv)
	}
	pe := byOp["gradr.product-engineer"]
	if pe.StartTime != 1772323200000000 || len(pe.References) != 1 || pe.References[0].SpanID != root.SpanID || pe.References[0].RefType != "CHILD_OF" {
		t.Errorf("product-engineer = %+v", pe)
	}
	if len(pe.Logs) != 1 || pe.Logs[0].Timestamp != 1772323200000000 || pe.Logs[0].Fields[0].Key != "event" || pe.Logs[0].Fields[0].Value != "promoted to Product Engineer" || pe.Logs[0].Fields[1].Key != "from" {
		t.Errorf("event log = %+v", pe.Logs)
	}
	edu := byOp["edu.btech-ece"]
	if kv, _ := tagOf(edu, "divy.end_planned"); kv.Value != "2027" {
		t.Errorf("planned end = %+v", kv)
	}
	if kv, _ := tagOf(edu, "divy.end_precision"); kv.Value != "year" {
		t.Errorf("edu end_precision = %+v", kv)
	}
	inc2 := byOp["gradr.inc-002"]
	if kv, _ := tagOf(inc2, "otel.status_code"); kv.Value != "ERROR" {
		t.Errorf("status = %+v", kv)
	}
	if kv, _ := tagOf(inc2, "error"); kv.Type != "bool" || kv.Value != true {
		t.Errorf("error tag = %+v", kv)
	}
	if kv, _ := tagOf(inc2, "divy.start_precision"); kv.Value != "todo" {
		t.Errorf("todo precision = %+v", kv)
	}
	if kv, _ := tagOf(inc2, "divy.postmortems"); kv.Value != "INC-002" {
		t.Errorf("postmortems tag = %+v", kv)
	}
	if kv, _ := tagOf(inc2, "divy.links"); !strings.Contains(kv.Value.(string), `"ref":"INC-002"`) {
		t.Errorf("links tag = %+v", kv)
	}
	if len(inc2.Logs) != 1 || inc2.Logs[0].Timestamp != inc2.StartTime {
		t.Errorf("TODO event must land on the span start: %+v", inc2.Logs)
	} else if f := inc2.Logs[0].Fields; f[len(f)-1].Key != "divy.ts_precision" || f[len(f)-1].Value != "todo" {
		t.Errorf("TODO event fields = %+v", f)
	}
	euro := byOp["euro-tech.go-iam-intern"]
	if kv, _ := tagOf(euro, "stack"); kv.Type != "string" || kv.Value != `["go","gin","gorm","postgres","redis","asynq"]` {
		t.Errorf("list tag = %+v", kv)
	}
	quant := byOp["quant.worldquant-iqc"]
	if kv, _ := tagOf(quant, "global_rank"); kv.Type != "int64" || kv.Value != int64(98) {
		t.Errorf("int tag = %+v", kv)
	}
	if p := tr.Processes["p-gradr"]; p.ServiceName != "gradr" || len(p.Tags) != 3 || p.Tags[1].Value != "#5794f2" || p.Tags[2].Key != "divy.counts_as_experience" {
		t.Errorf("process = %+v", p)
	}
	if p := tr.Processes["p-divy"]; len(p.Tags) != 2 {
		t.Errorf("divy process must not carry counts_as_experience=false: %+v", p)
	}
	// stable JSON, errors/warnings null
	b, _ := json.Marshal(JaegerResponse(tr))
	if !strings.Contains(string(b), `"errors":null`) || !strings.HasPrefix(string(b), `{"data":[{"traceID":"9f3a0703b53d5b0aae2fb3bdacea0ff6","spans":[{"traceID"`) {
		t.Errorf("envelope = %s", b[:120])
	}
}

func TestPostmortemRendering(t *testing.T) {
	c := loadValid(t)
	pm, ok := c.Postmortem("INC-001")
	if !ok {
		t.Fatal("INC-001 missing")
	}
	if !strings.Contains(pm.HTML, `<h2 id="timeline-utc">Timeline (UTC)</h2>`) || !strings.Contains(pm.HTML, "<table>") || !strings.Contains(pm.HTML, `<input`) {
		t.Errorf("html = %s", pm.HTML)
	}
	if pm.TOC[2].ID != "timeline-utc" || pm.TOC[2].Level != 2 {
		t.Errorf("toc = %+v", pm.TOC)
	}
	if Slug("Timeline (UTC)") != "timeline-utc" || Slug("  Root  cause!") != "root-cause" {
		t.Error("slug rule")
	}
	// raw HTML and dangerous links are stripped
	html, toc, h2 := renderMarkdown([]byte("## A\n<script>alert(1)</script>\n\n[x](javascript:alert(1))\n\n## A\n"))
	if strings.Contains(html, "<script") || strings.Contains(html, "javascript:") {
		t.Errorf("unsafe html survived: %s", html)
	}
	if len(toc) != 2 || toc[1].ID != "a-2" || len(h2) != 2 {
		t.Errorf("duplicate heading ids: %+v", toc)
	}
	v, _ := c.PostmortemView("INC-001", "https://x.test")
	if v.OgImage != "https://x.test/og/postmortems/INC-001.png" || v.SpanURL != "/#trace?span=gradr.inc-001" || v.TodoCount == 0 {
		t.Errorf("view = %+v", v.PostmortemSummary)
	}
}

func TestDates(t *testing.T) {
	cases := []struct {
		in   string
		prec Precision
		ok   bool
	}{
		{"2023", PrecisionYear, true}, {"2024-05", PrecisionMonth, true}, {"2024-05-14", PrecisionDay, true},
		{"TODO(divy)", PrecisionTodo, true}, {"TODO(divy): later", PrecisionTodo, true},
		{"2025-02-30", "", false}, {"2024-13", "", false}, {"24-05", "", false}, {"", "", false}, {"TODO(divy)later", "", false},
	}
	for _, c := range cases {
		_, p, err := ParseDate(c.in)
		if (err == nil) != c.ok || (c.ok && p != c.prec) {
			t.Errorf("ParseDate(%q) = %v %v", c.in, p, err)
		}
	}
	if !EndOf(time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC), PrecisionMonth).Equal(time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("EndOf month")
	}
	if d, err := ParsePromDuration("1h30m"); err != nil || d != 90*time.Minute {
		t.Error("ParsePromDuration")
	}
	if _, err := ParsePromDuration("30m1h"); err == nil {
		t.Error("units must be descending")
	}
	if d, _ := ParsePromDuration("7d"); d != 7*24*time.Hour {
		t.Error("days")
	}
}

// ruleFixtures derive one failing content set each from testdata/valid: the
// named file gets one substitution, and validation must report the rule.
var ruleFixtures = []struct {
	name, file, old, new, rule string
}{
	{"yaml-date-quoted", "spans.yaml", "start: \"2024-05\"\n      end: \"2025-07\"", "start: 2024-05-14\n      end: \"2025-07\"", "yaml.date-quoted"},
	{"spans-id-unique", "spans.yaml", "- id: oss.kubeflow", "- id: oss.minikube", "spans.id-unique"},
	{"spans-root", "spans.yaml", "  id: divy.career\n  service: divy", "  id: divy.life\n  service: divy", "spans.root"},
	{"spans-service", "spans.yaml", "- id: oss.kubeflow\n      service: oss", "- id: oss.kubeflow\n      service: nope", "spans.service"},
	{"spans-dates-order", "spans.yaml", "start: \"2025-08\"\n      end: \"2025-11\"", "start: \"2025-11\"\n      end: \"2025-08\"", "spans.dates"},
	{"spans-dates-calendar", "spans.yaml", "start: \"2025-08\"\n      end: \"2025-11\"", "start: \"2025-02-30\"\n      end: \"2025-11\"", "spans.dates"},
	{"spans-dates-child", "spans.yaml", "title: Euro-IAM — multi-tenant OIDC provider core\n          start: TODO(divy)\n          end: TODO(divy)", "title: Euro-IAM — multi-tenant OIDC provider core\n          start: \"2024-01\"\n          end: \"2025-11\"", "spans.dates"},
	{"spans-dates-closed-future", "spans.yaml", "start: \"2025-12\"\n      end: \"2026-03\"", "start: \"2025-12\"\n      end: \"2099-03\"", "spans.dates"},
	{"spans-dates-event", "spans.yaml", "{ts: \"2025-11\", name: shipped Euro-IAM", "{ts: \"2019-11\", name: shipped Euro-IAM", "spans.dates"},
	{"spans-link-url", "spans.yaml", "{kind: repo, url: TODO(divy), label: 128-bit arithmetic library}", "{kind: repo, url: \"ftp://example.org/x\", label: 128-bit arithmetic library}", "spans.link-url"},
	{"links-postmortem-missing-back", "spans.yaml", "links: [{kind: postmortem, ref: INC-003}]", "links: []", "links.postmortem-bidirectional"},
	{"links-postmortem-wrong-span", "postmortems/INC-002.md", "span: gradr.inc-002", "span: gradr.inc-003", "links.postmortem-bidirectional"},
	{"logs-ndjson", "logs.ndjson", "\"platform\":\"brain\"}", "\"platform\":{\"x\":1}}", "logs.ndjson"},
	{"logs-reserved-key", "logs.ndjson", "\"platform\":\"brain\"}", "\"platform\":\"brain\",\"__error__\":\"x\"}", "logs.ndjson"},
	{"logs-ts", "logs.ndjson", "{\"ts\":\"2026-03-01T00:00:00Z\",\"precision\":\"month\"", "{\"ts\":\"2026-03-15T00:00:00Z\",\"precision\":\"month\"", "logs.ts"},
	{"logs-span", "logs.ndjson", "\"span\":\"gradr.inc-002\"", "\"span\":\"gradr.inc-009\"", "logs.span"},
	{"logs-service", "logs.ndjson", "\"service\":\"quant\"", "\"service\":\"quantum\"", "spans.service"},
	{"pm-sections", "postmortems/INC-003.md", "## Root cause", "## Cause", "pm.sections"},
	{"pm-frontmatter-stem", "postmortems/INC-004.md", "id: INC-004", "id: INC-044", "pm.frontmatter"},
	{"pm-frontmatter-duration", "postmortems/INC-004.md", "duration: TODO(divy)", "duration: 2 hours", "pm.frontmatter"},
	{"pm-sanitize-ip", "postmortems/INC-001.md", "TODO(divy): how it was noticed.", "noticed on 10.0.4.12 by the proxy", "pm.sanitize"},
	{"pm-sanitize-token", "postmortems/INC-001.md", "TODO(divy): how it was noticed.", "the token=abcdefghijklmnop was in the logs", "pm.sanitize"},
	{"panels-grid-overlap", "panels.yaml", "gridPos: {x: 6, y: 0, w: 6, h: 4}", "gridPos: {x: 3, y: 0, w: 6, h: 4}", "panels.grid"},
	{"panels-grid-bounds", "panels.yaml", "gridPos: {x: 12, y: 4, w: 12, h: 8}", "gridPos: {x: 14, y: 4, w: 12, h: 8}", "panels.grid"},
	{"panels-manual-source", "panels.yaml", "      - {refId: B, expr: 'divy_manual_metric_updated_timestamp_seconds{metric=\"savely_active_users\"}', instant: true, hide: true}\n", "", "panels.manual-source"},
	{"alerts-required", "alerts.yaml", "- alert: LFXApplicationPending", "- alert: LFXApplicationsPending", "alerts.required"},
	{"alerts-threshold", "alerts.yaml", "threshold_per_week: \"20\"", "threshold_per_week: \"25\"", "alerts.threshold-matches"},
	{"alerts-template", "alerts.yaml", "{{ $value }} LFX", "{{ humanize $value }} LFX", "alerts.rulefmt"},
	{"uptime-ids", "uptime.yaml", "- id: codemind-demo", "- id: savely-landing", "uptime.ids"},
	{"uptime-span", "uptime.yaml", "span: project.codemind\n  - id: pypi-codemind", "span: project.codemine\n  - id: pypi-codemind", "uptime.ids"},
	{"panels-expr-parse", "panels.yaml", "expr: divy_experience_years,", "expr: rate(divy_experience_years),", "panels.expr"},
	{"alerts-expr-parse", "alerts.yaml", "expr: divy_open_to_work == 1", "expr: divy_open_to_work offset 1d == 1", "panels.expr"},
	{"manual-catalogue", "manual_metrics.yaml", "metric: lfx_applications", "metric: lfx_apps", "manual.catalogue"},
	{"manual-updated-at", "manual_metrics.yaml", "updated_at: TODO(divy)\n    note: \"5,000+", "updated_at: \"2026-09\"\n    note: \"5,000+", "manual.catalogue"},
	{"profile-pod-span", "profile.yaml", "span: oss.lfx-velero-application}", "span: oss.lfx-velero}", "profile.healthz"},
	{"profile-tz", "profile.yaml", "tz: Asia/Kolkata", "tz: Mars/Olympus", "profile.healthz"},
	{"profile-link", "profile.yaml", "  github: https://github.com/divysinghvi", "  github: github.com/divysinghvi", "spans.link-url"},
	{"schema-unknown-key", "profile.yaml", "handle: divysinghvi", "handle: divysinghvi\nnickname: d", "schema"},
	{"schema-enum", "logs.ndjson", "\"level\":\"debug\",\"service\":\"quant\"", "\"level\":\"trace\",\"service\":\"quant\"", "logs.ndjson"},
	{"file-missing", "uptime.yaml", "", "", "file.missing"},
}

// copyDir copies testdata/valid into dst.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRules derives one failing content set per rule from testdata/valid and
// checks that validation reports exactly that rule with a location.
func TestRules(t *testing.T) {
	for _, fx := range ruleFixtures {
		t.Run(fx.name, func(t *testing.T) {
			dir := t.TempDir()
			copyDir(t, "testdata/valid", dir)
			p := filepath.Join(dir, fx.file)
			if fx.old == "" {
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
			} else {
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(b), fx.old) {
					t.Fatalf("fixture text %q not found in %s", fx.old, fx.file)
				}
				if err := os.WriteFile(p, []byte(strings.Replace(string(b), fx.old, fx.new, 1)), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			c, err := Load(dir, Options{Now: frozen})
			if err != nil {
				t.Fatal(err)
			}
			var rules []string
			hit := false
			for _, f := range c.Report.Errors {
				rules = append(rules, f.Rule)
				if f.Rule != fx.rule {
					continue
				}
				hit = true
				if f.File == "" || f.Message == "" {
					t.Errorf("finding without file/message: %+v", f)
				}
				located := f.Line > 0
				switch f.Rule {
				case "alerts.required", "pm.sections", "pm.frontmatter", "links.postmortem-bidirectional", "file.missing", "profile.healthz":
					located = true // file-level findings
				}
				if !located {
					t.Errorf("finding without a line: %+v", f)
				}
			}
			if !hit {
				t.Errorf("expected rule %s, got errors %v", fx.rule, rules)
			}
		})
	}
}

func TestReportOutput(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, "testdata/valid", dir)
	b, _ := os.ReadFile(filepath.Join(dir, "logs.ndjson"))
	_ = os.WriteFile(filepath.Join(dir, "logs.ndjson"), []byte(strings.Replace(string(b), `"span":"gradr.inc-002"`, `"span":"gradr.inc-009"`, 1)), 0o644)
	c, _ := Load(dir, Options{Now: frozen})
	var sb strings.Builder
	c.Report.Write(&sb, false)
	out := sb.String()
	if !strings.Contains(out, "content/logs.ndjson:9:1") || !strings.Contains(out, "logs.span") || !strings.Contains(out, "— FAIL") {
		t.Errorf("human output = %s", out)
	}
	var j struct {
		OK     bool
		Errors []Finding
		Todos  struct{ Count int }
	}
	if err := json.Unmarshal(c.Report.JSON(false), &j); err != nil || j.OK || len(j.Errors) != 1 || j.Errors[0].Rule != "logs.span" || j.Todos.Count == 0 {
		t.Errorf("json output = %s", c.Report.JSON(false))
	}
	if !c.Report.HasErrors(false) {
		t.Error("must fail")
	}
	v := loadValid(t)
	if v.Report.HasErrors(false) || !v.Report.HasErrors(true) {
		t.Error("strict must promote warnings")
	}
}

func TestLogLineJSON(t *testing.T) {
	var l model.LogLine
	if err := json.Unmarshal([]byte(`{"ts":"2026-03-01T00:00:00Z","level":"info","service":"gradr","msg":"x","from":"intern","n":3,"ok":true}`), &l); err != nil {
		t.Fatal(err)
	}
	if l.Extra["from"] != "intern" || l.Extra["n"] != int64(3) || l.Extra["ok"] != true {
		t.Errorf("extra = %+v", l.Extra)
	}
	b, _ := json.Marshal(l)
	var back map[string]any
	_ = json.Unmarshal(b, &back)
	if back["from"] != "intern" || back["msg"] != "x" || back["n"] != float64(3) {
		t.Errorf("round trip = %s", b)
	}
	if err := json.Unmarshal([]byte(`{"ts":"x","level":"info","service":"g","msg":"m","bad key":1}`), &l); err == nil {
		t.Error("bad key must fail")
	}
	if err := json.Unmarshal([]byte(`{"ts":"x","level":"info","service":"g","msg":"m","obj":{}}`), &l); err == nil {
		t.Error("object value must fail")
	}
}
