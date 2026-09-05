<script lang="ts">
	// One monitored target, Uptime-Kuma style: name, url (or the unconfigured
	// TODO state), current state (red when down, never green without a
	// successful probe), latency, last probe, 90-day heartbeat bar, uptime
	// windows, incident history and the target's note (self-probe caveat).
	import type { HeartbeatTarget, UptimeTargetView } from '$lib/api/types.gen';
	import { spanTraceHref } from '$lib/postmortem';
	import TodoBadge from '$lib/postmortem/TodoBadge.svelte';
	import HeartbeatBar from '$lib/uptime/HeartbeatBar.svelte';
	import IncidentTable from '$lib/uptime/IncidentTable.svelte';
	import {
		dayCells,
		errorClass,
		errorMessage,
		formatAgo,
		formatLatency,
		formatPct,
		formatUtc
	} from '$lib/uptime/model';

	let {
		target,
		view = undefined,
		days,
		nowMs
	}: { target: HeartbeatTarget; view?: UptimeTargetView; days: number; nowMs: number } = $props();

	const uid = $props.id();
	let cells = $derived(dayCells(target.buckets, days, nowMs));
	let unconfigured = $derived(target.status === 'unconfigured');
	let label = $derived(
		target.status === 'up'
			? 'up'
			: target.status === 'down'
				? 'down'
				: target.status === 'unknown'
					? 'no probe yet'
					: 'unconfigured'
	);
	let expected = $derived(view?.expected_status.join('/') ?? '');
	let windows = $derived(
		(['24h', '7d', '30d', '90d'] as const).map((w) => ({ w, v: target.uptime[w] }))
	);
</script>

<section
	class="card panel"
	aria-labelledby="t-{uid}"
	data-target={target.target}
	data-status={target.status}
