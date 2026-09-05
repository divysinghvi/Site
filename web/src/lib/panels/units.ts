// Grafana unit ids used by content/panels.yaml (`short`, `none`, `percent`,
// `s`, `dtdurations`), thresholds → palette colors, and value formatting for
// stat/gauge/bar-gauge panels, axes and tooltips.
import type { PaletteColor, Thresholds } from '$lib/api/types.gen';

export type Unit = 'short' | 'none' | 'percent' | 's' | 'dtdurations' | string;

function fixed(n: number, decimals: number | undefined, fallback: number): string {
	const d = decimals ?? fallback;
	const s = n.toFixed(d);
	// trim trailing fraction zeros only when decimals were not pinned by the panel
	return decimals === undefined && s.includes('.') ? s.replace(/\.?0+$/, '') : s;
}

/** Grafana `short`: 1.5K · 2.3M · 1.1B, with the panel's decimals. */
export function formatShort(n: number, decimals?: number): string {
	const abs = Math.abs(n);
	if (abs >= 1e12) return fixed(n / 1e12, decimals, 1) + 'T';
	if (abs >= 1e9) return fixed(n / 1e9, decimals, 1) + 'B';
	if (abs >= 1e6) return fixed(n / 1e6, decimals, 1) + 'M';
	if (abs >= 1e3) return fixed(n / 1e3, decimals, 1) + 'K';
	return fixed(n, decimals, abs >= 100 ? 0 : abs >= 10 ? 1 : 2);
}

/** Seconds as a Grafana `dtdurations`-style string: 3d 4h 5m 6s (largest two parts). */
export function formatDurationS(seconds: number): string {
	if (!Number.isFinite(seconds)) return String(seconds);
	let s = Math.max(0, Math.round(seconds));
	const parts: string[] = [];
	const units: [string, number][] = [
		['y', 365 * 86400],
		['d', 86400],
		['h', 3600],
		['m', 60],
		['s', 1]
	];
	for (const [u, size] of units) {
		if (s >= size || (u === 's' && parts.length === 0)) {
			parts.push(`${Math.floor(s / size)}${u}`);
			s %= size;
		}
		if (parts.length === 2) break;
	}
	return parts.join(' ');
}

/** `s` unit: sub-second and human seconds (1.25 s · 3.4 min · 2.1 h). */
export function formatSeconds(seconds: number, decimals?: number): string {
	const abs = Math.abs(seconds);
	if (abs < 1e-3) return fixed(seconds * 1e6, decimals, 1) + ' µs';
	if (abs < 1) return fixed(seconds * 1e3, decimals, 1) + ' ms';
	if (abs < 60) return fixed(seconds, decimals, 2) + ' s';
	if (abs < 3600) return fixed(seconds / 60, decimals, 1) + ' min';
	if (abs < 86400) return fixed(seconds / 3600, decimals, 1) + ' h';
	return fixed(seconds / 86400, decimals, 1) + ' d';
}

/** Formats a value in the panel's unit. Non-finite values are spelled out, never hidden. */
export function formatValue(n: number, unit: Unit | undefined, decimals?: number): string {
	if (Number.isNaN(n)) return 'NaN';
	if (n === Infinity) return '+Inf';
	if (n === -Infinity) return '-Inf';
	switch (unit) {
		case 'percent':
			return fixed(n, decimals, 1) + '%';
		case 's':
			return formatSeconds(n, decimals);
		case 'dtdurations':
			return formatDurationS(n);
		case 'short':
			return formatShort(n, decimals);
		case 'none':
		case undefined:
		case '':
			return fixed(n, decimals, Number.isInteger(n) ? 0 : 2);
		default:
			return fixed(n, decimals, Number.isInteger(n) ? 0 : 2) + ' ' + unit;
	}
}

/** Axis tick formatter: compact, no unit suffix noise. */
export function formatAxis(n: number, unit: Unit | undefined): string {
	if (unit === 'dtdurations' || unit === 's') return formatValue(n, unit);
	if (unit === 'percent') return formatShort(n) + '%';
	return formatShort(n);
}

/** Resolves the threshold step that applies to `value` (Grafana: highest step ≤ value). */
export function thresholdColor(
	value: number,
	thresholds: Thresholds | undefined,
	min = 0,
	max = 100
): PaletteColor | undefined {
	if (!thresholds) return undefined;
	let color: PaletteColor | undefined;
	for (const step of thresholds.steps) {
		let v = step.value;
		if (v === null) {
			color = step.color;
			continue;
		}
		if (thresholds.mode === 'percentage') v = min + ((max - min) * v) / 100;
		if (value >= v) color = step.color;
	}
	return color;
}

/** CSS variable for a palette token (`--green`), resolved by the active theme. */
export function paletteVar(
	color: PaletteColor | undefined,
	fallback: PaletteColor = 'green'
): string {
	return `var(--${color ?? fallback})`;
}

/** Fixed categorical order for series (brief §5 palette; identity, never rank). */
export const SERIES_COLORS: readonly PaletteColor[] = [
	'green',
	'blue',
	'orange',
	'purple',
	'yellow',
	'red'
];

export function seriesColor(i: number): PaletteColor {
	return SERIES_COLORS[i % SERIES_COLORS.length]!;
}
