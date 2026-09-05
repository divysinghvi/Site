// Prometheus HTTP API client for the dashboard and Explore (GET form of
// /api/v1/query, /api/v1/query_range, /api/v1/labels, /api/v1/label/{name}/values,
// /api/v1/series, /api/v1/metadata). Same origin/base and error envelope
// handling as $lib/api/client (ApiError); the narrowing of the four
// `resultType` shapes happens here and nowhere else.
import { api, ApiError } from '$lib/api/client';

// PromResultType of schema/index.schema.json (types.gen.ts predates the Prom* API types;
// regenerate with `make gen` and switch to the import once it carries them).
type PromResultType = 'vector' | 'matrix' | 'scalar' | 'string';

export type SamplePair = [number, string];

export interface VectorSample {
	metric: Record<string, string>;
	value: SamplePair;
}

export interface MatrixSeries {
	metric: Record<string, string>;
	values: SamplePair[];
}

export type PromData =
	| { resultType: 'vector'; result: VectorSample[] }
	| { resultType: 'matrix'; result: MatrixSeries[] }
	| { resultType: 'scalar'; result: SamplePair }
	| { resultType: 'string'; result: SamplePair };

export interface PromResponse {
	data: PromData;
	warnings: string[];
}

export interface QueryOptions {
	/** Unix seconds; omitted = now (the API's default). */
	time?: number;
	signal?: AbortSignal;
}

export interface RangeOptions {
	from: number;
	to: number;
	step: number;
	signal?: AbortSignal;
}

function isPair(v: unknown): v is SamplePair {
	return Array.isArray(v) && v.length === 2 && typeof v[0] === 'number' && typeof v[1] === 'string';
}

function isLabels(v: unknown): v is Record<string, string> {
	return (
		!!v && typeof v === 'object' && Object.values(v as object).every((x) => typeof x === 'string')
	);
}

/** Narrows the `data` object of a /api/v1/query(_range) success envelope. */
export function narrowData(raw: unknown): PromData {
	if (!raw || typeof raw !== 'object') throw new Error('malformed Prometheus response: no data');
	const d = raw as { resultType?: unknown; result?: unknown };
	const t = d.resultType as PromResultType;
	const r = d.result;
	switch (t) {
		case 'vector': {
			if (!Array.isArray(r)) throw new Error('malformed vector result');
			const out: VectorSample[] = [];
			for (const s of r as unknown[]) {
				const o = s as { metric?: unknown; value?: unknown };
				if (isLabels(o.metric) && isPair(o.value)) out.push({ metric: o.metric, value: o.value });
			}
			return { resultType: 'vector', result: out };
		}
		case 'matrix': {
			if (!Array.isArray(r)) throw new Error('malformed matrix result');
			const out: MatrixSeries[] = [];
			for (const s of r as unknown[]) {
				const o = s as { metric?: unknown; values?: unknown };
				if (isLabels(o.metric) && Array.isArray(o.values))
					out.push({ metric: o.metric, values: (o.values as unknown[]).filter(isPair) });
			}
			return { resultType: 'matrix', result: out };
		}
		case 'scalar':
		case 'string': {
			if (!isPair(r)) throw new Error(`malformed ${t} result`);
			return { resultType: t, result: r };
		}
		default:
			throw new Error(`unknown resultType ${String(t)}`);
	}
}

async function errorFromResponse(res: Response): Promise<ApiError> {
	const traceId = res.headers.get('x-divy-trace-id') ?? undefined;
	let message = `${res.status} ${res.statusText}`.trim();
	let errorType: string | undefined;
	try {
		const body: unknown = await res.json();
		if (body && typeof body === 'object') {
			const b = body as Record<string, unknown>;
			if (typeof b.error === 'string') message = b.error;
			if (typeof b.errorType === 'string') errorType = b.errorType;
		}
	} catch {
		// keep the status line
	}
	return new ApiError(message, { status: res.status, url: res.url, errorType, traceId });
}

async function getJSON(path: string, signal?: AbortSignal): Promise<unknown> {
	const res = await fetch(api.base + path, { headers: { Accept: 'application/json' }, signal });
	if (!res.ok) throw await errorFromResponse(res);
	return (await res.json()) as unknown;
}

function envelope(raw: unknown): { data: unknown; warnings: string[] } {
	const o = (raw ?? {}) as { data?: unknown; warnings?: unknown };
	const warnings = Array.isArray(o.warnings) ? o.warnings.filter((w) => typeof w === 'string') : [];
	return { data: o.data, warnings };
}

/** GET /api/v1/query — the URL the page requests (also shown by "View query"). */
export function queryPath(expr: string, time?: number): string {
	const p = new URLSearchParams({ query: expr });
	if (time !== undefined) p.set('time', String(time));
	return `/api/v1/query?${p.toString()}`;
}

/** GET /api/v1/query_range */
export function queryRangePath(expr: string, from: number, to: number, step: number): string {
	const p = new URLSearchParams({
		query: expr,
		start: String(from),
		end: String(to),
		step: String(step)
	});
	return `/api/v1/query_range?${p.toString()}`;
}

