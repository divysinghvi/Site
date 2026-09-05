// /contact: the profile comes from the (site) layout; the healthz snapshot
// taken at build time is shown until the browser fetches the live one.
import type { Healthz } from '$lib/api/types.gen';
import { serverApi } from '$lib/server/api';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ fetch }) => {
	const api = serverApi(fetch);
	let healthz: Healthz | null = null;
	try {
		healthz = await api.healthz();
	} catch {
		healthz = null;
	}
	return { healthz };
};
