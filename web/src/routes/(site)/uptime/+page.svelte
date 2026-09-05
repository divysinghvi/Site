<script lang="ts">
	// /uptime: Uptime-Kuma-style status page over /api/uptime. Honest by
	// construction: a target is green only after a successful probe, red when
	// its last probe failed, grey when unconfigured (TODO url) or not yet
	// probed; days without probes are grey cells; the banner excludes
	// unconfigured targets and lists them.
	import { onMount } from 'svelte';
	import { api } from '$lib/api/client';
	import type { HeartbeatTarget, Readyz, UptimeHeartbeats } from '$lib/api/types.gen';
	import { collectorStatus, fetchReadyz } from '$lib/panels/model';
	import { contentFor, formatAgo, overallStatus } from '$lib/uptime/model';
	import TargetCard from '$lib/uptime/TargetCard.svelte';

	let { data } = $props();

	const REFRESH_MS = 60_000;
	const DAYS = 90;

	let live = $state<UptimeHeartbeats | null>(null);
	let fetchedAt = $state<number | null>(null);
	let error = $state('');
	let auto = $state(true);
	let readyz = $state<Readyz | null>(null);
	let busy = $state(false);

	let hb = $derived(live ?? data.snapshot);
	// The clock the cells and "ago" labels are measured against: the live
	// fetch time, else the snapshot's generation time (deterministic HTML).
	let nowMs = $derived(fetchedAt ?? (hb ? Date.parse(hb.generated_at) : 0));

	// Without any heartbeat payload (API unreachable at build and in the
	// browser) the content targets still render — as unknown/unconfigured.
	let targets = $derived<HeartbeatTarget[]>(
		hb
			? hb.targets
			: data.targets.map((v) => ({
					target: v.id,
					name: v.name,
					url: v.url,
					span: v.span,
					note: v.note,
					status: v.configured ? 'unknown' : 'unconfigured',
					last: null,
					uptime: { '24h': null, '7d': null, '30d': null, '90d': null },
					buckets: [],
					incidents: []
				}))
	);
	let overall = $derived(overallStatus(targets));
	let collector = $derived(collectorStatus('uptime', readyz));
	let storage = $derived(readyz?.checks.db.storage);

	async function refresh() {
		if (busy) return;
		busy = true;
		try {
			live = await api.uptime.heartbeats(DAYS, '1d');
			fetchedAt = Date.now();
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			busy = false;
		}
		readyz = await fetchReadyz(true);
	}

	onMount(() => {
		void refresh();
	});

	$effect(() => {
		if (!auto) return;
		const tick = () => {
			if (document.visibilityState === 'visible') void refresh();
		};
		const t = setInterval(tick, REFRESH_MS);
		document.addEventListener('visibilitychange', tick);
		return () => {
			clearInterval(t);
			document.removeEventListener('visibilitychange', tick);
		};
	});

	let description = $derived(
		`${overall.monitored} monitored target${overall.monitored === 1 ? '' : 's'} probed every 5 minutes: ${DAYS}-day heartbeat bars, latency, uptime and incident history. ${overall.text}.`
	);
	let canonical = $derived(data.siteOrigin ? `${data.siteOrigin}/uptime` : '');
</script>

