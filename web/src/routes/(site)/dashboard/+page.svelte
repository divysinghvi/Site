<script lang="ts">
	// The metrics dashboard (brief §3.2): panels from /api/content/panels on
	// the 24-column grid, every value from /api/v1/query(_range) with the exact
	// PromQL of content/panels.yaml. Time range + layout overrides + refresh
	// live in the URL hash (shareable); #panel=<id> scrolls to and flashes a
	// panel (alerts' runbook_url form).
	import { onMount, tick } from 'svelte';
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';
	import { replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import type { Panel, Readyz } from '$lib/api/types.gen';
	import { media } from '$lib/state/media.svelte';
	import {
		humanStep,
		presetToRelative,
		resolveRange,
		type Preset,
		type ResolvedRange
	} from '$lib/timerange';
	import { diffLayout, formatDashboardHash, parseDashboardHash } from '$lib/hash/layout';
	import { nudge, place, type GridItem } from '$lib/panels/grid';
	import { fetchReadyz, lastUpdated, runPanel, type PanelRun } from '$lib/panels/model';
	import DashboardGrid, { type CellApi } from '$lib/panels/DashboardGrid.svelte';
	import PanelFrame from '$lib/panels/Panel.svelte';
	import type { MenuItem } from '$lib/panels/PanelMenu.svelte';
	import TimeseriesPanel from '$lib/panels/TimeseriesPanel.svelte';
	import StatPanel from '$lib/panels/StatPanel.svelte';
	import GaugePanel from '$lib/panels/GaugePanel.svelte';
	import BarGaugePanel from '$lib/panels/BarGaugePanel.svelte';
	import TimeRangePicker from '$lib/panels/TimeRangePicker.svelte';
	import QueryInspector, { type InspectorEntry } from '$lib/panels/QueryInspector.svelte';

	let { data } = $props();

	// The panel file is a build-time snapshot for this page (no navigation re-runs it).
	// svelte-ignore state_referenced_locally
	const file = data.panels;
	const panels: Panel[] = file.panels;
	const panelsById = new Map(panels.map((p) => [p.id, p]));
	const basePos = Object.fromEntries(panels.map((p) => [p.id, p.gridPos]));
	const options = file.dashboard.time.options;
	const REFRESH_MS = 30_000;

	let preset = $state<Preset>(file.dashboard.time.default);
	let items = $state<GridItem[]>(panels.map((p) => ({ id: p.id, ...p.gridPos })));
	let autoRefresh = $state(true);
	let hashRest = $state<Record<string, string>>({});
	let panelParam = $state<string | undefined>(undefined);
	let flashId = $state<string | null>(null);
	let readyz = $state<Readyz | null>(null);
	// initial value only; refreshAll() recomputes it on every query round
	// svelte-ignore state_referenced_locally
	let range = $state<ResolvedRange>(resolveRange(preset, { allFrom: data.allFrom }));
	let lastRefresh = $state<number | null>(null);
	let inspectorPanel = $state<Panel | null>(null);
	let inspectorOpen = $state(false);
	let hashReady = false;
	let controller: AbortController | null = null;

	const runs = new SvelteMap<string, PanelRun>();
	const loading = new SvelteSet<string>();
	const legendVisible = new SvelteMap<string, boolean>();

	let layoutChanged = $derived(Object.keys(diffLayout(basePos, byId(items))).length > 0);
	let origin = $derived(
		data.siteOrigin || (typeof location !== 'undefined' ? location.origin : '')
	);
	let narrow = $derived(media.hydrated && media.narrow);

	function byId(list: GridItem[]) {
		return Object.fromEntries(list.map(({ id, x, y, w, h }) => [id, { x, y, w, h }]));
	}

	// ---- URL hash ----
	function readHash() {
		const h = parseDashboardHash(window.location.hash);
		if (h.range && options.includes(h.range)) preset = h.range;
		lastPreset = preset; // the caller re-queries; the picker effect must not double up
		autoRefresh = h.refresh !== 'off';
		hashRest = h.rest;
		panelParam = h.panel;
		const overrides = h.layout;
		items = panels.map((p) => {
			const o = overrides[p.id];
			return { id: p.id, ...(o ?? p.gridPos) };
		});
		// a shared layout may overlap after content edits: resolve once
		items = place(items, items[0]!.id, items[0]!);
	}

	let writeTimer: ReturnType<typeof setTimeout> | undefined;
	/** Hash writes are deferred a tick (coalesced; the router is initialised by then). */
	function writeHash() {
		if (!hashReady) return;
		clearTimeout(writeTimer);
		writeTimer = setTimeout(writeHashNow, 0);
	}

	function writeHashNow() {
		const hash = formatDashboardHash({
			range: preset === file.dashboard.time.default ? undefined : preset,
			layout: diffLayout(basePos, byId(items)),
			refresh: autoRefresh ? undefined : 'off',
			panel: panelParam,
			rest: hashRest
		});
		const url = window.location.pathname + window.location.search + hash;
		if (url === window.location.pathname + window.location.search + window.location.hash) return;
		try {
			replaceState(url, page.state);
		} catch {
			history.replaceState(history.state, '', url);
		}
	}

	async function flashPanel(id: string | undefined) {
		if (!id || !panelsById.has(id)) return;
		await tick();
		const el = document.querySelector<HTMLElement>(`[data-panel-id="${CSS.escape(id)}"]`);
		if (!el) return;
		el.scrollIntoView({ block: 'center' });
		el.focus({ preventScroll: true });
		flashId = null;
		await tick();
		flashId = id;
		setTimeout(() => (flashId = null), 1800);
	}

	// ---- queries ----
	async function refreshAll() {
		controller?.abort();
		const ctl = new AbortController();
		controller = ctl;
		range = resolveRange(preset, { allFrom: data.allFrom });
		const current = range;
		const rz = fetchReadyz(true).then((v) => {
			if (!ctl.signal.aborted) readyz = v;
		});
		await Promise.all(
			panels.map(async (p) => {
				loading.add(p.id);
				try {
					const run = await runPanel(p, current, ctl.signal);
					if (!ctl.signal.aborted) runs.set(p.id, run);
				} catch {
					// only aborts throw; the run is discarded
				} finally {
					loading.delete(p.id);
				}
			})
		);
		await rz;
		if (!ctl.signal.aborted) lastRefresh = Date.now();
	}

	onMount(() => {
		readHash();
		hashReady = true;
		void refreshAll().then(() => flashPanel(panelParam));
		const onHash = () => {
			readHash();
			void refreshAll().then(() => flashPanel(panelParam));
		};
		window.addEventListener('hashchange', onHash);
		const onVis = () => {
			if (!document.hidden && autoRefresh) void refreshAll();
		};
		document.addEventListener('visibilitychange', onVis);
		return () => {
			window.removeEventListener('hashchange', onHash);
			document.removeEventListener('visibilitychange', onVis);
			controller?.abort();
		};
	});

	// range change (picker) → re-query + hash
	// svelte-ignore state_referenced_locally
	let lastPreset = preset;
	$effect(() => {
		const p = preset;
		if (!hashReady || p === lastPreset) return;
		lastPreset = p;
		writeHash();
		void refreshAll();
	});

	$effect(() => {
		void autoRefresh;
		if (hashReady) writeHash();
	});

	$effect(() => {
		if (!autoRefresh) return;
		const t = setInterval(() => {
			if (!document.hidden) void refreshAll();
		}, REFRESH_MS);
		return () => clearInterval(t);
	});

	// ---- layout ----
	function onLayoutChange(next: GridItem[]) {
		items = next;
		writeHash();
	}

	function resetLayout() {
		items = panels.map((p) => ({ id: p.id, ...p.gridPos }));
		writeHash();
	}

	function move(id: string, dx: number, dy: number) {
		items = nudge(items, id, dx, dy);
		writeHash();
	}

	function resize(id: string, dw: number, dh: number) {
		items = nudge(items, id, 0, 0, dw, dh);
		writeHash();
	}

	function resetSize(p: Panel) {
		const it = items.find((i) => i.id === p.id);
		if (!it) return;
		items = place(items, p.id, { x: it.x, y: it.y, w: p.gridPos.w, h: p.gridPos.h });
		writeHash();
	}

	// ---- per-panel affordances ----
	function exploreHref(p: Panel): string {
		const t = p.targets.find((x) => !x.hide) ?? p.targets[0];
		const rel = presetToRelative(preset, data.allFrom);
		const q = new URLSearchParams({ ds: 'prom', expr: t.expr, from: rel.from, to: rel.to });
		if (t.instant) q.set('instant', '1');
		return `/explore?${q.toString()}`;
	}

	function menuFor(p: Panel): MenuItem[] {
		return [
			{ id: 'view', label: 'View query', action: () => openInspector(p) },
			{ id: 'explore', label: 'Explore ↗', href: exploreHref(p) },
			{
				id: 'legend',
				label: (legendVisible.get(p.id) ?? true) ? 'Hide legend' : 'Show legend',
				hidden: p.type !== 'timeseries',
				action: () => legendVisible.set(p.id, !(legendVisible.get(p.id) ?? true))
			},
			{ id: 'reset', label: 'Reset size', action: () => resetSize(p), separator: true },
			{
				id: 'up',
				label: 'Move up',
				hidden: narrow,
				action: () => move(p.id, 0, -1),
				separator: true
			},
			{ id: 'down', label: 'Move down', hidden: narrow, action: () => move(p.id, 0, 1) },
			{ id: 'left', label: 'Move left', hidden: narrow, action: () => move(p.id, -1, 0) },
			{ id: 'right', label: 'Move right', hidden: narrow, action: () => move(p.id, 1, 0) },
			{
				id: 'wider',
				label: 'Wider',
				hidden: narrow,
				action: () => resize(p.id, 1, 0),
				separator: true
			},
			{ id: 'narrower', label: 'Narrower', hidden: narrow, action: () => resize(p.id, -1, 0) },
			{ id: 'taller', label: 'Taller', hidden: narrow, action: () => resize(p.id, 0, 1) },
			{ id: 'shorter', label: 'Shorter', hidden: narrow, action: () => resize(p.id, 0, -1) }
		];
	}

	function openInspector(p: Panel) {
		inspectorPanel = p;
		inspectorOpen = true;
	}

	let inspectorEntries = $derived.by((): InspectorEntry[] => {
		const p = inspectorPanel;
		if (!p) return [];
		return p.targets.map((t) => ({
			refId: t.refId,
			expr: t.expr,
			kind: t.instant ? 'instant' : 'range',
			hidden: !!t.hide
		}));
	});

	let pointCount = $derived(Math.floor((range.to - range.from) / range.step) + 1);
	let refreshedAt = $derived(
		lastRefresh ? new Date(lastRefresh).toISOString().slice(11, 19) + 'Z' : '—'
	);
</script>

<svelte:head>
	<title>Metrics · {file.dashboard.title}</title>
	<meta
		name="description"
		content="Grafana-style metrics dashboard fed by a Prometheus-compatible API: GitHub contributions, merged PRs, PyPI downloads, uptime — every number is a query result."
	/>
</svelte:head>

<div class="dash">
	<header class="toolbar">
		<div class="titles">
			<h1 class="h1">{file.dashboard.title}</h1>
			<p class="sub mono">
				{panels.length} panels · step {humanStep(range.step)} · {pointCount} points/series · refreshed
				{refreshedAt}
			</p>
		</div>
		<div class="controls">
			<TimeRangePicker bind:value={preset} {options} />
			<button
				type="button"
				class="btn"
				aria-pressed={autoRefresh}
				title="Re-query every 30 s while the tab is visible"
				onclick={() => (autoRefresh = !autoRefresh)}
			>
				<span class="dot" class:on={autoRefresh} aria-hidden="true"></span>
				Auto-refresh 30s
			</button>
			<button type="button" class="btn" onclick={() => void refreshAll()}>Refresh</button>
			<button type="button" class="btn" disabled={!layoutChanged} onclick={resetLayout}>
				Reset layout
			</button>
			<a class="btn" href="/explore?ds=prom">Explore ↗</a>
		</div>
	</header>

	<p class="hint mono">
		Drag a panel by its grip, resize from the corner; the layout and range live in the URL hash —
		copy the link to share this view.
	</p>

	<DashboardGrid {items} panels={panelsById} {narrow} onchange={onLayoutChange} {cell} />
</div>

{#snippet cell(p: Panel, api: CellApi)}
	<PanelFrame
		panel={p}
		loading={loading.has(p.id)}
		updated={runs.has(p.id) ? lastUpdated(p, runs.get(p.id)!) : undefined}
		flash={flashId === p.id}
		draggable={!narrow}
		menuItems={menuFor(p)}
		onDragStart={api.dragStart}
		onResizeStart={api.resizeStart}
	>
		{#if p.type === 'timeseries'}
			<TimeseriesPanel
				panel={p}
				run={runs.get(p.id) ?? null}
				{readyz}
				legendVisible={legendVisible.get(p.id) ?? true}
			/>
		{:else if p.type === 'stat'}
			<StatPanel panel={p} run={runs.get(p.id) ?? null} {readyz} />
		{:else if p.type === 'gauge'}
			<GaugePanel panel={p} run={runs.get(p.id) ?? null} {readyz} />
		{:else}
			<BarGaugePanel panel={p} run={runs.get(p.id) ?? null} {readyz} />
		{/if}
	</PanelFrame>
{/snippet}

{#if inspectorPanel}
	<QueryInspector
		bind:open={inspectorOpen}
		title={inspectorPanel.title}
		entries={inspectorEntries}
		{range}
		{origin}
		exploreHref={exploreHref(inspectorPanel)}
	/>
{/if}

<style>
	.dash {
		display: flex;
		flex-direction: column;
		gap: 0.6rem;
	}
	.toolbar {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		flex-wrap: wrap;
		gap: 0.5rem 1rem;
		padding-top: 0.25rem;
	}
	.h1 {
		margin: 0;
		font-size: 1.05rem;
		font-weight: 600;
	}
	.sub {
		margin: 0.15rem 0 0;
		font-size: 0.7rem;
		color: var(--fg-dim);
	}
	.controls {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.4rem;
	}
	.dot {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 50%;
		background: var(--fg-dim);
	}
	.dot.on {
		background: var(--green);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--green) 30%, transparent);
	}
	.hint {
		margin: 0;
		font-size: 0.7rem;
		color: var(--fg-dim);
	}
	@media (max-width: 639.98px) {
		.hint {
			display: none;
		}
		.controls {
			width: 100%;
		}
	}
</style>
