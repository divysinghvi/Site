/* eslint-disable */
// Types derived from schema/index.schema.json (Go structs in internal/model).
// Do not edit by hand: change the Go structs, then run `make gen`.

/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ReadyStatus".
 */
export type ReadyStatus = 'ok' | 'unavailable' | 'shutting_down';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "StorageKind".
 */
export type StorageKind = 'file' | 'libsql' | 'ephemeral';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "DateOrTodo".
 */
export type DateOrTodo = string;
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "SpanStatus".
 */
export type SpanStatus = 'ok' | 'error';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "TagValue".
 */
export type TagValue = (string | number | boolean) | string[];
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Scalar".
 */
export type Scalar = string | number | boolean;
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Link".
 */
export type Link = {
	kind: LinkKind;
	ref?: string;
	url?: {
		[k: string]: unknown | undefined;
	} & string;
	label?: string;
};
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LinkKind".
 */
export type LinkKind = 'postmortem' | 'repo' | 'pr' | 'pypi' | 'url';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Severity".
 */
export type Severity = 'SEV1' | 'SEV2' | 'SEV3' | 'SEV4';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PostmortemStatus".
 */
export type PostmortemStatus = 'resolved' | 'monitoring' | 'open';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "TimeOption".
 */
export type TimeOption = '24h' | '7d' | '30d' | '1y' | 'all';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PanelType".
 */
export type PanelType = 'timeseries' | 'stat' | 'gauge' | 'bargauge';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ThresholdMode".
 */
export type ThresholdMode = 'absolute' | 'percentage';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PaletteColor".
 */
export type PaletteColor = 'green' | 'yellow' | 'red' | 'blue' | 'orange' | 'purple';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "SourceKind".
 */
export type SourceKind = 'github' | 'pypi' | 'manual' | 'process' | 'content';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ProbeMethod".
 */
export type ProbeMethod = 'GET' | 'HEAD';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PodStatus".
 */
export type PodStatus = 'Running' | 'Pending' | 'Completed' | 'CrashLoopBackOff';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "RestartsFrom".
 */
export type RestartsFrom = 'postmortems' | 'none';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "UptimeStatus".
 */
export type UptimeStatus = 'up' | 'down' | 'unconfigured' | 'unknown';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromErrorType".
 */
export type PromErrorType =
	'bad_data' | 'execution' | 'timeout' | 'internal' | 'unavailable' | 'not_found';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromResultType".
 */
export type PromResultType = 'vector' | 'matrix' | 'scalar' | 'string';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromResult".
 */
export type PromResult =
	| {
			metric: {
				[k: string]: string | undefined;
			};
			/**
			 * @minItems 2
			 * @maxItems 2
			 */
			value: [number | string, number | string];
	  }[]
	| {
			metric: {
				[k: string]: string | undefined;
			};
			values: [number | string, number | string][];
	  }[]
	| [number | string, number | string];
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiResultType".
 */
export type LokiResultType = 'streams' | 'matrix' | 'vector';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiResult".
 */
export type LokiResult =
	| {
			stream: {
				[k: string]: string | undefined;
			};
			values: [string, string][];
	  }[]
	| {
			metric: {
				[k: string]: string | undefined;
			};
			values: [number | string, number | string][];
	  }[]
	| {
			metric: {
				[k: string]: string | undefined;
			};
			/**
			 * @minItems 2
			 * @maxItems 2
			 */
			value: [number | string, number | string];
	  }[];
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "IntOrList".
 */
export type IntOrList = number | [number, ...number[]];
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LogLevel".
 */
export type LogLevel = 'debug' | 'info' | 'warn' | 'error';
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LogPrecision".
 */
export type LogPrecision = 'day' | 'month' | 'year';

