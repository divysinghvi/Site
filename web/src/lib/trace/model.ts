// Turns a Jaeger trace (the career trace from content or an OTel self-trace)
// into the tree the viewer renders. Nothing here is specific to Divy: every
// label comes from the payload (operation names, divy.* tags, process tags).
import type { JaegerKeyValue, JaegerSpan, JaegerTrace } from '$lib/api/types.gen';
import {
	parentSpanId,
	parseLinks,
	parseTodos,
	tagBool,
	tagNumber,
	tagString,
	type SpanLink
} from '$lib/api/client';
import type { DatePrecision } from '$lib/format';

export interface TraceEvent {
	us: number;
	name: string;
	fields: JaegerKeyValue[];
	/** true when the event's date was a TODO(divy) (placed at the span start). */
	todo: boolean;
}

export interface TraceNode {
	/** Jaeger spanID (hex). */
	id: string;
	/** Content span id (divy.id) when present, else the spanID. */
	key: string;
	name: string;
	title: string;
	service: string;
	serviceTitle: string;
	color: string;
	processID: string;
	startUs: number;
	/** Resolved end for the viewer's `now`: planned end, now (open) or the real end. */
	endUs: number;
	/** Duration as served by the API (µs). */
	durationUs: number;
	open: boolean;
	plannedEndUs?: number;
	startRaw?: string;
	endRaw?: string;
	startPrecision: DatePrecision;
	endPrecision: DatePrecision;
	/** Either date came from a TODO fallback: render hatched, never solid. */
	todoDates: boolean;
	error: boolean;
	statusCode?: string;
	/** Tags minus the divy.* bookkeeping keys. */
	tags: JaegerKeyValue[];
	/** The divy.* keys, for the drawer's provenance table. */
	meta: JaegerKeyValue[];
	events: TraceEvent[];
	links: SpanLink[];
	postmortems: string[];
	todos: string[];
	depth: number;
	parentId?: string;
	children: TraceNode[];
	/** Position in DFS order. */
	index: number;
}

export interface TraceService {
	name: string;
	title: string;
	color: string;
	processID: string;
	spans: number;
}

export interface TraceModel {
	traceID: string;
	roots: TraceNode[];
	/** DFS order, children sorted by (start, name). */
	nodes: TraceNode[];
	byId: Map<string, TraceNode>;
	byKey: Map<string, TraceNode>;
	startUs: number;
	endUs: number;
	/** The `now` the open spans were resolved against, when any span is open. */
	nowUs?: number;
	hasOpen: boolean;
	services: TraceService[];
	/** true when the payload carries content (divy.*) tags. */
	isContent: boolean;
}

const FALLBACK_COLORS = [
	'#5794f2',
	'#73bf69',
	'#f2cc0c',
	'#ff9830',
	'#b877d9',
	'#ff7383',
	'#8ab8ff',
	'#96d98d',
	'#c7d0d9'
];

function hashColor(name: string): string {
	let h = 0;
	for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
	return FALLBACK_COLORS[h % FALLBACK_COLORS.length]!;
}

function precision(v: string | undefined, fallback: DatePrecision): DatePrecision {
	switch (v) {
		case 'year':
		case 'month':
		case 'day':
		case 'todo':
		case 'open':
			return v;
		default:
			return fallback;
	}
}

/**
 * The request time encoded in the payload: an open span without a planned end
 * has duration = now − start. Undefined for traces without such spans.
 */
export function snapshotNow(trace: JaegerTrace): number | undefined {
	let now: number | undefined;
	for (const s of trace.spans) {
		if (!tagBool(s.tags, 'divy.open') || tagString(s.tags, 'divy.end_planned')) continue;
		const end = s.startTime + s.duration;
		if (now === undefined || end > now) now = end;
	}
	return now;
}

function eventsOf(span: JaegerSpan): TraceEvent[] {
	return span.logs.map((log) => {
		const nameField = log.fields.find((f) => f.key === 'event');
		const todo = log.fields.some((f) => f.key === 'divy.ts_precision' && f.value === 'todo');
		const fields = log.fields.filter((f) => f !== nameField && f.key !== 'divy.ts_precision');
		const name =
			nameField && typeof nameField.value === 'string'
				? nameField.value
				: (span.operationName ?? '');
		return { us: log.timestamp, name, fields, todo };
	});
}

function compare(a: TraceNode, b: TraceNode): number {
	return a.startUs - b.startUs || a.name.localeCompare(b.name);
}

