package model

// ---- Jaeger JSON (traces) ----

// JaegerTraceResponse is the body of /api/traces/{id} and /api/traces?service=.
type JaegerTraceResponse struct {
	Data   []JaegerTrace `json:"data"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
	Errors []string      `json:"errors" jsonschema:"nullable"`
}

// JaegerTrace is one trace in Jaeger JSON shape.
type JaegerTrace struct {
	TraceID   string                   `json:"traceID"`
	Spans     []JaegerSpan             `json:"spans"`
	Processes map[string]JaegerProcess `json:"processes"`
	Warnings  []string                 `json:"warnings" jsonschema:"nullable"`
}

// JaegerSpan is one span; times are microseconds since the epoch.
type JaegerSpan struct {
	TraceID       string            `json:"traceID"`
	SpanID        string            `json:"spanID"`
	OperationName string            `json:"operationName"`
	References    []JaegerReference `json:"references"`
	StartTime     int64             `json:"startTime"`
	Duration      int64             `json:"duration"`
	Tags          []JaegerKeyValue  `json:"tags"`
	Logs          []JaegerLog       `json:"logs"`
	ProcessID     string            `json:"processID"`
	Warnings      []string          `json:"warnings" jsonschema:"nullable"`
}

// JaegerReference links a span to its parent (CHILD_OF).
type JaegerReference struct {
	RefType string `json:"refType"`
	TraceID string `json:"traceID"`
	SpanID  string `json:"spanID"`
}

// JaegerKeyValue is a typed tag or log field.
type JaegerKeyValue struct {
	Key string `json:"key"`
	// Type is string, bool, int64 or float64.
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// JaegerLog is a span event.
type JaegerLog struct {
	Timestamp int64            `json:"timestamp"`
	Fields    []JaegerKeyValue `json:"fields"`
}

// JaegerProcess is the service a span belongs to.
type JaegerProcess struct {
	ServiceName string           `json:"serviceName"`
	Tags        []JaegerKeyValue `json:"tags"`
}

// JaegerStringsResponse is the body of /api/services and /api/services/{service}/operations.
type JaegerStringsResponse struct {
	Data   []string `json:"data"`
	Total  int      `json:"total"`
	Limit  int      `json:"limit"`
	Offset int      `json:"offset"`
	Errors []string `json:"errors" jsonschema:"nullable"`
}

// JaegerOperation is one operation of a service.
type JaegerOperation struct {
	Name     string `json:"name"`
	SpanKind string `json:"spanKind"`
}

// JaegerOperationsResponse is the body of /api/operations?service=.
type JaegerOperationsResponse struct {
	Data   []JaegerOperation `json:"data"`
	Total  int               `json:"total"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
	Errors []string          `json:"errors" jsonschema:"nullable"`
}

// ---- health ----

// Healthz is the liveness body: values come from content/profile.yaml.
type Healthz struct {
	Status string   `json:"status"`
	OpenTo []string `json:"open_to"`
	TZ     string   `json:"tz"`
}

// Readyz is the readiness body.
type Readyz struct {
	Status  ReadyStatus  `json:"status"`
	Version string       `json:"version"`
	Commit  string       `json:"commit"`
	UptimeS int64        `json:"uptime_s"`
	Checks  ReadyzChecks `json:"checks"`
}

// ReadyzChecks groups the readiness checks.
type ReadyzChecks struct {
	DB         ReadyzDB                   `json:"db"`
	Content    ReadyzContent              `json:"content"`
	Collectors map[string]ReadyzCollector `json:"collectors"`
}

// ReadyzDB is the database check.
type ReadyzDB struct {
	OK        bool    `json:"ok"`
	LatencyMs float64 `json:"latency_ms"`
	Error     string  `json:"error,omitempty"`
	// Storage is file (SQLite on disk), libsql (Turso) or ephemeral (a file
	// database under /tmp on Vercel: samples vanish with the instance).
	Storage StorageKind `json:"storage,omitempty"`
}

// ReadyzContent describes the loaded content.
type ReadyzContent struct {
	OK       bool   `json:"ok"`
	Files    int    `json:"files"`
	Spans    int    `json:"spans"`
	LogLines int    `json:"log_lines"`
	Todos    int    `json:"todos"`
	LoadedAt string `json:"loaded_at"`
}

// ReadyzCollector is the freshness of one collector.
type ReadyzCollector struct {
	// OK is null when the collector never succeeded or is disabled.
	OK          *bool   `json:"ok" jsonschema:"nullable"`
	LastSuccess *string `json:"last_success" jsonschema:"nullable"`
	AgeS        *int64  `json:"age_s" jsonschema:"nullable"`
	StaleAfterS int64   `json:"stale_after_s"`
	Disabled    bool    `json:"disabled,omitempty"`
}

// ---- content endpoints ----

// ContentServices is the body of /api/content/services.
type ContentServices struct {
	Services []Service `json:"services"`
}

// PostmortemSummary is one item of /api/content/postmortems.
type PostmortemSummary struct {
	PostmortemFrontmatter
	TodoCount int    `json:"todo_count"`
	OgImage   string `json:"og_image"`
}

// ContentPostmortemList is the body of /api/content/postmortems.
type ContentPostmortemList struct {
	Items []PostmortemSummary `json:"items"`
}

// TOCEntry is one heading of a postmortem.
type TOCEntry struct {
	Level int    `json:"level"`
	ID    string `json:"id"`
	Text  string `json:"text"`
}

