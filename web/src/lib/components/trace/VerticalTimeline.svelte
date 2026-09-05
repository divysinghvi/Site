<script lang="ts">
	// ≤ 640 px: the same DFS order as a vertical list, indented by depth, with
	// dates and durations as text. Each entry is a button opening the drawer.
	import type { TraceNode } from '$lib/trace/model';
	import { formatDateAt, formatDuration } from '$lib/format';

	let {
		nodes,
		selectedId,
		onselect
	}: {
		nodes: TraceNode[];
		selectedId: string | null;
		onselect: (node: TraceNode) => void;
	} = $props();

	function endLabel(n: TraceNode): string {
		return n.open && n.plannedEndUs === undefined
			? 'now'
			: formatDateAt(n.endRaw, n.endPrecision, n.endUs);
	}
</script>

<ol class="vt" aria-label="Spans, vertical timeline">
	{#each nodes as n (n.id)}
		<li class="item" style="--c: {n.color}; --d: {Math.min(n.depth, 6)}">
			<button
				type="button"
				class="entry"
				class:selected={selectedId === n.id}
				class:error={n.error}
				class:todo={n.todoDates}
				class:open={n.open}
				data-vt-id={n.id}
				aria-current={selectedId === n.id ? 'true' : undefined}
				onclick={() => onselect(n)}
			>
				<span class="rail" aria-hidden="true"></span>
				<span class="body">
					<span class="line1">
						<span class="op mono">{n.name}</span>
						{#if n.error}<span class="err" aria-label="error">!</span>{/if}
					</span>
					{#if n.title !== n.name}
						<span class="title">{n.title}</span>
					{/if}
					<span class="line2 mono">
						<span class="svc">{n.service}</span>
						<span>{formatDateAt(n.startRaw, n.startPrecision, n.startUs)} → {endLabel(n)}</span>
						<span class="dur">{formatDuration(n.endUs - n.startUs)}</span>
						{#if n.events.length}<span class="chip"
								>{n.events.length} event{n.events.length === 1 ? '' : 's'}</span
							>{/if}
						{#if n.todoDates}<span class="chip">date TODO</span>{/if}
					</span>
				</span>
			</button>
		</li>
	{/each}
</ol>

<style>
	.vt {
		list-style: none;
		margin: 0;
		padding: 0.25rem 0;
	}
	.item {
		padding-left: calc(0.5rem + var(--d) * 0.75rem);
	}
	.entry {
		display: flex;
		width: 100%;
		gap: 0.6rem;
		padding: 0.45rem 0.6rem 0.45rem 0;
		text-align: left;
		background: none;
		border: 0;
		border-bottom: 1px solid color-mix(in srgb, var(--border) 60%, transparent);
		color: var(--fg);
		cursor: pointer;
		min-height: 44px;
	}
	.entry.selected {
		background: var(--selection);
	}
	.rail {
		flex: none;
		width: 5px;
		border-radius: 3px;
		background: var(--c);
		align-self: stretch;
	}
	.entry.todo .rail {
		background: repeating-linear-gradient(180deg, var(--c) 0 4px, transparent 4px 8px);
	}
	.entry.open .rail {
		border-bottom: 6px dashed var(--c);
		background: linear-gradient(180deg, var(--c) 70%, transparent);
	}
	.entry.error .rail {
		box-shadow: 0 0 0 1px var(--red);
	}
	.body {
		display: flex;
		flex-direction: column;
		gap: 0.15rem;
		min-width: 0;
		flex: 1;
	}
	.line1 {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		font-size: 0.85rem;
	}
	.op {
		overflow-wrap: anywhere;
	}
	.title {
		font-size: 0.78rem;
		color: var(--fg-muted);
	}
	.line2 {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 0.6rem;
		font-size: 0.7rem;
		color: var(--fg-muted);
	}
	.svc {
		color: var(--c);
		filter: saturate(0.8);
	}
	.dur {
		color: var(--fg);
	}
	.err {
		flex: none;
		width: 1rem;
		height: 1rem;
		border-radius: 50%;
		background: var(--red);
		color: #fff;
		font-size: 0.7rem;
		font-weight: 700;
		line-height: 1rem;
		text-align: center;
	}
</style>