export async function query(expr: string, opts: QueryOptions = {}): Promise<PromResponse> {
	const env = envelope(await getJSON(queryPath(expr, opts.time), opts.signal));
	return { data: narrowData(env.data), warnings: env.warnings };
}

export async function queryRange(expr: string, opts: RangeOptions): Promise<PromResponse> {
	const env = envelope(
		await getJSON(queryRangePath(expr, opts.from, opts.to, opts.step), opts.signal)
	);
	return { data: narrowData(env.data), warnings: env.warnings };
}

function stringList(raw: unknown): string[] {
	const env = envelope(raw);
	return Array.isArray(env.data) ? env.data.filter((x): x is string => typeof x === 'string') : [];
}

/** GET /api/v1/label/{name}/values (metric names via `__name__`). */
export async function labelValues(name: string, match?: string[], signal?: AbortSignal) {
	const p = new URLSearchParams();
	for (const m of match ?? []) p.append('match[]', m);
	const qs = p.toString();
	return stringList(
		await getJSON(`/api/v1/label/${encodeURIComponent(name)}/values${qs ? '?' + qs : ''}`, signal)
	);
}

/** GET /api/v1/labels */
export async function labelNames(match?: string[], signal?: AbortSignal) {
	const p = new URLSearchParams();
	for (const m of match ?? []) p.append('match[]', m);
	const qs = p.toString();
	return stringList(await getJSON(`/api/v1/labels${qs ? '?' + qs : ''}`, signal));
}

/** GET /api/v1/series?match[]=… */
export async function series(match: string[], signal?: AbortSignal) {
	const p = new URLSearchParams();
	for (const m of match) p.append('match[]', m);
	const env = envelope(await getJSON(`/api/v1/series?${p.toString()}`, signal));
	return Array.isArray(env.data) ? env.data.filter(isLabels) : [];
}

export interface MetricMeta {
	type: string;
	help: string;
}

/** GET /api/v1/metadata → {name: {type, help}} (first entry per family). */
export async function metadata(signal?: AbortSignal): Promise<Record<string, MetricMeta>> {
	const env = envelope(await getJSON('/api/v1/metadata', signal));
	const out: Record<string, MetricMeta> = {};
	if (env.data && typeof env.data === 'object') {
		for (const [name, list] of Object.entries(env.data as Record<string, unknown>)) {
			if (Array.isArray(list) && list[0] && typeof list[0] === 'object') {
				const m = list[0] as { type?: unknown; help?: unknown };
				out[name] = {
					type: typeof m.type === 'string' ? m.type : '',
					help: typeof m.help === 'string' ? m.help : ''
				};
			}
		}
	}
	return out;
}

// ---- helpers shared by panels, Explore and the query inspector ----

/** Parses a sample value; NaN/±Inf stay as such, anything unparsable becomes NaN. */
export function sampleValue(v: string): number {
	if (v === 'NaN') return NaN;
	if (v === '+Inf' || v === 'Inf') return Infinity;
	if (v === '-Inf') return -Infinity;
	const n = Number(v);
	return Number.isFinite(n) ? n : NaN;
}

/** `{a="1", b="2"}` — Prometheus' own label-set rendering (name outside). */
export function formatLabels(metric: Record<string, string>, withName = true): string {
	const name = metric.__name__ ?? '';
	const pairs = Object.keys(metric)
		.filter((k) => k !== '__name__')
		.sort()
		.map((k) => `${k}=${JSON.stringify(metric[k])}`);
	const body = pairs.length ? `{${pairs.join(', ')}}` : '';
	if (!withName) return body || '{}';
	return name + body || '{}';
}

/**
 * Grafana legendFormat templating: `{{org}}` → the label's value (empty if the
 * series lacks it). Without a format the legend is the label set.
 */
export function legendName(format: string | undefined, metric: Record<string, string>): string {
	if (!format) return formatLabels(metric);
	const out = format.replace(
		/\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g,
		(_, k: string) => metric[k] ?? ''
	);
	return out.trim() || formatLabels(metric);
}

function shellQuote(s: string): string {
	return `'${s.replace(/'/g, `'\\''`)}'`;
}

/** The copyable curl for a range query (GET form, --data-urlencode, docs/drafts/content.md C.6.2). */
export function curlRange(origin: string, expr: string, from: number, to: number, step: number) {
	return [
		`curl -sG ${shellQuote(origin + '/api/v1/query_range')}`,
		`--data-urlencode ${shellQuote('query=' + expr)}`,
		`--data-urlencode ${shellQuote('start=' + from)}`,
		`--data-urlencode ${shellQuote('end=' + to)}`,
		`--data-urlencode ${shellQuote('step=' + step)}`
	].join(' \\\n  ');
}

/** The copyable curl for an instant query. */
export function curlInstant(origin: string, expr: string, time?: number) {
	const parts = [
		`curl -sG ${shellQuote(origin + '/api/v1/query')}`,
		`--data-urlencode ${shellQuote('query=' + expr)}`
	];
	if (time !== undefined) parts.push(`--data-urlencode ${shellQuote('time=' + time)}`);
	return parts.join(' \\\n  ');
}
