<script lang="ts">
	// Alerts (brief §3.6): an Alertmanager-style view of the rules in
	// content/alerts.yaml (via /api/v1/rules), evaluated in the browser by
	// $lib/alerts/engine — inactive → pending → firing per `for`, "no data"
	// when a metric has no series, the API's error text when a query fails —
	// plus the session's silences. The engine runs on every route; this page
	// renders its state.
	import { alerts, exploreHref, type AlertEval, type AlertState } from '$lib/alerts/engine.svelte';
	import { humanStep } from '$lib/timerange';
	import Seo from '$lib/components/ui/Seo.svelte';

	let { data } = $props();

	// The prerendered list: rules from the build-time load, state inactive.
	// svelte-ignore state_referenced_locally
	alerts.seed(data.groups);

	const ORDER: AlertState[] = ['firing', 'pending', 'error', 'nodata', 'inactive'];

	let rules = $derived(
		[...alerts.rules].sort(
			(a, b) =>
				ORDER.indexOf(a.state) - ORDER.indexOf(b.state) || a.def.name.localeCompare(b.def.name)
		)
	);
	let counts = $derived.by(() => {
		const c: Record<AlertState, number> = {
			firing: 0,
			pending: 0,
			inactive: 0,
			nodata: 0,
			error: 0
		};
		for (const r of alerts.rules) c[r.state]++;
		return c;
	});
	let now = $state(Date.now());

	$effect(() => {
		const t = setInterval(() => (now = Date.now()), 1000);
		return () => clearInterval(t);
	});

	function hhmmss(ms: number): string {
		return new Date(ms).toISOString().slice(11, 19) + 'Z';
	}

	function stateLabel(s: AlertState): string {
		return s === 'nodata' ? 'no data' : s;
	}

	function forText(r: AlertEval): string {
		const f = r.def.forS * (alerts.fast ? 0.1 : 1);
		return f === 0 ? '0s' : humanStep(f);
	}

	function since(r: AlertEval): string {
		if (r.state === 'firing' && r.firedAt) return `firing since ${hhmmss(r.firedAt)}`;
		if (r.state === 'pending' && r.activeAt) {
			const forMs = r.def.forS * 1000 * (alerts.fast ? 0.1 : 1);
			const left = Math.max(0, Math.ceil((r.activeAt + forMs - now) / 1000));
			return `pending since ${hhmmss(r.activeAt)} · fires in ${left}s`;
		}
		return '';
	}

	function valueText(r: AlertEval): string {
		if (r.value === undefined) return '';
		return Number.isInteger(r.value) ? String(r.value) : String(Number(r.value.toPrecision(6)));
	}

	function labelsText(l: Record<string, string>): string {
		return Object.entries(l)
			.map(([k, v]) => `${k}="${v}"`)
			.join(', ');
	}

	const description =
		'Alertmanager-style rules evaluated in your browser against the Prometheus-compatible API: DivyAvailableForHire, HighContributionRate, LFXApplicationPending — with session silences.';
</script>

<Seo title="Alerts · divy.dev" {description} path="/alerts" origin={data.siteOrigin} />

