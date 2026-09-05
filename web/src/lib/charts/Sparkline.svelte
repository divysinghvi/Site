<script lang="ts">
	// Inline SVG sparkline for stat panels (graph_mode: area). Draws in via a
	// stroke-dashoffset animation, disabled under prefers-reduced-motion.
	import type { PaletteColor } from '$lib/api/types.gen';

	let {
		points,
		color = 'green',
		height = 40
	}: { points: [number, number][]; color?: PaletteColor; height?: number } = $props();

	const W = 200;
	let geometry = $derived.by(() => {
		const pts = points.filter((p) => Number.isFinite(p[1]));
		if (pts.length < 2) return null;
		const x0 = pts[0]![0];
		const x1 = pts[pts.length - 1]![0];
		let lo = Math.min(...pts.map((p) => p[1]));
		let hi = Math.max(...pts.map((p) => p[1]));
		if (lo > 0) lo = 0;
		if (hi - lo < 1e-9) hi = lo + 1;
		const sx = (x: number) => (x1 === x0 ? 0 : ((x - x0) / (x1 - x0)) * W);
		const sy = (y: number) => height - 2 - ((y - lo) / (hi - lo)) * (height - 4);
		const line = pts
			.map((p, i) => `${i ? 'L' : 'M'}${sx(p[0]).toFixed(1)},${sy(p[1]).toFixed(1)}`)
			.join(' ');
		const area = `${line} L${W},${height} L0,${height} Z`;
		return { line, area };
	});
</script>

{#if geometry}
	<svg
		class="spark"
		viewBox="0 0 {W} {height}"
		preserveAspectRatio="none"
		aria-hidden="true"
		style:height="{height}px"
	>
		<path class="area" d={geometry.area} style:fill="var(--{color})" />
		<path class="line" d={geometry.line} style:stroke="var(--{color})" pathLength="1" />
	</svg>
{/if}

<style>
	.spark {
		display: block;
		width: 100%;
		overflow: visible;
	}
	.area {
		opacity: 0.16;
	}
	.line {
		fill: none;
		stroke-width: 1.5;
		vector-effect: non-scaling-stroke;
	}
	@media (prefers-reduced-motion: no-preference) {
		.line {
			stroke-dasharray: 1;
			stroke-dashoffset: 1;
			animation: spark-draw 700ms cubic-bezier(0.2, 0.7, 0.2, 1) forwards;
		}
		.area {
			animation: fade-in 700ms ease-out both;
		}
	}
	@keyframes spark-draw {
		to {
			stroke-dashoffset: 0;
		}
	}
</style>
