<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api/client';
	import type { PostmortemSummary } from '$lib/api/types.gen';
	import TraceViewer from '$lib/components/trace/TraceViewer.svelte';
	import TraceIdBox from '$lib/components/trace/TraceIdBox.svelte';
	import Seo from '$lib/components/ui/Seo.svelte';

	let { data } = $props();

	// Postmortem titles for the drawer's links (best effort; this route has no layout data).
	let postmortems = $state<PostmortemSummary[]>([]);
	onMount(async () => {
		try {
			postmortems = (await api.content.postmortems()).items;
		} catch {
			postmortems = [];
		}
	});

	let title = $derived(`Trace ${data.id}`);
</script>

<Seo
	{title}
	description="A trace in the site's Jaeger-shaped API: the career trace, or a request's own span from its X-Divy-Trace-Id."
	path="/trace/{data.id}"
	noindex
/>

<section class="page" aria-labelledby="trace-title">
	<div class="head">
		<div>
			<p class="kicker mono"><span class="chip">trace</span></p>
			<h1 id="trace-title" class="title mono">{data.id}</h1>
		</div>
		<TraceIdBox value={data.id} />
	</div>

	{#if data.trace}
		<TraceViewer trace={data.trace} {postmortems} />
	{:else if data.error}
		<div class="panel notfound" role="alert">
			<div class="panel-header">
				<span class="mono">{data.error.status || 'network'}</span>
				<span>trace not available</span>
			</div>
			<div class="body">
				<p class="mono msg">{data.error.message}</p>
				{#if data.error.status === 404}
					<p class="hint">
						Self-traces are sampled: a response whose <code>X-Divy-Trace-Sampled</code> header is
						<code>0</code> was not recorded and cannot be opened. Recorded ones are kept for 24
						hours. The career trace is always at <a href="/trace/career">/trace/career</a>.
					</p>
				{/if}
				{#if data.error.traceId}
					<p class="hint mono">
						this request's trace id: <a href="/trace/{data.error.traceId}">{data.error.traceId}</a>
					</p>
				{/if}
			</div>
		</div>
	{/if}
</section>

<style>
	.page {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		justify-content: space-between;
		align-items: flex-end;
		gap: 0.75rem 1.5rem;
		padding: 0.5rem 0.25rem 0;
	}
	.kicker {
		margin: 0 0 0.25rem;
		font-size: 0.72rem;
	}
	.title {
		margin: 0;
		font-size: clamp(1.1rem, 3vw, 1.6rem);
		font-weight: 700;
		overflow-wrap: anywhere;
	}
	.notfound .body {
		padding: 1rem;
		font-size: 0.85rem;
	}
	.msg {
		color: var(--red);
		margin: 0;
	}
	.hint {
		margin: 0.75rem 0 0;
		color: var(--fg-muted);
	}
	@media (max-width: 639.98px) {
		.head {
			align-items: stretch;
		}
	}
</style>
