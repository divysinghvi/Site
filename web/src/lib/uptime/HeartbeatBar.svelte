<script lang="ts">
	// Uptime-Kuma-style heartbeat bar: one cell per UTC day, oldest on the
	// left. A day without probes is grey; a day is green only when every
	// probe succeeded; red when none did; orange in between.
	import { cellState, cellTitle, summarizeCells, type DayCell } from '$lib/uptime/model';

	let { cells, name }: { cells: DayCell[]; name: string } = $props();

	let sum = $derived(summarizeCells(cells));
	let label = $derived(
		`${cells.length}-day heartbeat for ${name}: ${sum.up} day${sum.up === 1 ? '' : 's'} fully up, ${sum.partial} partial, ${sum.down} down, ${sum.none} without probes`
	);
	let first = $derived(cells[0]?.key ?? '');
	let last = $derived(cells[cells.length - 1]?.key ?? '');
</script>

<div class="hb">
	<div class="bar" role="img" aria-label={label} data-cells={cells.length}>
		{#each cells as c (c.key)}
			<span class="cell cell-{cellState(c.bucket)}" title={cellTitle(c)} data-day={c.key}></span>
		{/each}
	</div>
	<div class="axis mono" aria-hidden="true">
		<span>{first}</span>
		<span>{last}</span>
	</div>
</div>

<style>
	.hb {
		display: flex;
		flex-direction: column;
		gap: 0.2rem;
		min-width: 0;
	}
	.bar {
		display: flex;
		gap: 2px;
		width: 100%;
		height: 26px;
	}
	.cell {
		flex: 1 1 0;
		min-width: 1px;
		border-radius: 2px;
		background: var(--panel-3);
	}
	.cell-up {
		background: var(--green);
	}
	.cell-partial {
		background: var(--orange);
	}
	.cell-down {
		background: var(--red);
	}
	.cell-none {
		background: var(--panel-3);
		opacity: 0.9;
	}
	.axis {
		display: flex;
		justify-content: space-between;
		font-size: 0.66rem;
		color: var(--fg-dim);
	}
	@media (max-width: 639.98px) {
		.bar {
			gap: 1px;
			height: 22px;
		}
		.cell {
			border-radius: 1px;
		}
	}
</style>
