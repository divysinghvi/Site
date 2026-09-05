// The client-side alert evaluator (brief §3.6, content §C.7): rules from
// /api/v1/rules, every `expr` polled through /api/v1/query on the group's
// interval (15 s), inactive → pending → firing per `for`, silences for the
// session, and a toast for every firing transition through $lib/toasts. One
// instance for the whole app: the root layout calls `alerts.start()` once and
// client-side navigation keeps it alive; /alerts renders the same state.
//
// Honesty rules: an empty result whose metric has no series at all is
// "no data" (with the collector's status as the reason), never "inactive";
// an API error is shown as such.
//
// Test hook: `?alerts_fast=1` on the first page loaded (Playwright) polls
// every second and divides every `for` by 10 (30 s → 3 s). Nothing else
// changes; the real cadences stay 15 s / 30 s.
import type { PromRuleGroup } from '$lib/api/types.gen';
import { collectorForMetric, collectorStatus, fetchReadyz } from '$lib/panels/model';
import { labelValues, query, sampleValue } from '$lib/panels/prom';
import { toasts } from '$lib/toasts/store.svelte';
import { loadRules, ruleDefs, type RuleDef } from './rules';
import { renderTemplate } from './template';

export type AlertState = 'inactive' | 'pending' | 'firing' | 'nodata' | 'error';

export interface AlertSample {
	labels: Record<string, string>;
	value: number;
}

export interface AlertEval {
	def: RuleDef;
	state: AlertState;
	/** When the condition first became true (ms), kept while pending/firing. */
	activeAt?: number;
	/** When the alert started firing (ms). */
	firedAt?: number;
	value?: number;
	samples: AlertSample[];
	lastEval?: number;
	error?: string;
	/** Metric names of the expression with no series in the API. */
	missing: string[];
	/** Why there is no data (collector status), when `missing` is non-empty. */
	reason?: string;
	/** `summary` annotation with {{ $value }} / {{ $labels.x }} substituted. */
	summary: string;
	silenced: boolean;
}

export interface Silence {
	alertname: string;
	createdAt: number;
}

export const SILENCES_KEY = 'divy.alerts.silences';
export const DEFAULT_INTERVAL_S = 15;

// PromQL words that are not metric names (docs/promql-subset.md §1).
const NOT_METRICS = new Set([
	'sum',
	'avg',
	'min',
	'max',
	'count',
	'by',
	'without',
	'bool',
	'and',
	'or',
	'unless',
	'on',
	'ignoring',
	'group_left',
	'group_right',
	'offset',
	'rate',
	'increase',
	'irate',
	'delta',
	'sum_over_time',
	'avg_over_time',
	'min_over_time',
	'max_over_time',
	'count_over_time',
	'last_over_time',
	'abs',
	'ceil',
	'floor',
	'round',
	'clamp_min',
	'clamp_max',
	'time',
	'vector',
	'scalar',
	'inf',
	'nan',
	'Inf',
	'NaN'
]);

