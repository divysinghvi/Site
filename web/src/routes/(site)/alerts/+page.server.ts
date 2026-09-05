// Build-time data for /alerts: the rule groups of /api/v1/rules, so the
// prerendered page already lists every rule (state inactive until the
// browser engine evaluates them).
import { loadRules } from '$lib/alerts/rules';
import { API_BASE } from '$lib/server/api';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ fetch }) => {
	const groups = await loadRules(fetch, API_BASE);
	return { groups };
};
