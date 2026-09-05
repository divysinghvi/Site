<script lang="ts">
	// Log volume by level: stacked SVG bars from the matrix of
	// sum by (level) (count_over_time(<query>[<bucket>])). Pure SVG so it
	// prerenders and scales to 390 px; each bar carries a <title> with the
	// counts and the legend lists the totals.
	import type { Volume } from './lines';
	import { levelVar } from './lines';

	let {
		volume,
		from,
		to,
		height = 96
	}: { volume: Volume | null; from: number; to: number; height?: number } = $props();

	const W = 1000;
	const PAD = 2;

	let bars = $derived(volume?.bars ?? []);
	let max = $derived(bars.reduce((m, b) => Math.max(m, b.total), 0));
	let span = $derived(Math.max(1, to - from));
	let barW = $derived(volume ? Math.max(1.5, (W * volume.step) / span - PAD) : 0);

	function x(ts: number): number {
		return ((ts - from) / span) * W;
	}

	function h(n: number): number {
		return max > 0 ? (n / max) * (height - 4) : 0;
	}

	const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
	function tickLabel(ts: number): string {
		const d = new Date(ts * 1000);
		if (span > 400 * 86400) return `${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
		if (span > 3 * 86400) return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]}`;
		return `${String(d.getUTCHours()).padStart(2, '0')}:${String(d.getUTCMinutes()).padStart(2, '0')}`;
	}

	let ticks = $derived.by(() => {
		const n = 6;
		const out: { ts: number; label: string }[] = [];
		for (let i = 0; i <= n; i++) {
			const ts = from + (span * i) / n;
			out.push({ ts, label: tickLabel(ts) });
		}
		return out;
	});

	function barTitle(b: Volume['bars'][number]): string {
		const d = new Date(b.ts * 1000).toISOString().slice(0, 16).replace('T', ' ');
		const parts = Object.entries(b.counts).map(([l, n]) => `${l} ${n}`);
		return `${d}Z — ${b.total} line${b.total === 1 ? '' : 's'}${parts.length ? ` (${parts.join(', ')})` : ''}`;
	}

	let total = $derived(Object.values(volume?.totals ?? {}).reduce((a, b) => a + b, 0));
</script>

<figure class="hist" data-volume={volume ? total : undefined}>
	<div class="legend mono">
		<span class="dim">volume by level</span>
		{#if volume}
			{#each volume.levels as l (l)}
				<span class="lg"><span class="sw" style="background: {levelVar(l)}"></span>{l} {volume.totals[l] ?? 0}</span>
			{/each}
			<span class="dim">· bucket {volume.step >= 86400 ? `${volume.step / 86400}d` : `${volume.step / 3600}h`}</span>
		{:else}
			<span class="dim">no histogram (metric query or no result)</span>
		{/if}
	</div>
	<svg
		viewBox="0 0 {W} {height + 16}"
		preserveAspectRatio="none"
		class="svg"
		role="img"
		aria-label={volume
			? `Log volume histogram: ${total} lines in ${bars.filter((b) => b.total > 0).length} buckets`
			: 'Log volume histogram: no data'}
	>
		<line x1="0" y1={height} x2={W} y2={height} class="axis" />
		{#each bars as b (b.ts)}
			{#if b.total > 0}
				{@const x0 = x(b.ts - (volume?.step ?? 0)) + PAD / 2}
				<g>
					<title>{barTitle(b)}</title>
					{#each volume?.levels ?? [] as l, i (l)}
						{@const n = b.counts[l] ?? 0}
						{#if n > 0}
							{@const below = (volume?.levels ?? [])
								.slice(0, i)
								.reduce((a, k) => a + (b.counts[k] ?? 0), 0)}
							<rect
								x={Math.max(0, x0)}
								y={height - h(below + n)}
								width={barW}
								height={h(n)}
								fill={levelVar(l)}
							/>
						{/if}
					{/each}
				</g>
			{/if}
		{/each}
	</svg>
	<div class="ticks mono" aria-hidden="true">
		{#each ticks as t (t.ts)}
			<span>{t.label}</span>
		{/each}
	</div>
</figure>

<style>
	.hist {
		margin: 0;
		padding: 0.4rem 0.6rem 0.3rem;
	}
	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: 0.3rem 0.8rem;
		margin-bottom: 0.25rem;
		font-size: 0.7rem;
		color: var(--fg-muted);
	}
	.lg {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
	}
	.sw {
		width: 0.6rem;
		height: 0.6rem;
		border-radius: 2px;
	}
	.dim {
		color: var(--fg-dim);
	}
	.svg {
		display: block;
		width: 100%;
		height: 6.5rem;
	}
	.axis {
		stroke: var(--border);
		stroke-width: 1;
		vector-effect: non-scaling-stroke;
	}
	rect {
		vector-effect: non-scaling-stroke;
	}
	.ticks {
		display: flex;
		justify-content: space-between;
		margin-top: 0.15rem;
		font-size: 0.62rem;
		color: var(--fg-dim);
	}
	@media (max-width: 639.98px) {
		.ticks span:nth-child(even) {
			display: none;
		}
		.svg {
			height: 4.5rem;
		}
	}
</style>
