// Time-axis ticks for any visible range: calendar-aligned for years/months/
// days, decimal for sub-day steps. Returns at most ~`max` ticks.
import { UNITS, formatTick } from '$lib/format';

export interface Tick {
	us: number;
	label: string;
	major: boolean;
}

const LADDER: number[] = [
	UNITS.US,
	10 * UNITS.US,
	100 * UNITS.US,
	UNITS.MS,
	10 * UNITS.MS,
	100 * UNITS.MS,
	UNITS.S,
	5 * UNITS.S,
	15 * UNITS.S,
	30 * UNITS.S,
	UNITS.MIN,
	5 * UNITS.MIN,
	15 * UNITS.MIN,
	30 * UNITS.MIN,
	UNITS.H,
	3 * UNITS.H,
	6 * UNITS.H,
	12 * UNITS.H,
	UNITS.D,
	7 * UNITS.D,
	UNITS.MO,
	3 * UNITS.MO,
	6 * UNITS.MO,
	UNITS.Y,
	2 * UNITS.Y,
	5 * UNITS.Y,
	10 * UNITS.Y
];

export function tickStep(rangeUs: number, max: number): number {
	const want = rangeUs / Math.max(1, max);
	for (const step of LADDER) if (step >= want) return step;
	return LADDER[LADDER.length - 1]!;
}

function monthStart(y: number, mo: number): number {
	return Date.UTC(y, mo, 1) * UNITS.MS;
}

export function ticks(v0: number, v1: number, max = 8): Tick[] {
	const range = v1 - v0;
	if (!(range > 0)) return [];
	const step = tickStep(range, max);
	const out: Tick[] = [];
	if (step >= UNITS.MO) {
		// calendar-aligned months / years
		const months = step >= UNITS.Y ? Math.round(step / UNITS.Y) * 12 : Math.round(step / UNITS.MO);
		const start = new Date(Math.floor(v0 / UNITS.MS));
		let y = start.getUTCFullYear();
		let mo = Math.floor(start.getUTCMonth() / months) * months;
		let us = monthStart(y, mo);
		while (us <= v1) {
			if (us >= v0) out.push({ us, label: formatTick(us, step), major: mo === 0 });
			mo += months;
			while (mo >= 12) {
				mo -= 12;
				y += 1;
			}
			us = monthStart(y, mo);
		}
		return out;
	}
	const first = Math.ceil(v0 / step) * step;
	for (let us = first; us <= v1; us += step) {
		const major =
			step >= UNITS.D
				? new Date(us / UNITS.MS).getUTCDate() === 1
				: Math.round(us / step) % 5 === 0;
		out.push({ us, label: formatTick(us, step), major });
	}
	return out;
}

/** Position of `us` inside [v0, v1] as a percentage (may be outside 0..100). */
export function pct(us: number, v0: number, v1: number): number {
	return ((us - v0) / (v1 - v0)) * 100;
}

export function clamp(n: number, lo: number, hi: number): number {
	return Math.min(hi, Math.max(lo, n));
}
