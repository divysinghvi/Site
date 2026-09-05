<script lang="ts">
	// /postmortems/[id]: the report as sanitized HTML from the API (heading ids
	// are fixed slugs), a sticky TOC with scroll-spy, the metadata header and
	// prev/next. Monospace-heavy on purpose (brief §3.5).
	import { isTodo } from '$lib/api/client';
	import { formatContentDate, neighbours, spanTraceHref } from '$lib/postmortem';
	import SeverityBadge from '$lib/postmortem/SeverityBadge.svelte';
	import Toc from '$lib/postmortem/Toc.svelte';
	import TodoBadge from '$lib/postmortem/TodoBadge.svelte';

	let { data } = $props();

	let pm = $derived(data.pm);
	let nav = $derived(neighbours(data.postmortems, pm.id));
	let colorOf = $derived(
		(id: string) => data.services.find((s) => s.id === id)?.color ?? 'var(--fg-dim)'
	);
	let title = $derived(`${pm.id} — ${pm.title}`);
	let canonical = $derived(data.siteOrigin ? `${data.siteOrigin}/postmortems/${pm.id}` : '');
	let apiHref = $derived(`/api/content/postmortems/${pm.id}`);
	let mdHref = $derived(`/api/content/postmortems/${pm.id}.md`);

	// Scroll-spy: the TOC highlights the last heading that has passed the
	// upper 40 % of the viewport (the last one when scrolled to the bottom).
	// Recomputed on scroll/resize, one rAF at a time; re-armed whenever a
	// different report renders into `doc` (prev/next reuses this component).
	let doc = $state<HTMLElement | undefined>(undefined);
	let active = $state('');
	$effect(() => {
		const el = doc;
		active = pm.toc[0]?.id ?? '';
		if (!el) return;
		// task-list checkboxes (goldmark `- [ ]`) carry no label: name them by their item text
		for (const box of el.querySelectorAll<HTMLInputElement>('li > input[type="checkbox"]')) {
			const text = box.parentElement?.textContent?.trim().replace(/\s+/g, ' ') ?? '';
			if (text && !box.getAttribute('aria-label'))
				box.setAttribute('aria-label', text.slice(0, 160));
		}
		const headings = Array.from(el.querySelectorAll<HTMLElement>('h2[id], h3[id]'));
		if (headings.length === 0) return;
		let raf = 0;
		const compute = () => {
			raf = 0;
			const root = document.documentElement;
			const scrollable = root.scrollHeight > window.innerHeight + 2;
			const atBottom = scrollable && window.innerHeight + window.scrollY >= root.scrollHeight - 2;
			let cur = headings[0]!;
			if (atBottom) cur = headings[headings.length - 1]!;
			else {
				const limit = 64 + window.innerHeight * 0.4;
				for (const h of headings) {
					if (h.getBoundingClientRect().top <= limit) cur = h;
					else break;
				}
			}
			active = cur.id;
		};
		const schedule = () => {
			if (!raf) raf = requestAnimationFrame(compute);
		};
		compute();
		window.addEventListener('scroll', schedule, { passive: true });
		window.addEventListener('resize', schedule);
		return () => {
			cancelAnimationFrame(raf);
			window.removeEventListener('scroll', schedule);
			window.removeEventListener('resize', schedule);
		};
	});
</script>

