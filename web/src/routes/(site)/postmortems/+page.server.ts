// Postmortem list: the reports come from the (site) layout (/api/content/
// postmortems); the service colours for the chips from /api/content/services.
import { serverApi } from '$lib/server/api';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ fetch }) => {
	const api = serverApi(fetch);
	const services = await api.content.services();
	return { services: services.services };
};
