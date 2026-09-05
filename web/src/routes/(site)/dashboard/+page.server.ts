// Build-time data for /dashboard: the panel definitions (content/panels.yaml
// via the API) and the career root span's start, which is where the `all`
// time range begins. Live values are fetched by the browser after hydration.
import { serverApi } from '$lib/server/api';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ fetch }) => {
	const api = serverApi(fetch);
	const [panels, traces] = await Promise.all([api.content.panels(), api.trace('career')]);
	const spans = traces.data[0]?.spans ?? [];
	// Jaeger startTime is microseconds; the earliest span is the root.
	const startUs = spans.reduce((m, s) => Math.min(m, s.startTime), Number.POSITIVE_INFINITY);
	const allFrom = Number.isFinite(startUs) ? Math.floor(startUs / 1e6) : undefined;
	return { panels, allFrom };
};
