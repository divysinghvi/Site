<script lang="ts">
	// Jaeger-like span detail: operation, service, start, duration, tags,
	// events (logs), links (postmortems), TODOs and the process. A right-hand
	// panel on desktop, a bottom sheet under 640 px. Esc closes; focus is
	// trapped inside and restored to the row by the viewer.
	import { onMount } from 'svelte';
	import type { PostmortemSummary } from '$lib/api/types.gen';
	import type { TraceModel, TraceNode } from '$lib/trace/model';
	import { isTodo, tagStringList, tagValueText } from '$lib/api/client';
	import { formatDateAt, formatDuration, formatTimestamp, formatOffset } from '$lib/format';

	let {
		node,
		model,
		postmortems,
		narrow,
		onclose,
		onnavigate
	}: {
		node: TraceNode;
		model: TraceModel;
		postmortems: ReadonlyMap<string, PostmortemSummary>;
		narrow: boolean;
		onclose: () => void;
		onnavigate: (node: TraceNode) => void;
	} = $props();

	let dialog = $state<HTMLElement | null>(null);
	let heading = $state<HTMLElement | null>(null);
	let parent = $derived(node.parentId ? model.byId.get(node.parentId) : undefined);
	let process = $derived(model.services.find((s) => s.processID === node.processID));
	let startLabel = $derived(formatDateAt(node.startRaw, node.startPrecision, node.startUs));
	let endLabel = $derived(
		node.open && node.plannedEndUs === undefined
			? 'now'
			: formatDateAt(node.endRaw, node.endPrecision, node.endUs)
	);
	let liveDuration = $derived(formatDuration(node.endUs - node.startUs));
	let relStart = $derived(node.startUs - model.startUs);
	let links = $derived(node.links.filter((l) => l.kind !== 'postmortem'));

	onMount(() => {
		heading?.focus({ preventScroll: true });
	});

	// Re-focus the heading when the shown span changes (j/k while open).
	$effect(() => {
		void node.id;
		heading?.focus({ preventScroll: true });
	});

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.preventDefault();
			e.stopPropagation();
			onclose();
			return;
		}
		if (e.key !== 'Tab' || !dialog) return;
		const items = Array.from(
			dialog.querySelectorAll<HTMLElement>(
				'a[href], button:not([disabled]), input, select, textarea, [tabindex]:not([tabindex="-1"])'
			)
		).filter((el) => el.offsetParent !== null);
		if (items.length === 0) return;
		const first = items[0]!;
		const last = items[items.length - 1]!;
		if (e.shiftKey && (document.activeElement === first || document.activeElement === heading)) {
			e.preventDefault();
			last.focus();
		} else if (!e.shiftKey && document.activeElement === last) {
			e.preventDefault();
			first.focus();
		}
	}

	function valueChips(v: unknown): string[] | undefined {
		return tagStringList(v);
	}
</script>

<div
	bind:this={dialog}
	class="drawer {narrow ? 'sheet slide-up' : 'side slide-in'}"
	role="dialog"
	tabindex="-1"
	aria-modal="false"
	aria-labelledby="drawer-title"
	data-keyboard-ignore
	{onkeydown}
	style="--c: {node.color}"
