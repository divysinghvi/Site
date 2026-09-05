// GET /api/v1/rules (Prometheus shape, content/alerts.yaml rendered by the
// API): fetch-bound so the /alerts page load (build time) and the browser
// engine share it. The API never evaluates: `state` is always inactive and
// the browser owns the state machine ($lib/alerts/engine).
import type { Fetch } from '$lib/api/client';
import { ApiError } from '$lib/api/client';
import type { PromAlertingRule, PromRuleGroup, PromRulesResult } from '$lib/api/types.gen';

export interface RuleDef {
	name: string;
	group: string;
	/** Canonical PromQL from the API. */
	expr: string;
	/** `for` in seconds. */
	forS: number;
	/** Group evaluation interval in seconds. */
	intervalS: number;
	labels: Record<string, string>;
	annotations: Record<string, string>;
}

function clean(m: Record<string, string | undefined> | undefined): Record<string, string> {
	const out: Record<string, string> = {};
	for (const [k, v] of Object.entries(m ?? {})) if (typeof v === 'string') out[k] = v;
	return out;
}

export function ruleDefs(groups: readonly PromRuleGroup[]): RuleDef[] {
	const out: RuleDef[] = [];
	for (const g of groups) {
		for (const r of g.rules as PromAlertingRule[]) {
			if (r.type && r.type !== 'alerting') continue;
			out.push({
				name: r.name,
				group: g.name,
				expr: r.query,
				forS: r.duration,
				intervalS: g.interval > 0 ? g.interval : 15,
				labels: clean(r.labels),
				annotations: clean(r.annotations)
			});
		}
	}
	return out;
}

export async function loadRules(fetch: Fetch, base = ''): Promise<PromRuleGroup[]> {
	const url = base.replace(/\/+$/, '') + '/api/v1/rules';
	const res = await fetch(url, { headers: { Accept: 'application/json' } });
	if (!res.ok) {
		let message = `${res.status} ${res.statusText}`.trim();
		try {
			const b = (await res.json()) as { error?: unknown };
			if (typeof b?.error === 'string') message = b.error;
		} catch {
			// keep the status line
		}
		throw new ApiError(message, { status: res.status, url });
	}
	const body = (await res.json()) as PromRulesResult;
	return body.data?.groups ?? [];
}
