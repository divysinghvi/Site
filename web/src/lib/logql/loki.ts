// Loki HTTP API client (docs/logql-subset.md §5): /loki/api/v1/query_range,
// /query, /labels, /label/{name}/values, /series. Errors are plain text in the
// Loki family and become ApiError like everywhere else. The narrowing of the
// three `resultType` shapes (streams | matrix | vector) happens here only.
// `createLoki` is fetch-bound so the same code serves the build-time page
// loads (prerender) and the browser.
import { api, ApiError, type Fetch } from '$lib/api/client';
import type { LokiResultType, LokiStats } from '$lib/api/types.gen';

/** One log entry: nanosecond timestamp as a decimal string + the raw line. */
export type StreamValue = [ts: string, line: string];

export interface Stream {
	stream: Record<string, string>;
	values: StreamValue[];
}

export type LokiSamplePair = [number, string];

export interface LokiMatrixSeries {
	metric: Record<string, string>;
	values: LokiSamplePair[];
}

export interface LokiVectorSample {
	metric: Record<string, string>;
	value: LokiSamplePair;
}

export type LokiData =
	| { resultType: 'streams'; result: Stream[] }
	| { resultType: 'matrix'; result: LokiMatrixSeries[] }
	| { resultType: 'vector'; result: LokiVectorSample[] };

export interface LokiResponse {
	data: LokiData;
	stats: LokiStats | null;
}

export type Direction = 'backward' | 'forward';

export interface RangeParams {
	/** Unix seconds (≤ 10 digits: the API reads them as seconds). */
	start: number;
	end: number;
	/** Log queries only (default 100, max 5000). */
	limit?: number;
	direction?: Direction;
	/** Metric queries: step in seconds. */
	step?: number;
	signal?: AbortSignal;
}

export interface InstantParams {
	time?: number;
	limit?: number;
	direction?: Direction;
	signal?: AbortSignal;
}

function isLabels(v: unknown): v is Record<string, string> {
	return (
		!!v &&
		typeof v === 'object' &&
		!Array.isArray(v) &&
		Object.values(v as object).every((x) => typeof x === 'string')
	);
}

function isStreamValue(v: unknown): v is StreamValue {
	return Array.isArray(v) && v.length === 2 && typeof v[0] === 'string' && typeof v[1] === 'string';
}

function isPair(v: unknown): v is LokiSamplePair {
	return Array.isArray(v) && v.length === 2 && typeof v[0] === 'number' && typeof v[1] === 'string';
}

/** Narrows the `data` of a query(_range) success envelope. */
export function narrowLokiData(raw: unknown): LokiData {
	if (!raw || typeof raw !== 'object') throw new Error('malformed Loki response: no data');
	const d = raw as { resultType?: unknown; result?: unknown };
	const t = d.resultType as LokiResultType;
	const r = d.result;
	if (!Array.isArray(r)) throw new Error(`malformed ${String(t)} result`);
	switch (t) {
		case 'streams': {
			const out: Stream[] = [];
			for (const s of r as unknown[]) {
				const o = s as { stream?: unknown; values?: unknown };
				if (isLabels(o.stream) && Array.isArray(o.values))
					out.push({ stream: o.stream, values: (o.values as unknown[]).filter(isStreamValue) });
			}
			return { resultType: 'streams', result: out };
		}
		case 'matrix': {
			const out: LokiMatrixSeries[] = [];
			for (const s of r as unknown[]) {
				const o = s as { metric?: unknown; values?: unknown };
				if (isLabels(o.metric) && Array.isArray(o.values))
					out.push({ metric: o.metric, values: (o.values as unknown[]).filter(isPair) });
			}
			return { resultType: 'matrix', result: out };
		}
		case 'vector': {
			const out: LokiVectorSample[] = [];
			for (const s of r as unknown[]) {
				const o = s as { metric?: unknown; value?: unknown };
				if (isLabels(o.metric) && isPair(o.value)) out.push({ metric: o.metric, value: o.value });
			}
			return { resultType: 'vector', result: out };
		}
		default:
			throw new Error(`unknown resultType ${String(t)}`);
	}
}

function statsOf(raw: unknown): LokiStats | null {
	const d = raw as { stats?: unknown } | null;
	const s = d?.stats;
	return s && typeof s === 'object' ? (s as LokiStats) : null;
}

/** GET /loki/api/v1/query_range — the request path (also what the curl reproduces). */
export function queryRangePath(query: string, p: RangeParams): string {
	const q = new URLSearchParams({ query, start: String(p.start), end: String(p.end) });
	if (p.limit !== undefined) q.set('limit', String(p.limit));
	q.set('direction', p.direction ?? 'backward');
	if (p.step !== undefined) q.set('step', String(p.step));
	return `/loki/api/v1/query_range?${q.toString()}`;
}

/** GET /loki/api/v1/query */
export function queryPath(query: string, p: InstantParams = {}): string {
	const q = new URLSearchParams({ query });
	if (p.time !== undefined) q.set('time', String(p.time));
	if (p.limit !== undefined) q.set('limit', String(p.limit));
	q.set('direction', p.direction ?? 'backward');
	return `/loki/api/v1/query?${q.toString()}`;
}

