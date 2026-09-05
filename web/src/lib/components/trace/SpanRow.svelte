<script module lang="ts">
	function countDescendants(n: { children: { children: unknown[] }[] }): number {
		let c = 0;
		const walk = (x: { children: unknown[] }) => {
			for (const ch of x.children as { children: unknown[] }[]) {
				c += 1;
				walk(ch);
			}
		};
		walk(n);
		return c;
	}
</script>

<script lang="ts">
	// One waterfall row: name cell (indented by depth, caret when it has
	// children) and the timeline cell with the bar, event markers, the open
	// dashed tail and the duration label. Positions are percentages of the
	// current viewport [v0, v1].
	import type { TraceNode } from '$lib/trace/model';
	import { formatDuration, formatDateAt } from '$lib/format';
	import { pct, clamp } from '$lib/trace/axis';

	let {
		node,
		position,
		size,
		v0,
		v1,
		nowUs,
		focused,
		selected,
		collapsed,
		animate,
		onfocus,
		onselect,
		ontoggle
	}: {
		node: TraceNode;
		position: number;
		size: number;
		v0: number;
		v1: number;
		nowUs: number | undefined;
		focused: boolean;
		selected: boolean;
		collapsed: boolean;
		animate: boolean;
		onfocus: () => void;
		onselect: () => void;
		ontoggle: () => void;
	} = $props();

	let hasChildren = $derived(node.children.length > 0);
	let left = $derived(pct(node.startUs, v0, v1));
	let right = $derived(pct(node.endUs, v0, v1));
	let l = $derived(clamp(left, 0, 100));
	let r = $derived(clamp(right, 0, 100));
	let width = $derived(Math.max(r - l, 0));
	let outside = $derived(right < 0 || left > 100);
	// open spans: solid up to now, dashed from now to the planned end
	let nowPct = $derived(nowUs !== undefined ? clamp(pct(nowUs, v0, v1), 0, 100) : undefined);
	let solidRight = $derived(
		node.open && node.plannedEndUs !== undefined && nowPct !== undefined
			? clamp(Math.min(r, Math.max(nowPct, l)), l, r)
			: r
	);
	let solidWidth = $derived(Math.max(solidRight - l, 0));
	// duration label: after the bar, before it when the bar ends near the right
	// edge, inside it when it also starts near the left edge
	let labelMode = $derived(r <= 82 ? 'after' : l >= 18 ? 'before' : 'inside');
	let duration = $derived(node.todoDates ? 'TODO' : formatDuration(node.endUs - node.startUs));
	let startLabel = $derived(formatDateAt(node.startRaw, node.startPrecision, node.startUs));
	let endLabel = $derived(
		node.open && node.plannedEndUs === undefined
			? 'now'
			: formatDateAt(node.endRaw, node.endPrecision, node.endUs)
	);
	let describe = $derived(
		`${node.name}, ${node.service}, ${startLabel} to ${endLabel}, ${duration}` +
			(node.open ? ', open' : '') +
			(node.error ? ', error' : '') +
			(node.todoDates ? ', date TODO' : '')
	);

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' || e.key === ' ') {
			e.preventDefault();
			onselect();
		} else if (e.key === 'ArrowRight' && hasChildren && collapsed) {
			e.preventDefault();
			ontoggle();
		} else if (e.key === 'ArrowLeft' && hasChildren && !collapsed) {
			e.preventDefault();
			ontoggle();
		}
	}
</script>

<div
	class="row"
	class:selected
	class:error={node.error}
	role="treeitem"
	aria-level={node.depth + 1}
	aria-posinset={position}
	aria-setsize={size}
	aria-selected={selected}
	aria-expanded={hasChildren ? !collapsed : undefined}
	aria-label={describe}
	tabindex={focused ? 0 : -1}
	data-row-id={node.id}
	data-span-key={node.key}
	style="--c: {node.color}"
	{onfocus}
	onclick={onselect}
	{onkeydown}
