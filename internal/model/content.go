package model

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/invopop/jsonschema"
)

// ---- content/spans.yaml ----

// SpansFile is content/spans.yaml: the service list and the career trace tree.
type SpansFile struct {
	// Version is the file format version; always 1.
	Version int `json:"version" jsonschema:"minimum=1,maximum=1"`
	// Services lists every service (colour, title) spans and logs may reference.
	Services []Service `json:"services" jsonschema:"minItems=1"`
	// Trace is the root span; its id must be divy.career.
	Trace Span `json:"trace"`
}

// Service is a trace service: a company, a project family or a personal bucket.
type Service struct {
	ID    string `json:"id" jsonschema:"pattern=^[a-z0-9]+(-[a-z0-9]+)*$"`
	Title string `json:"title" jsonschema:"minLength=1"`
	// Color is the hex colour used for the service's spans.
	Color string `json:"color" jsonschema:"pattern=^#[0-9a-f]{6}$"`
	// CountsAsExperience marks services whose spans feed divy_experience_years.
	CountsAsExperience bool `json:"counts_as_experience,omitempty"`
}

// Span is one node of the career trace.
type Span struct {
	ID      string `json:"id" jsonschema:"pattern=^[a-z0-9]+(-[a-z0-9]+)*(\\.[a-z0-9]+(-[a-z0-9]+)*)+$"`
	Service string `json:"service"`
	// Title is the human label shown in the drawer; defaults to the id.
	Title string     `json:"title,omitempty"`
	Start DateOrTodo `json:"start"`
	// End is required unless open is true; with open it is the planned end.
	End DateOrTodo `json:"end,omitempty"`
	// Open marks a span that is still running (end = now unless a planned end is given).
	Open   bool       `json:"open,omitempty"`
	Status SpanStatus `json:"status,omitempty"`
	// Tags carries well-known keys (stack, role, lang, location) and any extra key.
	Tags     map[string]TagValue `json:"tags,omitempty"`
	Events   []Event             `json:"events,omitempty"`
	Links    []Link              `json:"links,omitempty"`
	Todo     []string            `json:"todo,omitempty"`
	Children []Span              `json:"children,omitempty"`
}

// JSONSchemaExtend adds the tag-key grammar, the todo-item pattern and the open/end conditional.
func (Span) JSONSchemaExtend(s *jsonschema.Schema) {
	if tags, ok := s.Properties.Get("tags"); ok {
		tags.PropertyNames = &jsonschema.Schema{Pattern: `^[a-z][a-z0-9_.]*$`}
	}
	if todo, ok := s.Properties.Get("todo"); ok {
		todo.Items = &jsonschema.Schema{Type: "string", Pattern: `^TODO\(divy\)`}
	}
	open := jsonschema.NewProperties()
	open.Set("open", &jsonschema.Schema{Const: true})
	s.If = &jsonschema.Schema{Required: []string{"open"}, Properties: open}
	s.Then = &jsonschema.Schema{}
	s.Else = &jsonschema.Schema{Required: []string{"end"}}
}

// Event is a point-in-time marker inside a span (promotion, first deploy, outage resolved).
type Event struct {
	TS    DateOrTodo        `json:"ts"`
	Name  string            `json:"name" jsonschema:"minLength=1"`
	Attrs map[string]Scalar `json:"attrs,omitempty"`
}

// Link points from a span to a postmortem, repository, PR, package page or URL.
type Link struct {
	Kind LinkKind `json:"kind"`
	// Ref is the postmortem id when kind is postmortem.
	Ref string `json:"ref,omitempty" jsonschema:"pattern=^INC-[0-9]{3}$"`
	// URL is required for every kind except postmortem; may be TODO(divy).
	URL   string `json:"url,omitempty"`
	Label string `json:"label,omitempty"`
}