async function lokiError(res: Response): Promise<ApiError> {
	const traceId = res.headers.get('x-divy-trace-id') ?? undefined;
	let message = `${res.status} ${res.statusText}`.trim();
	try {
		const ct = res.headers.get('content-type') ?? '';
		if (ct.includes('application/json')) {
			const body = (await res.json()) as { error?: unknown };
			if (typeof body?.error === 'string') message = body.error;
		} else {
			const text = (await res.text()).trim();
			if (text) message = text;
		}
	} catch {
		// keep the status line
	}
	return new ApiError(message, { status: res.status, url: res.url, traceId });
}

export interface LokiOptions {
	fetch: Fetch;
	/** Absolute origin of the API, or '' for same-origin. */
	base: string;
}

export function createLoki({ fetch, base }: LokiOptions) {
	const root = base.replace(/\/+$/, '');

	async function getJSON(path: string, signal?: AbortSignal): Promise<unknown> {
		const res = await fetch(root + path, { headers: { Accept: 'application/json' }, signal });
		if (!res.ok) throw await lokiError(res);
		return (await res.json()) as unknown;
	}

	function stringList(raw: unknown): string[] {
		const d = (raw as { data?: unknown } | null)?.data;
		return Array.isArray(d) ? d.filter((x): x is string => typeof x === 'string') : [];
	}

	function windowQS(p?: { start?: number; end?: number; query?: string }): string {
		const q = new URLSearchParams();
		if (p?.start !== undefined) q.set('start', String(p.start));
		if (p?.end !== undefined) q.set('end', String(p.end));
		if (p?.query) q.set('query', p.query);
		const s = q.toString();
		return s ? '?' + s : '';
	}

	return {
		base: root,
		async queryRange(query: string, p: RangeParams): Promise<LokiResponse> {
			const raw = await getJSON(queryRangePath(query, p), p.signal);
			const data = (raw as { data?: unknown }).data;
			return { data: narrowLokiData(data), stats: statsOf(data) };
		},
		async query(query: string, p: InstantParams = {}): Promise<LokiResponse> {
			const raw = await getJSON(queryPath(query, p), p.signal);
			const data = (raw as { data?: unknown }).data;
			return { data: narrowLokiData(data), stats: statsOf(data) };
		},
		/** Label names of the streams with entries in the window. */
		labels: (p?: { start?: number; end?: number; query?: string }, signal?: AbortSignal) =>
			getJSON('/loki/api/v1/labels' + windowQS(p), signal).then(stringList),
		labelValues: (
			name: string,
			p?: { start?: number; end?: number; query?: string },
			signal?: AbortSignal
		) =>
			getJSON(`/loki/api/v1/label/${encodeURIComponent(name)}/values` + windowQS(p), signal).then(
				stringList
			),
		async series(match: string[], signal?: AbortSignal): Promise<Record<string, string>[]> {
			const q = new URLSearchParams();
			for (const m of match) q.append('match[]', m);
			const raw = await getJSON(`/loki/api/v1/series?${q.toString()}`, signal);
			const d = (raw as { data?: unknown } | null)?.data;
			return Array.isArray(d) ? d.filter(isLabels) : [];
		}
	};
}

export type Loki = ReturnType<typeof createLoki>;

/** Browser client (same origin as the rest of the API). */
export const loki: Loki = createLoki({
	fetch: (input, init) => globalThis.fetch(input, init),
	base: api.base
});

// ---- helpers ----

function shellQuote(s: string): string {
	return `'${s.replace(/'/g, `'\\''`)}'`;
}

/** The copyable curl for a query_range call (GET form, --data-urlencode per parameter). */
export function curlQueryRange(origin: string, query: string, p: RangeParams): string {
	const parts = [
		`curl -sG ${shellQuote(origin + '/loki/api/v1/query_range')}`,
		`--data-urlencode ${shellQuote('query=' + query)}`,
		`--data-urlencode ${shellQuote('start=' + p.start)}`,
		`--data-urlencode ${shellQuote('end=' + p.end)}`
	];
	if (p.limit !== undefined) parts.push(`--data-urlencode ${shellQuote('limit=' + p.limit)}`);
	parts.push(`--data-urlencode ${shellQuote('direction=' + (p.direction ?? 'backward'))}`);
	if (p.step !== undefined) parts.push(`--data-urlencode ${shellQuote('step=' + p.step)}`);
	return parts.join(' \\\n  ');
}

/** True for a log query (streams), false for a metric or scalar query. */
export function isLogQuery(q: string): boolean {
	return q.trimStart().startsWith('{');
}

/** Loki sample values: 'f' -1 floats, "+Inf", "-Inf", "NaN". */
export function lokiValue(v: string): number {
	if (v === 'NaN') return NaN;
	if (v === '+Inf' || v === 'Inf') return Infinity;
	if (v === '-Inf') return -Infinity;
	const n = Number(v);
	return Number.isFinite(n) ? n : NaN;
}

/** `{a="1", b="2"}` (sorted keys), Loki's label-set rendering. */
export function formatStreamLabels(labels: Record<string, string>): string {
	const keys = Object.keys(labels).sort();
	if (keys.length === 0) return '{}';
	return `{${keys.map((k) => `${k}=${JSON.stringify(labels[k])}`).join(', ')}}`;
}
