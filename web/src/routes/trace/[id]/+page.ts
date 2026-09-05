// /trace/[id]: any trace id (career, the career trace id, or an OTel
// X-Divy-Trace-Id). The id space is open, so this route is not prerendered:
// the Go static handler serves 200.html and the browser fetches the trace.
// No server load anywhere in this route's chain (it lives outside (site)).
import { api, isApiError } from '$lib/api/client';
import type { JaegerTrace } from '$lib/api/types.gen';
import type { PageLoad } from './$types';

export const prerender = false;
export const ssr = false;

export interface TraceLoadError {
	status: number;
	message: string;
	traceId?: string;
}

export const load: PageLoad = async ({ params }) => {
	const id = params.id;
	try {
		const res = await api.trace(id);
		const trace: JaegerTrace | null = res.data[0] ?? null;
		return {
			id,
			trace,
			error: trace ? null : ({ status: 404, message: 'empty trace' } as TraceLoadError)
		};
	} catch (e) {
		const err: TraceLoadError = isApiError(e)
			? { status: e.status, message: e.message, traceId: e.traceId }
			: { status: 0, message: e instanceof Error ? e.message : String(e) };
		return { id, trace: null, error: err };
	}
};