export function buildTrace(trace: JaegerTrace, nowUs?: number): TraceModel {
	const byId = new Map<string, TraceNode>();
	const byKey = new Map<string, TraceNode>();
	const serviceMap = new Map<string, TraceService>();
	let isContent = false;

	for (const s of trace.spans) {
		const proc = trace.processes[s.processID];
		const service = proc?.serviceName ?? s.processID;
		const color = tagString(proc?.tags, 'divy.color') ?? hashColor(service);
		const serviceTitle = tagString(proc?.tags, 'divy.title') ?? service;
		const svc = serviceMap.get(service) ?? {
			name: service,
			title: serviceTitle,
			color,
			processID: s.processID,
			spans: 0
		};
		svc.spans += 1;
		serviceMap.set(service, svc);

		const key = tagString(s.tags, 'divy.id') ?? s.spanID;
		if (key !== s.spanID) isContent = true;
		const open = tagBool(s.tags, 'divy.open');
		const plannedRaw = tagString(s.tags, 'divy.end_planned');
		const startPrecision = precision(tagString(s.tags, 'divy.start_precision'), 'exact');
		const endPrecision = precision(
			tagString(s.tags, 'divy.end_precision'),
			open ? 'open' : 'exact'
		);
		const servedEnd = s.startTime + s.duration;

		let endUs = servedEnd;
		let plannedEndUs: number | undefined;
		if (open) {
			if (plannedRaw && (nowUs === undefined || servedEnd > nowUs)) {
				plannedEndUs = servedEnd;
				endUs = servedEnd;
			} else if (nowUs !== undefined) {
				endUs = Math.max(nowUs, s.startTime);
			}
		}

		const meta: JaegerKeyValue[] = [];
		const tags: JaegerKeyValue[] = [];
		for (const t of s.tags) (t.key.startsWith('divy.') ? meta : tags).push(t);

		const node: TraceNode = {
			id: s.spanID,
			key,
			name: s.operationName,
			title: tagString(s.tags, 'divy.title') ?? s.operationName,
			service,
			serviceTitle,
			color,
			processID: s.processID,
			startUs: s.startTime,
			endUs,
			durationUs: s.duration,
			open,
			plannedEndUs,
			startRaw: tagString(s.tags, 'divy.start'),
			endRaw: tagString(s.tags, 'divy.end'),
			startPrecision,
			endPrecision,
			todoDates: startPrecision === 'todo' || endPrecision === 'todo',
			error: tagBool(s.tags, 'error') || tagString(s.tags, 'otel.status_code') === 'ERROR',
			statusCode: tagString(s.tags, 'otel.status_code'),
			tags,
			meta,
			events: eventsOf(s),
			links: parseLinks(s.tags),
			postmortems: (tagString(s.tags, 'divy.postmortems') ?? '')
				.split(',')
				.map((x) => x.trim())
				.filter(Boolean),
			todos: parseTodos(s.tags),
			depth: tagNumber(s.tags, 'divy.depth') ?? 0,
			parentId: parentSpanId(s),
			children: [],
			index: 0
		};
		byId.set(node.id, node);
		byKey.set(node.key, node);
	}

	const roots: TraceNode[] = [];
	for (const n of byId.values()) {
		const parent = n.parentId ? byId.get(n.parentId) : undefined;
		if (parent) parent.children.push(n);
		else roots.push(n);
	}
	roots.sort(compare);

	const nodes: TraceNode[] = [];
	const walk = (n: TraceNode, depth: number) => {
		n.depth = depth;
		n.index = nodes.length;
		nodes.push(n);
		n.children.sort(compare);
		for (const c of n.children) walk(c, depth + 1);
	};
	for (const r of roots) walk(r, 0);

	let startUs = Number.POSITIVE_INFINITY;
	let endUs = Number.NEGATIVE_INFINITY;
	let hasOpen = false;
	for (const n of nodes) {
		startUs = Math.min(startUs, n.startUs);
		endUs = Math.max(endUs, n.endUs);
		hasOpen ||= n.open;
	}
	if (hasOpen && nowUs !== undefined) endUs = Math.max(endUs, nowUs);
	if (!Number.isFinite(startUs)) startUs = 0;
	if (!Number.isFinite(endUs)) endUs = startUs + 1;
	if (endUs <= startUs) endUs = startUs + 1;

	return {
		traceID: trace.traceID,
		roots,
		nodes,
		byId,
		byKey,
		startUs,
		endUs,
		nowUs: hasOpen ? nowUs : undefined,
		hasOpen,
		services: [...serviceMap.values()],
		isContent
	};
}

/** Nodes hidden because an ancestor is collapsed are skipped. */
export function visibleNodes(model: TraceModel, collapsed: ReadonlySet<string>): TraceNode[] {
	const out: TraceNode[] = [];
	const walk = (n: TraceNode) => {
		out.push(n);
		if (collapsed.has(n.id)) return;
		for (const c of n.children) walk(c);
	};
	for (const r of model.roots) walk(r);
	return out;
}

export function ancestorsOf(model: TraceModel, node: TraceNode): TraceNode[] {
	const out: TraceNode[] = [];
	let cur = node.parentId ? model.byId.get(node.parentId) : undefined;
	while (cur) {
		out.push(cur);
		cur = cur.parentId ? model.byId.get(cur.parentId) : undefined;
	}
	return out;
}
