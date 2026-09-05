<script lang="ts">
	// Level filter chips (review coverage-12): aria-pressed toggles that rewrite
	// the `level` matcher of the query's first selector. With no matcher every
	// chip is pressed ("all"); pressing one then narrows to that level, further
	// presses add/remove levels, and unpressing the last one returns to all
	// (matcher removed). Counts come from the volume histogram when known.
	import { LEVELS, selectedLevels, withLevels, type Level } from './selector';
	import { levelVar } from './lines';

	let {
		query,
		counts = {},
		onchange
	}: {
		query: string;
		counts?: Record<string, number>;
		onchange: (next: string) => void;
	} = $props();

	let selected = $derived(selectedLevels(query));
	let isAll = $derived(selected === 'all');

	function pressed(l: Level): boolean {
		return selected === 'all' || selected.includes(l);
	}

	function toggle(l: Level) {
		let next: Level[];
		if (selected === 'all') next = [l];
		else if (selected.includes(l)) next = selected.filter((x) => x !== l);
		else next = [...selected, l];
		onchange(withLevels(query, next));
	}

	function all() {
		onchange(withLevels(query, []));
	}
</script>

<div class="chips" role="group" aria-label="Level filter">
	{#each LEVELS as l (l)}
		<button
			type="button"
			class="chip level"
			style="--lv: {levelVar(l)}"
			aria-pressed={pressed(l)}
			data-level={l}
			onclick={() => toggle(l)}
		>
			<span class="dot" aria-hidden="true"></span>
			<span>{l}</span>
			{#if counts[l] !== undefined}
				<span class="count mono">{counts[l]}</span>
			{/if}
		</button>
	{/each}
	<button type="button" class="chip all" aria-pressed={isAll} onclick={all} disabled={isAll}
		>all</button
	>
</div>

<style>
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
	}
	.chip {
		min-height: 1.9rem;
		padding: 0 0.6rem;
		cursor: pointer;
		font-size: 0.75rem;
		color: var(--fg-muted);
		background: var(--panel-2);
	}
	.chip:hover {
		background: var(--panel-3);
	}
	.chip[aria-pressed='true'] {
		color: var(--fg);
		border-color: color-mix(in srgb, var(--lv, var(--fg-dim)) 60%, var(--border));
		background: color-mix(in srgb, var(--lv, var(--fg-dim)) 14%, var(--panel-2));
	}
	.chip.all[aria-pressed='true'] {
		border-color: var(--border);
		background: var(--panel-3);
	}
	.chip[disabled] {
		cursor: default;
	}
	.dot {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 50%;
		background: var(--lv);
		opacity: 0.45;
	}
	.chip[aria-pressed='true'] .dot {
		opacity: 1;
	}
	.count {
		padding-left: 0.1rem;
		font-size: 0.68rem;
		color: var(--fg-dim);
	}
	@media (pointer: coarse) {
		.chip {
			min-height: 2.5rem;
		}
	}
</style>