>
	<header class="head">
		<span class="dot dot-{target.status}" aria-hidden="true"></span>
		<h2 id="t-{uid}" class="name">{target.name}</h2>
		<span class="chip status status-{target.status}">{label}</span>
		{#if unconfigured}
			<span class="url mono unconf">unconfigured — <TodoBadge value={target.url} /></span>
		{:else}
			<a class="url mono" href={target.url} rel="external noopener" target="_blank">{target.url}</a>
		{/if}
		<span class="spacer"></span>
		{#if view}
			<span class="probe mono" title="Probe settings from content/uptime.yaml"
				>{view.method} every {view.interval} · timeout {view.timeout}{#if expected}
					· expect {expected}{/if}</span
			>
		{/if}
	</header>

	{#if target.note}
		<p class="note" data-note>
			<span class="i" aria-hidden="true">i</span>
			<span>{target.note}</span>
		</p>
	{/if}

	<div class="body">
		{#if unconfigured}
			<p class="empty mono" role="status">
				Not probed: the url in content/uptime.yaml is a TODO(divy) placeholder, so the uptime
				collector skips this target. It stays grey until a real URL is filled in — never green.
			</p>
		{:else}
			<dl class="stats mono">
				<div>
					<dt>latency</dt>
					<dd>{formatLatency(target.last?.latency_ms)}</dd>
				</div>
				<div>
					<dt>last probe</dt>
					<dd>
						{#if target.last}
							<time datetime={target.last.ts} title={formatUtc(target.last.ts)}
								>{formatAgo(target.last.ts, nowMs)}</time
							>
						{:else}—{/if}
					</dd>
				</div>
				<div>
					<dt>http</dt>
					<dd>{target.last?.status_code ? target.last.status_code : '—'}</dd>
				</div>
				{#each windows as x (x.w)}
					<div class="win">
						<dt>{x.w}</dt>
						<dd
							class:none={x.v === null}
							title={x.v === null ? 'no probes in this window' : undefined}
						>
							{formatPct(x.v)}
						</dd>
					</div>
				{/each}
			</dl>
			{#if target.status === 'down' && target.last?.error}
				<p class="err mono" role="alert">
					<span class="chip cls">{errorClass(target.last.error)}</span>
					<span>{errorMessage(target.last.error)}</span>
				</p>
			{:else if target.status === 'unknown'}
				<p class="empty mono" role="status">
					Configured, but no probe has been recorded yet — the uptime collector has not run for this
					target.
				</p>
			{/if}
		{/if}

		<HeartbeatBar {cells} name={target.name} />

		{#if !unconfigured}
			<div class="incidents">
				<h3 class="h3 mono">Incidents · {days} d</h3>
				<IncidentTable incidents={target.incidents} name={target.name} />
			</div>
		{/if}

		{#if target.span}
			<p class="span mono">
				span <a href={spanTraceHref(target.span)} title="Open this span in the trace viewer"
					>{target.span}</a
				>
			</p>
		{/if}
	</div>
</section>

<style>
	.card {
		display: flex;
		flex-direction: column;
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.4rem 0.6rem;
		padding: 0.6rem 0.9rem;
		border-bottom: 1px solid var(--border);
	}
	.dot {
		width: 0.65rem;
		height: 0.65rem;
		border-radius: 50%;
		background: var(--panel-3);
		flex: none;
	}
	.dot-up {
		background: var(--green);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--green) 25%, transparent);
	}
	.dot-down {
		background: var(--red);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--red) 25%, transparent);
	}
	@media (prefers-reduced-motion: no-preference) {
		.dot-down {
			animation: blink 1.2s ease-in-out infinite;
		}
	}
	@keyframes blink {
		50% {
			box-shadow: 0 0 0 6px color-mix(in srgb, var(--red) 12%, transparent);
		}
	}
	.name {
		margin: 0;
		font-size: 0.95rem;
		font-weight: 600;
	}
	.status {
		font-weight: 600;
	}
	.status-up {
		border-color: color-mix(in srgb, var(--green) 55%, var(--border));
		color: var(--green);
	}
	.status-down {
		border-color: var(--red);
		background: color-mix(in srgb, var(--red) 18%, transparent);
		color: var(--red);
	}
	.status-unknown,
	.status-unconfigured {
		color: var(--fg-dim);
	}
	.url {
		font-size: 0.74rem;
		color: var(--fg-muted);
		overflow-wrap: anywhere;
		min-width: 0;
	}
	.unconf {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		color: var(--fg-dim);
	}
	.spacer {
		flex: 1;
	}
	.probe {
		font-size: 0.68rem;
		color: var(--fg-dim);
		white-space: nowrap;
	}
	.note {
		display: flex;
		gap: 0.5rem;
		align-items: flex-start;
		margin: 0;
		padding: 0.45rem 0.9rem;
		border-bottom: 1px solid var(--border);
		background: color-mix(in srgb, var(--blue) 8%, transparent);
		color: var(--fg-muted);
		font-size: 0.76rem;
	}
	.i {
		flex: none;
		width: 1rem;
		height: 1rem;
		border-radius: 50%;
		border: 1px solid var(--blue);
		color: var(--blue);
		font-size: 0.66rem;
		font-weight: 700;
		text-align: center;
		line-height: 0.9rem;
		font-family: var(--font-mono);
	}
	.body {
		display: flex;
		flex-direction: column;
		gap: 0.7rem;
		padding: 0.7rem 0.9rem 0.8rem;
	}
	.stats {
		display: flex;
		flex-wrap: wrap;
		gap: 0.4rem 1.4rem;
		margin: 0;
		font-size: 0.78rem;
	}
	.stats div {
		display: flex;
		flex-direction: column;
		gap: 0.1rem;
	}
	.stats dt {
		font-size: 0.64rem;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--fg-dim);
	}
	.stats dd {
		margin: 0;
		font-weight: 600;
		font-variant-numeric: tabular-nums;
	}
	.stats dd.none {
		color: var(--fg-dim);
		font-weight: 400;
	}
	.err {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.4rem;
		margin: 0;
		padding: 0.4rem 0.6rem;
		border: 1px solid color-mix(in srgb, var(--red) 45%, var(--border));
		border-radius: 4px;
		background: color-mix(in srgb, var(--red) 10%, transparent);
		font-size: 0.76rem;
		color: var(--fg);
		overflow-wrap: anywhere;
	}
	.cls {
		border-color: color-mix(in srgb, var(--red) 50%, var(--border));
		color: var(--red);
	}
	.empty {
		margin: 0;
		padding: 0.45rem 0.6rem;
		border: 1px dashed var(--border);
		border-radius: 4px;
		color: var(--fg-muted);
		font-size: 0.76rem;
	}
	.h3 {
		margin: 0 0 0.3rem;
		font-size: 0.68rem;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--fg-dim);
		font-weight: 600;
	}
	.span {
		margin: 0;
		font-size: 0.72rem;
		color: var(--fg-dim);
	}
	@media (max-width: 639.98px) {
		.head {
			padding: 0.55rem 0.7rem;
		}
		.probe {
			white-space: normal;
		}
		.body {
			padding: 0.6rem 0.7rem 0.7rem;
		}
		.stats {
			display: grid;
			grid-template-columns: repeat(3, minmax(0, 1fr));
			gap: 0.5rem 0.75rem;
		}
	}
</style>