/** Metric names referenced by an expression (identifiers that are not functions, keywords or label names). */
export function metricNamesIn(expr: string): string[] {
	const stripped = expr
		.replace(/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|`[^`]*`/g, '""')
		.replace(/\{[^}]*\}/g, '{}')
		.replace(/\[[^\]]*\]/g, '[]');
	const out = new Set<string>();
	const re = /([A-Za-z_:][A-Za-z0-9_:]*)(\s*\()?/g;
	let m: RegExpExecArray | null;
	while ((m = re.exec(stripped))) {
		const name = m[1]!;
		if (m[2] || NOT_METRICS.has(name)) continue;
		// `by (a, b)` / `without (a)`: the grouping labels
		const before = stripped.slice(0, m.index);
		if (/\b(by|without)\s*\([^)]*$/.test(before)) continue;
		out.add(name);
	}
	return [...out];
}

function toneOf(severity: string | undefined): 'error' | 'warning' | 'info' {
	switch ((severity ?? '').toLowerCase()) {
		case 'page':
		case 'critical':
		case 'error':
			return 'error';
		case 'warning':
		case 'warn':
			return 'warning';
		default:
			return 'info';
	}
}

function hhmmss(ms: number): string {
	return new Date(ms).toISOString().slice(11, 19) + 'Z';
}

function readSilences(): Silence[] {
	try {
		const raw = sessionStorage.getItem(SILENCES_KEY);
		if (!raw) return [];
		const parsed: unknown = JSON.parse(raw);
		if (!Array.isArray(parsed)) return [];
		return parsed.flatMap((x) => {
			const o = x as { alertname?: unknown; createdAt?: unknown };
			return typeof o?.alertname === 'string'
				? [{ alertname: o.alertname, createdAt: typeof o.createdAt === 'number' ? o.createdAt : 0 }]
				: [];
		});
	} catch {
		return [];
	}
}

function writeSilences(list: Silence[]) {
	try {
		sessionStorage.setItem(SILENCES_KEY, JSON.stringify(list));
	} catch {
		// storage unavailable: the silence lasts for this page only
	}
}

class AlertEngine {
	rules = $state<AlertEval[]>([]);
	silences = $state<Silence[]>([]);
	/** idle until start(); loading while /api/v1/rules is in flight. */
	status = $state<'idle' | 'loading' | 'ready' | 'error'>('idle');
	loadError = $state<string | null>(null);
	/** Unix ms of the last completed evaluation round. */
	lastRound = $state<number | null>(null);
	intervalS = $state(DEFAULT_INTERVAL_S);
	/** True under the test hook (?alerts_fast=1). */
	fast = $state(false);
	startedAt = $state<number | null>(null);

	private started = false;
	private timer: ReturnType<typeof setInterval> | undefined;
	private evaluating = false;
	private names: Set<string> | null = null;
	private namesAt = 0;
	private forScale = 1;

	get firing(): AlertEval[] {
		return this.rules.filter((r) => r.state === 'firing');
	}
	get pending(): AlertEval[] {
		return this.rules.filter((r) => r.state === 'pending');
	}

	/** Rule definitions from a page load (prerendered /alerts); state by name is kept. */
	seed(groups: readonly PromRuleGroup[]) {
		this.setDefs(ruleDefs(groups));
	}

	private setDefs(defs: RuleDef[]) {
		const prev = new Map(this.rules.map((r) => [r.def.name, r]));
		this.rules = defs.map((def) => {
			const old = prev.get(def.name);
			return old
				? { ...old, def, summary: old.summary || def.annotations.summary || '' }
				: {
						def,
						state: 'inactive',
						samples: [],
						missing: [],
						summary: def.annotations.summary ?? '',
						silenced: this.isSilenced(def.name)
					};
		});
		const iv = defs.find((d) => d.intervalS > 0)?.intervalS;
		if (iv && !this.fast) this.intervalS = iv;
	}

	/** Starts polling (browser only, once per page load). */
	start() {
		if (this.started || typeof window === 'undefined') return;
		this.started = true;
		this.startedAt = Date.now();
		try {
			const q = new URLSearchParams(window.location.search);
			if (q.get('alerts_fast') === '1') {
				this.fast = true;
				this.intervalS = 1;
				this.forScale = 0.1;
			}
		} catch {
			// no location: keep the defaults
		}
		this.silences = readSilences();
		for (const r of this.rules) r.silenced = this.isSilenced(r.def.name);
		void this.reload().then(() => this.schedule());
		document.addEventListener('visibilitychange', this.onVisibility);
	}

	stop() {
		if (this.timer) clearInterval(this.timer);
		this.timer = undefined;
		document.removeEventListener('visibilitychange', this.onVisibility);
		this.started = false;
	}

	private onVisibility = () => {
		if (!document.hidden) void this.evaluate();
	};

	private schedule() {
		if (this.timer) clearInterval(this.timer);
		this.timer = setInterval(() => {
			if (typeof document !== 'undefined' && document.hidden) return;
			void this.evaluate();
		}, this.intervalS * 1000);
	}

	/** Fetches the rules and runs one round. */
	async reload() {
		this.status = 'loading';
		try {
			const groups = await loadRules((i, o) => globalThis.fetch(i, o));
			this.setDefs(ruleDefs(groups));
			this.status = 'ready';
			this.loadError = null;
		} catch (e) {
			this.status = this.rules.length ? 'ready' : 'error';
			this.loadError = e instanceof Error ? e.message : String(e);
		}
		await this.evaluate();
	}

	private async metricNames(): Promise<Set<string> | null> {
		const now = Date.now();
		if (this.names && now - this.namesAt < 60_000) return this.names;
		try {
			this.names = new Set(await labelValues('__name__'));
			this.namesAt = now;
		} catch {
			this.names = null;
		}
		return this.names;
	}

	/** One evaluation round over every rule. */
	async evaluate() {
		if (this.evaluating || this.rules.length === 0) return;
		this.evaluating = true;
		try {
			const names = await this.metricNames();
			const now = Date.now();
			await Promise.all(this.rules.map((r, i) => this.evaluateRule(i, r, names, now)));
			this.lastRound = Date.now();
		} finally {
			this.evaluating = false;
		}
	}

	private async evaluateRule(i: number, r: AlertEval, names: Set<string> | null, now: number) {
		const def = r.def;
		let next: AlertEval = { ...r, lastEval: now, error: undefined };
		try {
			const res = await query(def.expr);
			const d = res.data;
			const samples: AlertSample[] = [];
			if (d.resultType === 'vector') {
				for (const s of d.result) samples.push({ labels: s.metric, value: sampleValue(s.value[1]) });
			} else if (d.resultType === 'scalar') {
				const v = sampleValue(d.result[1]);
				if (v !== 0 && !Number.isNaN(v)) samples.push({ labels: {}, value: v });
			}
			const missing =
				samples.length === 0 && names ? metricNamesIn(def.expr).filter((n) => !names.has(n)) : [];
			next.samples = samples;
			next.missing = missing;
			next.value = samples[0]?.value;
			if (samples.length > 0) {
				const activeAt = r.activeAt ?? now;
				const forMs = def.forS * 1000 * this.forScale;
				const firing = now - activeAt >= forMs;
				next.activeAt = activeAt;
				next.state = firing ? 'firing' : 'pending';
				next.firedAt = firing ? (r.firedAt ?? now) : undefined;
				next.reason = undefined;
			} else if (missing.length > 0) {
				next.state = 'nodata';
				next.activeAt = undefined;
				next.firedAt = undefined;
				next.reason = await this.reasonFor(missing);
			} else {
				next.state = 'inactive';
				next.activeAt = undefined;
				next.firedAt = undefined;
				next.reason = undefined;
			}
			const labels = { ...(samples[0]?.labels ?? {}), ...def.labels };
			next.summary = renderTemplate(def.annotations.summary ?? '', { value: next.value, labels });
		} catch (e) {
			next = {
				...next,
				state: 'error',
				error: e instanceof Error ? e.message : String(e),
				activeAt: undefined,
				firedAt: undefined,
				samples: [],
				missing: []
			};
		}
		next.silenced = this.isSilenced(def.name);
		this.rules[i] = next;
		this.notify(r, next);
	}

	private async reasonFor(missing: string[]): Promise<string> {
		const readyz = await fetchReadyz();
		const parts = missing.map((m) => {
			const c = collectorForMetric(m);
			return c ? `${m}: no series — ${collectorStatus(c, readyz).text}` : `${m}: no series`;
		});
		return parts.join('; ');
	}

	private notify(prev: AlertEval, next: AlertEval) {
		const id = toastId(next.def.name);
		if (next.state === 'firing' && prev.state !== 'firing') {
			if (!next.silenced) this.pushToast(next);
		} else if (next.state !== 'firing' && prev.state === 'firing') {
			toasts.dismiss(id);
		}
	}

	private pushToast(r: AlertEval) {
		const def = r.def;
		const sev = def.labels.severity;
		const runbook = def.annotations.runbook_url;
		toasts.push({
			id: toastId(def.name),
			title: def.name,
			tone: toneOf(sev),
			meta: `firing${sev ? ` · severity=${sev}` : ''} · since ${hhmmss(r.firedAt ?? Date.now())}`,
			body: r.summary,
			actions: [
				{
					label: 'Silence',
					ariaLabel: `Silence ${def.name}`,
					onclick: () => this.silence(def.name)
				},
				...(runbook
					? [{ label: 'Runbook', ariaLabel: `Runbook for ${def.name}`, href: runbook }]
					: []),
				{ label: 'Alerts', ariaLabel: `Open the alerts page for ${def.name}`, href: '/alerts' }
			]
		});
	}

	isSilenced(name: string): boolean {
		return this.silences.some((s) => s.alertname === name);
	}

	/** Silences the alert for the rest of the browser session (sessionStorage). */
	silence(name: string) {
		if (!this.isSilenced(name)) {
			this.silences = [...this.silences, { alertname: name, createdAt: Date.now() }];
			writeSilences(this.silences);
		}
		toasts.dismiss(toastId(name));
		const i = this.rules.findIndex((r) => r.def.name === name);
		if (i >= 0) this.rules[i] = { ...this.rules[i]!, silenced: true };
	}

	unsilence(name: string) {
		this.silences = this.silences.filter((s) => s.alertname !== name);
		writeSilences(this.silences);
		const i = this.rules.findIndex((r) => r.def.name === name);
		if (i >= 0) {
			const r = { ...this.rules[i]!, silenced: false };
			this.rules[i] = r;
			if (r.state === 'firing') this.pushToast(r);
		}
	}
}

export function toastId(alertname: string): string {
	return `alert:${alertname}`;
}

/** Explore link for a rule's expression (instant). */
export function exploreHref(expr: string): string {
	const q = new URLSearchParams({ ds: 'prom', expr, from: 'now-7d', to: 'now', instant: '1' });
	return `/explore?${q.toString()}`;
}

export const alerts = new AlertEngine();
