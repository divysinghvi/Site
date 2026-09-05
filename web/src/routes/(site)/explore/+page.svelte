<script lang="ts">
	// Explore (/explore?ds=prom|loki&expr=…&from=…&to=…[&instant=1]): data-source
	// tabs, the PromQL query bar with autocomplete (or the LogQL bar, level
	// chips and log list of /logs on the Loki tab), a range picker, Run,
	// results as a timeseries (range) or a table (instant), and the copyable
	// curl. The page is a prerendered shell; params are read after hydration.
	import { onMount, tick } from 'svelte';
	import { replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import type { Readyz } from '$lib/api/types.gen';
	import { bindKeys } from '$lib/keyboard';
	import {
		ALL_PRESETS,
		humanStep,
		parseTimeParam,
		presetFromParams,
		presetToRelative,
		resolveRange,
		chooseStep,
		type Preset,
		type ResolvedRange
	} from '$lib/timerange';
	import {
		curlInstant,
		curlRange,
		formatLabels,
		labelNames,
		labelValues,
		metadata,
		query,
		queryRange,
		sampleValue,
		type MetricMeta,
		type PromData
	} from '$lib/panels/prom';
	import { emptyMessageForExpr, fetchReadyz } from '$lib/panels/model';
	import { formatValue } from '$lib/panels/units';
	import { matrixToChart, type ChartData } from '$lib/charts/uplot';
	import UPlotChart from '$lib/charts/UPlotChart.svelte';
	import QueryBar from '$lib/panels/QueryBar.svelte';
	import TimeRangePicker from '$lib/panels/TimeRangePicker.svelte';
	import EmptyState from '$lib/panels/EmptyState.svelte';
	import Seo from '$lib/components/ui/Seo.svelte';
	import {
		curlQueryRange,
		formatStreamLabels,
		isLogQuery,
		loki,
		lokiValue,
		type RangeParams
	} from '$lib/logql/loki';
	import { DEFAULT_LIMIT, fieldNames, rowsFromStreams, type LogRow } from '$lib/logql/lines';
	import { DEFAULT_SELECTOR } from '$lib/logql/selector';
	import LogQueryBar from '$lib/logql/LogQueryBar.svelte';
	import LevelChips from '$lib/logql/LevelChips.svelte';
	import LogList from '$lib/logql/LogList.svelte';
	import CurlBlock from '$lib/logql/CurlBlock.svelte';

	let { data } = $props();

	type DS = 'prom' | 'loki';
	let ds = $state<DS>('prom');
	let expr = $state('');
	let preset = $state<Preset>('7d');
	let customFrom = $state<number | undefined>(undefined);
	let customTo = $state<number | undefined>(undefined);
	let instant = $state(false);
	let running = $state(false);
	let result = $state<{
		expr: string;
		data: PromData;
		range: ResolvedRange;
		kind: 'instant' | 'range';
		warnings: string[];
	} | null>(null);
	let error = $state<string | null>(null);
	let readyz = $state<Readyz | null>(null);
	let metrics = $state<string[]>([]);
	let meta = $state<Record<string, MetricMeta>>({});
	let view = $state<'graph' | 'table'>('graph');
	let copied = $state(false);
	let bar = $state<QueryBar | null>(null);
	let hydrated = false;
	let controller: AbortController | null = null;

	// ---- Loki tab (same bar / chips / list as /logs) ----
	let logBar = $state<LogQueryBar | null>(null);
	let lokiLabels = $state<string[]>([]);
	let lokiServices = $state<string[]>([]);
	let lokiLoaded = false;
	let lokiResult = $state<{
		query: string;
		kind: 'streams' | 'matrix' | 'vector';
		rows: LogRow[];
		points: { key: string; series: string; ts: number; value: number }[];
		range: ResolvedRange;
		scanned: number;
	} | null>(null);
	let expandedLine = $state<string | null>(null);
	let lokiFields = $derived(fieldNames(lokiResult?.rows ?? []));

	function lokiParams(q: string, range: ResolvedRange): RangeParams {
		return isLogQuery(q)
			? { start: range.from, end: range.to, limit: DEFAULT_LIMIT, direction: 'backward' }
			: { start: range.from, end: range.to, step: chooseStep(range.from, range.to) };
	}

	async function loadLokiMeta() {
		if (lokiLoaded) return;
		lokiLoaded = true;
		const [labels, services] = await Promise.all([
			loki.labels().catch(() => [] as string[]),
			loki.labelValues('service').catch(() => [] as string[])
		]);
		lokiLabels = labels;
		lokiServices = services;
	}

	async function runLoki(ctl: AbortController) {
		const q = expr.trim() || DEFAULT_SELECTOR;
		const range = currentRange();
		const res = await loki.queryRange(q, { ...lokiParams(q, range), signal: ctl.signal });
		if (ctl.signal.aborted) return;
		const d = res.data;
		const points: { key: string; series: string; ts: number; value: number }[] = [];
		if (d.resultType === 'matrix')
			for (const s of d.result)
				for (const [t, v] of s.values)
					points.push({
						key: formatStreamLabels(s.metric) + t,
						series: formatStreamLabels(s.metric),
						ts: t,
						value: lokiValue(v)
					});
		if (d.resultType === 'vector')
			for (const s of d.result)
				points.push({
					key: formatStreamLabels(s.metric),
					series: formatStreamLabels(s.metric),
					ts: s.value[0],
					value: lokiValue(s.value[1])
				});
		lokiResult = {
			query: q,
			kind: d.resultType,
			rows: d.resultType === 'streams' ? rowsFromStreams(d.result) : [],
			points,
			range,
			scanned: res.stats?.summary.totalLinesProcessed ?? 0
		};
		expandedLine = null;
	}

	function onLevels(next: string) {
		expr = next;
		void run();
	}

	async function fetchLokiValues(label: string) {
		if (label === 'service' && lokiServices.length) return lokiServices;
		return loki.labelValues(label);
	}

	let lokiCurl = $derived.by(() => {
		if (ds !== 'loki') return '';
		const q = expr.trim() || DEFAULT_SELECTOR;
		const r = lokiResult?.query === q ? lokiResult.range : currentRange();
		return curlQueryRange(origin, q, lokiParams(q, r));
	});

	let lokiExamples = [
		'{service="gradr"} |= "sentry" | json | level="warn"',
		'{service=~"euro-tech|ef-polymer"} |~ "(?i)shipped|deployed"',
		'{level="debug"}',
		'sum by (service) (count_over_time({service=~".+"}[10y]))'
	];

	let origin = $derived(
		data.siteOrigin || (typeof location !== 'undefined' ? location.origin : '')
	);
	// the preset the last run used (the picker effect re-runs on a change)
	// svelte-ignore state_referenced_locally
	let lastPreset: Preset = preset;

	function currentRange(): ResolvedRange {
		if (customFrom !== undefined && customTo !== undefined && customTo > customFrom) {
			return { preset, from: customFrom, to: customTo, step: chooseStep(customFrom, customTo) };
		}
		return resolveRange(preset, { allFrom: data.allFrom });
	}

	function readParams() {
		const q = page.url.searchParams;
		ds = q.get('ds') === 'loki' ? 'loki' : 'prom';
		expr = q.get('expr') ?? '';
		instant = q.get('instant') === '1';
		const from = q.get('from');
		const to = q.get('to');
		// the log lines span the whole career: Loki defaults to `all`, Prometheus to 7d
		if (ds === 'loki' && !from) preset = 'all';
		const p = presetFromParams(from, to);
		if (p) {
			preset = p;
			customFrom = customTo = undefined;
		} else if (from) {
			const f = parseTimeParam(from);
			const t = parseTimeParam(to ?? 'now');
			if (f !== undefined && t !== undefined && t > f) {
				customFrom = f;
				customTo = t;
				preset = data.allFrom !== undefined && Math.abs(f - data.allFrom) < 86400 ? 'all' : preset;
			}
		}
	}

	let writeTimer: ReturnType<typeof setTimeout> | undefined;
	/** URL writes are deferred a tick so the router is initialised on the first run. */
	function writeParams() {
		if (!hydrated) return;
		clearTimeout(writeTimer);
		writeTimer = setTimeout(writeParamsNow, 0);
	}

	function writeParamsNow() {
		const q = new URLSearchParams();
		q.set('ds', ds);
		if (expr) q.set('expr', expr);
		if (customFrom !== undefined && customTo !== undefined) {
			q.set('from', String(customFrom));
			q.set('to', String(customTo));
		} else {
			const rel = presetToRelative(preset, data.allFrom);
			q.set('from', rel.from);
			q.set('to', rel.to);
		}
		if (instant) q.set('instant', '1');
		const url = `${page.url.pathname}?${q.toString()}${page.url.hash}`;
		try {
			replaceState(url, page.state);
		} catch {
			history.replaceState(history.state, '', url);
		}
	}

	async function run() {
		const e = expr.trim();
		writeParams();
		if (!e && ds === 'prom') return;
		controller?.abort();
		const ctl = new AbortController();
		controller = ctl;
		running = true;
		error = null;
		const range = currentRange();
		try {
			if (ds === 'loki') {
				await runLoki(ctl);
				return;
			}
			const res = instant
				? await query(e, { signal: ctl.signal })
				: await queryRange(e, { ...range, signal: ctl.signal });
			if (ctl.signal.aborted) return;
			result = {
				expr: e,
				data: res.data,
				range,
				kind: instant ? 'instant' : 'range',
				warnings: res.warnings
			};
			view = res.data.resultType === 'matrix' ? 'graph' : 'table';
			void fetchReadyz(true).then((v) => (readyz = v));
		} catch (err) {
			if (ctl.signal.aborted) return;
			result = null;
			error = err instanceof Error ? err.message : String(err);
		} finally {
			if (!ctl.signal.aborted) running = false;
		}
	}

	onMount(() => {
		readParams();
		lastPreset = preset;
		hydrated = true;
		if (ds === 'loki') void loadLokiMeta();
		void labelValues('__name__')
			.then((m) => (metrics = m))
			.catch(() => {});
		void metadata()
			.then((m) => (meta = m))
			.catch(() => {});
		void fetchReadyz().then((v) => (readyz = v));
		if (expr.trim() || ds === 'loki') void run();
		const unbind = bindKeys('explore', {
			'/': () => {
				(ds === 'loki' ? logBar : bar)?.focus();
				return true;
			}
		});
		return () => {
			unbind();
			controller?.abort();
		};
	});

	// picker change → re-run when there is a query
	$effect(() => {
		const p = preset;
		if (!hydrated || p === lastPreset) return;
		lastPreset = p;
		customFrom = customTo = undefined;
		if (expr.trim() || ds === 'loki') void run();
		else writeParams();
	});

	function setDs(next: DS) {
		if (ds === next) return;
		ds = next;
		error = null;
		// a query written for the other data source is not carried over
		expr = '';
		result = null;
		lokiResult = null;
		if (next === 'loki') {
			if (customFrom === undefined) preset = lastPreset = 'all';
			void loadLokiMeta();
			void run();
		} else writeParams();
	}

	function setInstant(v: boolean) {
		instant = v;
		if (expr.trim()) void run();
		else writeParams();
	}

	async function fetchLabels(metric?: string) {
		return labelNames(metric ? [metric] : undefined);
	}

	async function fetchValues(label: string, metric?: string) {
		return labelValues(label, metric ? [metric] : undefined);
	}

	let curl = $derived.by(() => {
		const e = expr.trim();
		if (!e) return '';
		const r = result?.range ?? currentRange();
		return instant ? curlInstant(origin, e) : curlRange(origin, e, r.from, r.to, r.step);
	});

	async function copyCurl() {
		try {
			await navigator.clipboard.writeText(curl);
			copied = true;
			setTimeout(() => (copied = false), 1500);
		} catch {
			copied = false;
		}
	}

	let chart = $derived.by((): ChartData | null => {
		if (!result || result.data.resultType !== 'matrix') return null;
		return matrixToChart(result.data.result, undefined);
	});

	interface Row {
		metric: string;
		labels: Record<string, string>;
		value: number;
		ts: number;
	}
	let rows = $derived.by((): Row[] => {
		if (!result) return [];
		const d = result.data;
		if (d.resultType === 'vector')
			return d.result.map((s) => ({
				metric: formatLabels(s.metric),
				labels: s.metric,
				value: sampleValue(s.value[1]),
				ts: s.value[0]
			}));
		if (d.resultType === 'matrix')
			return d.result
				.filter((s) => s.values.length > 0)
				.map((s) => {
					const last = s.values[s.values.length - 1]!;
					return {
						metric: formatLabels(s.metric),
						labels: s.metric,
						value: sampleValue(last[1]),
						ts: last[0]
					};
				});
		if (d.resultType === 'scalar')
			return [{ metric: 'scalar', labels: {}, value: sampleValue(d.result[1]), ts: d.result[0] }];
		return [{ metric: 'string', labels: {}, value: NaN, ts: d.result[0] }];
	});

	let empty = $derived(
		!!result &&
			((result.data.resultType === 'vector' && result.data.result.length === 0) ||
				(result.data.resultType === 'matrix' &&
					result.data.result.every((s) => s.values.length === 0)))
	);

	let stringValue = $derived(result?.data.resultType === 'string' ? result.data.result[1] : null);

	let exampleQueries = [
		'divy_uptime_seconds',
		'divy_experience_years',
		'sum(increase(github_commits_total[7d]))',
		'sum by (org) (github_merged_prs_total)',
		'rate(pypi_downloads_total{package="codemind-ci"}[2d]) * 86400',
		'probe_success'
	];

	async function useExample(q: string) {
		expr = q;
		await tick();
		void run();
	}
</script>

<Seo
	title="Explore · divy.dev"
	description="Run PromQL or LogQL against the site's Prometheus- and Loki-compatible APIs: autocomplete, time range, graph, table or log lines, and the curl to reproduce it."
	path="/explore"
	origin={data.siteOrigin}
/>

<div class="explore">
	<header class="head">
		<h1 class="h1">Explore</h1>
		<div class="tabs" role="tablist" aria-label="Data source">
			<button
				type="button"
				role="tab"
				class="tab"
				aria-selected={ds === 'prom'}
				aria-controls="ds-prom"
				id="tab-prom"
				onclick={() => setDs('prom')}
			>
				Prometheus
			</button>
			<button
				type="button"
				role="tab"
				class="tab"
				aria-selected={ds === 'loki'}
				aria-controls="ds-loki"
				id="tab-loki"
				onclick={() => setDs('loki')}
			>
				Loki
			</button>
		</div>
	</header>

	{#if ds === 'loki'}
		<div id="ds-loki" role="tabpanel" aria-labelledby="tab-loki" class="prom">
			<form
				class="querybar panel"
				onsubmit={(e) => {
					e.preventDefault();
					void run();
				}}
			>
				<label class="sr-only" for="logql">LogQL query</label>
				<LogQueryBar
					bind:this={logBar}
					bind:value={expr}
					labels={lokiLabels}
					fields={lokiFields}
					fetchValues={fetchLokiValues}
					onrun={() => void run()}
					id="logql"
				/>
				<div class="row">
					<LevelChips query={expr} onchange={onLevels} />
					<TimeRangePicker bind:value={preset} options={ALL_PRESETS} />
					<button type="submit" class="btn btn-primary run" disabled={running}>
						{running ? 'Running…' : 'Run query'}
					</button>
					<a
						class="btn logs-link"
						href="/logs?q={encodeURIComponent(expr.trim() || DEFAULT_SELECTOR)}">Open in Logs ↗</a
					>
				</div>
			</form>

			<CurlBlock curl={lokiCurl} />

			<div class="results panel" aria-live="polite">
				{#if error}
					<EmptyState message={error} expr={expr.trim() || DEFAULT_SELECTOR} tone="error" />
				{:else if !lokiResult}
					<div class="starter">
						<p class="dim">Type a LogQL query or start from one of these:</p>
						<ul class="examples">
							{#each lokiExamples as q (q)}
								<li>
									<button type="button" class="ex mono" onclick={() => void useExample(q)}
										>{q}</button
									>
								</li>
							{/each}
						</ul>
					</div>
				{:else if lokiResult.kind === 'streams'}
					<div class="res-head">
						<span class="mono dim">
							streams · {lokiResult.rows.length} lines of {lokiResult.scanned} scanned · newest first
							· {lokiResult.range.from} → {lokiResult.range.to}
						</span>
					</div>
					<LogList
						rows={lokiResult.rows}
						bind:expanded={expandedLine}
						id="explore-logs"
						emptyText="No lines matched {lokiResult.query} in this range."
					/>
				{:else if lokiResult.points.length === 0}
					<EmptyState
						message="The metric query returned no series in this range."
						expr={lokiResult.query}
					/>
				{:else}
					<div class="res-head">
						<span class="mono dim">{lokiResult.kind} · {lokiResult.points.length} points</span>
					</div>
					<div class="table-wrap">
						<table class="table mono">
							<thead>
								<tr
									><th scope="col">Series</th><th scope="col">Time</th><th scope="col" class="num"
										>Value</th
									></tr
								>
							</thead>
							<tbody>
								{#each lokiResult.points as r (r.key)}
									<tr>
										<td class="series">{r.series}</td>
										<td class="dim">{new Date(r.ts * 1000).toISOString()}</td>
										<td class="num"
											>{Number.isInteger(r.value) ? r.value : r.value.toPrecision(6)}</td
										>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			</div>
		</div>
	{:else}
		<div id="ds-prom" role="tabpanel" aria-labelledby="tab-prom" class="prom">
			<form
				class="querybar panel"
				onsubmit={(e) => {
					e.preventDefault();
					void run();
				}}
			>
				<label class="sr-only" for="promql">PromQL query</label>
				<QueryBar
					bind:this={bar}
					bind:value={expr}
					{metrics}
					{meta}
					{fetchLabels}
					{fetchValues}
					onrun={() => void run()}
					id="promql"
				/>
				<div class="row">
					<TimeRangePicker bind:value={preset} options={ALL_PRESETS} />
					<div class="type" role="group" aria-label="Query type">
						<button
							type="button"
							class="btn"
							aria-pressed={!instant}
							onclick={() => setInstant(false)}>Range</button
						>
						<button
							type="button"
							class="btn"
							aria-pressed={instant}
							onclick={() => setInstant(true)}>Instant</button
						>
					</div>
					<button type="submit" class="btn btn-primary run" disabled={running || !expr.trim()}>
						{running ? 'Running…' : 'Run query'}
					</button>
					<span class="kbd-hint mono"
						><kbd>/</kbd> focus · <kbd>Enter</kbd> run · <kbd>Ctrl</kbd>+<kbd>Space</kbd> suggest</span
					>
				</div>
			</form>

			{#if curl}
				<div class="curl panel">
					<div class="curl-head">
						<span class="mono dim">curl</span>
						<button type="button" class="btn small" onclick={copyCurl}
							>{copied ? 'Copied' : 'Copy'}</button
						>
					</div>
					<pre class="code"><code>{curl}</code></pre>
				</div>
			{/if}

			<div class="results panel" aria-live="polite">
				{#if error}
					<EmptyState message={error} {expr} tone="error" />
				{:else if !result}
					<div class="starter">
						<p class="dim">Type a query or start from one of these:</p>
						<ul class="examples">
							{#each exampleQueries as q (q)}
								<li>
									<button type="button" class="ex mono" onclick={() => void useExample(q)}
										>{q}</button
									>
								</li>
							{/each}
						</ul>
					</div>
				{:else if empty}
					<EmptyState message={emptyMessageForExpr(result.expr, readyz)} expr={result.expr} />
				{:else}
					<div class="res-head">
						<span class="mono dim">
							{result.data.resultType}
							{#if result.kind === 'range'}
								· {result.range.from} → {result.range.to} · step {humanStep(result.range.step)}
							{/if}
							· {rows.length} series
						</span>
						{#if result.data.resultType === 'matrix'}
							<div class="type" role="group" aria-label="Result view">
								<button
									type="button"
									class="btn small"
									aria-pressed={view === 'graph'}
									onclick={() => (view = 'graph')}>Graph</button
								>
								<button
									type="button"
									class="btn small"
									aria-pressed={view === 'table'}
									onclick={() => (view = 'table')}>Table</button
								>
							</div>
						{/if}
					</div>
					{#if result.warnings.length}
						<p class="warn mono">{result.warnings.join(' · ')}</p>
					{/if}
					{#if chart && view === 'graph'}
						<div class="graph">
							<UPlotChart
								data={chart}
								syncKey="divy-explore"
								label="Result of {result.expr}"
								xRange={[result.range.from, result.range.to]}
							/>
						</div>
					{:else if stringValue !== null}
						<p class="mono">{stringValue}</p>
					{:else}
						<div class="table-wrap">
							<table class="table mono">
								<thead>
									<tr
										><th scope="col">Series</th><th scope="col" class="num">Value</th><th
											scope="col">Time</th
										></tr
									>
								</thead>
								<tbody>
									{#each rows as r (r.metric)}
										<tr>
											<td class="series">{r.metric}</td>
											<td class="num">{formatValue(r.value, undefined)}</td>
											<td class="dim">{new Date(r.ts * 1000).toISOString()}</td>
										</tr>
									{/each}
								</tbody>
							</table>
						</div>
					{/if}
				{/if}
			</div>
		</div>
	{/if}
</div>

<style>
	.explore {
		display: flex;
		flex-direction: column;
		gap: 0.6rem;
	}
	.head {
		display: flex;
		align-items: center;
		gap: 1rem;
		padding-top: 0.25rem;
	}
	.h1 {
		margin: 0;
		font-size: 1.05rem;
		font-weight: 600;
	}
	.tabs {
		display: inline-flex;
		border-bottom: 1px solid var(--border);
	}
	.tab {
		padding: 0.4rem 0.7rem;
		border: 0;
		border-bottom: 2px solid transparent;
		background: transparent;
		color: var(--fg-muted);
		font-size: 0.85rem;
		cursor: pointer;
	}
	.tab[aria-selected='true'] {
		color: var(--fg);
		border-bottom-color: var(--orange);
	}
	.logs-link {
		text-decoration: none;
	}
	.dim {
		color: var(--fg-dim);
		font-size: 0.78rem;
	}
	.prom {
		display: flex;
		flex-direction: column;
		gap: 0.6rem;
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
	.type {
		display: inline-flex;
		gap: 0.25rem;
	}
	.btn[aria-pressed='true'] {
		background: var(--panel-3);
		box-shadow: inset 0 -2px 0 var(--orange);
	}
	.run {
		min-width: 7rem;
	}
	.kbd-hint {
		margin-left: auto;
		font-size: 0.68rem;
		color: var(--fg-dim);
	}
	.curl {
		padding: 0.5rem 0.6rem;
	}
	.curl-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 0.3rem;
	}
	.btn.small {
		min-height: 1.6rem;
		font-size: 0.72rem;
	}
	.code {
		margin: 0;
		padding: 0.5rem 0.65rem;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: var(--bg);
		font-size: 0.75rem;
		line-height: 1.45;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		overflow-x: auto;
	}
	.results {
		min-height: 22rem;
		display: flex;
		flex-direction: column;
	}
	.starter {
		padding: 1rem;
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
	}
	.ex:hover {
		background: var(--panel-3);
	}
	.res-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		padding: 0.45rem 0.6rem;
		border-bottom: 1px solid var(--border);
	}
	.warn {
		margin: 0;
		padding: 0.3rem 0.6rem;
		color: var(--yellow);
		font-size: 0.72rem;
	}
	.graph {
		display: flex;
		flex-direction: column;
		height: 22rem;
		padding: 0.25rem 0;
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
	}
</style>
