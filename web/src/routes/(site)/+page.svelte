<script lang="ts">
	import { onMount } from 'svelte';
	import { api, isTodo, tagString } from '$lib/api/client';
	import { formatDuration, formatInt } from '$lib/format';
	import { buildTrace, snapshotNow } from '$lib/trace/model';
	import TraceViewer from '$lib/components/trace/TraceViewer.svelte';
	import TraceIdBox from '$lib/components/trace/TraceIdBox.svelte';

	let { data } = $props();

	// Build-time snapshot first; replaced by one live fetch after hydration.
	// svelte-ignore state_referenced_locally
	let trace = $state(data.trace);
	let live = $state(false);
	let liveError = $state('');

	onMount(async () => {
		try {
			const res = await api.trace('career');
			if (res.data[0]) {
				trace = res.data[0];
				live = true;
			}
		} catch (e) {
			liveError = e instanceof Error ? e.message : String(e);
		}
	});

	let model = $derived(buildTrace(trace, snapshotNow(trace)));
	let root = $derived(model.roots[0]);
	let rootSpan = $derived(trace.spans.find((s) => s.spanID === root?.id));
	let title = $derived(tagString(rootSpan?.tags, 'divy.title') ?? root?.name ?? trace.traceID);
	let tagline = $derived(isTodo(data.profile.tagline) ? '' : (data.profile.tagline ?? ''));
	let description = $derived(
		[
			tagline,
			`${formatInt(model.nodes.length)} spans across ${model.services.length} services` +
				(root
					? `, ${formatDuration(root.endUs - root.startUs)}${root.open ? ' and counting' : ''}`
					: '') +
				'.'
		]
			.filter(Boolean)
			.join(' ')
	);
	let pageTitle = $derived(tagline ? `${title} · ${tagline}` : title);
	let ogImage = $derived(data.siteOrigin ? `${data.siteOrigin}/og/default.png` : '');
	let canonical = $derived(data.siteOrigin ? `${data.siteOrigin}/` : '');
</script>

<svelte:head>
	<title>{pageTitle}</title>
	<meta name="description" content={description} />
	<meta property="og:type" content="website" />
	<meta property="og:title" content={pageTitle} />
	<meta property="og:description" content={description} />
	{#if canonical}
		<link rel="canonical" href={canonical} />
		<meta property="og:url" content={canonical} />
	{/if}
	{#if ogImage}
		<meta property="og:image" content={ogImage} />
		<meta property="og:image:width" content="1200" />
		<meta property="og:image:height" content="630" />
		<meta name="twitter:card" content="summary_large_image" />
	{:else}
		<meta name="twitter:card" content="summary" />
	{/if}
</svelte:head>

<section class="hero" aria-labelledby="hero-title">
	<div class="hero-head">
		<div class="hero-text">
			<p class="kicker mono">
				<span class="chip">trace</span>
				<span class="tid">{trace.traceID}</span>
			</p>
			<h1 id="hero-title" class="hero-title mono">{root?.name ?? trace.traceID}</h1>
			<p class="hero-sub">
				{title}{#if data.profile.open_to_work}<span class="chip chip-open" title="from /healthz"
						>open to work</span
					>{/if}
			</p>
		</div>
		<div class="hero-tools">
			<TraceIdBox />
			<p class="live mono" aria-live="polite">
				{#if live}
					<span class="ok">●</span> live from /api/traces/career
				{:else if liveError}
					<span class="warn">●</span> snapshot from build time · live refresh failed: {liveError}
				{:else}
					<span class="dim">●</span> snapshot from build time
				{/if}
			</p>
		</div>
	</div>

	<TraceViewer {trace} postmortems={data.postmortems} />
</section>

<style>
	.hero {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.hero-head {
		display: flex;
		flex-wrap: wrap;
		justify-content: space-between;
		align-items: flex-end;
		gap: 0.75rem 1.5rem;
		padding: 0.5rem 0.25rem 0;
	}
	.hero-text {
		min-width: 0;
	}
	.kicker {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		margin: 0 0 0.25rem;
		font-size: 0.72rem;
		color: var(--fg-dim);
	}
	.tid {
		overflow-wrap: anywhere;
	}
	.hero-title {
		margin: 0;
		font-size: clamp(1.4rem, 3.5vw, 2.1rem);
		font-weight: 700;
		letter-spacing: -0.01em;
		line-height: 1.15;
	}
	.hero-sub {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem;
		margin: 0.25rem 0 0;
		color: var(--fg-muted);
		font-size: 0.95rem;
	}
	.chip-open {
		border-color: var(--green);
		color: var(--green);
	}
	.hero-tools {
		display: flex;
		flex-direction: column;
		align-items: flex-end;
		gap: 0.35rem;
		min-width: 0;
		max-width: 100%;
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
	@media (max-width: 639.98px) {
		.hero-head {
			align-items: stretch;
		}
		.hero-tools {
			align-items: stretch;
		}
		.live {
			text-align: left;
		}
	}
</style>
