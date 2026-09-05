<script lang="ts">
	// Logs explorer (brief §3.3, Loki-style): LogQL query bar with
	// autocomplete, level chips rewriting the selector, time range (default
	// all), the volume-by-level histogram from a metric query, the newest-first
	// line list with expandable JSON and span links, a client-side live tail
	// that replays the result oldest → newest with a typewriter cadence, and the
	// copyable curl of the exact query_range call. The page is prerendered with
	// the default query's first 100 lines; /logs?q=…&from=…&to=…&limit=… is
	// read after hydration.
	import { onMount, tick } from 'svelte';
	import { replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { bindKeys } from '$lib/keyboard';
	import { motion } from '$lib/state/motion.svelte';
	import {
		ALL_PRESETS,
		chooseStep,
		humanStep,
		parseTimeParam,
		presetFromParams,
		presetToRelative,
		resolveRange,
		type Preset,
		type ResolvedRange
	} from '$lib/timerange';
	import {
		curlQueryRange,
		formatStreamLabels,
		isLogQuery,
		loki,
		lokiValue,
		type LokiMatrixSeries,
		type LokiVectorSample,
		type RangeParams
	} from '$lib/logql/loki';
	import {
		DEFAULT_LIMIT,
		fieldNames,
		rowsFromStreams,
		volumeBucket,
		volumeFromMatrix,
		volumeQuery,
		type LogRow,
		type Volume
	} from '$lib/logql/lines';
	import { DEFAULT_SELECTOR } from '$lib/logql/selector';
	import LogQueryBar from '$lib/logql/LogQueryBar.svelte';
	import LevelChips from '$lib/logql/LevelChips.svelte';
	import LogList from '$lib/logql/LogList.svelte';
	import VolumeHistogram from '$lib/logql/VolumeHistogram.svelte';
	import CurlBlock from '$lib/logql/CurlBlock.svelte';
	import TimeRangePicker from '$lib/panels/TimeRangePicker.svelte';
	import EmptyState from '$lib/panels/EmptyState.svelte';

	let { data } = $props();

	interface Result {
		query: string;
		kind: 'streams' | 'matrix' | 'vector';
		rows: LogRow[];
		volume: Volume | null;
		matrix: LokiMatrixSeries[];
		vector: LokiVectorSample[];
		range: ResolvedRange;
		limit: number;
		step?: number;
		/** Entries scanned by the API (stats.summary.totalLinesProcessed). */
		scanned: number;
		/** 'build' for the prerendered snapshot, else the fetch time (ms). */
		fetchedAt: number | 'build';
	}

	const LIMITS = [100, 500, 1000, 5000];

	function fromSnapshot(): Result {
		const s = data.snapshot;
		const range: ResolvedRange = { preset: 'all', from: s.from, to: s.to, step: s.bucket.step };
		return {
			query: s.query,
			kind: 'streams',
			rows: rowsFromStreams(s.streams),
			volume: s.volume ? volumeFromMatrix(s.volume, s.from, s.to, s.bucket.step) : null,
			matrix: [],
			vector: [],
			range,
			limit: s.limit,
			scanned: s.entriesTotal,
			fetchedAt: 'build'
		};
	}

	let query = $state(data.snapshot.query);
	let preset = $state<Preset>('all');
	let customFrom = $state<number | undefined>(undefined);
	let customTo = $state<number | undefined>(undefined);
	let limit = $state(DEFAULT_LIMIT);
	let running = $state(false);
	let error = $state<string | null>(null);
	let result = $state<Result>(fromSnapshot());
	let expanded = $state<string | null>(null);
	let bar = $state<LogQueryBar | null>(null);
	let hydrated = false;
	let controller: AbortController | null = null;

	// ---- live tail (client-side replay, docs/drafts/logql.md L.3.3) ----
	interface Tail {
		active: boolean;
		paused: boolean;
		done: boolean;
		rows: LogRow[];
		typed: Record<string, number>;
		total: number;
	}
	let tail = $state<Tail>({ active: false, paused: false, done: false, rows: [], typed: {}, total: 0 });
	let tailSource: LogRow[] = [];
	let tailIdx = 0;
	let tailTimer: ReturnType<typeof setTimeout> | undefined;
	let tailEl = $state<HTMLElement | null>(null);

	let origin = $derived(
		data.siteOrigin || (typeof location !== 'undefined' ? location.origin : '')
	);
	let fields = $derived(fieldNames(result.rows));
	let levelCounts = $derived(result.volume?.totals ?? {});
	let visibleRows = $derived(tail.active ? tail.rows : result.rows);

	function currentRange(): ResolvedRange {
		if (customFrom !== undefined && customTo !== undefined && customTo > customFrom) {
			return { preset, from: customFrom, to: customTo, step: chooseStep(customFrom, customTo) };
		}
		return resolveRange(preset, { allFrom: data.allFrom });
	}

	function rangeParams(q: string, range: ResolvedRange): RangeParams {
		return isLogQuery(q)
			? { start: range.from, end: range.to, limit, direction: 'backward' }
			: { start: range.from, end: range.to, step: chooseStep(range.from, range.to) };
	}

	let curl = $derived.by(() => {
		const q = query.trim() || DEFAULT_SELECTOR;
		const r = result.query === q ? result.range : currentRange();
		return curlQueryRange(origin, q, rangeParams(q, r));
	});

	function readParams() {
		const p = page.url.searchParams;
		const q = p.get('q');
		if (q) query = q;
		const from = p.get('from');
		const to = p.get('to');
		const pr = presetFromParams(from, to);
		if (pr) {
			preset = pr;
			customFrom = customTo = undefined;
		} else if (from) {
			const f = parseTimeParam(from);
			const t = parseTimeParam(to ?? 'now');
			if (f !== undefined && t !== undefined && t > f) {
				customFrom = f;
				customTo = t;
			}
		}
		const l = Number(p.get('limit'));
		if (Number.isInteger(l) && l > 0 && l <= 5000) limit = l;
	}

	let writeTimer: ReturnType<typeof setTimeout> | undefined;
	function writeParams() {
		if (!hydrated) return;
		clearTimeout(writeTimer);
		writeTimer = setTimeout(() => {
			const p = new URLSearchParams();
			const q = query.trim();
			if (q && q !== DEFAULT_SELECTOR) p.set('q', q);
			if (customFrom !== undefined && customTo !== undefined) {
				p.set('from', String(customFrom));
				p.set('to', String(customTo));
			} else if (preset !== 'all') {
				const rel = presetToRelative(preset, data.allFrom);
				p.set('from', rel.from);
				p.set('to', rel.to);
			}
			if (limit !== DEFAULT_LIMIT) p.set('limit', String(limit));
			const qs = p.toString();
			const url = `${page.url.pathname}${qs ? '?' + qs : ''}${page.url.hash}`;
			try {
				replaceState(url, page.state);
			} catch {
				history.replaceState(history.state, '', url);
			}
		}, 0);
	}

	async function run() {
		const q = query.trim() || DEFAULT_SELECTOR;
		writeParams();
		stopTail();
		controller?.abort();
		const ctl = new AbortController();
		controller = ctl;
		running = true;
		error = null;
		const range = currentRange();
		try {
			if (isLogQuery(q)) {
				const bucket = volumeBucket(range.from, range.to);
				const [lines, vol] = await Promise.all([
					loki.queryRange(q, { ...rangeParams(q, range), signal: ctl.signal }),
					loki
						.queryRange(volumeQuery(q, bucket.dur), {
							start: range.from,
							end: range.to,
							step: bucket.step,
							signal: ctl.signal
						})
						.catch(() => null)
				]);
				if (ctl.signal.aborted) return;
				const streams = lines.data.resultType === 'streams' ? lines.data.result : [];
				result = {
					query: q,
					kind: 'streams',
					rows: rowsFromStreams(streams),
					volume:
						vol && vol.data.resultType === 'matrix'
							? volumeFromMatrix(vol.data.result, range.from, range.to, bucket.step)
							: null,
					matrix: [],
					vector: [],
					range: { ...range, step: bucket.step },
					limit,
					scanned: lines.stats?.summary.totalLinesProcessed ?? 0,
					fetchedAt: Date.now()
				};
			} else {
				const params = rangeParams(q, range);
				const res = await loki.queryRange(q, { ...params, signal: ctl.signal });
				if (ctl.signal.aborted) return;
				result = {
					query: q,
					kind: res.data.resultType === 'vector' ? 'vector' : 'matrix',
					rows: [],
					volume: null,
					matrix: res.data.resultType === 'matrix' ? res.data.result : [],
					vector: res.data.resultType === 'vector' ? res.data.result : [],
					range,
					limit,
					step: params.step,
					scanned: res.stats?.summary.totalLinesProcessed ?? 0,
					fetchedAt: Date.now()
				};
			}
			expanded = null;
		} catch (e) {
			if (ctl.signal.aborted) return;
			error = e instanceof Error ? e.message : String(e);
		} finally {
			if (!ctl.signal.aborted) running = false;
		}
	}

	function onLevels(next: string) {
		query = next;
		void run();
	}

	function clearQuery() {
		query = '';
		writeParams();
	}

	async function fetchValues(label: string) {
		if (label === 'service') return data.services;
		return loki.labelValues(label);
	}

	// ---- live tail ----
	const LINE_GAP_MS = 260;
	const CHAR_MS = 11;

	function startTail() {
		clearTimeout(tailTimer);
		tailSource = [...result.rows].reverse(); // oldest → newest
		tailIdx = 0;
		expanded = null;
		if (motion.reduced) {
			// cadence off: the whole replay is one frame
			tail = {
				active: true,
				paused: false,
				done: true,
				rows: tailSource,
				typed: {},
				total: tailSource.length
			};
			return;
		}
		tail = { active: true, paused: false, done: false, rows: [], typed: {}, total: tailSource.length };
		tailStep();
	}

	function tailStep() {
		if (!tail.active || tail.paused) return;
		if (tailIdx >= tailSource.length) {
			tail.done = true;
			return;
		}
		const row = tailSource[tailIdx++]!;
		tail.rows = [...tail.rows, row];
		tail.typed = { ...tail.typed, [row.ts]: 0 };
		void tick().then(() => {
			tailEl?.querySelector(`[data-log-row="${row.ts}"]`)?.scrollIntoView({ block: 'nearest' });
		});
		const type = () => {
			if (!tail.active || tail.paused) return;
			const n = (tail.typed[row.ts] ?? 0) + 2;
			if (n >= row.msg.length) {
				tail.typed = { ...tail.typed, [row.ts]: row.msg.length };
				tailTimer = setTimeout(tailStep, LINE_GAP_MS);
			} else {
				tail.typed = { ...tail.typed, [row.ts]: n };
				tailTimer = setTimeout(type, CHAR_MS);
			}
		};
		tailTimer = setTimeout(type, CHAR_MS);
	}

	function pauseTail() {
		if (!tail.active) return;
		tail.paused = !tail.paused;
		if (!tail.paused) {
			// resume: finish the current line, then continue
			const last = tail.rows[tail.rows.length - 1];
			if (last && (tail.typed[last.ts] ?? last.msg.length) < last.msg.length) {
				tail.typed = { ...tail.typed, [last.ts]: last.msg.length };
			}
			tailTimer = setTimeout(tailStep, LINE_GAP_MS);
		} else clearTimeout(tailTimer);
	}

	function stopTail() {
		clearTimeout(tailTimer);
		if (tail.active) tail = { active: false, paused: false, done: false, rows: [], typed: {}, total: 0 };
	}

	function toggleTail() {
		if (tail.active) stopTail();
		else startTail();
	}

	onMount(() => {
		readParams();
		hydrated = true;
		void run();
		const unbind = bindKeys('logs-page', {
			'/': () => {
				bar?.focus();
				return true;
			},
			Escape: () => {
				if (expanded !== null) return false; // LogList collapses it
				if (tail.active) {
					stopTail();
					return true;
				}
				return false;
			}
		});
		return () => {
			unbind();
			controller?.abort();
			clearTimeout(tailTimer);
		};
	});

	// picker change → re-run
	// svelte-ignore state_referenced_locally
	let lastPreset = preset;
	$effect(() => {
		const p = preset;
		if (!hydrated || p === lastPreset) return;
		lastPreset = p;
		customFrom = customTo = undefined;
		void run();
	});

	// mobile: the fixed tail bar must not sit under the toasts
	$effect(() => {
		if (typeof document === 'undefined') return;
		document.documentElement.style.setProperty('--toast-offset', tail.active ? '3.4rem' : '0px');
		return () => document.documentElement.style.removeProperty('--toast-offset');
	});

	function iso(s: number): string {
		return new Date(s * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z');
	}

	function hhmmss(ms: number): string {
		return new Date(ms).toISOString().slice(11, 19) + 'Z';
	}

	let exampleQueries = [
		'{service="gradr"} |= "sentry" | json | level="warn"',
		'{service="gradr"} | json | incident=~"INC-.*" | level="error"',
		'{service=~"euro-tech|ef-polymer"} |~ "(?i)shipped|deployed"',
		'{level="debug"}',
		'sum by (service) (count_over_time({service=~".+"}[10y]))'
	];

	async function useExample(q: string) {
		query = q;
		await tick();
		void run();
	}

	let matrixRows = $derived.by(() => {
		const out: { key: string; series: string; ts: number; value: number }[] = [];
		for (const s of result.matrix)
			for (const [t, v] of s.values)
				out.push({ key: formatStreamLabels(s.metric) + t, series: formatStreamLabels(s.metric), ts: t, value: lokiValue(v) });
		for (const s of result.vector)
			out.push({ key: formatStreamLabels(s.metric), series: formatStreamLabels(s.metric), ts: s.value[0], value: lokiValue(s.value[1]) });
		return out;
	});
</script>

<svelte:head>
	<title>Logs</title>
	<meta
		name="description"
		content="The career log as Loki streams: LogQL with autocomplete, level filters, a volume histogram, expandable JSON lines linked to the trace, and a live-tail replay."
	/>
</svelte:head>

<div class="logs" class:tailing={tail.active}>
	<header class="head">
		<h1 class="h1">Logs</h1>
		<span class="dim mono">{data.services.length} services · {data.labels.join(', ')} labels · content/logs.ndjson via /loki/api/v1</span>
	</header>

	<form
		class="querybar panel"
		onsubmit={(e) => {
			e.preventDefault();
			void run();
		}}
	>
		<label class="sr-only" for="logql">LogQL query</label>
		<LogQueryBar
			bind:this={bar}
			bind:value={query}
			labels={data.labels}
			{fields}
			{fetchValues}
			onrun={() => void run()}
			onclear={clearQuery}
			id="logql"
		/>
		<div class="row">
			<LevelChips {query} counts={levelCounts} onchange={onLevels} />
			<div class="spacer"></div>
			<TimeRangePicker bind:value={preset} options={ALL_PRESETS} />
			<label class="limit mono">
				<span class="dim">limit</span>
				<select class="input" bind:value={limit} onchange={() => void run()} aria-label="Line limit">
					{#each LIMITS as l (l)}
						<option value={l}>{l}</option>
					{/each}
				</select>
			</label>
			<button type="submit" class="btn btn-primary run" disabled={running}>
				{running ? 'Running…' : 'Run query'}
			</button>
			<button
				type="button"
				class="btn tail-btn"
				aria-pressed={tail.active}
				disabled={result.kind !== 'streams' || result.rows.length === 0}
				onclick={toggleTail}
				title="Replay the matching lines oldest → newest"
			>
				<span class="live-dot" class:on={tail.active && !tail.paused && !tail.done} aria-hidden="true"
				></span>
				{tail.active ? 'Stop tail' : 'Live tail'}
			</button>
		</div>
		<p class="kbd-hint mono">
			<kbd>/</kbd> focus · <kbd>Enter</kbd> run · <kbd>Esc</kbd> clear · <kbd>j</kbd>/<kbd>k</kbd> move ·
			<kbd>Ctrl</kbd>+<kbd>Space</kbd> suggest
		</p>
	</form>

	{#if result.kind === 'streams' && !error}
		<div class="panel">
			<VolumeHistogram volume={result.volume} from={result.range.from} to={result.range.to} />
		</div>
	{/if}

	<CurlBlock {curl} />

	<section class="results panel" aria-label="Log lines" aria-busy={running}>
		<div class="res-head mono">
			{#if error}
				<span class="dim">query failed</span>
			{:else if result.kind === 'streams'}
				<span>
					<strong>{result.rows.length}</strong> line{result.rows.length === 1 ? '' : 's'}
					{#if result.rows.length >= result.limit}
						<span class="dim">(limit {result.limit})</span>
					{/if}
					<span class="dim">of {result.scanned} scanned · newest first</span>
				</span>
			{:else}
				<span><strong>{matrixRows.length}</strong> points · {result.kind}{#if result.step} · step {humanStep(result.step)}{/if}</span>
			{/if}
			<span class="dim">
				{iso(result.range.from)} → {iso(result.range.to)}
				· {result.fetchedAt === 'build' ? 'prerendered at build' : `fetched ${hhmmss(result.fetchedAt)}`}
			</span>
		</div>

		{#if tail.active}
			<div class="tail-bar mono" data-tail-count={tail.rows.length} data-tail-done={tail.done}>
				<span class="live-dot" class:on={!tail.paused && !tail.done} aria-hidden="true"></span>
				<span aria-live="polite">
					live tail · {tail.rows.length}/{tail.total} lines · oldest → newest
					{#if tail.done}· replay complete{:else if tail.paused}· paused{/if}
					{#if motion.reduced}· cadence off (reduced motion){/if}
				</span>
				<span class="spacer"></span>
				<button type="button" class="btn small" onclick={pauseTail} disabled={tail.done}
					>{tail.paused ? 'Resume' : 'Pause'}</button
				>
				<button type="button" class="btn small" onclick={stopTail}>Stop</button>
			</div>
		{/if}

		{#if error}
			<EmptyState message={error} expr={result.query} tone="error" />
		{:else if result.kind === 'streams'}
			<div bind:this={tailEl}>
				<LogList
					rows={visibleRows}
					bind:expanded
					typed={tail.typed}
					emptyText={tail.active
						? 'Replaying…'
						: `No lines matched ${result.query} between ${iso(result.range.from)} and ${iso(result.range.to)} — content/logs.ndjson has ${result.scanned || 'no'} lines in the selected streams.`}
				/>
			</div>
			{#if result.rows.length === 0 && !tail.active}
				<div class="starter">
					<p class="dim">Try one of these:</p>
					<ul class="examples">
						{#each exampleQueries as q (q)}
							<li>
								<button type="button" class="ex mono" onclick={() => void useExample(q)}>{q}</button>
							</li>
						{/each}
					</ul>
				</div>
			{/if}
		{:else if matrixRows.length === 0}
			<EmptyState
				message="The metric query returned no series in this range."
				expr={result.query}
			/>
		{:else}
			<div class="table-wrap">
				<table class="table mono">
					<thead>
						<tr><th scope="col">Series</th><th scope="col">Time</th><th scope="col" class="num">Value</th></tr>
					</thead>
					<tbody>
						{#each matrixRows as r (r.key)}
							<tr>
								<td class="series">{r.series}</td>
								<td class="dim">{iso(r.ts)}</td>
								<td class="num">{Number.isInteger(r.value) ? r.value : r.value.toPrecision(6)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</section>
</div>

<style>
	.logs {
		display: flex;
		flex-direction: column;
		gap: 0.6rem;
	}
	.head {
		display: flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: 0.25rem 1rem;
		padding-top: 0.25rem;
	}
	.h1 {
		margin: 0;
		font-size: 1.05rem;
		font-weight: 600;
	}
	.dim {
		color: var(--fg-dim);
		font-size: 0.75rem;
	}
	.querybar {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
		padding: 0.6rem;
	}
	.row {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	.spacer {
		flex: 1;
	}
	.limit {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		font-size: 0.72rem;
	}
	.limit .input {
		min-height: 2rem;
	}
	.run {
		min-width: 7rem;
	}
	.tail-btn[aria-pressed='true'] {
		background: var(--panel-3);
		box-shadow: inset 0 -2px 0 var(--green);
	}
	.live-dot {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 50%;
		background: var(--fg-dim);
	}
	.live-dot.on {
		background: var(--green);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--green) 30%, transparent);
	}
	.kbd-hint {
		margin: 0;
		font-size: 0.68rem;
		color: var(--fg-dim);
	}
	.results {
		min-height: 14rem;
		display: flex;
		flex-direction: column;
	}
	.res-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		gap: 0.25rem 0.75rem;
		padding: 0.45rem 0.6rem;
		border-bottom: 1px solid var(--border);
		font-size: 0.74rem;
	}
	.tail-bar {
		position: sticky;
		top: var(--nav-h);
		z-index: 5;
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.35rem 0.6rem;
		border-bottom: 1px solid var(--border);
		background: color-mix(in srgb, var(--green) 10%, var(--panel));
		font-size: 0.74rem;
	}
	.btn.small {
		min-height: 1.6rem;
		font-size: 0.72rem;
	}
	.starter {
		padding: 0.75rem;
		border-top: 1px dashed var(--border);
	}
	.starter p {
		margin: 0;
	}
	.examples {
		display: flex;
		flex-wrap: wrap;
		gap: 0.4rem;
		margin: 0.5rem 0 0;
		padding: 0;
		list-style: none;
	}
	.ex {
		padding: 0.3rem 0.55rem;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: var(--panel-2);
		color: var(--fg);
		font-size: 0.75rem;
		cursor: pointer;
		text-align: left;
		overflow-wrap: anywhere;
	}
	.ex:hover {
		background: var(--panel-3);
	}
	.table-wrap {
		overflow-x: auto;
	}
	.table {
		width: 100%;
		border-collapse: collapse;
		font-size: 0.75rem;
	}
	.table th,
	.table td {
		padding: 0.35rem 0.6rem;
		border-bottom: 1px solid var(--border);
		text-align: left;
		vertical-align: top;
	}
	.table th {
		color: var(--fg-muted);
		font-weight: 500;
	}
	.num {
		text-align: right !important;
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
	}
	.series {
		overflow-wrap: anywhere;
	}
	@media (max-width: 639.98px) {
		.kbd-hint {
			display: none;
		}
		.run {
			flex: 1;
		}
		.tailing .tail-bar {
			position: fixed;
			top: auto;
			left: 0;
			right: 0;
			bottom: 0;
			z-index: 40;
			padding-bottom: calc(0.35rem + env(safe-area-inset-bottom, 0px));
			border-top: 1px solid var(--border);
			border-bottom: 0;
			background: var(--panel);
		}
		.tailing .results {
			padding-bottom: 3.4rem;
		}
	}
</style>
