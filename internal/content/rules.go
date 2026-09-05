package content

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"divy.dev/internal/model"
)

// ManualMetricNames is the catalogue of hand-maintained metric names.
var ManualMetricNames = map[string]bool{"savely_active_users": true, "lfx_applications": true}

// RequiredAlerts are the alert names the brief demands.
var RequiredAlerts = []string{"DivyAvailableForHire", "HighContributionRate", "LFXApplicationPending"}

var (
	rePromDuration  = regexp.MustCompile(`^(([0-9]+)(ms|s|m|h|d|w|y))+$`)
	reDurationPart  = regexp.MustCompile(`([0-9]+)(ms|s|m|h|d|w|y)`)
	reTemplate      = regexp.MustCompile(`\{\{[^}]*\}\}`)
	reTemplateOK    = regexp.MustCompile(`^\{\{\s*(\$value|\$labels\.[a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}$`)
	reThresholdExpr = regexp.MustCompile(`(>=|<=|==|!=|>|<)\s*([0-9]+(\.[0-9]+)?)\s*$`)
	reHTTPURL       = regexp.MustCompile(`^https?://`)
)

var unitSeconds = map[string]int64{"ms": 0, "s": 1, "m": 60, "h": 3600, "d": 86400, "w": 604800, "y": 31536000}
var unitOrder = map[string]int{"y": 0, "w": 1, "d": 2, "h": 3, "m": 4, "s": 5, "ms": 6}

// ParsePromDuration parses a Prometheus duration (units y w d h m s ms in
// descending order, each at most once).
func ParsePromDuration(s string) (time.Duration, error) {
	if !rePromDuration.MatchString(s) {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	var total time.Duration
	last := -1
	for _, m := range reDurationPart.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		unit := m[2]
		if unitOrder[unit] <= last {
			return 0, fmt.Errorf("invalid duration %q: units must be descending and unique", s)
		}
		last = unitOrder[unit]
		if unit == "ms" {
			total += time.Duration(n) * time.Millisecond
		} else {
			total += time.Duration(n*unitSeconds[unit]) * time.Second
		}
	}
	return total, nil
}

func (l *loader) rules() {
	c := l.c
	if c.loaded["spans.yaml"] {
		l.spanRules()
	}
	if c.loaded["logs.ndjson"] {
		l.logRules()
	}
	l.postmortemRules()
	if c.loaded["panels.yaml"] {
		l.panelRules()
	}
	if c.loaded["alerts.yaml"] {
		l.alertRules()
	}
	if c.loaded["uptime.yaml"] {
		l.uptimeRules()
	}
	if c.loaded["manual_metrics.yaml"] {
		l.manualRules()
	}
	if c.loaded["profile.yaml"] {
		l.profileRules()
	}
}

// loc returns the line/col of a JSONPath inside a loaded YAML file.
func (l *loader) loc(name, path string) (int, int) {
	doc := l.docs[name]
	if doc == nil {
		return 0, 0
	}
	n := locate(doc.root, pathTokens(path))
	if n == nil {
		return 0, 0
	}
	return n.Line, n.Column
}