<section class="alerts" aria-labelledby="alerts-title">
	<header class="head">
		<div class="titles">
			<h1 id="alerts-title" class="h1">Alerts</h1>
			<p class="sub mono">
				{alerts.rules.length} rules · content/alerts.yaml via /api/v1/rules · evaluated in this browser
				every {alerts.intervalS}s
				{#if alerts.lastRound}
					· last round {hhmmss(alerts.lastRound)}
				{:else if alerts.status === 'loading'}
					· loading…
				{:else}
					· not evaluated yet (prerendered)
				{/if}
				{#if alerts.fast}
					· <span class="fast">test hook: 1s polling, `for` ÷ 10</span>
				{/if}
			</p>
		</div>
		<ul class="summary mono" aria-label="Rule states">
			{#each ORDER as s (s)}
				<li class="pill state-{s}" data-count={s}>
					<span class="dot" aria-hidden="true"></span>{stateLabel(s)}
					<strong>{counts[s]}</strong>
				</li>
			{/each}
			<li class="pill state-silenced">
				silenced <strong>{alerts.silences.length}</strong>
			</li>
		</ul>
	</header>

	{#if alerts.loadError}
		<p class="panel err mono" role="alert">/api/v1/rules: {alerts.loadError}</p>
	{/if}

	<div class="list" role="list" aria-label="Alert rules">
		{#each rules as r (r.def.name)}
			<article
				class="rule panel state-{r.state}"
				class:silenced={r.silenced}
				role="listitem"
				data-alert={r.def.name}
				data-state={r.state}
				aria-labelledby="rule-{r.def.name}"
			>
				<div class="rule-head">
					<span class="badge mono state-{r.state}" data-badge>{stateLabel(r.state)}</span>
					<h2 id="rule-{r.def.name}" class="name mono">{r.def.name}</h2>
					{#if r.def.labels.severity}
						<span class="chip sev sev-{r.def.labels.severity}"
							>severity={r.def.labels.severity}</span
						>
					{/if}
					<span class="chip">for {forText(r)}</span>
					{#if r.silenced}
						<span class="chip muted">silenced</span>
					{/if}
					<span class="spacer"></span>
					<div class="actions">
						{#if r.silenced}
							<button
								type="button"
								class="btn small"
								onclick={() => alerts.unsilence(r.def.name)}
								aria-label="Expire the silence for {r.def.name}">Unsilence</button
							>
						{:else}
							<button
								type="button"
								class="btn small"
								onclick={() => alerts.silence(r.def.name)}
								aria-label="Silence {r.def.name} for this session">Silence</button
							>
						{/if}
						{#if r.def.annotations.runbook_url}
							<a class="btn small" href={r.def.annotations.runbook_url}>Runbook</a>
						{/if}
						<a class="btn small" href={exploreHref(r.def.expr)}>Explore</a>
					</div>
				</div>
				<dl class="kv mono">
					<dt>expr</dt>
					<dd><code>{r.def.expr}</code></dd>
					{#if r.summary}
						<dt>summary</dt>
						<dd class="summary-text">{r.summary}</dd>
					{/if}
					{#if r.state === 'firing' || r.state === 'pending'}
						<dt>since</dt>
						<dd>{since(r)}</dd>
						<dt>value</dt>
						<dd>
							{valueText(r)}
							{#if r.samples[0] && Object.keys(r.samples[0].labels).length}
								<span class="dim">{'{'}{labelsText(r.samples[0].labels)}{'}'}</span>
							{/if}
							{#if r.samples.length > 1}
								<span class="dim">· {r.samples.length} series</span>
							{/if}
						</dd>
					{:else if r.state === 'nodata'}
						<dt>no data</dt>
						<dd class="nodata">
							{r.reason ?? `${r.missing.join(', ')}: no series`} — the rule cannot fire until the series
							exists.
						</dd>
					{:else if r.state === 'error'}
						<dt>error</dt>
						<dd class="err">{r.error}</dd>
					{:else if r.lastEval}
						<dt>value</dt>
						<dd class="dim">condition false at {hhmmss(r.lastEval)}</dd>
					{/if}
					{#if Object.keys(r.def.labels).length}
						<dt>labels</dt>
						<dd class="dim">{labelsText(r.def.labels)}</dd>
					{/if}
				</dl>
			</article>
		{:else}
			<p class="panel empty mono">No rules: /api/v1/rules returned no alerting rules.</p>
		{/each}
	</div>

	<section class="silences panel" aria-labelledby="silences-title">
		<div class="panel-header">
			<h2 id="silences-title">Silences</h2>
			<span class="dim">this browser session (sessionStorage)</span>
		</div>
		{#if alerts.silences.length === 0}
			<p class="empty mono">No silences. "Silence" on a firing toast or a rule above adds one.</p>
		{:else}
			<ul class="sil-list mono" data-silences>
				{#each alerts.silences as s (s.alertname)}
					<li>
						<span class="name">{s.alertname}</span>
						<span class="dim">since {hhmmss(s.createdAt)}</span>
						<button
							type="button"
							class="btn small"
							onclick={() => alerts.unsilence(s.alertname)}
							aria-label="Expire the silence for {s.alertname}">Expire</button
						>
					</li>
				{/each}
			</ul>
		{/if}
	</section>

	<p class="note dim">
		The API serves the rules (<a href="/api/v1/rules" rel="external" class="mono">/api/v1/rules</a>)
		and never evaluates them; every state above comes from this page polling
		<span class="mono">/api/v1/query</span>. A firing rule slides in as a toast on every route;
		Silence hides it until the tab is closed.
	</p>
</section>

<style>
	.alerts {
		display: flex;
		flex-direction: column;
		gap: 0.6rem;
	}
	.head {
		display: flex;
		align-items: flex-start;
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
		font-size: 0.72rem;
		color: var(--fg-dim);
	}
	.fast {
		color: var(--yellow);
	}
	.summary {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.pill {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		min-height: 1.8rem;
		padding: 0 0.6rem;
		border: 1px solid var(--border);
		border-radius: 999px;
		background: var(--panel-2);
		font-size: 0.72rem;
		color: var(--fg-muted);
	}
	.pill strong {
		color: var(--fg);
	}
	.dot {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 50%;
		background: var(--lv, var(--fg-dim));
	}
	.state-firing {
		--lv: var(--red);
	}
	.state-pending {
		--lv: var(--yellow);
	}
	.state-inactive {
		--lv: var(--green);
	}
	.state-nodata {
		--lv: var(--fg-dim);
	}
	.state-error {
		--lv: var(--red);
	}
	.list {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}
	.rule {
		padding: 0.6rem 0.75rem;
		border-left: 4px solid var(--lv, var(--border));
	}
	.rule.silenced {
		opacity: 0.75;
	}
	.rule-head {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.4rem;
	}
	.badge {
		display: inline-flex;
		align-items: center;
		min-height: 1.4rem;
		padding: 0 0.45rem;
		border-radius: 4px;
		font-size: 0.66rem;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--bg);
		background: var(--lv, var(--fg-dim));
	}
	.name {
		margin: 0;
		font-size: 0.9rem;
		font-weight: 600;
		overflow-wrap: anywhere;
	}
	.chip.sev {
		background: var(--bg);
	}
	.sev-page,
	.sev-critical {
		color: var(--red);
		border-color: color-mix(in srgb, var(--red) 50%, var(--border));
	}
	.sev-warning {
		color: var(--yellow);
		border-color: color-mix(in srgb, var(--yellow) 50%, var(--border));
	}
	.chip.muted {
		color: var(--fg-dim);
	}
	.spacer {
		flex: 1;
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
	}
	.btn.small {
		min-height: 1.8rem;
		font-size: 0.72rem;
		text-decoration: none;
	}
	.kv {
		display: grid;
		grid-template-columns: max-content minmax(0, 1fr);
		gap: 0.15rem 0.75rem;
		margin: 0.5rem 0 0;
		font-size: 0.75rem;
	}
	.kv dt {
		color: var(--fg-dim);
	}
	.kv dd {
		margin: 0;
		overflow-wrap: anywhere;
	}
	.kv code {
		color: var(--orange);
	}
	.summary-text {
		font-family: var(--font-sans);
		color: var(--fg);
	}
	.dim {
		color: var(--fg-dim);
		font-size: 0.72rem;
	}
	.nodata {
		color: var(--fg-muted);
	}
	.err {
		color: var(--red);
	}
	.panel.err {
		margin: 0;
		padding: 0.5rem 0.75rem;
		font-size: 0.78rem;
	}
	.empty {
		margin: 0;
		padding: 0.75rem;
		font-size: 0.78rem;
		color: var(--fg-muted);
	}
	.panel-header h2 {
		margin: 0;
		font-size: inherit;
	}
	.sil-list {
		margin: 0;
		padding: 0.25rem 0.75rem;
		list-style: none;
		font-size: 0.78rem;
	}
	.sil-list li {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.5rem;
		padding: 0.35rem 0;
		border-bottom: 1px solid var(--border);
	}
	.sil-list li:last-child {
		border-bottom: 0;
	}
	.sil-list .name {
		font-size: 0.78rem;
		font-weight: 600;
	}
	.sil-list .btn {
		margin-left: auto;
	}
	.note {
		margin: 0;
		font-size: 0.74rem;
	}
	@media (pointer: coarse) {
		.btn.small {
			min-height: 2.75rem;
		}
	}
	@media (max-width: 639.98px) {
		.kv {
			grid-template-columns: 1fr;
			gap: 0;
		}
		.kv dt {
			margin-top: 0.35rem;
		}
	}
</style>