// JSONSchemaExtend adds the url-or-TODO rule and the kind conditional.
func (Link) JSONSchemaExtend(s *jsonschema.Schema) {
	if u, ok := s.Properties.Get("url"); ok {
		u.AnyOf = []*jsonschema.Schema{{Format: "uri"}, {Pattern: `^TODO\(divy\)`}}
	}
	kind := jsonschema.NewProperties()
	kind.Set("kind", &jsonschema.Schema{Const: "postmortem"})
	s.AllOf = []*jsonschema.Schema{{
		If:   &jsonschema.Schema{Properties: kind},
		Then: &jsonschema.Schema{Required: []string{"ref"}},
		Else: &jsonschema.Schema{Required: []string{"url"}},
	}}
}

// ---- content/logs.ndjson ----

// LogLine is one line of content/logs.ndjson. Fixed fields are typed; any
// other key is a free-form scalar kept in Extra.
type LogLine struct {
	// TS is an RFC 3339 UTC timestamp ending in Z, or TODO(divy).
	TS string `json:"ts"`
	// Precision says what the timestamp stands for; default day.
	Precision LogPrecision `json:"precision,omitempty"`
	Level     LogLevel     `json:"level"`
	Service   string       `json:"service"`
	Msg       string       `json:"msg" jsonschema:"minLength=1,maxLength=200"`
	// Span links the line to a span id from spans.yaml.
	Span string `json:"span,omitempty"`
	// Component is an optional Loki stream label (bounded cardinality).
	Component string `json:"component,omitempty" jsonschema:"pattern=^[a-z0-9-]+$"`
	// Extra holds every other key of the line (string, number or boolean values).
	Extra map[string]any `json:"-"`
}

// JSONSchemaExtend allows free-form scalar keys with the label-name grammar and the ts rule.
func (LogLine) JSONSchemaExtend(s *jsonschema.Schema) {
	s.AdditionalProperties = &jsonschema.Schema{Extras: map[string]any{"type": []string{"string", "number", "boolean"}}}
	s.PropertyNames = &jsonschema.Schema{Pattern: LabelNamePattern}
	if ts, ok := s.Properties.Get("ts"); ok {
		ts.AnyOf = []*jsonschema.Schema{{Format: "date-time", Pattern: `Z$`}, {Pattern: TodoPattern}}
	}
}

var logFixedKeys = map[string]bool{"ts": true, "precision": true, "level": true, "service": true, "msg": true, "span": true, "component": true}

// LogReservedKeys may not be used as free-form log field names.
var LogReservedKeys = map[string]bool{"__error__": true, "__error_details__": true, "stream": true, "line": true}

