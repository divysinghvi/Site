<script lang="ts">
	// Sticky table of contents built from the API's toc[] (heading ids are
	// fixed slugs). `active` is the id the page's scroll-spy reports. Wide
	// screens get a sticky sidebar; below 900 px the same list collapses into
	// a <details> bar pinned under the top bar (repo §R.9.4).
	import { onMount } from 'svelte';
	import type { TOCEntry } from '$lib/api/types.gen';

	let { entries, active = '' }: { entries: TOCEntry[]; active?: string } = $props();

	// Both variants are in the static HTML (CSS picks one); after mount only
	// the matching one renders.
	let hydrated = $state(false);
	let wide = $state(false);
	onMount(() => {
		const mq = window.matchMedia('(min-width: 900px)');
		wide = mq.matches;
		const on = (e: MediaQueryListEvent) => (wide = e.matches);
		mq.addEventListener('change', on);
		hydrated = true;
		return () => mq.removeEventListener('change', on);
	});

	let detailsOpen = $state(false);
</script>

{#snippet list()}
	<ol class="list mono">
		{#each entries as e (e.id)}
			<li class="item" class:sub={e.level > 2} class:active={active === e.id}>
				<a
					href="#{e.id}"
					aria-current={active === e.id ? 'location' : undefined}
					onclick={() => (detailsOpen = false)}>{e.text}</a
				>
			</li>
		{/each}
	</ol>
{/snippet}

{#if !hydrated || wide}
	<nav class="toc toc-wide" class:hidden-narrow={!hydrated} aria-label="Contents">
		<p class="label">Contents</p>
		{@render list()}
	</nav>
{/if}
{#if !hydrated || !wide}
	<nav class="toc toc-narrow" class:hidden-wide={!hydrated} aria-label="Contents">
		<details bind:open={detailsOpen}>
			<summary class="mono"
				>Contents{#if active}<span class="cur"
						>· {entries.find((e) => e.id === active)?.text ?? ''}</span
					>{/if}</summary
			>
			{@render list()}
		</details>
	</nav>
{/if}

<style>
	.toc {
		font-size: 0.78rem;
	}
	.toc-wide {
		position: sticky;
		top: calc(var(--nav-h) + 0.75rem);
		align-self: start;
		max-height: calc(100dvh - var(--nav-h) - 1.5rem);
		overflow: auto;
		padding: 0.5rem 0.75rem;
		border-left: 2px solid var(--border);
	}
	.label {
		margin: 0 0 0.4rem;
		font-size: 0.68rem;
		font-weight: 600;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--fg-dim);
	}
	.list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.15rem;
	}
	.item a {
		display: block;
		padding: 0.25rem 0.5rem;
		margin-left: -0.75rem;
		border-left: 2px solid transparent;
		color: var(--fg-muted);
		text-decoration: none;
		border-radius: 0 3px 3px 0;
	}
	.toc-narrow .item a {
		margin-left: 0;
		padding-left: 0.5rem;
	}
	.item.sub a {
		padding-left: 1.25rem;
		font-size: 0.72rem;
	}
	.item a:hover {
		color: var(--fg);
		background: var(--panel-2);
	}
	.item.active a {
		color: var(--fg);
		border-left-color: var(--orange);
		background: color-mix(in srgb, var(--orange) 10%, transparent);
	}
	.toc-narrow {
		position: sticky;
		top: var(--nav-h);
		z-index: 20;
		margin: 0 -0.25rem 0.75rem;
		background: var(--panel);
		border: 1px solid var(--border);
		border-radius: 6px;
	}
	.toc-narrow summary {
		padding: 0.5rem 0.75rem;
		cursor: pointer;
		font-weight: 600;
		list-style: revert;
	}
	.cur {
		margin-left: 0.4rem;
		font-weight: 400;
		color: var(--fg-muted);
	}
	.toc-narrow .list {
		padding: 0 0.5rem 0.5rem;
		max-height: 50dvh;
		overflow: auto;
	}
	@media (max-width: 899.98px) {
		.hidden-narrow {
			display: none;
		}
	}
	@media (min-width: 900px) {
		.hidden-wide {
			display: none;
		}
	}
</style>
