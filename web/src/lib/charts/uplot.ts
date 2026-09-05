// uPlot glue: matrix → aligned data, stacking (cumulative sums + bands, the
// pattern from uPlot's own stacked-series demo), theme colours read from the
// CSS variables, and the options factory used by every timeseries panel and
// Explore. Type-only import of uPlot here; the class itself is loaded lazily
// by the chart component so the library never runs during prerender.
import type uPlot from 'uplot';
import type { PaletteColor } from '$lib/api/types.gen';
import { legendName, sampleValue, type MatrixSeries } from '$lib/panels/prom';
import { formatAxis, formatValue, seriesColor, type Unit } from '$lib/panels/units';

export interface ChartSeries {
	name: string;
	color: PaletteColor;
	metric: Record<string, string>;
	values: (number | null)[];
}

export interface ChartData {
	/** Unix seconds, ascending. */
	xs: number[];
	series: ChartSeries[];
}

/** Aligns a matrix on the union of its timestamps; gaps are null (uPlot draws a break). */
export function matrixToChart(
	matrix: readonly MatrixSeries[],
	legendFormat: string | undefined,
	colorOffset = 0
): ChartData {
	const xset = new Set<number>();
	for (const s of matrix) for (const [t] of s.values) xset.add(t);
	const xs = [...xset].sort((a, b) => a - b);
	const index = new Map<number, number>();
	xs.forEach((t, i) => index.set(t, i));
	const series = matrix.map((s, i) => {
		const values: (number | null)[] = new Array<number | null>(xs.length).fill(null);
		for (const [t, v] of s.values) {
			const n = sampleValue(v);
			values[index.get(t)!] = Number.isFinite(n) ? n : null;
		}
		return {
			name: legendName(legendFormat, s.metric),
			color: seriesColor(i + colorOffset),
			metric: s.metric,
			values
		};
	});
	return { xs, series };
}

/** Cumulative sums bottom → top over the shown series; hidden ones contribute nothing. */
export function stack(
	data: ChartData,
	shown: readonly boolean[]
): { data: ChartData; bands: uPlot.Band[] } {
	const n = data.xs.length;
	const accum = new Array<number>(n).fill(0);
	const series = data.series.map((s, i) => {
		if (!shown[i]) return { ...s, values: s.values.map(() => null) };
		return {
			...s,
			values: s.values.map((v, k) => {
				const n = (accum[k] ?? 0) + (v ?? 0);
				accum[k] = n;
				return n;
			})
		};
	});
	const bands: uPlot.Band[] = [];
	for (let i = 0; i < series.length; i++) {
		if (!shown[i]) continue;
		let upper = -1;
		for (let j = i + 1; j < series.length; j++) {
			if (shown[j]) {
				upper = j;
				break;
			}
		}
		// uPlot series indices are 1-based (0 is x); band = [upper edge, lower edge]
		if (upper > -1) bands.push({ series: [upper + 1, i + 1] });
	}
	return { data: { xs: data.xs, series }, bands };
}

export function toAligned(data: ChartData): uPlot.AlignedData {
	return [data.xs, ...data.series.map((s) => s.values)] as uPlot.AlignedData;
}

export interface ThemeColors {
	fg: string;
	muted: string;
	grid: string;
	axis: string;
	panel: string;
	palette: Record<PaletteColor, string>;
}

const PALETTE: PaletteColor[] = ['green', 'yellow', 'red', 'blue', 'orange', 'purple'];

/** Reads the active theme's tokens (canvas needs real colour strings, not var()). */
export function readTheme(el: Element = document.documentElement): ThemeColors {
	const cs = getComputedStyle(el);
	const get = (name: string, fallback: string) => cs.getPropertyValue(name).trim() || fallback;
	const palette = {} as Record<PaletteColor, string>;
	for (const p of PALETTE) palette[p] = get(`--${p}`, '#73bf69');
	const border = get('--border', '#2c3235');
	return {
		fg: get('--fg', '#d8d9da'),
		muted: get('--fg-muted', '#a1a8b0'),
		grid: withAlpha(border, 0.6),
		axis: get('--fg-dim', '#7b838d'),
		panel: get('--panel', '#181b1f'),
		palette
	};
}