<svelte:head>
	<title>Uptime</title>
	<meta name="description" content={description} />
	<meta property="og:type" content="website" />
	<meta property="og:title" content="Uptime · divy.dev" />
	<meta property="og:description" content={description} />
	{#if canonical}
		<link rel="canonical" href={canonical} />
		<meta property="og:url" content={canonical} />
	{/if}
	<meta name="twitter:card" content="summary" />
</svelte:head>

<section class="page" aria-labelledby="up-title">
	<header class="head">
		<div class="head-text">
			<p class="kicker mono"><span class="chip">status</span><span>{DAYS}-day window</span></p>
			<h1 id="up-title" class="title mono">Uptime</h1>
		</div>
		<div class="tools">
			<p class="live mono" aria-live="polite">
				{#if live && fetchedAt}
					<span class="ok">●</span> live · refreshed {new Date(fetchedAt)
						.toISOString()
						.slice(11, 19)}Z
				{:else if error}
					<span class="warn">●</span> snapshot from build time · live refresh failed: {error}
				{:else if hb}
					<span class="dim">●</span> snapshot from build time ({hb.generated_at})
				{:else}
					<span class="warn">●</span> no uptime data (/api/uptime unreachable)
				{/if}
			</p>
			<div class="refresh">
				<label class="auto mono"
					><input type="checkbox" bind:checked={auto} /> auto-refresh {REFRESH_MS / 1000} s</label
				>
				<button type="button" class="btn" onclick={refresh} disabled={busy}>Refresh</button>
			</div>
		</div>
	</header>

	<div
		class="banner banner-{overall.level}"
		role="status"
		aria-live="polite"
		data-overall={overall.level}
	>
		<span class="bdot" aria-hidden="true"></span>
		<strong class="btext">{overall.text}</strong>
		<span class="bdetail mono">
			{overall.up.length} up · {overall.down.length} down · {overall.unknown.length} no data
			{#if overall.unconfigured.length}
				· {overall.unconfigured.length} unconfigured, excluded:
				{overall.unconfigured.map((t) => t.target).join(', ')}
			{/if}
		</span>
	</div>

	<p class="collector mono" data-collector-state={collector.state}>
		{#if readyz}
			{collector.text}{#if storage}
				· storage: {storage}{/if}
		{:else}
			uptime collector status: unknown until /readyz answers
		{/if}
		· probes every 5 min via the collector; days without probes are grey
	</p>

	<div class="legend mono" aria-label="Heartbeat legend">
		<span><i class="sw sw-up"></i> all probes up</span>
		<span><i class="sw sw-partial"></i> partial</span>
		<span><i class="sw sw-down"></i> all down</span>
		<span><i class="sw sw-none"></i> no probes</span>
	</div>

	<div class="targets">
		{#each targets as t (t.target)}
			<TargetCard target={t} view={contentFor(data.targets, t.target)} days={DAYS} {nowMs} />
		{/each}
	</div>

	{#if hb}
		<p class="foot mono">
			generated {hb.generated_at}
			{#if fetchedAt}({formatAgo(hb.generated_at, fetchedAt)}){/if} · source
			<a href="/api/uptime" rel="external">/api/uptime</a>
		</p>
	{/if}
</section>

<style>
	.page {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
		max-width: 1100px;
		margin: 0 auto;
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		justify-content: space-between;
		align-items: flex-end;
		gap: 0.5rem 1.5rem;
		padding: 0.5rem 0.25rem 0;
	}
	.kicker {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		margin: 0 0 0.25rem;
		font-size: 0.72rem;
		color: var(--fg-dim);
	}
	.title {
		margin: 0;
		font-size: clamp(1.4rem, 3.5vw, 2rem);
		font-weight: 700;
		letter-spacing: -0.01em;
	}
	.tools {
		display: flex;
		flex-direction: column;
		align-items: flex-end;
		gap: 0.35rem;
	}
	.live {
		margin: 0;
		font-size: 0.7rem;
		color: var(--fg-dim);
		text-align: right;
	}
	.ok {
		color: var(--green);
	}
	.warn {
		color: var(--yellow);
	}
	.dim {
		color: var(--fg-dim);
	}
	.refresh {
		display: flex;
		align-items: center;
		gap: 0.6rem;
	}
	.auto {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		font-size: 0.74rem;
		color: var(--fg-muted);
		cursor: pointer;
	}
	.auto input {
		accent-color: var(--green);
		width: 1rem;
		height: 1rem;
	}

	.banner {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem 0.9rem;
		padding: 0.7rem 0.9rem;
		border: 1px solid var(--c);
		border-radius: 6px;
		background: color-mix(in srgb, var(--c) 12%, var(--panel));
		--c: var(--fg-dim);
	}
	.banner-ok {
		--c: var(--green);
	}
	.banner-degraded {
		--c: var(--red);
	}
	.banner-partial {
		--c: var(--yellow);
	}
	.bdot {
		width: 0.75rem;
		height: 0.75rem;
		border-radius: 50%;
		background: var(--c);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--c) 25%, transparent);
	}
	.btext {
		font-size: 1rem;
		color: var(--fg);
	}
	.bdetail {
		font-size: 0.74rem;
		color: var(--fg-muted);
		overflow-wrap: anywhere;
	}
	.collector {
		margin: 0;
		font-size: 0.72rem;
		color: var(--fg-dim);
		overflow-wrap: anywhere;
	}
	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem 1rem;
		font-size: 0.7rem;
		color: var(--fg-dim);
	}
	.legend span {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
	}
	.sw {
		display: inline-block;
		width: 0.7rem;
		height: 0.7rem;
		border-radius: 2px;
		background: var(--panel-3);
	}
	.sw-up {
		background: var(--green);
	}
	.sw-partial {
		background: var(--orange);
	}
	.sw-down {
		background: var(--red);
	}
	.targets {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.foot {
		margin: 0;
		font-size: 0.7rem;
		color: var(--fg-dim);
	}
	@media (max-width: 639.98px) {
		.head {
			align-items: stretch;
		}
		.tools {
			align-items: stretch;
		}
		.live {
			text-align: left;
		}
		.refresh {
			justify-content: space-between;
		}
	}
</style>
