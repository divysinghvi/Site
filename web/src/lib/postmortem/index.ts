// Postmortem helpers: the fixed severity vocabulary (content §C.5.1), date
// formatting that never guesses a TODO(divy) date, list ordering and
// prev/next neighbours. Everything about an incident itself comes from
// /api/content/postmortems*; nothing here is prose about Divy.
import type { PostmortemSummary, Severity } from '$lib/api/types.gen';
import { isTodo } from '$lib/api/client';

export type PaletteTone = 'red' | 'orange' | 'yellow' | 'blue';

export interface SeverityDef {
	level: Severity;
	tone: PaletteTone;
	definition: string;
}

/** The severity scale the badge tooltip shows (fixed by the plan, not editorial per incident). */
export const SEVERITIES: readonly SeverityDef[] = [
	{ level: 'SEV1', tone: 'red', definition: 'User-facing production outage or data-loss risk' },
	{
		level: 'SEV2',
		tone: 'orange',
		definition: 'Production degraded or partially unavailable; host-level resource exhaustion'
	},
	{
		level: 'SEV3',
		tone: 'yellow',
		definition: 'Internal tooling down or blind; no user-facing impact'
	},
	{ level: 'SEV4', tone: 'blue', definition: 'Hygiene / near-miss; no outage' }
];

export function severityDef(level: Severity): SeverityDef {
	return SEVERITIES.find((s) => s.level === level) ?? SEVERITIES[SEVERITIES.length - 1]!;
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/**
 * Renders a content date (YYYY | YYYY-MM | YYYY-MM-DD | TODO(divy)) the way it
 * was written: "2024", "May 2024", "14 May 2024". TODO markers come back
 * verbatim so the page never prints an invented date.
 */
export function formatContentDate(raw: string): string {
	if (isTodo(raw)) return raw;
	const m = /^(\d{4})(?:-(\d{2})(?:-(\d{2}))?)?$/.exec(raw);
	if (!m) return raw;
	const y = m[1];
	const mo = m[2] ? MONTHS[Number(m[2]) - 1] : undefined;
	if (!mo) return y!;
	if (!m[3]) return `${mo} ${y}`;
	return `${Number(m[3])} ${mo} ${y}`;
}

/** Sort key of a content date: ms since the epoch (start of the period) or NaN for TODO/invalid. */
export function dateSortKey(raw: string): number {
	if (isTodo(raw)) return NaN;
	const m = /^(\d{4})(?:-(\d{2})(?:-(\d{2}))?)?$/.exec(raw);
	if (!m) return NaN;
	return Date.UTC(Number(m[1]), m[2] ? Number(m[2]) - 1 : 0, m[3] ? Number(m[3]) : 1);
}

/** Newest first: dated incidents by date (desc), then undated ones by id (desc). */
export function sortNewestFirst<T extends PostmortemSummary>(items: readonly T[]): T[] {
	return [...items].sort((a, b) => {
		const ka = dateSortKey(a.date);
		const kb = dateSortKey(b.date);
		const aKnown = !Number.isNaN(ka);
		const bKnown = !Number.isNaN(kb);
		if (aKnown && bKnown && ka !== kb) return kb - ka;
		if (aKnown !== bKnown) return aKnown ? -1 : 1;
		return b.id.localeCompare(a.id);
	});
}

export function byIdAsc<T extends PostmortemSummary>(items: readonly T[]): T[] {
	return [...items].sort((a, b) => a.id.localeCompare(b.id));
}

/** Previous/next reports in id order (INC-001 → INC-002 → …). */
export function neighbours<T extends PostmortemSummary>(
	items: readonly T[],
	id: string
): { prev?: T; next?: T } {
	const sorted = byIdAsc(items);
	const i = sorted.findIndex((p) => p.id === id);
	if (i < 0) return {};
	return { prev: sorted[i - 1], next: sorted[i + 1] };
}

/** A Prometheus duration ("2h30m") or TODO(divy), printed as written. */
export function formatIncidentDuration(raw: string): string {
	return raw;
}

/** Deep link that opens the span's drawer in the trace viewer (?span= is read on load). */
export function spanTraceHref(spanId: string): string {
	return `/trace/career?span=${encodeURIComponent(spanId)}`;
}