/** #rrggbb → rgba(); other syntaxes pass through (canvas accepts them). */
export function withAlpha(color: string, alpha: number): string {
	const m = /^#([0-9a-f]{6})$/i.exec(color.trim());
	if (!m) return color;
	const n = parseInt(m[1]!, 16);
	return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`;
}

export interface OptionsInput {
	width: number;
	height: number;
	unit: Unit | undefined;
	decimals?: number;
	stacked: boolean;
	syncKey?: string;
	theme: ThemeColors;
	series: readonly ChartSeries[];
	shown: readonly boolean[];
	bands?: uPlot.Band[];
	/** Horizontal threshold lines (value → colour). */
	thresholds?: { value: number; color: string }[];
	/** Called with the hovered index (null when the cursor leaves). */
	onCursor?: (idx: number | null, u: uPlot) => void;
	/** Show point markers (few samples). */
	points?: boolean;
	/** Draw hook after the series (used by the draw-in animation and threshold lines). */
	onDraw?: (u: uPlot) => void;
	/** Fixed x extent (the queried [from, to]) so sparse results keep the real range. */
	xRange?: [number, number];
}

export function buildOptions(p: OptionsInput): uPlot.Options {
	const font = '11px "JetBrains Mono Variable", ui-monospace, Menlo, monospace';
	const series: uPlot.Series[] = [
		{},
		...p.series.map((s, i) => {
			const c = p.theme.palette[s.color];
			return {
				label: s.name,
				stroke: c,
				width: p.stacked ? 1.5 : 2,
				fill: p.stacked ? withAlpha(c, 0.45) : withAlpha(c, 0.08),
				show: p.shown[i] ?? true,
				spanGaps: false,
				points: { show: !!p.points, size: 6, width: 1.5, fill: p.theme.panel, stroke: c },
				value: (_u: uPlot, v: number | null) =>
					v == null ? '–' : formatValue(v, p.unit, p.decimals)
			} satisfies uPlot.Series;
		})
	];
	const hooks: uPlot.Hooks.Arrays = {};
	if (p.onCursor) {
		const cb = p.onCursor;
		hooks.setCursor = [(u) => cb(u.cursor.idx ?? null, u)];
	}
	if (p.onDraw || p.thresholds?.length) {
		hooks.draw = [
			(u) => {
				drawThresholds(u, p.thresholds ?? []);
				p.onDraw?.(u);
			}
		];
	}
	return {
		width: Math.max(10, Math.floor(p.width)),
		height: Math.max(10, Math.floor(p.height)),
		padding: [8, 12, 0, 4],
		legend: { show: false },
		cursor: {
			x: true,
			y: false,
			points: { size: 7 },
			drag: { x: false, y: false, setScale: false },
			sync: p.syncKey ? { key: p.syncKey, setSeries: false, scales: ['x', null] } : undefined
		},
		scales: {
			x: p.xRange ? { time: true, range: p.xRange } : { time: true },
			y: { range: yRange }
		},
		axes: [
			{
				stroke: p.theme.axis,
				grid: { stroke: p.theme.grid, width: 1 },
				ticks: { stroke: p.theme.grid, width: 1, size: 4 },
				font,
				gap: 6,
				size: 26
			},
			{
				stroke: p.theme.axis,
				grid: { stroke: p.theme.grid, width: 1 },
				ticks: { show: false },
				font,
				gap: 6,
				size: 52,
				values: (_u: uPlot, vals: number[]) => vals.map((v) => formatAxis(v, p.unit))
			}
		],
		series,
		bands: p.bands ?? [],
		hooks
	};
}

/** y range: from 0 (counters/gauges) unless negative values exist; 10 % headroom. */
function yRange(_u: uPlot, min: number, max: number): [number, number] {
	if (!Number.isFinite(min) || !Number.isFinite(max)) return [0, 1];
	const lo = min < 0 ? min - Math.abs(min) * 0.1 : 0;
	let hi = max > 0 ? max * 1.1 : max === 0 ? 1 : 0;
	if (hi - lo < 1e-9) hi = lo + 1;
	return [lo, hi];
}

function drawThresholds(u: uPlot, thresholds: { value: number; color: string }[]) {
	if (thresholds.length === 0) return;
	const ctx = u.ctx;
	const { left, width } = u.bbox;
	ctx.save();
	ctx.setLineDash([6, 4]);
	ctx.lineWidth = 1;
	for (const t of thresholds) {
		const y = u.valToPos(t.value, 'y', true);
		if (!Number.isFinite(y)) continue;
		ctx.strokeStyle = t.color;
		ctx.beginPath();
		ctx.moveTo(left, y);
		ctx.lineTo(left + width, y);
		ctx.stroke();
	}
	ctx.restore();
}

/** Formats an x value for tooltips (local time with the date when the range spans days). */
export function formatX(ts: number, spanS: number): string {
	const d = new Date(ts * 1000);
	const pad = (n: number) => String(n).padStart(2, '0');
	const date = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
	const time = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
	return spanS > 2 * 86400 ? `${date} ${time}` : `${date} ${time}:${pad(d.getSeconds())}`;
}