// pathTokens turns $.a.b[0].c into ["a","b","0","c"].
func pathTokens(path string) []string {
	path = strings.TrimPrefix(path, "$")
	var out []string
	cur := strings.Builder{}
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range path {
		switch r {
		case '.':
			flush()
		case '[':
			flush()
		case ']':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

func (l *loader) spanErr(n *Node, field, rule, format string, args ...any) {
	path := n.Path
	if field != "" {
		path += "." + field
	}
	line, col := l.loc("spans.yaml", path)
	l.c.Report.errorf(l.rel("spans.yaml"), line, col, rule, path, format, args...)
}

func (l *loader) spanRules() {
	c := l.c
	now := l.opts.Now
	root := c.Root()
	if root == nil {
		return
	}
	// spans.root
	if root.Span.ID != RootSpanID {
		l.spanErr(root, "id", "spans.root", "root span id must be %s, got %q", RootSpanID, root.Span.ID)
	}
	if !root.Span.Open {
		l.spanErr(root, "open", "spans.root", "root span must have open: true")
	}
	if root.StartPrecision == PrecisionTodo {
		l.spanErr(root, "start", "spans.root", "root span start must not be TODO(divy)")
	}
	seen := map[string]*Node{}
	for _, n := range c.nodes {
		sp := n.Span
		// spans.id-unique
		if prev, dup := seen[sp.ID]; dup {
			l.spanErr(n, "id", "spans.id-unique", "duplicate span id %q (first at %s)", sp.ID, prev.Path)
		} else {
			seen[sp.ID] = n
		}
		// spans.service
		if _, ok := c.services[sp.Service]; !ok {
			l.spanErr(n, "service", "spans.service", "service %q is not in services[]", sp.Service)
		}
		// spans.dates
		if n.startErr != nil {
			l.spanErr(n, "start", "spans.dates", "%v", n.startErr)
		}
		if n.endErr != nil {
			l.spanErr(n, "end", "spans.dates", "%v", n.endErr)
		}
		startKnown := n.StartPrecision != PrecisionTodo
		endKnown := n.EndPrecision != PrecisionTodo && n.EndPrecision != PrecisionOpen
		if startKnown && endKnown && !n.End.After(n.Start) {
			l.spanErr(n, "end", "spans.dates", "end %s must be after start %s", sp.End, sp.Start)
		}
		if sp.Open && sp.End != "" && endKnown && !n.End.After(now) {
			l.spanErr(n, "end", "spans.dates", "open span's planned end %s must be after now", sp.End)
		}
		if !sp.Open && endKnown && n.End.After(now) {
			l.spanErr(n, "end", "spans.dates", "closed span ends in the future (%s); set open: true or fix the date", sp.End)
		}
		if p := n.Parent; p != nil {
			if startKnown && p.StartPrecision != PrecisionTodo && n.Start.Before(p.Start) {
				l.spanErr(n, "start", "spans.dates", "child starts before parent %s (%s < %s)", p.Span.ID, sp.Start, p.Span.Start)
			}
			pEnd := p.AxisEnd(now)
			if endKnown && !p.Span.Open && p.EndPrecision != PrecisionTodo && n.End.After(pEnd) {
				l.spanErr(n, "end", "spans.dates", "child ends after parent %s (%s > %s)", p.Span.ID, sp.End, p.Span.End)
			}
		}
		for i, ev := range sp.Events {
			t, prec, err := ParseDate(string(ev.TS))
			field := "events[" + strconv.Itoa(i) + "].ts"
			if err != nil {
				l.spanErr(n, field, "spans.dates", "%v", err)
				continue
			}
			if prec == PrecisionTodo {
				continue
			}
			if startKnown && t.Before(n.Start) {
				l.spanErr(n, field, "spans.dates", "event %q at %s is before the span start %s", ev.Name, ev.TS, sp.Start)
			}
			if end := n.AxisEnd(now); endKnown && !t.Before(end) {
				l.spanErr(n, field, "spans.dates", "event %q at %s is after the span end %s", ev.Name, ev.TS, sp.End)
			}
		}
		// spans.todo-prefix
		for i, t := range sp.Todo {
			if !strings.HasPrefix(t, TodoMarker) {
				l.spanErr(n, "todo["+strconv.Itoa(i)+"]", "spans.todo-prefix", "todo items must start with TODO(divy)")
			}
		}
		// spans.link-url
		for i, lk := range sp.Links {
			field := "links[" + strconv.Itoa(i) + "]"
			if lk.Kind == "postmortem" {
				if lk.Ref == "" {
					l.spanErr(n, field, "spans.link-url", "postmortem link needs ref")
				}
				continue
			}
			if !reHTTPURL.MatchString(lk.URL) && !model.IsTodo(lk.URL) {
				l.spanErr(n, field+".url", "spans.link-url", "url must be http(s):// or TODO(divy), got %q", lk.URL)
			}
		}
	}
	// links.postmortem-bidirectional (span → postmortem direction)
	for _, n := range c.nodes {
		for i, lk := range n.Span.Links {
			if lk.Kind != "postmortem" {
				continue
			}
			field := "links[" + strconv.Itoa(i) + "]"
			pm, ok := c.pms[lk.Ref]
			if !ok {
				if c.loaded["postmortems"] {
					l.spanErr(n, field, "links.postmortem-bidirectional", "span %s links %s but content/postmortems/%s.md does not exist", n.Span.ID, lk.Ref, lk.Ref)
				}
				continue
			}
			if pm.Front.Span != n.Span.ID {
				l.spanErr(n, field, "links.postmortem-bidirectional", "span %s links %s but %s has span: %s", n.Span.ID, lk.Ref, pm.File, pm.Front.Span)
			}
		}
	}
}

func (l *loader) logRules() {
	c := l.c
	rel := l.rel("logs.ndjson")
	components := map[string]bool{}
	referenced := map[string]bool{}
	for _, e := range c.Logs {
		ln := e.Line
		if _, ok := c.services[ln.Service]; !ok && c.loaded["spans.yaml"] {
			c.Report.errorf(rel, e.FileLn, 1, "spans.service", "$.service", "service %q is not in content/spans.yaml services[]", ln.Service)
		}
		if ln.Span != "" {
			if _, ok := c.byID[ln.Span]; !ok && c.loaded["spans.yaml"] {
				c.Report.errorf(rel, e.FileLn, 1, "logs.span", "$.span", "span %q not found in content/spans.yaml", ln.Span)
			}
			referenced[ln.Span] = true
		}
		if ln.Component != "" {
			components[ln.Component] = true
		}
		if !model.IsTodo(ln.TS) {
			t, err := time.Parse(time.RFC3339Nano, ln.TS)
			switch {
			case err != nil || !strings.HasSuffix(ln.TS, "Z"):
				c.Report.errorf(rel, e.FileLn, 1, "logs.ts", "$.ts", "ts must be RFC 3339 UTC ending in Z or TODO(divy), got %q", ln.TS)
			case ln.Precision == "month" && (t.Day() != 1 || t.Hour() != 0 || t.Minute() != 0 || t.Second() != 0 || t.Nanosecond() != 0):
				c.Report.errorf(rel, e.FileLn, 1, "logs.ts", "$.ts", "precision month requires the first of the month at 00:00:00Z, got %q", ln.TS)
			case ln.Precision == "year" && (t.YearDay() != 1 || t.Hour() != 0 || t.Minute() != 0 || t.Second() != 0):
				c.Report.errorf(rel, e.FileLn, 1, "logs.ts", "$.ts", "precision year requires January 1 at 00:00:00Z, got %q", ln.TS)
			}
		}
	}
	if len(components) > 20 {
		c.Report.warnf(rel, 0, 0, "logs.cardinality", "", "%d distinct component values (max 20)", len(components))
	}
	if n := len(c.Logs); n < 60 || n > 100 {
		c.Report.warnf(rel, 0, 0, "logs.count", "", "%d log lines; want 60..100", n)
	}
	if c.loaded["spans.yaml"] {
		var missing []string
		for _, n := range c.nodes {
			if (n.StartPrecision != PrecisionTodo || n.Span.Status == "error") && !referenced[n.Span.ID] {
				missing = append(missing, n.Span.ID)
			}
		}
		if len(missing) > 0 {
			c.Report.warnf(rel, 0, 0, "logs.coverage", "", "%d spans with a known start or status error have no log line: %s", len(missing), strings.Join(missing, ", "))
		}
	}
}

func (l *loader) postmortemRules() {
	c := l.c
	for _, pm := range c.Postmortems {
		if c.loaded["spans.yaml"] {
			for i, s := range pm.Front.Services {
				if _, ok := c.services[s]; !ok {
					c.Report.errorf(pm.File, 0, 0, "pm.frontmatter", "$.services["+strconv.Itoa(i)+"]", "service %q is not in content/spans.yaml services[]", s)
				}
			}
			n, ok := c.byID[pm.Front.Span]
			if !ok {
				c.Report.errorf(pm.File, 0, 0, "links.postmortem-bidirectional", "$.span", "span %q not found in content/spans.yaml", pm.Front.Span)
			} else {
				linked := false
				for _, lk := range n.Span.Links {
					if lk.Kind == "postmortem" && lk.Ref == pm.Front.ID {
						linked = true
					}
				}
				if !linked {
					c.Report.errorf(pm.File, 0, 0, "links.postmortem-bidirectional", "$.span", "span %s must carry links: [{kind: postmortem, ref: %s}]", pm.Front.Span, pm.Front.ID)
				}
			}
		}
		if !model.IsTodo(pm.Front.Duration) {
			if _, err := ParsePromDuration(pm.Front.Duration); err != nil {
				c.Report.errorf(pm.File, 0, 0, "pm.frontmatter", "$.duration", "%v", err)
			}
		}
		if _, _, err := ParseDate(string(pm.Front.Date)); err != nil {
			c.Report.errorf(pm.File, 0, 0, "pm.frontmatter", "$.date", "%v", err)
		}
		if !equalStrings(pm.Sections, RequiredSections) {
			c.Report.errorf(pm.File, 0, 0, "pm.sections", "", "H2 sections must be exactly [%s] in order; got [%s]", strings.Join(RequiredSections, " · "), strings.Join(pm.Sections, " · "))
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (l *loader) panelRules() {
	c := l.c
	rel := l.rel("panels.yaml")
	ids := map[string]int{}
	type rect struct {
		id         string
		x, y, w, h int
	}
	var rects []rect
	for i, p := range c.Panels.Panels {
		path := "$.panels[" + strconv.Itoa(i) + "]"
		line, col := l.loc("panels.yaml", path)
		if prev, dup := ids[p.ID]; dup {
			c.Report.errorf(rel, line, col, "panels.grid", path+".id", "duplicate panel id %q (first at $.panels[%d])", p.ID, prev)
		}
		ids[p.ID] = i
		g := p.GridPos
		if g.X+g.W > 24 || g.W < 1 || g.H < 2 || g.X < 0 || g.Y < 0 {
			c.Report.errorf(rel, line, col, "panels.grid", path+".gridPos", "gridPos must satisfy 0 ≤ x, x+w ≤ 24, w ≥ 1, h ≥ 2; got x=%d y=%d w=%d h=%d", g.X, g.Y, g.W, g.H)
		}
		for _, r := range rects {
			if g.X < r.x+r.w && r.x < g.X+g.W && g.Y < r.y+r.h && r.y < g.Y+g.H {
				c.Report.errorf(rel, line, col, "panels.grid", path+".gridPos", "panel %q overlaps panel %q", p.ID, r.id)
			}
		}
		rects = append(rects, rect{p.ID, g.X, g.Y, g.W, g.H})
		for j, t := range p.Targets {
			tpath := path + ".targets[" + strconv.Itoa(j) + "].expr"
			if strings.TrimSpace(t.Expr) == "" {
				c.Report.errorf(rel, line, col, "panels.expr", tpath, "expr must not be empty")
			} else if l.opts.CheckExpr != nil {
				if err := l.opts.CheckExpr(t.Expr); err != nil {
					c.Report.errorf(rel, line, col, "panels.expr", tpath, "%v", err)
				}
			}
		}
		if p.Source.Kind == "manual" {
			if p.Source.UpdatedMetric == "" {
				c.Report.errorf(rel, line, col, "panels.manual-source", path+".source", "manual source needs updated_metric")
			} else {
				found := false
				for _, t := range p.Targets {
					if t.Hide && strings.TrimSpace(t.Expr) == strings.TrimSpace(p.Source.UpdatedMetric) {
						found = true
					}
				}
				if !found {
					c.Report.errorf(rel, line, col, "panels.manual-source", path+".targets", "updated_metric %q must be referenced by a hidden target", p.Source.UpdatedMetric)
				}
			}
		}
	}
	if c.Panels.Dashboard.Refresh != "" {
		if _, err := ParsePromDuration(c.Panels.Dashboard.Refresh); err != nil {
			c.Report.errorf(rel, 0, 0, "panels.grid", "$.dashboard.refresh", "%v", err)
		}
	}
	found := false
	for _, o := range c.Panels.Dashboard.Time.Options {
		if o == c.Panels.Dashboard.Time.Default {
			found = true
		}
	}
	if !found {
		c.Report.errorf(rel, 0, 0, "panels.grid", "$.dashboard.time.default", "default %q is not one of options", c.Panels.Dashboard.Time.Default)
	}
}

func (l *loader) alertRules() {
	c := l.c
	rel := l.rel("alerts.yaml")
	have := map[string]bool{}
	for gi, g := range c.Alerts.Groups {
		gpath := "$.groups[" + strconv.Itoa(gi) + "]"
		if g.Interval != "" {
			if _, err := ParsePromDuration(g.Interval); err != nil {
				line, col := l.loc("alerts.yaml", gpath+".interval")
				c.Report.errorf(rel, line, col, "alerts.rulefmt", gpath+".interval", "%v", err)
			}
		}
		for ri, r := range g.Rules {
			path := gpath + ".rules[" + strconv.Itoa(ri) + "]"
			line, col := l.loc("alerts.yaml", path)
			have[r.Alert] = true
			if r.For != "" {
				if _, err := ParsePromDuration(r.For); err != nil {
					c.Report.errorf(rel, line, col, "alerts.rulefmt", path+".for", "%v", err)
				}
			}
			if strings.TrimSpace(r.Expr) == "" {
				c.Report.errorf(rel, line, col, "alerts.rulefmt", path+".expr", "expr must not be empty")
			} else if l.opts.CheckExpr != nil {
				if err := l.opts.CheckExpr(r.Expr); err != nil {
					c.Report.errorf(rel, line, col, "panels.expr", path+".expr", "%v", err)
				}
			}
			for k, v := range r.Annotations {
				for _, m := range reTemplate.FindAllString(v, -1) {
					if !reTemplateOK.MatchString(m) {
						c.Report.errorf(rel, line, col, "alerts.rulefmt", path+".annotations."+k, "annotations may only use {{ $value }} and {{ $labels.<x> }}; got %s", m)
					}
				}
			}
			if th, ok := r.Labels["threshold_per_week"]; ok {
				m := reThresholdExpr.FindStringSubmatch(strings.TrimSpace(r.Expr))
				if m == nil {
					c.Report.errorf(rel, line, col, "alerts.threshold-matches", path+".expr", "expr must end with a comparison against a numeric literal when labels.threshold_per_week is set")
				} else if a, b := parseNum(m[2]), parseNum(th); a != b {
					c.Report.errorf(rel, line, col, "alerts.threshold-matches", path+".expr", "literal %s in expr does not equal labels.threshold_per_week %q", m[2], th)
				}
			}
		}
	}
	for _, name := range RequiredAlerts {
		if !have[name] {
			c.Report.errorf(rel, 0, 0, "alerts.required", "$.groups", "alert %s is required", name)
		}
	}
}

func parseNum(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func (l *loader) uptimeRules() {
	c := l.c
	rel := l.rel("uptime.yaml")
	ids := map[string]bool{}
	real := 0
	for i, t := range c.Uptime.Targets {
		path := "$.targets[" + strconv.Itoa(i) + "]"
		line, col := l.loc("uptime.yaml", path)
		if ids[t.ID] {
			c.Report.errorf(rel, line, col, "uptime.ids", path+".id", "duplicate target id %q", t.ID)
		}
		ids[t.ID] = true
		if t.Span != "" && c.loaded["spans.yaml"] {
			if _, ok := c.byID[t.Span]; !ok {
				c.Report.errorf(rel, line, col, "uptime.ids", path+".span", "span %q not found in content/spans.yaml", t.Span)
			}
		}
		if reHTTPURL.MatchString(t.URL) {
			real++
		} else if !model.IsTodo(t.URL) {
			c.Report.errorf(rel, line, col, "spans.link-url", path+".url", "url must be http(s):// or TODO(divy), got %q", t.URL)
		}
		for _, d := range []struct{ k, v string }{{"timeout", t.Timeout}, {"interval", t.Interval}} {
			if d.v != "" {
				if _, err := ParsePromDuration(d.v); err != nil {
					c.Report.errorf(rel, line, col, "uptime.ids", path+"."+d.k, "%v", err)
				}
			}
		}
		for _, code := range t.ExpectedStatus {
			if code < 100 || code > 599 {
				c.Report.errorf(rel, line, col, "uptime.ids", path+".expected_status", "status code %d out of range", code)
			}
		}
	}
	if real == 0 {
		c.Report.warnf(rel, 0, 0, "uptime.ids", "$.targets", "no target has a real URL; every probe is unconfigured")
	}
}

func (l *loader) manualRules() {
	c := l.c
	rel := l.rel("manual_metrics.yaml")
	seen := map[string]bool{}
	for i, m := range c.Manual.Metrics {
		path := "$.metrics[" + strconv.Itoa(i) + "]"
		line, col := l.loc("manual_metrics.yaml", path)
		if !ManualMetricNames[m.Metric] {
			names := make([]string, 0, len(ManualMetricNames))
			for n := range ManualMetricNames {
				names = append(names, n)
			}
			sort.Strings(names)
			c.Report.errorf(rel, line, col, "manual.catalogue", path+".metric", "metric %q is not in the manual catalogue (%s)", m.Metric, strings.Join(names, ", "))
		}
		key := m.Metric + CanonicalLabels(m.Labels)
		if seen[key] {
			c.Report.errorf(rel, line, col, "manual.catalogue", path, "duplicate (metric, labels) %s", key)
		}
		seen[key] = true
		if !m.UpdatedAt.IsTodo() && !reDay.MatchString(string(m.UpdatedAt)) {
			c.Report.errorf(rel, line, col, "manual.catalogue", path+".updated_at", "updated_at must be YYYY-MM-DD or TODO(divy), got %q", m.UpdatedAt)
		}
		if _, _, err := ParseDate(string(m.UpdatedAt)); err != nil {
			c.Report.errorf(rel, line, col, "manual.catalogue", path+".updated_at", "%v", err)
		}
	}
}

// CanonicalLabels renders labels as a sorted {k="v",…} string.
func CanonicalLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, labels[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func (l *loader) profileRules() {
	c := l.c
	rel := l.rel("profile.yaml")
	p := c.Profile
	if len(p.OpenTo) == 0 {
		c.Report.errorf(rel, 0, 0, "profile.healthz", "$.open_to", "open_to must not be empty")
	}
	if _, err := time.LoadLocation(p.TZ); err != nil {
		line, col := l.loc("profile.yaml", "$.tz")
		c.Report.errorf(rel, line, col, "profile.healthz", "$.tz", "tz %q: %v", p.TZ, err)
	}
	links := []struct{ k, v string }{{"github", p.Links.GitHub}, {"email", p.Links.Email}, {"linkedin", p.Links.LinkedIn}, {"resume", p.Links.Resume}, {"calendar", p.Links.Calendar}}
	for _, lk := range links {
		ok := reHTTPURL.MatchString(lk.v) || model.IsTodo(lk.v) || strings.HasPrefix(lk.v, "mailto:") || (lk.k == "resume" && strings.HasPrefix(lk.v, "/"))
		if !ok {
			line, col := l.loc("profile.yaml", "$.links."+lk.k)
			c.Report.errorf(rel, line, col, "spans.link-url", "$.links."+lk.k, "must be http(s)://, mailto:, a site-relative path (resume only) or TODO(divy); got %q", lk.v)
		}
	}
	names := map[string]bool{}
	for i, pod := range p.Pods {
		path := "$.pods[" + strconv.Itoa(i) + "]"
		line, col := l.loc("profile.yaml", path)
		if names[pod.Name] {
			c.Report.errorf(rel, line, col, "profile.healthz", path+".name", "duplicate pod name %q", pod.Name)
		}
		names[pod.Name] = true
		if c.loaded["spans.yaml"] {
			if _, ok := c.byID[pod.Span]; !ok {
				c.Report.errorf(rel, line, col, "profile.healthz", path+".span", "span %q not found in content/spans.yaml", pod.Span)
			}
		}
	}
}
