// The hero: the career trace as served at build time. The page re-fetches it
// once after hydration so open-span durations are live (review coverage-21).
import { error } from '@sveltejs/kit';
import { serverApi } from '$lib/server/api';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ fetch }) => {
	const api = serverApi(fetch);
	const [traces, services] = await Promise.all([api.trace('career'), api.content.services()]);
	const trace = traces.data[0];
	if (!trace) error(502, 'the API returned no career trace');
	return { trace, services: services.services };
};
