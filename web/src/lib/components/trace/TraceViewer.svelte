<script lang="ts">
	// The trace viewer used by / (career trace) and /trace/[id] (any trace).
	// Owns the viewport (zoom/brush), collapse state, the focused row (roving
	// tabindex, j/k), the selected span (drawer) and the #span= deep link.
	import { onMount, tick } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import type { JaegerTrace, PostmortemSummary } from '$lib/api/types.gen';
	import {
		ancestorsOf,
		buildTrace,
		snapshotNow,
		visibleNodes,
		type TraceNode
	} from '$lib/trace/model';
	import { bindKeys } from '$lib/keyboard';
	import { media } from '$lib/state/media.svelte';
	import { parseHash, formatHash, patchHash } from '$lib/hash';
	import { formatDuration, formatInt } from '$lib/format';
	import Waterfall from './Waterfall.svelte';
	import VerticalTimeline from './VerticalTimeline.svelte';
	import Minimap from './Minimap.svelte';
	import SpanDrawer from './SpanDrawer.svelte';

	let {
		trace,
		postmortems = []
	}: {
		trace: JaegerTrace;
		postmortems?: PostmortemSummary[];
	} = $props();

	// The viewer's clock: the payload's request time until hydration, then the browser's.
	let nowUs = $state<number | undefined>(undefined);
	let model = $derived(buildTrace(trace, nowUs ?? snapshotNow(trace)));
	let view = $state<{ v0: number; v1: number } | null>(null);
	let v0 = $derived(view ? Math.max(view.v0, model.startUs) : model.startUs);
	let v1 = $derived(view ? Math.min(view.v1, model.endUs) : model.endUs);
	let zoomed = $derived(view !== null && (v0 > model.startUs || v1 < model.endUs));
	const collapsed = new SvelteSet<string>();
	let visible = $derived(visibleNodes(model, collapsed));
	let focusedId = $state<string | null>(null);
	let selectedId = $state<string | null>(null);
	let selected = $derived(selectedId ? (model.byId.get(selectedId) ?? null) : null);
	let pmMap = $derived(new Map(postmortems.map((p) => [p.id, p])));
	let animate = $state(true);
	let root = $derived(model.roots[0]);
	let showWaterfall = $derived(!media.hydrated || !media.narrow);
	let showVertical = $derived(!media.hydrated || media.narrow);

	function rowEl(id: string): HTMLElement | null {
		return document.querySelector<HTMLElement>(`[data-row-id="${CSS.escape(id)}"]`);
	}

	function focusRow(id: string, scroll = true) {
		focusedId = id;
		void tick().then(() => {
			const el = rowEl(id);
			el?.focus({ preventScroll: !scroll });
			if (scroll) el?.scrollIntoView({ block: 'nearest' });
		});
	}

	function moveFocus(delta: number | 'first' | 'last') {
		if (visible.length === 0) return;
		const cur = Math.max(
			0,
			visible.findIndex((n) => n.id === (focusedId ?? selectedId))
		);
		let next: number;
		if (delta === 'first') next = 0;
		else if (delta === 'last') next = visible.length - 1;
		else next = Math.min(visible.length - 1, Math.max(0, cur + delta));
		const node = visible[next]!;
		focusRow(node.id);
		// browsing with the drawer open follows the focus
		if (selectedId) select(node, false);
	}

	function writeHash(spanKey: string | undefined) {
		if (typeof window === 'undefined') return;
		const st = patchHash(parseHash(window.location.hash), { span: spanKey });
		const url = window.location.pathname + window.location.search + formatHash(st);
		try {
			replaceState(url, page.state);
		} catch {
			history.replaceState(history.state, '', url);
		}
	}

	function select(node: TraceNode, focus = true) {
		selectedId = node.id;
		focusedId = node.id;
		writeHash(node.key);
		if (focus) void tick().then(() => rowEl(node.id)?.scrollIntoView({ block: 'nearest' }));
	}

	function close() {
		const id = selectedId;
		selectedId = null;
		writeHash(undefined);
		if (id) focusRow(id, false);
	}

	function toggle(node: TraceNode) {
		if (collapsed.has(node.id)) collapsed.delete(node.id);
		else collapsed.add(node.id);
	}

	function expandTo(node: TraceNode) {
		for (const a of ancestorsOf(model, node)) collapsed.delete(a.id);
	}

	function navigate(node: TraceNode) {
		expandTo(node);
		select(node);
	}

	function zoom(factor: number) {
		const c = (v0 + v1) / 2;
		const w = Math.max((v1 - v0) * factor, (model.endUs - model.startUs) / 1000);
		let lo = c - w / 2;
		let hi = c + w / 2;
		if (lo < model.startUs) {
			hi += model.startUs - lo;
			lo = model.startUs;
		}
		if (hi > model.endUs) {
			lo -= hi - model.endUs;
			hi = model.endUs;
		}
		lo = Math.max(lo, model.startUs);
		view = { v0: lo, v1: hi };
	}

	function resetZoom() {
		view = null;
	}

	// #span=<id> (or the API's #trace?span=<id>) opens that span; applied on
	// mount, on hash changes and on router-driven URL updates.
	let mounted = $state(false);
	function applyHash(hash: string) {
		const h = parseHash(hash);
		if (!h.span) return;
		const n = model.byKey.get(h.span) ?? model.byId.get(h.span);
		if (n && selectedId !== n.id) {
			expandTo(n);
			select(n);
		}
	}
	$effect(() => {
		const hash = page.url.hash;
		if (mounted) applyHash(hash);
	});

	onMount(() => {
		nowUs = Date.now() * 1000;
		const clock = setInterval(() => (nowUs = Date.now() * 1000), 60_000);
		applyHash(window.location.hash);
		mounted = true;
		const onhash = () => applyHash(window.location.hash);
		window.addEventListener('hashchange', onhash);
		const unbind = bindKeys('trace', {
			j: () => moveFocus(1),
			k: () => moveFocus(-1),
			Escape: () => {
				if (!selectedId) return false;
				close();
			}
		});
		const anim = setTimeout(() => (animate = false), 1600);
		return () => {
			clearInterval(clock);
			window.removeEventListener('hashchange', onhash);
			unbind();
			clearTimeout(anim);
		};
	});