<svelte:head>
	<title>{title}</title>
	<meta name="description" content={pm.summary} />
	<meta property="og:type" content="article" />
	<meta property="og:title" content={title} />
	<meta property="og:description" content={pm.summary} />
	{#if canonical}
		<link rel="canonical" href={canonical} />
		<meta property="og:url" content={canonical} />
	{/if}
	<meta property="og:image" content={pm.og_image} />
	<meta property="og:image:width" content="1200" />
	<meta property="og:image:height" content="630" />
	<meta name="twitter:card" content="summary_large_image" />
	{#each pm.tags ?? [] as t (t)}
		<meta property="article:tag" content={t} />
	{/each}
</svelte:head>

<article class="pm" aria-labelledby="pm-title" data-postmortem-id={pm.id}>
	<header class="head">
		<p class="kicker mono">
			<a href="/postmortems">postmortems</a><span class="slash" aria-hidden="true">/</span><span
				class="id">{pm.id}</span
			>
		</p>
		<div class="titlebar">
			<SeverityBadge severity={pm.severity} size="lg" />
			<h1 id="pm-title" class="title mono">{pm.title}</h1>
		</div>
		<p class="summary">{pm.summary}</p>
		<dl class="meta mono">
			<div>
				<dt>Date</dt>
				<dd>
					{#if isTodo(pm.date)}<TodoBadge value={pm.date} />{:else}{formatContentDate(pm.date)}{/if}
				</dd>
			</div>
			<div>
				<dt>Duration</dt>
				<dd>
					{#if isTodo(pm.duration)}<TodoBadge value={pm.duration} />{:else}{pm.duration}{/if}
				</dd>
			</div>
			<div>
				<dt>Status</dt>
				<dd><span class="chip status status-{pm.status}">{pm.status}</span></dd>
			</div>
			<div>
				<dt>Services</dt>
				<dd class="chips">
					{#each pm.services as s (s)}
						<span class="chip svc" style="--c: {colorOf(s)}"
							><span class="dot" aria-hidden="true"></span>{s}</span
						>
					{/each}
				</dd>
			</div>
			<div>
				<dt>Span</dt>
				<dd class="chips">
					<a class="btn trace-link" href={spanTraceHref(pm.span)} data-span={pm.span}
						>View in trace <span aria-hidden="true">↗</span></a
					>
					<code class="spanid">{pm.span}</code>
				</dd>
			</div>
			{#if pm.tags?.length}
				<div class="wide">
					<dt>Tags</dt>
					<dd class="chips">
						{#each pm.tags as t (t)}<span class="chip">{t}</span>{/each}
					</dd>
				</div>
			{/if}
			<div class="wide">
				<dt>Open TODOs</dt>
				<dd>
					{#if pm.todo_count}
						<span class="todo-count"
							>{pm.todo_count} TODO(divy) marker{pm.todo_count === 1 ? '' : 's'}</span
						> in this report — facts still to be filled in, never guessed
					{:else}none{/if}
				</dd>
			</div>
		</dl>
	</header>

	<div class="layout">
		<Toc entries={pm.toc} {active} />
		<div class="doc mono" bind:this={doc}>
			<!-- eslint-disable-next-line svelte/no-at-html-tags -- sanitized by the API (bluemonday UGC + heading ids) -->
			{@html pm.html}
		</div>
	</div>

	<footer class="foot mono">
		<nav class="pager" aria-label="Other postmortems">
			{#if nav.prev}
				<a class="btn" href="/postmortems/{nav.prev.id}" rel="prev"
					><span aria-hidden="true">←</span> {nav.prev.id}</a
				>
			{:else}
				<span class="btn" aria-disabled="true">← none</span>
			{/if}
			<a class="btn" href="/postmortems">All postmortems</a>
			{#if nav.next}
				<a class="btn" href="/postmortems/{nav.next.id}" rel="next"
					>{nav.next.id} <span aria-hidden="true">→</span></a
				>
			{:else}
				<span class="btn" aria-disabled="true">none →</span>
			{/if}
		</nav>
		<p class="sources">
			source: <a href={mdHref} rel="external">{pm.id}.md</a> ·
			<a href={apiHref} rel="external">JSON</a> · rendered server-side, sanitized
		</p>
	</footer>
</article>

<style>
	.pm {
		display: flex;
		flex-direction: column;
		gap: 1rem;
		max-width: 1100px;
		margin: 0 auto;
	}
	.head {
		padding: 0.5rem 0.25rem 0;
	}
	.kicker {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		margin: 0 0 0.35rem;
		font-size: 0.74rem;
		color: var(--fg-dim);
	}
	.slash {
		color: var(--fg-dim);
	}
	.id {
		color: var(--orange);
		font-weight: 600;
	}
	.titlebar {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.6rem 0.8rem;
	}
	.title {
		margin: 0;
		font-size: clamp(1.25rem, 3vw, 1.75rem);
		font-weight: 700;
		letter-spacing: -0.01em;
		line-height: 1.25;
		overflow-wrap: anywhere;
	}
	.summary {
		margin: 0.6rem 0 0;
		max-width: 80ch;
		color: var(--fg-muted);
		font-size: 0.95rem;
	}
	.meta {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
		gap: 0.6rem 1.25rem;
		margin: 0.9rem 0 0;
		padding: 0.75rem 0.9rem;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--panel);
		font-size: 0.78rem;
	}
	.meta div {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		min-width: 0;
	}
	.meta .wide {
		grid-column: 1 / -1;
	}
	.meta dt {
		font-size: 0.66rem;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--fg-dim);
	}
	.meta dd {
		margin: 0;
		overflow-wrap: anywhere;
	}
	.chips {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.3rem;
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
	.trace-link {
		min-height: 1.75rem;
		font-size: 0.76rem;
		color: var(--fg);
		text-decoration: none;
	}
	.spanid {
		font-size: 0.74rem;
		color: var(--fg-muted);
	}
	.todo-count {
		color: var(--yellow);
	}

	.layout {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 1rem 2rem;
		align-items: start;
	}
	@media (min-width: 900px) {
		.layout {
			grid-template-columns: 13rem minmax(0, 1fr);
		}
	}

	/* ---- the document: monospace-heavy, ≤ 75ch prose, headings with a `##` marker ---- */
	.doc {
		max-width: 78ch;
		font-size: 0.84rem;
		line-height: 1.65;
		color: var(--fg);
	}
	.doc :global(h2) {
		margin: 2.25rem 0 0.75rem;
		padding-bottom: 0.35rem;
		border-bottom: 1px solid var(--border);
		font-size: 1.05rem;
		font-weight: 700;
		scroll-margin-top: calc(var(--nav-h) + 3.5rem);
	}
	.doc :global(h2)::before {
		content: '## ';
		color: var(--fg-dim);
	}
	.doc :global(h2:first-child) {
		margin-top: 0.25rem;
	}
	.doc :global(h3) {
		margin: 1.5rem 0 0.5rem;
		font-size: 0.92rem;
		font-weight: 700;
		scroll-margin-top: calc(var(--nav-h) + 3.5rem);
	}
	.doc :global(h3)::before {
		content: '### ';
		color: var(--fg-dim);
	}
	.doc :global(p) {
		margin: 0 0 0.9rem;
	}
	.doc :global(ul),
	.doc :global(ol) {
		margin: 0 0 0.9rem;
		padding-left: 1.4rem;
	}
	.doc :global(ul) {
		list-style: disc;
	}
	.doc :global(ol) {
		list-style: decimal;
	}
	.doc :global(li::marker) {
		color: var(--fg-dim);
	}
	.doc :global(li) {
		margin: 0.2rem 0;
	}
	.doc :global(li > input[type='checkbox']) {
		margin: 0 0.45rem 0 -1.4rem;
		accent-color: var(--green);
		vertical-align: -0.1em;
	}
	.doc :global(li:has(> input[type='checkbox'])) {
		list-style: none;
	}
	.doc :global(code) {
		padding: 0.05rem 0.3rem;
		border-radius: 3px;
		background: var(--panel-2);
		font-size: 0.95em;
	}
	.doc :global(pre) {
		margin: 0 0 0.9rem;
		padding: 0.6rem 0.75rem;
		overflow-x: auto;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: var(--panel);
		font-size: 0.8rem;
	}
	.doc :global(pre code) {
		padding: 0;
		background: none;
	}
	.doc :global(table) {
		display: block;
		max-width: 100%;
		overflow-x: auto;
		margin: 0 0 1rem;
		border-collapse: collapse;
		font-size: 0.78rem;
	}
	.doc :global(th),
	.doc :global(td) {
		padding: 0.35rem 0.6rem;
		border: 1px solid var(--border);
		text-align: left;
		vertical-align: top;
	}
	.doc :global(th) {
		background: var(--panel-2);
		color: var(--fg-muted);
		white-space: nowrap;
	}
	.doc :global(td:first-child) {
		white-space: nowrap;
		color: var(--fg-muted);
	}
	.doc :global(blockquote) {
		margin: 0 0 0.9rem;
		padding: 0.1rem 0.9rem;
		border-left: 3px solid var(--border);
		color: var(--fg-muted);
	}
	.doc :global(hr) {
		border: 0;
		border-top: 1px solid var(--border);
		margin: 1.5rem 0;
	}

	.foot {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem 1rem;
		padding-top: 1rem;
		border-top: 1px solid var(--border);
		font-size: 0.74rem;
		color: var(--fg-dim);
	}
	.pager {
		display: flex;
		flex-wrap: wrap;
		gap: 0.4rem;
	}
	.pager .btn {
		text-decoration: none;
		color: var(--fg);
	}
	.pager .btn[aria-disabled='true'] {
		opacity: 0.45;
	}
	.sources {
		margin: 0;
	}
	@media (max-width: 639.98px) {
		.meta {
			grid-template-columns: 1fr 1fr;
			padding: 0.6rem 0.7rem;
		}
		.doc {
			font-size: 0.82rem;
		}
	}
</style>
