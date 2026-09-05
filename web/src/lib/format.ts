// Formatting helpers. Trace times are microseconds since the epoch (Jaeger);
// the career trace spans years while a request self-trace spans milliseconds,
// so every formatter picks its unit from the magnitude.

const US = 1;
const MS = 1000 * US;
const S = 1000 * MS;
const MIN = 60 * S;
const H = 60 * MIN;
const D = 24 * H;
const MO = 30.436875 * D; // mean Gregorian month
const Y = 365.2425 * D; // mean Gregorian year

export const UNITS = { US, MS, S, MIN, H, D, MO, Y } as const;

function two(main: number, mainUnit: string, rest: number, restUnit: string): string {
	return rest > 0 ? `${main}${mainUnit} ${rest}${restUnit}` : `${main}${mainUnit}`;
}

/** Human duration from microseconds: 2y 3mo · 14d · 3h 20m · 1.2s · 8.01ms · 410µs. */
export function formatDuration(us: number): string {
	if (!Number.isFinite(us) || us < 0) return '–';
	if (us >= Y) {
		const y = Math.floor(us / Y);
		const mo = Math.floor((us - y * Y) / MO);
		return two(y, 'y', mo, 'mo');
	}
	if (us >= MO) {
		const mo = Math.floor(us / MO);
		const d = Math.floor((us - mo * MO) / D);
		return two(mo, 'mo', d, 'd');
	}
	if (us >= D) {
		const d = Math.floor(us / D);
		const h = Math.floor((us - d * D) / H);
		return two(d, 'd', h, 'h');
	}
	if (us >= H) {
		const h = Math.floor(us / H);
		const m = Math.floor((us - h * H) / MIN);
		return two(h, 'h', m, 'm');
	}
	if (us >= MIN) {
		const m = Math.floor(us / MIN);
		const s = Math.floor((us - m * MIN) / S);
		return two(m, 'm', s, 's');
	}
	if (us >= S) return `${(us / S).toFixed(us >= 10 * S ? 1 : 2)}s`;
	if (us >= MS) return `${(us / MS).toFixed(us >= 10 * MS ? 1 : 2)}ms`;
	return `${Math.round(us)}µs`;
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/** UTC calendar parts of a microsecond timestamp. */
export function utcParts(us: number) {
	const d = new Date(Math.floor(us / MS));
	return {
		y: d.getUTCFullYear(),
		mo: d.getUTCMonth(),
		d: d.getUTCDate(),
		h: d.getUTCHours(),
		mi: d.getUTCMinutes(),
		s: d.getUTCSeconds(),
		ms: d.getUTCMilliseconds()
	};
}

export type DatePrecision = 'year' | 'month' | 'day' | 'todo' | 'open' | 'exact';

/**
 * Renders a content date the way it was written (precision from the
 * divy.*_precision tags): "2023", "May 2024", "14 May 2024"; TODO markers are
 * shown verbatim (never a guessed date); exact timestamps as ISO-8601 with ms.
 */
export function formatDateAt(
	raw: string | undefined,
	precision: DatePrecision,
	us: number
): string {
	if (precision === 'todo') return raw && raw.startsWith('TODO(divy)') ? raw : 'TODO(divy)';
	if (precision === 'open') return 'now';
	if (precision === 'exact') return formatTimestamp(us);
	const p = utcParts(us);
	if (precision === 'year') return String(p.y);
	if (precision === 'month') return `${MONTHS[p.mo]} ${p.y}`;
	return `${p.d} ${MONTHS[p.mo]} ${p.y}`;
}

/** ISO-8601 UTC with milliseconds: 2026-09-05T09:28:15.123Z */
export function formatTimestamp(us: number): string {
	return new Date(Math.floor(us / MS)).toISOString();
}

/** Short absolute label for axis ticks, chosen by the tick step. */
export function formatTick(us: number, stepUs: number): string {
	const p = utcParts(us);
	if (stepUs >= Y) return String(p.y);
	if (stepUs >= MO) return p.mo === 0 ? String(p.y) : `${MONTHS[p.mo]} ${String(p.y).slice(2)}`;
	if (stepUs >= D) return `${p.d} ${MONTHS[p.mo]}`;
	if (stepUs >= MIN) return `${pad(p.h)}:${pad(p.mi)}`;
	if (stepUs >= S) return `${pad(p.h)}:${pad(p.mi)}:${pad(p.s)}`;
	return `${pad(p.s)}.${String(p.ms).padStart(3, '0')}s`;
}

function pad(n: number): string {
	return String(n).padStart(2, '0');
}

/** Relative offset from a trace start, for self-traces (Jaeger's left column). */
export function formatOffset(us: number): string {
	return us === 0 ? '0' : '+' + formatDuration(us);
}

export function formatInt(n: number): string {
	return new Intl.NumberFormat('en-US').format(n);
}
