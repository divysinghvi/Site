<script lang="ts">
	// /postmortems: every incident report from the API, newest first.
	import { isTodo } from '$lib/api/client';
	import { formatContentDate, sortNewestFirst, spanTraceHref } from '$lib/postmortem';
	import SeverityBadge from '$lib/postmortem/SeverityBadge.svelte';
	import TodoBadge from '$lib/postmortem/TodoBadge.svelte';

	let { data } = $props();

	let items = $derived(sortNewestFirst(data.postmortems));
	let colorOf = $derived(
		(id: string) => data.services.find((s) => s.id === id)?.color ?? 'var(--fg-dim)'
	);
	let todoTotal = $derived(items.reduce((n, p) => n + p.todo_count, 0));
	let description = $derived(
		`${items.length} blameless incident report${items.length === 1 ? '' : 's'} (Summary, Impact, Timeline, Root cause, Detection, Resolution, Action items, Lessons), each linked to its span in the career trace.`
	);
	let canonical = $derived(data.siteOrigin ? `${data.siteOrigin}/postmortems` : '');
	let ogImage = $derived(data.siteOrigin ? `${data.siteOrigin}/og/default.png` : '');
</script>

<svelte:head>
	<title>Postmortems</title>
	<meta name="description" content={description} />
	<meta property="og:type" content="website" />
	<meta property="og:title" content="Postmortems · divy.dev" />
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
	{/if}
</svelte:head>

<section class="page" aria-labelledby="pm-title">
	<header class="head">
		<p class="kicker mono">
			<span class="chip">postmortems</span>
			<span>{items.length} reports</span>
			{#if todoTotal}
				<span class="dim">· {todoTotal} TODO(divy) markers to fill</span>
			{/if}
		</p>
		<h1 id="pm-title" class="title mono">Postmortems</h1>
		<p class="sub">
			Blameless incident reports, newest first. Every report keeps the same eight sections and links
			to its span in the <a href="/">career trace</a>. Source:
			<a href="/api/content/postmortems" rel="external" class="mono">/api/content/postmortems</a>.
		</p>
	</header>

	<ol class="list" aria-label="Incident reports">
		{#each items as pm (pm.id)}
			<li class="row panel" data-postmortem-id={pm.id}>
				<div class="row-head">
					<SeverityBadge severity={pm.severity} size="sm" />
					<a class="pm-title mono" href="/postmortems/{pm.id}"
						><span class="id">{pm.id}</span><span class="sep" aria-hidden="true">
							—
						</span>{pm.title}</a
					>
					<span class="chip status status-{pm.status}">{pm.status}</span>
				</div>
				<p class="summary">{pm.summary}</p>
				<dl class="meta mono">
					<div>
						<dt>date</dt>
						<dd>
							{#if isTodo(pm.date)}<TodoBadge value={pm.date} />{:else}{formatContentDate(
									pm.date
								)}{/if}
						</dd>
					</div>
					<div>
						<dt>duration</dt>
						<dd>
							{#if isTodo(pm.duration)}<TodoBadge value={pm.duration} />{:else}{pm.duration}{/if}
						</dd>
					</div>
					<div>
						<dt>services</dt>
						<dd class="chips">
							{#each pm.services as s (s)}
								<span class="chip svc" style="--c: {colorOf(s)}"
									><span class="dot" aria-hidden="true"></span>{s}</span
								>
							{/each}
						</dd>
					</div>
					<div>
						<dt>span</dt>
						<dd>
							<a href={spanTraceHref(pm.span)} title="Open this span in the trace viewer"
								>{pm.span}</a
							>
						</dd>
					</div>
					{#if pm.tags?.length}
						<div class="tags">
							<dt>tags</dt>
							<dd class="chips">
								{#each pm.tags as t (t)}<span class="chip">{t}</span>{/each}
							</dd>
						</div>
					{/if}
				</dl>
			</li>
		{/each}
	</ol>
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
		padding: 0.5rem 0.25rem 0;
	}
	.kicker {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem;
		margin: 0 0 0.25rem;
		font-size: 0.72rem;
		color: var(--fg-dim);
	}
	.dim {
		color: var(--fg-dim);
	}
	.title {
		margin: 0;
		font-size: clamp(1.4rem, 3.5vw, 2rem);
		font-weight: 700;
		letter-spacing: -0.01em;
	}
	.sub {
		margin: 0.35rem 0 0;
		max-width: 75ch;
		color: var(--fg-muted);
		font-size: 0.9rem;
	}
	.list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.6rem;
	}
	.row {
		padding: 0.75rem 0.9rem;
	}
	.row-head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem 0.65rem;
	}
	.pm-title {
		flex: 1 1 20rem;
		min-width: 0;
		font-size: 1rem;
		font-weight: 600;
		color: var(--fg);
		text-decoration: none;
		overflow-wrap: anywhere;
	}
	.pm-title:hover {
		color: var(--link);
		text-decoration: underline;
	}
	.id {
		color: var(--orange);
	}
	.sep {
		color: var(--fg-dim);
	}
	.status-resolved {
		border-color: color-mix(in srgb, var(--green) 50%, var(--border));
		color: var(--green);
	}
	.status-monitoring {
		border-color: color-mix(in srgb, var(--yellow) 50%, var(--border));
		color: var(--yellow);
	}
	.status-open {
		border-color: color-mix(in srgb, var(--red) 50%, var(--border));
		color: var(--red);
	}
	.summary {
		margin: 0.5rem 0 0;
		max-width: 80ch;
		color: var(--fg-muted);
		font-size: 0.88rem;
	}
	.meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem 1.4rem;
		margin: 0.6rem 0 0;
		font-size: 0.74rem;
	}
	.meta div {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		min-width: 0;
	}
	.meta dt {
		color: var(--fg-dim);
	}
	.meta dd {
		margin: 0;
		color: var(--fg);
		overflow-wrap: anywhere;
	}
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem;
	}
	.svc {
		border-color: color-mix(in srgb, var(--c) 55%, var(--border));
		color: var(--fg);
	}
	.dot {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 2px;
		background: var(--c);
	}
	.tags {
		flex-basis: 100%;
	}
	@media (max-width: 639.98px) {
		.row {
			padding: 0.65rem 0.7rem;
		}
		.meta {
			flex-direction: column;
			gap: 0.35rem;
		}
	}
</style>