export interface HttpsDivyDevSchemaIndexSchemaJson {
	[k: string]: unknown | undefined;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "APIRoots".
 */
export interface APIRoots {
	JaegerTraceResponse: JaegerTraceResponse;
	JaegerStringsResponse: JaegerStringsResponse;
	JaegerOperationsResponse: JaegerOperationsResponse;
	Healthz: Healthz;
	Readyz: Readyz;
	ContentServices: ContentServices;
	ContentSpans: SpansFile;
	ContentPostmortemList: ContentPostmortemList;
	ContentPostmortem: ContentPostmortem;
	ContentPanels: PanelsFile;
	ContentAlerts: AlertsFile;
	ContentUptime: ContentUptime;
	ContentManualMetrics: ContentManualMetrics;
	ContentProfile: ContentProfile;
	ContentTodos: ContentTodos;
	UptimeHeartbeats: UptimeHeartbeats;
	CollectSummary: CollectSummary;
	PlainError: PlainError;
	PromError: PromError;
	PromQueryResult: PromQueryResult;
	PromSeriesResult: PromSeriesResult;
	PromLabelsResult: PromLabelsResult;
	PromMetadataResult: PromMetadataResult;
	PromBuildInfoResult: PromBuildInfoResult;
	PromRulesResult: PromRulesResult;
	PromAlertsResult: PromAlertsResult;
	PromExemplarsResult: PromExemplarsResult;
	LokiQueryResult: LokiQueryResult;
	LokiLabelsResult: LokiLabelsResult;
	LokiSeriesResult: LokiSeriesResult;
	LokiIndexStats: LokiIndexStats;
	LokiVolumeResult: LokiVolumeResult;
	LokiBuildInfo: LokiBuildInfo;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "JaegerTraceResponse".
 */
export interface JaegerTraceResponse {
	data: JaegerTrace[];
	total: number;
	limit: number;
	offset: number;
	errors: string[] | null;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "JaegerTrace".
 */
export interface JaegerTrace {
	traceID: string;
	spans: JaegerSpan[];
	processes: {
		[k: string]: JaegerProcess | undefined;
	};
	warnings: string[] | null;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "JaegerSpan".
 */
export interface JaegerSpan {
	traceID: string;
	spanID: string;
	operationName: string;
	references: JaegerReference[];
	startTime: number;
	duration: number;
	tags: JaegerKeyValue[];
	logs: JaegerLog[];
	processID: string;
	warnings: string[] | null;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "JaegerReference".
 */
export interface JaegerReference {
	refType: string;
	traceID: string;
	spanID: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "JaegerKeyValue".
 */
export interface JaegerKeyValue {
	key: string;
	type: string;
	value: unknown;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "JaegerLog".
 */
export interface JaegerLog {
	timestamp: number;
	fields: JaegerKeyValue[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "JaegerProcess".
 */
export interface JaegerProcess {
	serviceName: string;
	tags: JaegerKeyValue[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "JaegerStringsResponse".
 */
export interface JaegerStringsResponse {
	data: string[];
	total: number;
	limit: number;
	offset: number;
	errors: string[] | null;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "JaegerOperationsResponse".
 */
export interface JaegerOperationsResponse {
	data: JaegerOperation[];
	total: number;
	limit: number;
	offset: number;
	errors: string[] | null;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "JaegerOperation".
 */
export interface JaegerOperation {
	name: string;
	spanKind: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Healthz".
 */
export interface Healthz {
	status: string;
	open_to: string[];
	tz: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Readyz".
 */
export interface Readyz {
	status: ReadyStatus;
	version: string;
	commit: string;
	uptime_s: number;
	checks: ReadyzChecks;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ReadyzChecks".
 */
export interface ReadyzChecks {
	db: ReadyzDB;
	content: ReadyzContent;
	collectors: {
		[k: string]: ReadyzCollector | undefined;
	};
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ReadyzDB".
 */
export interface ReadyzDB {
	ok: boolean;
	latency_ms: number;
	error?: string;
	storage?: StorageKind;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ReadyzContent".
 */
export interface ReadyzContent {
	ok: boolean;
	files: number;
	spans: number;
	log_lines: number;
	todos: number;
	loaded_at: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ReadyzCollector".
 */
export interface ReadyzCollector {
	ok: boolean | null;
	last_success: string | null;
	age_s: number | null;
	stale_after_s: number;
	disabled?: boolean;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ContentServices".
 */
export interface ContentServices {
	services: Service[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Service".
 */
export interface Service {
	id: string;
	title: string;
	color: string;
	counts_as_experience?: boolean;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "SpansFile".
 */
export interface SpansFile {
	version: number;
	/**
	 * @minItems 1
	 */
	services: [Service, ...Service[]];
	trace: Span;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Span".
 */
export interface Span {
	id: string;
	service: string;
	title?: string;
	start: DateOrTodo;
	end?: DateOrTodo;
	open?: boolean;
	status?: SpanStatus;
	tags?: {
		[k: string]: TagValue | undefined;
	};
	events?: Event[];
	links?: Link[];
	todo?: string[];
	children?: Span[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Event".
 */
export interface Event {
	ts: DateOrTodo;
	name: string;
	attrs?: {
		[k: string]: Scalar | undefined;
	};
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ContentPostmortemList".
 */
export interface ContentPostmortemList {
	items: PostmortemSummary[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PostmortemSummary".
 */
export interface PostmortemSummary {
	id: string;
	title: string;
	severity: Severity;
	date: DateOrTodo;
	span: string;
	/**
	 * @minItems 1
	 */
	services: [string, ...string[]];
	duration: string;
	status: PostmortemStatus;
	tags?: string[];
	summary: string;
	todo_count: number;
	og_image: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ContentPostmortem".
 */
export interface ContentPostmortem {
	id: string;
	title: string;
	severity: Severity;
	date: DateOrTodo;
	span: string;
	/**
	 * @minItems 1
	 */
	services: [string, ...string[]];
	duration: string;
	status: PostmortemStatus;
	tags?: string[];
	summary: string;
	todo_count: number;
	og_image: string;
	html: string;
	toc: TOCEntry[];
	markdown: string;
	span_url: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "TOCEntry".
 */
export interface TOCEntry {
	level: number;
	id: string;
	text: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PanelsFile".
 */
export interface PanelsFile {
	version: number;
	dashboard: Dashboard;
	/**
	 * @minItems 1
	 */
	panels: [Panel, ...Panel[]];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Dashboard".
 */
export interface Dashboard {
	title: string;
	refresh?: string;
	time: DashboardTime;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "DashboardTime".
 */
export interface DashboardTime {
	default: TimeOption;
	/**
	 * @minItems 1
	 */
	options: [TimeOption, ...TimeOption[]];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Panel".
 */
export interface Panel {
	id: string;
	title: string;
	type: PanelType;
	gridPos: GridPos;
	/**
	 * @minItems 1
	 */
	targets: [Target, ...Target[]];
	unit?: string;
	decimals?: number;
	min?: number;
	max?: number;
	stack?: boolean;
	thresholds?: Thresholds;
	description: string;
	source: PanelSource;
	options?: {};
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "GridPos".
 */
export interface GridPos {
	x: number;
	y: number;
	w: number;
	h: number;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Target".
 */
export interface Target {
	refId: string;
	expr: string;
	legendFormat?: string;
	instant?: boolean;
	hide?: boolean;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Thresholds".
 */
export interface Thresholds {
	mode: ThresholdMode;
	/**
	 * @minItems 1
	 */
	steps: [ThresholdStep, ...ThresholdStep[]];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ThresholdStep".
 */
export interface ThresholdStep {
	value: number | null;
	color: PaletteColor;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PanelSource".
 */
export interface PanelSource {
	kind: SourceKind;
	cadence?: string;
	note?: string;
	updated_metric?: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "AlertsFile".
 */
export interface AlertsFile {
	/**
	 * @minItems 1
	 */
	groups: [AlertGroup, ...AlertGroup[]];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "AlertGroup".
 */
export interface AlertGroup {
	name: string;
	interval?: string;
	/**
	 * @minItems 1
	 */
	rules: [AlertRule, ...AlertRule[]];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "AlertRule".
 */
export interface AlertRule {
	alert: string;
	expr: string;
	for?: string;
	labels?: {
		[k: string]: string | undefined;
	};
	annotations?: {
		[k: string]: string | undefined;
	};
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ContentUptime".
 */
export interface ContentUptime {
	targets: UptimeTargetView[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "UptimeTargetView".
 */
export interface UptimeTargetView {
	id: string;
	name: string;
	url: string;
	method: ProbeMethod;
	expected_status: number[];
	timeout: string;
	interval: string;
	follow_redirects: boolean;
	span: string | null;
	note: string | null;
	configured: boolean;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ContentManualMetrics".
 */
export interface ContentManualMetrics {
	metrics: ManualMetric[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ManualMetric".
 */
export interface ManualMetric {
	metric: string;
	labels?: {
		[k: string]: string | undefined;
	};
	value: number;
	source: string;
	updated_at: DateOrTodo;
	note?: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ContentProfile".
 */
export interface ContentProfile {
	name: string;
	handle: string;
	location: string;
	tz: string;
	open_to_work: boolean;
	open_to: string[];
	tagline?: string;
	links: ProfileLinks;
	escalation: Escalation[];
	pods: PodView[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ProfileLinks".
 */
export interface ProfileLinks {
	github: string;
	email: string;
	linkedin: string;
	resume: string;
	calendar: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Escalation".
 */
export interface Escalation {
	step: number;
	channel: string;
	target: string;
	response_time: string;
	note?: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PodView".
 */
export interface PodView {
	name: string;
	ready: string;
	status: PodStatus;
	restarts_from: RestartsFrom;
	span: string;
	note?: string;
	restarts: number;
	age_s: number;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ContentTodos".
 */
export interface ContentTodos {
	generated_at: string;
	count: number;
	by_file: {
		[k: string]: number | undefined;
	};
	items: TodoItem[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "TodoItem".
 */
export interface TodoItem {
	file: string;
	line: number;
	col: number;
	path: string;
	context: string;
	text: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "UptimeHeartbeats".
 */
export interface UptimeHeartbeats {
	generated_at: string;
	days: number;
	bucket: string;
	targets: HeartbeatTarget[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "HeartbeatTarget".
 */
export interface HeartbeatTarget {
	target: string;
	name: string;
	url: string;
	span: string | null;
	note: string | null;
	status: UptimeStatus;
	last: ProbeLast | null;
	uptime: UptimeWindows;
	buckets: HeartbeatBucket[];
	incidents: UptimeIncident[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ProbeLast".
 */
export interface ProbeLast {
	ts: string;
	up: boolean;
	latency_ms: number;
	status_code: number;
	error: string | null;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "UptimeWindows".
 */
export interface UptimeWindows {
	'24h': number | null;
	'7d': number | null;
	'30d': number | null;
	'90d': number | null;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "HeartbeatBucket".
 */
export interface HeartbeatBucket {
	ts: string;
	samples: number;
	up_ratio: number;
	avg_latency_ms: number;
	max_latency_ms: number;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "UptimeIncident".
 */
export interface UptimeIncident {
	started_at: string;
	ended_at: string | null;
	duration_s: number;
	probes: number;
	first_error: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "CollectSummary".
 */
export interface CollectSummary {
	collectors: CollectorResult[];
	budget_ms: number;
	truncated: boolean;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "CollectorResult".
 */
export interface CollectorResult {
	name: string;
	ok: boolean;
	items: number;
	duration_ms: number;
	error?: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PlainError".
 */
export interface PlainError {
	error: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromError".
 */
export interface PromError {
	status: string;
	errorType: PromErrorType;
	error: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromQueryResult".
 */
export interface PromQueryResult {
	status: string;
	data: PromQueryData;
	warnings?: string[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromQueryData".
 */
export interface PromQueryData {
	resultType: PromResultType;
	result: PromResult;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromSeriesResult".
 */
export interface PromSeriesResult {
	status: string;
	data: {
		[k: string]: string | undefined;
	}[];
	warnings?: string[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromLabelsResult".
 */
export interface PromLabelsResult {
	status: string;
	data: string[];
	warnings?: string[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromMetadataResult".
 */
export interface PromMetadataResult {
	status: string;
	data: {
		[k: string]: PromMetadata[] | undefined;
	};
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromMetadata".
 */
export interface PromMetadata {
	type: string;
	help: string;
	unit: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromBuildInfoResult".
 */
export interface PromBuildInfoResult {
	status: string;
	data: PromBuildInfo;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromBuildInfo".
 */
export interface PromBuildInfo {
	version: string;
	revision: string;
	branch: string;
	buildUser: string;
	buildDate: string;
	goVersion: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromRulesResult".
 */
export interface PromRulesResult {
	status: string;
	data: PromRuleGroups;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromRuleGroups".
 */
export interface PromRuleGroups {
	groups: PromRuleGroup[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromRuleGroup".
 */
export interface PromRuleGroup {
	name: string;
	file: string;
	rules: PromAlertingRule[];
	interval: number;
	limit: number;
	evaluationTime: number;
	lastEvaluation: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromAlertingRule".
 */
export interface PromAlertingRule {
	state: string;
	name: string;
	query: string;
	duration: number;
	keepFiringFor: number;
	labels: {
		[k: string]: string | undefined;
	};
	annotations: {
		[k: string]: string | undefined;
	};
	alerts: PromAlert[];
	health: string;
	evaluationTime: number;
	lastEvaluation: string;
	type: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromAlert".
 */
export interface PromAlert {
	labels: {
		[k: string]: string | undefined;
	};
	annotations: {
		[k: string]: string | undefined;
	};
	state: string;
	activeAt?: string;
	value: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromAlertsResult".
 */
export interface PromAlertsResult {
	status: string;
	data: PromAlerts;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromAlerts".
 */
export interface PromAlerts {
	alerts: PromAlert[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PromExemplarsResult".
 */
export interface PromExemplarsResult {
	status: string;
	data: unknown[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiQueryResult".
 */
export interface LokiQueryResult {
	status: string;
	data: LokiQueryData;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiQueryData".
 */
export interface LokiQueryData {
	resultType: LokiResultType;
	result: LokiResult;
	stats: LokiStats;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiStats".
 */
export interface LokiStats {
	ingester: LokiIngesterStats;
	store: LokiStoreStats;
	summary: LokiSummaryStats;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiIngesterStats".
 */
export interface LokiIngesterStats {
	compressedBytes: number;
	decompressedBytes: number;
	decompressedLines: number;
	headChunkBytes: number;
	headChunkLines: number;
	totalBatches: number;
	totalChunksMatched: number;
	totalDuplicates: number;
	totalLinesSent: number;
	totalReached: number;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiStoreStats".
 */
export interface LokiStoreStats {
	compressedBytes: number;
	decompressedBytes: number;
	decompressedLines: number;
	chunksDownloadTime: number;
	totalChunksRef: number;
	totalChunksDownloaded: number;
	totalDuplicates: number;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiSummaryStats".
 */
export interface LokiSummaryStats {
	bytesProcessedPerSecond: number;
	execTime: number;
	linesProcessedPerSecond: number;
	queueTime: number;
	totalBytesProcessed: number;
	totalLinesProcessed: number;
	totalEntriesReturned: number;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiLabelsResult".
 */
export interface LokiLabelsResult {
	status: string;
	data: string[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiSeriesResult".
 */
export interface LokiSeriesResult {
	status: string;
	data: {
		[k: string]: string | undefined;
	}[];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiIndexStats".
 */
export interface LokiIndexStats {
	streams: number;
	chunks: number;
	entries: number;
	bytes: number;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiVolumeResult".
 */
export interface LokiVolumeResult {
	status: string;
	data: LokiVolumeData;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiVolumeData".
 */
export interface LokiVolumeData {
	resultType: LokiResultType;
	result: LokiResult;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LokiBuildInfo".
 */
export interface LokiBuildInfo {
	version: string;
	revision: string;
	branch: string;
	buildUser: string;
	buildDate: string;
	goVersion: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "LogLine".
 */
export interface LogLine {
	ts: {
		[k: string]: unknown | undefined;
	} & string;
	precision?: LogPrecision;
	level: LogLevel;
	service: string;
	msg: string;
	span?: string;
	component?: string;
	[k: string]:
		| string
		| number
		| boolean
		| ({
				[k: string]: unknown | undefined;
		  } & string)
		| LogPrecision
		| LogLevel
		| undefined;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "ManualMetricsFile".
 */
export interface ManualMetricsFile {
	/**
	 * @minItems 1
	 */
	metrics: [ManualMetric, ...ManualMetric[]];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Pod".
 */
export interface Pod {
	name: string;
	ready: string;
	status: PodStatus;
	restarts_from: RestartsFrom;
	span: string;
	note?: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "PostmortemFrontmatter".
 */
export interface PostmortemFrontmatter {
	id: string;
	title: string;
	severity: Severity;
	date: DateOrTodo;
	span: string;
	/**
	 * @minItems 1
	 */
	services: [string, ...string[]];
	duration: string;
	status: PostmortemStatus;
	tags?: string[];
	summary: string;
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "Profile".
 */
export interface Profile {
	name: string;
	handle: string;
	location: string;
	tz: string;
	open_to_work: boolean;
	/**
	 * @minItems 1
	 */
	open_to: [string, ...string[]];
	tagline?: string;
	links: ProfileLinks;
	/**
	 * @minItems 1
	 */
	escalation: [Escalation, ...Escalation[]];
	/**
	 * @minItems 1
	 */
	pods: [Pod, ...Pod[]];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "UptimeFile".
 */
export interface UptimeFile {
	/**
	 * @minItems 1
	 */
	targets: [UptimeTarget, ...UptimeTarget[]];
}
/**
 * This interface was referenced by `HttpsDivyDevSchemaIndexSchemaJson`'s JSON-Schema
 * via the `definition` "UptimeTarget".
 */
export interface UptimeTarget {
	id: string;
	name: string;
	url: {
		[k: string]: unknown | undefined;
	} & string;
	method?: ProbeMethod;
	expected_status?: IntOrList;
	timeout?: string;
	interval?: string;
	follow_redirects?: boolean;
	span?: string;
	note?: string;
}