// MarshalJSON writes the fixed fields followed by the extra fields (sorted by encoding/json).
func (l LogLine) MarshalJSON() ([]byte, error) {
	type fixed LogLine
	b, err := json.Marshal(fixed(l))
	if err != nil {
		return nil, err
	}
	if len(l.Extra) == 0 {
		return b, nil
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for k, v := range l.Extra {
		if _, fixedKey := logFixedKeys[k]; !fixedKey {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

var labelNameRe = regexp.MustCompile(LabelNamePattern)

// UnmarshalJSON reads the fixed fields and collects the rest into Extra.
func (l *LogLine) UnmarshalJSON(b []byte) error {
	type fixed LogLine
	var f fixed
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	dec := json.NewDecoder(bytesReader(b))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	extra := map[string]any{}
	for k, v := range raw {
		if logFixedKeys[k] {
			continue
		}
		if !labelNameRe.MatchString(k) {
			return fmt.Errorf("field %q: name must match %s", k, LabelNamePattern)
		}
		if LogReservedKeys[k] {
			return fmt.Errorf("field %q is reserved", k)
		}
		switch t := v.(type) {
		case string, bool:
			extra[k] = t
		case json.Number:
			if i, err := t.Int64(); err == nil {
				extra[k] = i
			} else if fl, err := t.Float64(); err == nil {
				extra[k] = fl
			} else {
				return fmt.Errorf("field %q: bad number %q", k, t.String())
			}
		default:
			return fmt.Errorf("field %q: value must be a string, number or boolean", k)
		}
	}
	*l = LogLine(f)
	l.Extra = extra
	return nil
}

// ---- content/postmortems/INC-NNN.md frontmatter ----

// PostmortemFrontmatter is the YAML header of a postmortem file.
type PostmortemFrontmatter struct {
	ID       string     `json:"id" jsonschema:"pattern=^INC-[0-9]{3}$"`
	Title    string     `json:"title" jsonschema:"minLength=1,maxLength=90"`
	Severity Severity   `json:"severity"`
	Date     DateOrTodo `json:"date"`
	// Span is the span id this incident hangs under; that span must link back.
	Span     string   `json:"span"`
	Services []string `json:"services" jsonschema:"minItems=1"`
	// Duration is a Prometheus duration (2h30m) or TODO(divy).
	Duration string           `json:"duration" jsonschema:"pattern=^(([0-9]+)(ms|s|m|h|d|w|y))+$|^TODO\\(divy\\)(: .+)?$"`
	Status   PostmortemStatus `json:"status"`
	Tags     []string         `json:"tags,omitempty"`
	// Summary is one sentence used on cards and OG images.
	Summary string `json:"summary" jsonschema:"minLength=1,maxLength=160"`
}

// JSONSchemaExtend constrains tag items.
func (PostmortemFrontmatter) JSONSchemaExtend(s *jsonschema.Schema) {
	if t, ok := s.Properties.Get("tags"); ok {
		t.Items = &jsonschema.Schema{Type: "string", Pattern: `^[a-z0-9-]+$`}
	}
}

// ---- content/panels.yaml ----

// PanelsFile is content/panels.yaml: the dashboard definition.
type PanelsFile struct {
	Version   int       `json:"version" jsonschema:"minimum=1,maximum=1"`
	Dashboard Dashboard `json:"dashboard"`
	Panels    []Panel   `json:"panels" jsonschema:"minItems=1"`
}

// Dashboard is the dashboard header: title, refresh cadence and time presets.
type Dashboard struct {
	Title string `json:"title" jsonschema:"minLength=1"`
	// Refresh is how often the page re-polls, as a Prometheus duration; default 60s.
	Refresh string        `json:"refresh,omitempty" jsonschema:"pattern=^(([0-9]+)(ms|s|m|h|d|w|y))+$"`
	Time    DashboardTime `json:"time"`
}

// DashboardTime holds the default time preset and the selectable options.
type DashboardTime struct {
	Default TimeOption   `json:"default"`
	Options []TimeOption `json:"options" jsonschema:"minItems=1"`
}

// Panel is one Grafana-style panel.
type Panel struct {
	ID    string    `json:"id" jsonschema:"pattern=^[a-z0-9]+(-[a-z0-9]+)*$"`
	Title string    `json:"title" jsonschema:"minLength=1"`
	Type  PanelType `json:"type"`
	// GridPos places the panel on the 24-column grid.
	GridPos GridPos  `json:"gridPos"`
	Targets []Target `json:"targets" jsonschema:"minItems=1"`
	// Unit is a Grafana unit id (short, none, percent, s, dtdurations).
	Unit     string   `json:"unit,omitempty"`
	Decimals *int     `json:"decimals,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	// Stack stacks timeseries.
	Stack      bool        `json:"stack,omitempty"`
	Thresholds *Thresholds `json:"thresholds,omitempty"`
	// Description is shown behind the panel header's (i).
	Description string `json:"description" jsonschema:"minLength=1"`
	// Source says where the numbers come from and how fresh they are.
	Source PanelSource `json:"source"`
	// Options are type-specific display options.
	Options map[string]any `json:"options,omitempty"`
}

// GridPos places a panel on the 24-column dashboard grid.
type GridPos struct {
	X int `json:"x" jsonschema:"minimum=0,maximum=23"`
	Y int `json:"y" jsonschema:"minimum=0"`
	W int `json:"w" jsonschema:"minimum=1,maximum=24"`
	H int `json:"h" jsonschema:"minimum=2"`
}

// Target is one PromQL query of a panel.
type Target struct {
	RefID        string `json:"refId" jsonschema:"pattern=^[A-Z]$"`
	Expr         string `json:"expr" jsonschema:"minLength=1"`
	LegendFormat string `json:"legendFormat,omitempty"`
	// Instant runs an instant query instead of a range query.
	Instant bool `json:"instant,omitempty"`
	// Hide runs the query but does not draw it (its value feeds last-updated stamps).
	Hide bool `json:"hide,omitempty"`
}

// Thresholds colours a panel by value.
type Thresholds struct {
	Mode  ThresholdMode   `json:"mode"`
	Steps []ThresholdStep `json:"steps" jsonschema:"minItems=1"`
}

// ThresholdStep is one threshold; the first step has a null value.
type ThresholdStep struct {
	Value *float64     `json:"value" jsonschema:"nullable"`
	Color PaletteColor `json:"color"`
}

// PanelSource documents provenance: kind, cadence and the companion timestamp metric for manual gauges.
type PanelSource struct {
	Kind    SourceKind `json:"kind"`
	Cadence string     `json:"cadence,omitempty"`
	Note    string     `json:"note,omitempty"`
	// UpdatedMetric is required for manual sources: the series that carries "last updated".
	UpdatedMetric string `json:"updated_metric,omitempty"`
}

// ---- content/alerts.yaml (Prometheus rule-file shape) ----

// AlertsFile is content/alerts.yaml in Prometheus rule-file shape.
type AlertsFile struct {
	Groups []AlertGroup `json:"groups" jsonschema:"minItems=1"`
}

// AlertGroup is a Prometheus rule group.
type AlertGroup struct {
	Name string `json:"name" jsonschema:"minLength=1"`
	// Interval is the evaluation interval as a Prometheus duration.
	Interval string      `json:"interval,omitempty" jsonschema:"pattern=^(([0-9]+)(ms|s|m|h|d|w|y))+$"`
	Rules    []AlertRule `json:"rules" jsonschema:"minItems=1"`
}

// AlertRule is a Prometheus alerting rule.
type AlertRule struct {
	Alert string `json:"alert" jsonschema:"pattern=^[A-Z][A-Za-z0-9]*$"`
	Expr  string `json:"expr" jsonschema:"minLength=1"`
	// For is how long the condition must hold before firing, as a Prometheus duration.
	For         string            `json:"for,omitempty" jsonschema:"pattern=^(([0-9]+)(ms|s|m|h|d|w|y))+$"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ---- content/uptime.yaml ----

// UptimeFile is content/uptime.yaml: the probe targets.
type UptimeFile struct {
	Targets []UptimeTarget `json:"targets" jsonschema:"minItems=1"`
}

// UptimeTarget is one probed URL.
type UptimeTarget struct {
	ID   string `json:"id" jsonschema:"pattern=^[a-z0-9]+(-[a-z0-9]+)*$"`
	Name string `json:"name" jsonschema:"minLength=1"`
	// URL is http(s) or TODO(divy); TODO targets are skipped and shown as unconfigured.
	URL    string      `json:"url"`
	Method ProbeMethod `json:"method,omitempty"`
	// ExpectedStatus is one status code or a list; default [200].
	ExpectedStatus IntOrList `json:"expected_status,omitempty"`
	// Timeout is a Prometheus duration; default 10s.
	Timeout string `json:"timeout,omitempty" jsonschema:"pattern=^(([0-9]+)(ms|s|m|h|d|w|y))+$"`
	// Interval is a Prometheus duration; default 5m.
	Interval        string `json:"interval,omitempty" jsonschema:"pattern=^(([0-9]+)(ms|s|m|h|d|w|y))+$"`
	FollowRedirects *bool  `json:"follow_redirects,omitempty"`
	// Span links the status row to a span id.
	Span string `json:"span,omitempty"`
	// Note is rendered on the uptime page (e.g. the self-probe caveat).
	Note string `json:"note,omitempty"`
}

// JSONSchemaExtend adds the url-or-TODO rule.
func (UptimeTarget) JSONSchemaExtend(s *jsonschema.Schema) {
	if u, ok := s.Properties.Get("url"); ok {
		u.AnyOf = []*jsonschema.Schema{{Format: "uri", Pattern: `^https?://`}, {Pattern: `^TODO\(divy\)`}}
	}
}

// ---- content/manual_metrics.yaml ----

// ManualMetricsFile is content/manual_metrics.yaml: hand-maintained gauges with provenance.
type ManualMetricsFile struct {
	Metrics []ManualMetric `json:"metrics" jsonschema:"minItems=1"`
}

// ManualMetric is one hand-maintained gauge.
type ManualMetric struct {
	Metric string            `json:"metric" jsonschema:"pattern=^[a-zA-Z_:][a-zA-Z0-9_:]*$"`
	Labels map[string]string `json:"labels,omitempty"`
	Value  float64           `json:"value"`
	// Source says where the number comes from; may be TODO(divy).
	Source string `json:"source" jsonschema:"minLength=1"`
	// UpdatedAt is YYYY-MM-DD or TODO(divy).
	UpdatedAt DateOrTodo `json:"updated_at"`
	Note      string     `json:"note,omitempty"`
}

// ---- content/profile.yaml ----

// Profile is content/profile.yaml: identity, links, the healthz payload, the escalation path and the pods.
type Profile struct {
	Name     string `json:"name" jsonschema:"minLength=1"`
	Handle   string `json:"handle" jsonschema:"minLength=1"`
	Location string `json:"location" jsonschema:"minLength=1"`
	// TZ is an IANA zone name served by /healthz.
	TZ         string `json:"tz" jsonschema:"minLength=1"`
	OpenToWork bool   `json:"open_to_work"`
	// OpenTo is served verbatim by /healthz.
	OpenTo []string `json:"open_to" jsonschema:"minItems=1"`
	// Tagline is a one-line tagline used by titles and the default OG image; may be TODO(divy).
	Tagline    string       `json:"tagline,omitempty"`
	Links      ProfileLinks `json:"links"`
	Escalation []Escalation `json:"escalation" jsonschema:"minItems=1"`
	Pods       []Pod        `json:"pods" jsonschema:"minItems=1"`
}

// ProfileLinks are the contact links; every one may be TODO(divy).
type ProfileLinks struct {
	GitHub   string `json:"github"`
	Email    string `json:"email"`
	LinkedIn string `json:"linkedin"`
	// Resume may be a site-relative path once the PDF is committed.
	Resume   string `json:"resume"`
	Calendar string `json:"calendar"`
}

// Escalation is one step of the runbook's escalation path.
type Escalation struct {
	Step         int    `json:"step" jsonschema:"minimum=1"`
	Channel      string `json:"channel" jsonschema:"minLength=1"`
	Target       string `json:"target" jsonschema:"minLength=1"`
	ResponseTime string `json:"response_time" jsonschema:"minLength=1"`
	Note         string `json:"note,omitempty"`
}

// Pod is one row of the promql console's kubectl get pods output.
type Pod struct {
	Name   string    `json:"name" jsonschema:"pattern=^[a-z0-9]+(-[a-z0-9]+)*$"`
	Ready  string    `json:"ready" jsonschema:"pattern=^\\d+/\\d+$"`
	Status PodStatus `json:"status"`
	// RestartsFrom says whether RESTARTS counts postmortems under the pod's span.
	RestartsFrom RestartsFrom `json:"restarts_from"`
	// Span is the span whose start gives the pod's AGE.
	Span string `json:"span"`
	Note string `json:"note,omitempty"`
}
