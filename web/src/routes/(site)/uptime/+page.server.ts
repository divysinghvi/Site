// /uptime: the target list with probe settings (/api/content/uptime) and a
// build-time snapshot of /api/uptime (90 daily buckets per target). The
// browser replaces the snapshot with a live fetch after hydration and every
// 60 s while visible.
import type { UptimeHeartbeats } from '$lib/api/types.gen';
import { serverApi } from '$lib/server/api';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ fetch }) => {
	const api = serverApi(fetch);
	const content = await api.content.uptime();
	let snapshot: UptimeHeartbeats | null = null;
	try {
		snapshot = await api.uptime.heartbeats(90, '1d');
	} catch {
		snapshot = null;
	}
	return { targets: content.targets, snapshot };
};