</script>

<div class="viewer panel" class:has-drawer={selected !== null && !media.narrow}>
	<div class="bar">
		<div class="stats mono">
			<span><b>{formatInt(model.nodes.length)}</b> spans</span>
			<span><b>{model.services.length}</b> services</span>
			{#if root}
				<span
					><b>{formatDuration(root.endUs - root.startUs)}</b>{root.open
						? ' and counting'
						: ''}</span
				>
			{/if}
			{#if model.nowUs !== undefined}
				<span class="dim" title="Open spans are measured up to this instant"
					>now = {new Date(model.nowUs / 1000).toISOString().slice(0, 16)}Z</span
				>
			{/if}
		</div>
		<div class="tools">
			<div class="legend" aria-label="Services">
				{#each model.services as s (s.name)}
					<span class="chip" style="--c: {s.color}"
						><span class="dot" aria-hidden="true"></span>{s.name}</span
					>
				{/each}
			</div>
			<div class="zoom" role="group" aria-label="Zoom">
				<button
					type="button"
					class="btn"
					onclick={() => zoom(0.5)}
					aria-label="Zoom in"
					title="Zoom in">+</button
				>
				<button
					type="button"
					class="btn"
					onclick={() => zoom(2)}
					aria-label="Zoom out"
					title="Zoom out"
					disabled={!zoomed}>−</button
				>
				<button type="button" class="btn" onclick={resetZoom} disabled={!zoomed}>Reset</button>
			</div>
			<p class="keys" aria-label="Keyboard shortcuts">
				<kbd>j</kbd><kbd>k</kbd> move · <kbd>Enter</kbd> open · <kbd>Esc</kbd> close ·
				<kbd>←</kbd><kbd>→</kbd> collapse
			</p>
		</div>
	</div>

	{#if showWaterfall}
		<div class="desktop" class:hidden-narrow={!media.hydrated}>
			<Minimap {model} {v0} {v1} onchange={(a, b) => (view = { v0: a, v1: b })} />
			<Waterfall
				{model}
				{visible}
				{v0}
				{v1}
				nowUs={model.nowUs}
				{focusedId}
				{selectedId}
				{collapsed}
				{animate}
				onmove={moveFocus}
				onfocus={(n) => (focusedId = n.id)}
				onselect={(n) => select(n)}
				ontoggle={toggle}
			/>
		</div>
	{/if}
	{#if showVertical}
		<div class="mobile" class:hidden-wide={!media.hydrated}>
			<VerticalTimeline nodes={model.nodes} {selectedId} onselect={(n) => select(n)} />
		</div>
	{/if}

	{#if selected}
		{#if media.narrow}
			<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
			<div class="backdrop fade-in" onclick={close}></div>
		{/if}
		<SpanDrawer
			node={selected}
			{model}
			postmortems={pmMap}
			narrow={media.narrow}
			onclose={close}
			onnavigate={navigate}
		/>
	{/if}
</div>

<style>
	.viewer {
		position: relative;
		overflow: hidden;
	}
	.viewer.has-drawer {
		padding-right: 0;
	}
	.bar {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem 1rem;
		padding: 0.5rem 0.75rem;
		border-bottom: 1px solid var(--border);
	}
	.stats {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 1rem;
		font-size: 0.75rem;
		color: var(--fg-muted);
	}
	.stats b {
		color: var(--fg);
		font-weight: 600;
	}
	.dim {
		color: var(--fg-dim);
	}
	.tools {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem 1rem;
	}
	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem;
	}
	.dot {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 2px;
		background: var(--c);
	}
	.zoom {
		display: flex;
		gap: 0.25rem;
	}
	.keys {
		margin: 0;
		font-size: 0.7rem;
		color: var(--fg-dim);
		display: flex;
		align-items: center;
		gap: 0.25rem;
	}
	.backdrop {
		position: fixed;
		inset: 0;
		z-index: 39;
		background: rgba(0, 0, 0, 0.45);
	}
	@media (max-width: 639.98px) {
		.hidden-narrow {
			display: none;
		}
		.keys {
			display: none;
		}
	}
	@media (min-width: 640px) {
		.hidden-wide {
			display: none;
		}
	}
</style>