>
	<header class="hdr">
		<div class="hdr-main">
			<span class="swatch" aria-hidden="true"></span>
			<span class="svc">{node.service}</span>
			<span class="sep" aria-hidden="true">·</span>
			<span class="depth">depth {node.depth}</span>
			{#if node.error}<span class="chip chip-error">error</span>{/if}
			{#if node.open}<span class="chip">open</span>{/if}
			{#if node.todoDates}<span class="chip">date TODO</span>{/if}
		</div>
		<button type="button" class="btn close" onclick={onclose} aria-label="Close span details">
			<svg
				aria-hidden="true"
				width="14"
				height="14"
				viewBox="0 0 24 24"
				fill="none"
				stroke="currentColor"
				stroke-width="2.5"
				stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg
			>
		</button>
	</header>
	<h2 id="drawer-title" class="title mono" bind:this={heading} tabindex="-1">{node.name}</h2>
	{#if node.title !== node.name}
		<p class="subtitle">{node.title}</p>
	{/if}

	<dl class="meta">
		<dt>Service</dt>
		<dd>
			{node.serviceTitle}{#if node.serviceTitle !== node.service}<span class="mono dim">
					({node.service})</span
				>{/if}
		</dd>
		<dt>Start</dt>
		<dd>
			{startLabel}
			{#if node.startPrecision === 'exact'}<span class="dim mono">
					{formatOffset(relStart)}</span
				>{/if}
		</dd>
		<dt>End</dt>
		<dd>
			{endLabel}
			{#if node.open && node.plannedEndUs !== undefined}<span class="dim"> (planned)</span>{/if}
		</dd>
		<dt>Duration</dt>
		<dd class="mono">
			{liveDuration}
			{#if node.open}<span class="dim"> and counting</span>{/if}
			{#if node.todoDates}<span class="todo">
					(dates TODO: interval inherited from the parent)</span
				>{/if}
		</dd>
		{#if node.statusCode}
			<dt>Status</dt>
			<dd class="mono" class:red={node.error}>{node.statusCode}</dd>
		{/if}
		{#if parent}
			<dt>Parent</dt>
			<dd>
				<button type="button" class="linkish mono" onclick={() => onnavigate(parent)}
					>{parent.name}</button
				>
			</dd>
		{/if}
		{#if node.children.length}
			<dt>Children</dt>
			<dd>{node.children.length}</dd>
		{/if}
	</dl>

	{#if node.postmortems.length || links.length}
		<section class="sec" aria-labelledby="sec-links">
			<h3 id="sec-links">Links</h3>
			<ul class="links">
				{#each node.postmortems as id (id)}
					{@const pm = postmortems.get(id)}
					<li>
						<a href="/postmortems/{id}" class="pm">
							<span class="chip chip-sev">{pm?.severity ?? 'postmortem'}</span>
							<span class="mono">{id}</span>
							{#if pm}<span class="pm-title">{pm.title}</span>{/if}
						</a>
					</li>
				{/each}
				{#each links as l, i (i)}
					<li>
						{#if l.url && !isTodo(l.url)}
							<a href={l.url} rel="external noopener" target="_blank">
								<span class="chip">{l.kind}</span>
								{l.label ?? l.url}
							</a>
						{:else}
							<span class="dim"
								><span class="chip">{l.kind}</span>
								{l.label ?? l.kind}: <span class="todo mono">{l.url ?? 'TODO(divy)'}</span></span
							>
						{/if}
					</li>
				{/each}
			</ul>
		</section>
	{/if}

	<section class="sec" aria-labelledby="sec-tags">
		<h3 id="sec-tags">Tags <span class="dim">({node.tags.length})</span></h3>
		{#if node.tags.length}
			<table class="kv">
				<tbody>
					{#each node.tags as t (t.key)}
						{@const chips = valueChips(t.value)}
						<tr>
							<th scope="row" class="mono">{t.key}</th>
							<td>
								{#if chips}
									<span class="chips">
										{#each chips as c (c)}<span class="chip">{c}</span>{/each}
									</span>
								{:else if typeof t.value === 'string' && isTodo(t.value)}
									<span class="todo mono">{t.value}</span>
								{:else}
									<span class="mono val">{tagValueText(t.value)}</span>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		{:else}
			<p class="dim">No tags.</p>
		{/if}
	</section>

	<section class="sec" aria-labelledby="sec-events">
		<h3 id="sec-events">Events <span class="dim">({node.events.length})</span></h3>
		{#if node.events.length}
			<ol class="events">
				{#each node.events as ev, i (i)}
					<li>
						<div class="ev-head">
							<span class="ev-ts mono">
								{#if ev.todo}
									<span class="todo">TODO(divy)</span>
								{:else if node.startPrecision === 'exact'}
									{formatOffset(ev.us - model.startUs)}
								{:else}
									{formatDateAt(undefined, 'day', ev.us)}
								{/if}
							</span>
							<span class="ev-name">{ev.name}</span>
						</div>
						{#if ev.fields.length}
							<table class="kv small">
								<tbody>
									{#each ev.fields as f (f.key)}
										<tr>
											<th scope="row" class="mono">{f.key}</th>
											<td class="mono val">{tagValueText(f.value)}</td>
										</tr>
									{/each}
								</tbody>
							</table>
						{/if}
					</li>
				{/each}
			</ol>
		{:else}
			<p class="dim">No events.</p>
		{/if}
	</section>

	{#if node.todos.length}
		<section class="sec" aria-labelledby="sec-todo">
			<h3 id="sec-todo">Open TODOs <span class="dim">({node.todos.length})</span></h3>
			<ul class="todos">
				{#each node.todos as t, i (i)}
					<li class="todo mono">{t}</li>
				{/each}
			</ul>
		</section>
	{/if}

	<section class="sec" aria-labelledby="sec-process">
		<h3 id="sec-process">Process</h3>
		<table class="kv">
			<tbody>
				<tr
					><th scope="row" class="mono">serviceName</th><td class="mono val">{node.service}</td></tr
				>
				{#if process}
					<tr><th scope="row" class="mono">title</th><td class="val">{process.title}</td></tr>
					<tr
						><th scope="row" class="mono">color</th><td class="mono val"
							><span class="swatch inline" aria-hidden="true"></span>{process.color}</td
						></tr
					>
				{/if}
				{#each node.meta as m (m.key)}
					<tr
						><th scope="row" class="mono">{m.key}</th><td class="mono val"
							>{tagValueText(m.value)}</td
						></tr
					>
				{/each}
			</tbody>
		</table>
	</section>

	<footer class="foot mono">
		<div><span class="dim">span</span> {node.id}</div>
		<div><span class="dim">trace</span> {model.traceID}</div>
		<div><span class="dim">start</span> {formatTimestamp(node.startUs)}</div>
		<a href="/api/traces/{model.traceID}" rel="external">JSON ↗</a>
	</footer>
</div>

<style>
	.drawer {
		position: fixed;
		z-index: 40;
		background: var(--panel);
		border-left: 1px solid var(--border);
		box-shadow: -8px 0 24px rgba(0, 0, 0, 0.25);
		overflow-y: auto;
		overscroll-behavior: contain;
		padding: 0.75rem 1rem 1.25rem;
		font-size: 0.8rem;
		display: flex;
		flex-direction: column;
		gap: 0.6rem;
	}
	.side {
		top: var(--nav-h, 0px);
		right: 0;
		bottom: 0;
		width: min(480px, 100vw);
	}
	.sheet {
		left: 0;
		right: 0;
		bottom: 0;
		max-height: 82dvh;
		border-left: 0;
		border-top: 1px solid var(--border);
		border-radius: 12px 12px 0 0;
		padding-bottom: calc(1.25rem + env(safe-area-inset-bottom));
	}
	.hdr {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}
	.hdr-main {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		flex-wrap: wrap;
		color: var(--fg-muted);
		font-size: 0.75rem;
	}
	.swatch {
		width: 0.7rem;
		height: 0.7rem;
		border-radius: 2px;
		background: var(--c);
		display: inline-block;
	}
	.swatch.inline {
		width: 0.6rem;
		height: 0.6rem;
		margin-right: 0.35rem;
		vertical-align: middle;
	}
	.close {
		flex: none;
	}
	.title {
		font-size: 1rem;
		font-weight: 600;
		margin: 0;
		overflow-wrap: anywhere;
	}
	.subtitle {
		margin: -0.35rem 0 0;
		color: var(--fg-muted);
	}
	.meta {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: 0.25rem 0.75rem;
		margin: 0;
		padding: 0.5rem 0;
		border-top: 1px solid var(--border);
		border-bottom: 1px solid var(--border);
	}
	.meta dt {
		color: var(--fg-dim);
	}
	.meta dd {
		margin: 0;
		overflow-wrap: anywhere;
	}
	.sec h3 {
		margin: 0 0 0.35rem;
		font-size: 0.72rem;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--fg-dim);
	}
	.kv {
		width: 100%;
		border-collapse: collapse;
	}
	.kv th,
	.kv td {
		vertical-align: top;
		padding: 0.2rem 0.4rem 0.2rem 0;
		border-bottom: 1px solid color-mix(in srgb, var(--border) 60%, transparent);
		text-align: left;
		font-weight: 400;
	}
	.kv th {
		color: var(--fg-muted);
		width: 34%;
		overflow-wrap: anywhere;
	}
	.kv.small th,
	.kv.small td {
		font-size: 0.72rem;
		padding: 0.1rem 0.4rem 0.1rem 0;
	}
	.val {
		overflow-wrap: anywhere;
		white-space: pre-wrap;
	}
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem;
	}
	.links {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
	}
	.links a {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		flex-wrap: wrap;
	}
	.pm-title {
		color: var(--fg);
	}
	.chip-sev {
		border-color: var(--red);
		color: var(--red);
	}
	.chip-error {
		border-color: var(--red);
		color: var(--red);
	}
	.events {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}
	.ev-head {
		display: flex;
		gap: 0.6rem;
		align-items: baseline;
	}
	.ev-ts {
		flex: none;
		color: var(--fg-muted);
		font-size: 0.72rem;
	}
	.ev-name::before {
		content: '◆ ';
		color: var(--fg-muted);
	}
	.todos {
		margin: 0;
		padding-left: 1rem;
	}
	.todo {
		color: var(--yellow);
	}
	.dim {
		color: var(--fg-dim);
	}
	.red {
		color: var(--red);
	}
	.linkish {
		background: none;
		border: 0;
		padding: 0;
		color: var(--link);
		cursor: pointer;
		font-size: inherit;
	}
	.linkish:hover {
		text-decoration: underline;
	}
	.foot {
		margin-top: auto;
		padding-top: 0.5rem;
		border-top: 1px solid var(--border);
		font-size: 0.68rem;
		color: var(--fg-muted);
		display: flex;
		flex-direction: column;
		gap: 0.15rem;
		overflow-wrap: anywhere;
	}
</style>
