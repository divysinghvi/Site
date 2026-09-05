// Build-time data for /explore: only the career root span's start (where the
// `all` range begins). Queries run in the browser from the URL's parameters.
import { serverApi } from '$lib/server/api';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ fetch }) => {
	const api = serverApi(fetch);
	const traces = await api.trace('career');
	const spans = traces.data[0]?.spans ?? [];
	const startUs = spans.reduce((m, s) => Math.min(m, s.startTime), Number.POSITIVE_INFINITY);
	const allFrom = Number.isFinite(startUs) ? Math.floor(startUs / 1e6) : undefined;
	return { allFrom };
};
