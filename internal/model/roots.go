package model

// Root is one generated schema file: its name and the Go type reflected into it.
type Root struct {
	Name string
	Type any
}

// APIRoots has one field per API response envelope so a single reflection walk
// collects every API type into schema/api.schema.json.
type APIRoots struct {
	JaegerTraceResponse      JaegerTraceResponse      `json:"JaegerTraceResponse"`
	JaegerStringsResponse    JaegerStringsResponse    `json:"JaegerStringsResponse"`
	JaegerOperationsResponse JaegerOperationsResponse `json:"JaegerOperationsResponse"`
	Healthz                  Healthz                  `json:"Healthz"`
	Readyz                   Readyz                   `json:"Readyz"`
	ContentServices          ContentServices          `json:"ContentServices"`
	ContentSpans             SpansFile                `json:"ContentSpans"`
	ContentPostmortemList    ContentPostmortemList    `json:"ContentPostmortemList"`
	ContentPostmortem        ContentPostmortem        `json:"ContentPostmortem"`
	ContentPanels            PanelsFile               `json:"ContentPanels"`
	ContentAlerts            AlertsFile               `json:"ContentAlerts"`
	ContentUptime            ContentUptime            `json:"ContentUptime"`
	ContentManualMetrics     ContentManualMetrics     `json:"ContentManualMetrics"`
	ContentProfile           ContentProfile           `json:"ContentProfile"`
	ContentTodos             ContentTodos             `json:"ContentTodos"`
	UptimeHeartbeats         UptimeHeartbeats         `json:"UptimeHeartbeats"`
	CollectSummary           CollectSummary           `json:"CollectSummary"`
	PlainError               PlainError               `json:"PlainError"`
	PromError                PromError                `json:"PromError"`
}

// SchemaRoots lists every schema file divy schemagen writes, in output order.
var SchemaRoots = []Root{
	{Name: "spans", Type: SpansFile{}},
	{Name: "logs", Type: LogLine{}},
	{Name: "postmortem", Type: PostmortemFrontmatter{}},
	{Name: "panels", Type: PanelsFile{}},
	{Name: "alerts", Type: AlertsFile{}},
	{Name: "uptime", Type: UptimeFile{}},
	{Name: "manual_metrics", Type: ManualMetricsFile{}},
	{Name: "profile", Type: Profile{}},
	{Name: "api", Type: APIRoots{}},
}

// ContentSchemaFor maps a content file kind to its schema root name.
var ContentSchemaFor = map[string]string{
	"spans.yaml":          "spans",
	"logs.ndjson":         "logs",
	"postmortem":          "postmortem",
	"panels.yaml":         "panels",
	"alerts.yaml":         "alerts",
	"uptime.yaml":         "uptime",
	"manual_metrics.yaml": "manual_metrics",
	"profile.yaml":        "profile",
}
