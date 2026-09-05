// Build-time data shared by every prerendered page: the profile (name,
// open-to-work, tagline) and the postmortem list (span → INC links, titles).
// The site origin for absolute og:/canonical URLs comes from
// PUBLIC_SITE_ORIGIN, falling back to the origin the API itself put into the
// postmortems' og_image URLs (SITE_ORIGIN on the Go side).
import { env } from '$env/dynamic/public';
import { serverApi } from '$lib/server/api';
import type { LayoutServerLoad } from './$types';

function originOf(url: string | undefined): string {
	if (!url) return '';
	try {
		return new URL(url).origin;
	} catch {
		return '';
	}
}

export const load: LayoutServerLoad = async ({ fetch }) => {
	const api = serverApi(fetch);
	const [profile, postmortems] = await Promise.all([
		api.content.profile(),
		api.content.postmortems()
	]);
	const siteOrigin = env.PUBLIC_SITE_ORIGIN || originOf(postmortems.items[0]?.og_image);
	return { profile, postmortems: postmortems.items, siteOrigin };
};
