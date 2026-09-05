// Typed client for the Go API. Every method is a GET returning the shape the
// Go struct of the same name serialises (types.gen.ts); non-2xx responses
// throw ApiError with the message from the family's error envelope.
import { env } from '$env/dynamic/public';
import type {
	AlertsFile,
	ContentManualMetrics,
	ContentPostmortem,
	ContentPostmortemList,
	ContentProfile,
	ContentServices,
	ContentTodos,
	ContentUptime,
	Healthz,
	JaegerKeyValue,
	JaegerOperationsResponse,
	JaegerSpan,
	JaegerStringsResponse,
	JaegerTrace,
	JaegerTraceResponse,
	PanelsFile,
	Readyz,
	UptimeHeartbeats
} from './types.gen';

export type Fetch = typeof globalThis.fetch;

export interface ApiOptions {
	fetch: Fetch;
	/** Absolute origin of the API, or '' for same-origin. */
	base: string;
}

/** A non-2xx API response. `message` is the API's own error text. */
export class ApiError extends Error {
	readonly status: number;
	readonly url: string;
	/** Prometheus errorType when the body was a Prometheus envelope. */
	readonly errorType?: string;
	/** X-Divy-Trace-Id of the failed request, when the API sent one. */
	readonly traceId?: string;

	constructor(
		message: string,
		init: { status: number; url: string; errorType?: string; traceId?: string }
	) {
		super(message);
		this.name = 'ApiError';
		this.status = init.status;
		this.url = init.url;
		this.errorType = init.errorType;
		this.traceId = init.traceId;
	}
}

export function isApiError(e: unknown): e is ApiError {
	return e instanceof ApiError;
}

async function errorFromResponse(res: Response): Promise<ApiError> {
	const traceId = res.headers.get('x-divy-trace-id') ?? undefined;
	let message = `${res.status} ${res.statusText}`.trim();
	let errorType: string | undefined;
	const ct = res.headers.get('content-type') ?? '';
	try {
		if (ct.includes('application/json')) {
			const body: unknown = await res.json();
			if (body && typeof body === 'object') {
				const b = body as Record<string, unknown>;
				if (typeof b.error === 'string') message = b.error;
				if (typeof b.errorType === 'string') errorType = b.errorType;
			}
		} else {
			// Loki-family errors are plain text.
			const text = (await res.text()).trim();
			if (text) message = text;
		}
	} catch {
		// keep the status line
	}
	return new ApiError(message, { status: res.status, url: res.url, errorType, traceId });
}

export function createApi({ fetch, base }: ApiOptions) {
	const root = base.replace(/\/+$/, '');

	async function get<T>(path: string, init?: RequestInit): Promise<T> {
		const res = await fetch(root + path, {
			...init,
			headers: { Accept: 'application/json', ...(init?.headers ?? {}) }
		});
		if (!res.ok) throw await errorFromResponse(res);
		return (await res.json()) as T;
	}

	return {
		base: root,
		/** GET /api/traces/{id}: `career`, the career trace id or any 32-hex OTel trace id. */
		trace: (id: string) => get<JaegerTraceResponse>(`/api/traces/${encodeURIComponent(id)}`),
		/** GET /api/services */
		services: () => get<JaegerStringsResponse>('/api/services'),
		/** GET /api/operations?service= */
		operations: (service: string) =>
			get<JaegerOperationsResponse>(`/api/operations?service=${encodeURIComponent(service)}`),
		healthz: () => get<Healthz>('/healthz'),
		readyz: () => get<Readyz>('/readyz'),
		content: {
			services: () => get<ContentServices>('/api/content/services'),
			postmortems: () => get<ContentPostmortemList>('/api/content/postmortems'),
			postmortem: (id: string) =>
				get<ContentPostmortem>(`/api/content/postmortems/${encodeURIComponent(id)}`),
			panels: () => get<PanelsFile>('/api/content/panels'),
			alerts: () => get<AlertsFile>('/api/content/alerts'),
			uptime: () => get<ContentUptime>('/api/content/uptime'),
			manualMetrics: () => get<ContentManualMetrics>('/api/content/manual-metrics'),
			profile: () => get<ContentProfile>('/api/content/profile'),
			todos: () => get<ContentTodos>('/api/content/todos')
		},
		uptime: {
			heartbeats: (days = 90, bucket: '1d' | '1h' = '1d') =>
				get<UptimeHeartbeats>(`/api/uptime/heartbeats?days=${days}&bucket=${bucket}`)
		}
	};
}