// ContentPostmortem is the body of /api/content/postmortems/{id}.
type ContentPostmortem struct {
	PostmortemSummary
	HTML     string     `json:"html"`
	TOC      []TOCEntry `json:"toc"`
	Markdown string     `json:"markdown"`
	SpanURL  string     `json:"span_url"`
}

// UptimeTargetView is one target of /api/content/uptime with defaults applied.
type UptimeTargetView struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	URL             string      `json:"url"`
	Method          ProbeMethod `json:"method"`
	ExpectedStatus  []int       `json:"expected_status"`
	Timeout         string      `json:"timeout"`
	Interval        string      `json:"interval"`
	FollowRedirects bool        `json:"follow_redirects"`
	Span            *string     `json:"span" jsonschema:"nullable"`
	Note            *string     `json:"note" jsonschema:"nullable"`
	// Configured is false when the URL is a TODO(divy).
	Configured bool `json:"configured"`
}

// ContentUptime is the body of /api/content/uptime.
type ContentUptime struct {
	Targets []UptimeTargetView `json:"targets"`
}

// ContentManualMetrics is the body of /api/content/manual-metrics.
type ContentManualMetrics struct {
	Metrics []ManualMetric `json:"metrics"`
}

// PodView is a pod row with the computed RESTARTS and AGE columns.
type PodView struct {
	Pod
	Restarts int   `json:"restarts"`
	AgeS     int64 `json:"age_s"`
}

// ContentProfile is the body of /api/content/profile.
type ContentProfile struct {
	Name       string       `json:"name"`
	Handle     string       `json:"handle"`
	Location   string       `json:"location"`
	TZ         string       `json:"tz"`
	OpenToWork bool         `json:"open_to_work"`
	OpenTo     []string     `json:"open_to"`
	Tagline    string       `json:"tagline,omitempty"`
	Links      ProfileLinks `json:"links"`
	Escalation []Escalation `json:"escalation"`
	Pods       []PodView    `json:"pods"`
}

// TodoItem is one TODO(divy) marker found in content/.
type TodoItem struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Path    string `json:"path"`
	Context string `json:"context"`
	Text    string `json:"text"`
}

// ContentTodos is the body of /api/content/todos.
type ContentTodos struct {
	GeneratedAt string         `json:"generated_at"`
	Count       int            `json:"count"`
	ByFile      map[string]int `json:"by_file"`
	Items       []TodoItem     `json:"items"`
}

// ---- uptime heartbeats (served by the uptime endpoint; shape fixed here) ----

// ProbeLast is the newest probe of a target.
type ProbeLast struct {
	TS         string  `json:"ts"`
	Up         bool    `json:"up"`
	LatencyMs  float64 `json:"latency_ms"`
	StatusCode int     `json:"status_code"`
	Error      *string `json:"error" jsonschema:"nullable"`
}

// UptimeWindows holds uptime ratios per window; null when the window has no data.
type UptimeWindows struct {
	H24 *float64 `json:"24h" jsonschema:"nullable"`
	D7  *float64 `json:"7d" jsonschema:"nullable"`
	D30 *float64 `json:"30d" jsonschema:"nullable"`
	D90 *float64 `json:"90d" jsonschema:"nullable"`
}

// HeartbeatBucket is one rollup bucket.
type HeartbeatBucket struct {
	TS           string  `json:"ts"`
	Samples      int     `json:"samples"`
	UpRatio      float64 `json:"up_ratio"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	MaxLatencyMs float64 `json:"max_latency_ms"`
}

// UptimeIncident is a maximal run of failed probes.
type UptimeIncident struct {
	StartedAt  string  `json:"started_at"`
	EndedAt    *string `json:"ended_at" jsonschema:"nullable"`
	DurationS  int64   `json:"duration_s"`
	Probes     int     `json:"probes"`
	FirstError string  `json:"first_error"`
}

// HeartbeatTarget is one target of /api/uptime/heartbeats.
type HeartbeatTarget struct {
	Target string  `json:"target"`
	Name   string  `json:"name"`
	URL    string  `json:"url"`
	Span   *string `json:"span" jsonschema:"nullable"`
	// Note is the target's caveat from uptime.yaml (e.g. the self-probe note).
	Note      *string           `json:"note" jsonschema:"nullable"`
	Status    UptimeStatus      `json:"status"`
	Last      *ProbeLast        `json:"last" jsonschema:"nullable"`
	Uptime    UptimeWindows     `json:"uptime"`
	Buckets   []HeartbeatBucket `json:"buckets"`
	Incidents []UptimeIncident  `json:"incidents"`
}

// UptimeHeartbeats is the body of /api/uptime and /api/uptime/heartbeats.
type UptimeHeartbeats struct {
	GeneratedAt string            `json:"generated_at"`
	Days        int               `json:"days"`
	Bucket      string            `json:"bucket"`
	Targets     []HeartbeatTarget `json:"targets"`
}

// ---- collect endpoint ----

// CollectorResult is the outcome of one collector inside a collection round.
type CollectorResult struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	Items      int    `json:"items"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// CollectSummary is the body of GET|POST /api/collect.
type CollectSummary struct {
	Collectors []CollectorResult `json:"collectors"`
	BudgetMs   int64             `json:"budget_ms"`
	// Truncated is true when the round budget expired before every collector finished.
	Truncated bool `json:"truncated"`
}

// ---- error envelopes ----

// PlainError is the error body of every JSON endpoint outside /api/v1 and /loki.
type PlainError struct {
	Error string `json:"error"`
}

// PromError is the Prometheus API error envelope used under /api/v1.
type PromError struct {
	Status    string        `json:"status"`
	ErrorType PromErrorType `json:"errorType"`
	Error     string        `json:"error"`
}
