// The floating query console (brief §4): `kubectl get pods` lists the
// profile's pods (/api/content/profile), any other kubectl verb/resource
// answers like kubectl does, `help` / `clear` are local, and anything else is
// sent to GET /api/v1/query and rendered as a table (or the API's error).
import { api } from '$lib/api/client';
import { query, sampleValue } from '$lib/panels/prom';

export interface Table {
	columns: string[];
	rows: string[][];
}

export type Output =
	| { kind: 'cmd'; text: string }
	| { kind: 'text'; text: string }
	| { kind: 'error'; text: string }
	| { kind: 'table'; table: Table; caption?: string }
	| { kind: 'clear' };

export const HELP_LINES = [
	'divy console — PromQL instant queries against /api/v1/query',
	'',
	'  <expr>              run a PromQL expression, e.g. divy_uptime_seconds',
	'  kubectl get pods    my projects as pods (from /api/content/profile)',
	'  help                this text',
	'  clear               clear the output',
	'',
	'  ↑ / ↓ history · Esc closes · type "promql" anywhere to reopen'
];

/** kubectl-style age: 2y, 188d, 5h, 12m, 40s. */
export function kubeAge(seconds: number): string {
	const s = Math.max(0, Math.floor(seconds));
	const d = Math.floor(s / 86400);
	if (d >= 730) return `${Math.floor(d / 365)}y`;
	if (d >= 2) return `${d}d`;
	const h = Math.floor(s / 3600);
	if (h >= 2) return `${h}h`;
	const m = Math.floor(s / 60);
	if (m >= 2) return `${m}m`;
	return `${s}s`;
}

const POD_ALIASES = new Set(['pods', 'pod', 'po']);

async function kubectl(args: string[]): Promise<Output[]> {
	const [verb, resource] = args;
	if (!verb) {
		return [
			{
				kind: 'text',
				text: 'kubectl controls the Kubernetes cluster manager.\n\nThis console knows one resource type: pods.\n\n  kubectl get pods'
			}
		];
	}
	if (verb !== 'get') {
		return [
			{
				kind: 'error',
				text: `error: unknown command "${verb}" for "kubectl"\n\nRun 'kubectl --help' for usage.`
			}
		];
	}
	if (!resource)
		return [
			{
				kind: 'error',
				text: 'error: You must specify the type of resource to get. Use "kubectl api-resources" for a complete list of supported resources.'
			}
		];
	if (!POD_ALIASES.has(resource)) {
		return [
			{ kind: 'error', text: `error: the server doesn't have a resource type "${resource}"` }
		];
	}
	const profile = await api.content.profile();
	const rows = profile.pods.map((p) => [
		p.name,
		p.ready,
		p.status,
		String(p.restarts),
		kubeAge(p.age_s),
		p.note ?? ''
	]);
	return [
		{
			kind: 'table',
			caption: `${rows.length} pods · restarts from ${[...new Set(profile.pods.map((p) => p.restarts_from))].join(', ')}`,
			table: { columns: ['NAME', 'READY', 'STATUS', 'RESTARTS', 'AGE', 'NOTE'], rows }
		}
	];
}

function fmt(v: number): string {
	if (Number.isNaN(v)) return 'NaN';
	if (!Number.isFinite(v)) return v > 0 ? '+Inf' : '-Inf';
	return Number.isInteger(v) ? String(v) : String(Number(v.toPrecision(8)));
}

function labelColumns(sets: Record<string, string>[]): string[] {
	const keys = new Set<string>();
	for (const s of sets) for (const k of Object.keys(s)) keys.add(k);
	const list = [...keys].filter((k) => k !== '__name__').sort();
	return keys.has('__name__') ? ['__name__', ...list] : list;
}

async function promql(expr: string): Promise<Output[]> {
	const res = await query(expr);
	const d = res.data;
	const warn: Output[] = res.warnings.length
		? [{ kind: 'text', text: 'warning: ' + res.warnings.join('; ') }]
		: [];
	switch (d.resultType) {
		case 'vector': {
			if (d.result.length === 0)
				return [...warn, { kind: 'text', text: 'empty result (no series matched)' }];
			const cols = labelColumns(d.result.map((s) => s.metric));
			return [
				...warn,
				{
					kind: 'table',
					caption: `vector · ${d.result.length} series · @${new Date(d.result[0]!.value[0] * 1000).toISOString()}`,
					table: {
						columns: [...cols, 'VALUE'],
						rows: d.result.map((s) => [
							...cols.map((c) => s.metric[c] ?? ''),
							fmt(sampleValue(s.value[1]))
						])
					}
				}
			];
		}
		case 'matrix': {
			if (d.result.length === 0)
				return [...warn, { kind: 'text', text: 'empty result (no series matched)' }];
			const cols = labelColumns(d.result.map((s) => s.metric));
			return [
				...warn,
				{
					kind: 'table',
					caption: `matrix · ${d.result.length} series (last sample of each)`,
					table: {
						columns: [...cols, 'SAMPLES', 'LAST'],
						rows: d.result.map((s) => {
							const last = s.values[s.values.length - 1];
							return [
								...cols.map((c) => s.metric[c] ?? ''),
								String(s.values.length),
								last ? fmt(sampleValue(last[1])) : ''
							];
						})
					}
				}
			];
		}
		case 'scalar':
			return [
				...warn,
				{
					kind: 'table',
					caption: 'scalar',
					table: { columns: ['VALUE'], rows: [[fmt(sampleValue(d.result[1]))]] }
				}
			];
		case 'string':
			return [
				...warn,
				{ kind: 'table', caption: 'string', table: { columns: ['VALUE'], rows: [[d.result[1]]] } }
			];
	}
}

/** Runs one console line. Never throws: errors come back as `error` outputs. */
export async function runCommand(line: string): Promise<Output[]> {
	const text = line.trim();
	if (!text) return [];
	const words = text.split(/\s+/);
	const head = words[0]!.toLowerCase();
	if (head === 'help' || head === '?') return [{ kind: 'text', text: HELP_LINES.join('\n') }];
	if (head === 'clear' || head === 'cls') return [{ kind: 'clear' }];
	try {
		if (head === 'kubectl' || head === 'k') return await kubectl(words.slice(1));
		return await promql(text);
	} catch (e) {
		return [{ kind: 'error', text: e instanceof Error ? e.message : String(e) }];
	}
}