>
	<div class="name" style="padding-left: {0.5 + node.depth * 1}rem">
		{#if hasChildren}
			<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
			<span
				class="caret"
				class:collapsed
				title={collapsed ? 'Expand' : 'Collapse'}
				onclick={(e) => {
					e.stopPropagation();
					ontoggle();
				}}
				aria-hidden="true"
			></span>
		{:else}
			<span class="caret leaf" aria-hidden="true"></span>
		{/if}
		<span class="swatch" aria-hidden="true"></span>
		<span class="svc">{node.service}</span>
		<span class="op mono" title={node.title}>{node.name}</span>
		{#if node.error}
			<span class="err" title="error" aria-hidden="true">!</span>
		{/if}
		{#if hasChildren && collapsed}
			<span class="chip" aria-hidden="true">+{countDescendants(node)}</span>
		{/if}
	</div>
	<div class="tl">
		{#if nowPct !== undefined}
			<span class="now" style="left: {nowPct}%" aria-hidden="true"></span>
		{/if}
		{#if !outside}
			<span
				class="bar"
				class:bar-grow={animate}
				class:todo={node.todoDates}
				class:open={node.open}
				class:clipped-left={left < 0}
				class:clipped-right={right > 100}
				style="left: {l}%; width: {Math.max(width, 0.15)}%; animation-delay: {Math.min(
					node.index,
					40
				) * 14}ms"
				aria-hidden="true"
			>
				{#if node.open && solidWidth < width}
					<span class="solid" style="width: {(solidWidth / Math.max(width, 0.0001)) * 100}%"></span>
					<span class="planned" style="left: {(solidWidth / Math.max(width, 0.0001)) * 100}%"
					></span>
				{/if}
			</span>
			{#each node.events as ev (ev.us + ev.name)}
				{@const x = clamp(pct(ev.us, v0, v1), 0, 100)}
				<span
					class="ev"
					class:todo={ev.todo}
					style="left: {x}%"
					title="{ev.name}{ev.todo ? ' (date TODO)' : ''}"
					aria-hidden="true"
				></span>
			{/each}
			<span
				class="dur mono {labelMode}"
				style={labelMode === 'after'
					? `left: ${r}%`
					: labelMode === 'before'
						? `right: ${100 - l}%`
						: `right: ${100 - r}%`}
				aria-hidden="true">{duration}</span
			>
		{:else}
			<span class="dur mono off" aria-hidden="true">{left > 100 ? '→' : '←'} {duration}</span>
		{/if}
	</div>
</div>

<style>
	.row {
		display: grid;
		grid-template-columns: var(--name-col, minmax(220px, 32%)) 1fr;
		align-items: stretch;
		min-height: 28px;
		border-bottom: 1px solid color-mix(in srgb, var(--border) 60%, transparent);
		cursor: pointer;
		outline-offset: -2px;
	}
	.row:hover {
		background: color-mix(in srgb, var(--panel-2) 70%, transparent);
	}
	.row.selected {
		background: var(--selection);
	}
	.name {
		display: flex;
		align-items: center;
		gap: 0.35rem;
		min-width: 0;
		padding-right: 0.5rem;
		font-size: 0.78rem;
		border-right: 1px solid var(--border);
	}
	.caret {
		flex: none;
		width: 0.7rem;
		height: 0.7rem;
		display: inline-block;
		position: relative;
	}
	.caret:not(.leaf)::before {
		content: '';
		position: absolute;
		left: 0.15rem;
		top: 0.1rem;
		border-left: 0.32rem solid var(--fg-muted);
		border-top: 0.25rem solid transparent;
		border-bottom: 0.25rem solid transparent;
		transform: rotate(90deg);
		transition: transform 120ms;
	}
	.caret.collapsed::before {
		transform: rotate(0deg);
	}
	.swatch {
		flex: none;
		width: 0.55rem;
		height: 0.55rem;
		border-radius: 2px;
		background: var(--c);
	}
	.svc {
		flex: none;
		color: var(--fg-muted);
		font-size: 0.72rem;
	}
	.op {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
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
	.tl {
		position: relative;
		overflow: hidden;
		min-width: 0;
	}
	.now {
		position: absolute;
		top: 0;
		bottom: 0;
		border-left: 1px dashed color-mix(in srgb, var(--fg-dim) 70%, transparent);
		pointer-events: none;
	}
	.bar {
		position: absolute;
		top: 7px;
		height: 14px;
		border-radius: 2px;
		background: var(--c);
		box-shadow: 0 0 0 1px color-mix(in srgb, var(--c) 40%, transparent);
	}
	.row.error .bar {
		box-shadow:
			0 0 0 1px var(--red),
			0 0 0 3px color-mix(in srgb, var(--red) 35%, transparent);
	}
	.bar.todo {
		background: repeating-linear-gradient(
				135deg,
				color-mix(in srgb, var(--c) 85%, transparent) 0 3px,
				transparent 3px 7px
			)
			var(--hatch);
		border: 1px dashed var(--c);
		box-shadow: none;
	}
	.bar.open {
		border-right: 2px dashed var(--c);
		border-top-right-radius: 0;
		border-bottom-right-radius: 0;
	}
	.bar.open .solid {
		position: absolute;
		inset: 0 auto 0 0;
		background: var(--c);
	}
	.bar.open .planned {
		position: absolute;
		top: -1px;
		bottom: -1px;
		right: 0;
		background: transparent;
		border: 1px dashed var(--c);
		border-left: none;
	}
	.bar.open:has(.solid) {
		background: transparent;
		box-shadow: none;
	}
	.bar.clipped-left {
		border-top-left-radius: 0;
		border-bottom-left-radius: 0;
	}
	.ev {
		position: absolute;
		top: 9px;
		width: 10px;
		height: 10px;
		margin-left: -5px;
		transform: rotate(45deg);
		background: var(--fg);
		border: 1.5px solid var(--bg);
		border-radius: 1px;
		pointer-events: auto;
	}
	.ev.todo {
		background: transparent;
		border: 1.5px dashed var(--fg-muted);
	}
	.dur {
		position: absolute;
		top: 0;
		line-height: 28px;
		padding: 0 6px;
		font-size: 0.7rem;
		color: var(--fg-muted);
		white-space: nowrap;
	}
	.dur.inside {
		top: 7px;
		height: 14px;
		line-height: 14px;
		padding: 0 4px;
		border-radius: 2px;
		background: color-mix(in srgb, var(--panel) 85%, transparent);
		color: var(--fg);
	}
	.dur.off {
		left: 6px;
		color: var(--fg-dim);
	}
</style>
