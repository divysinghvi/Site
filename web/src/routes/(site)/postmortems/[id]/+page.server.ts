// One postmortem, prerendered per id. `entries` lists every id the API knows
// so the pages exist even if no link reached them during the crawl.
import { error } from '@sveltejs/kit';
import { isApiError } from '$lib/api/client';
import { serverApi } from '$lib/server/api';
import type { EntryGenerator, PageServerLoad } from './$types';

export const entries: EntryGenerator = async () => {
	const api = serverApi((input, init) => globalThis.fetch(input, init));
	const list = await api.content.postmortems();
	return list.items.map((p) => ({ id: p.id }));
};

export const load: PageServerLoad = async ({ fetch, params }) => {
	const api = serverApi(fetch);
	try {
		const [pm, services] = await Promise.all([
			api.content.postmortem(params.id),
			api.content.services()
		]);
		return { pm, services: services.services };
	} catch (e) {
		if (isApiError(e) && e.status === 404) error(404, e.message);
		throw e;
	}
};
