// Build-time data for /logs (repo §R.7.1): the Loki label names, the service
// values, the career root start (where the `all` range begins) and a
// prerendered snapshot of the default query — the first 100 lines and their
// volume histogram — so the static HTML already carries the log list. The
// browser re-runs the query after hydration.
import { createLoki, type LokiMatrixSeries, type Stream } from '$lib/logql/loki';
import { DEFAULT_LIMIT, volumeBucket, volumeQuery } from '$lib/logql/lines';
import { DEFAULT_SELECTOR } from '$lib/logql/selector';
import { API_BASE, serverApi } from '$lib/server/api';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ fetch }) => {
	const lk = createLoki({ fetch, base: API_BASE });
	const api = serverApi(fetch);
	const [labels, services, traces] = await Promise.all([
		lk.labels(),
		lk.labelValues('service'),
		api.trace('career')
	]);
	const spans = traces.data[0]?.spans ?? [];
	const startUs = spans.reduce((m, s) => Math.min(m, s.startTime), Number.POSITIVE_INFINITY);
	const allFrom = Number.isFinite(startUs) ? Math.floor(startUs / 1e6) : 1672531200;
	const now = Math.floor(Date.now() / 1000);
	const query = DEFAULT_SELECTOR;
	const bucket = volumeBucket(allFrom, now);
	let streams: Stream[] = [];
	let volume: LokiMatrixSeries[] | null = null;
	let entriesTotal = 0;
	try {
		const [lines, vol] = await Promise.all([
			lk.queryRange(query, { start: allFrom, end: now, limit: DEFAULT_LIMIT }),
			lk.queryRange(volumeQuery(query, bucket.dur), { start: allFrom, end: now, step: bucket.step })
		]);
		if (lines.data.resultType === 'streams') streams = lines.data.result;
		if (vol.data.resultType === 'matrix') volume = vol.data.result;
		entriesTotal = lines.stats?.summary.totalLinesProcessed ?? 0;
	} catch {
		// the page still renders; the browser retries after hydration
	}
	return {
		labels,
		services,
		allFrom,
		snapshot: {
			query,
			from: allFrom,
			to: now,
			limit: DEFAULT_LIMIT,
			streams,
			volume,
			bucket,
			entriesTotal
		}
	};
};