export type Api = ReturnType<typeof createApi>;

/** Browser client: same origin by default (Vite proxies in dev, embedded in prod); PUBLIC_API_BASE overrides. */
export const api: Api = createApi({
	fetch: (input, init) => globalThis.fetch(input, init),
	base: env.PUBLIC_API_BASE ?? ''
});

// ---- runtime narrowing helpers (the only place the app looks inside `unknown` values) ----

/** The first trace of a Jaeger response, if any. */
export function firstTrace(res: JaegerTraceResponse): JaegerTrace | undefined {
	return res.data[0];
}

export function findTag(
	tags: readonly JaegerKeyValue[] | undefined,
	key: string
): JaegerKeyValue | undefined {
	return tags?.find((t) => t.key === key);
}

export function tagString(
	tags: readonly JaegerKeyValue[] | undefined,
	key: string
): string | undefined {
	const v = findTag(tags, key)?.value;
	if (v === undefined || v === null) return undefined;
	return typeof v === 'string' ? v : String(v);
}

export function tagBool(tags: readonly JaegerKeyValue[] | undefined, key: string): boolean {
	const v = findTag(tags, key)?.value;
	return v === true || v === 'true';
}

export function tagNumber(
	tags: readonly JaegerKeyValue[] | undefined,
	key: string
): number | undefined {
	const v = findTag(tags, key)?.value;
	if (typeof v === 'number') return v;
	if (typeof v === 'string' && v !== '' && Number.isFinite(Number(v))) return Number(v);
	return undefined;
}

/** Jaeger tag values are string | bool | int64 | float64; anything else is shown as JSON. */
export function tagValueText(v: unknown): string {
	if (typeof v === 'string') return v;
	if (typeof v === 'number' || typeof v === 'boolean') return String(v);
	if (v === null || v === undefined) return '';
	return JSON.stringify(v);
}

/** A string-list tag is exported as a JSON array string (OTel array rule); parse it back. */
export function tagStringList(v: unknown): string[] | undefined {
	if (typeof v !== 'string' || !v.startsWith('[')) return undefined;
	try {
		const parsed: unknown = JSON.parse(v);
		if (Array.isArray(parsed) && parsed.every((x) => typeof x === 'string'))
			return parsed as string[];
	} catch {
		// not a list
	}
	return undefined;
}

export interface SpanLink {
	kind: string;
	ref?: string;
	url?: string;
	label?: string;
}

/** Parses the `divy.links` tag (JSON text of content/spans.yaml links[]). */
export function parseLinks(tags: readonly JaegerKeyValue[] | undefined): SpanLink[] {
	const raw = tagString(tags, 'divy.links');
	if (!raw) return [];
	try {
		const parsed: unknown = JSON.parse(raw);
		if (!Array.isArray(parsed)) return [];
		return parsed.flatMap((x) => {
			if (!x || typeof x !== 'object') return [];
			const o = x as Record<string, unknown>;
			if (typeof o.kind !== 'string') return [];
			return [
				{
					kind: o.kind,
					ref: typeof o.ref === 'string' ? o.ref : undefined,
					url: typeof o.url === 'string' ? o.url : undefined,
					label: typeof o.label === 'string' ? o.label : undefined
				}
			];
		});
	} catch {
		return [];
	}
}

/** Parses the `divy.todo` tag (JSON array of TODO(divy) strings). */
export function parseTodos(tags: readonly JaegerKeyValue[] | undefined): string[] {
	return tagStringList(findTag(tags, 'divy.todo')?.value) ?? [];
}

export function isTodo(s: string | undefined): boolean {
	return typeof s === 'string' && s.startsWith('TODO(divy)');
}

/** The parent span id from CHILD_OF references. */
export function parentSpanId(span: JaegerSpan): string | undefined {
	return span.references.find((r) => r.refType === 'CHILD_OF')?.spanID;
}

/** Accepts `career` or a 32-hex trace id (the /api/traces/{id} rule). */
export function isTraceId(id: string): boolean {
	return id === 'career' || /^[0-9a-f]{32}$/i.test(id);
}
