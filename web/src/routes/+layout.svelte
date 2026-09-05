<script lang="ts">
	// Data-free chrome: skip link, top bar (route tabs, theme toggle), main,
	// footer. Installs the global keyboard listener and syncs theme/motion/
	// viewport state after hydration.
	import '../app.css';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { routes, isActive } from '$lib/nav';
	import { installKeyboard } from '$lib/keyboard';
	import { theme } from '$lib/state/theme.svelte';
	import { motion } from '$lib/state/motion.svelte';
	import { media } from '$lib/state/media.svelte';
	import ThemeToggle from '$lib/components/ui/ThemeToggle.svelte';

	let { children } = $props();
	// Flipped after hydration; tests and scripts wait for [data-hydrated="true"].
	let hydrated = $state(false);

	onMount(() => {
		theme.sync();
		motion.sync();
		media.sync();
		hydrated = true;
		return installKeyboard(window);
	});
</script>

<a href="#main" class="skip-link">Skip to content</a>

<header class="topbar">
	<nav class="nav" aria-label="Primary">
		<a href="/" class="brand mono" aria-label="Home">
			<span class="pulse" aria-hidden="true"></span>
			<span>divy.dev</span>
		</a>
		<ul class="tabs">
			{#each routes as r (r.href)}
				<li>
					<a
						href={r.href}
						class="tab"
						aria-current={isActive(page.url.pathname, r.href) ? 'page' : undefined}
						data-sveltekit-preload-data="hover">{r.label}</a
					>
				</li>
			{/each}
		</ul>
		<div class="right">
			<ThemeToggle />
		</div>
	</nav>
</header>

<main id="main" class="main" tabindex="-1" data-hydrated={hydrated}>
	{@render children()}
</main>

<footer class="footer mono">
	<span>This site is its own observability stack:</span>
	<a href="/metrics" rel="external">/metrics</a>
	<a href="/api/traces/career" rel="external">/api/traces/career</a>
	<a href="/healthz" rel="external">/healthz</a>
	<a href="/ascii" rel="external">/ascii</a>
</footer>

<style>
	:global(:root) {
		--nav-h: 48px;
	}
	.topbar {
		position: sticky;
		top: 0;
		z-index: 30;
		height: var(--nav-h);
		background: color-mix(in srgb, var(--panel) 92%, transparent);
		backdrop-filter: blur(8px);
		border-bottom: 1px solid var(--border);
	}
	.nav {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		height: 100%;
		max-width: 1600px;
		margin: 0 auto;
		padding: 0 0.75rem;
	}
	.brand {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		font-weight: 600;
		color: var(--fg);
		text-decoration: none;
		white-space: nowrap;
	}
	.pulse {
		width: 0.55rem;
		height: 0.55rem;
		border-radius: 50%;
		background: var(--green);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--green) 30%, transparent);
	}
	.tabs {
		display: flex;
		gap: 0.15rem;
		list-style: none;
		margin: 0;
		padding: 0;
		overflow-x: auto;
		scrollbar-width: none;
		flex: 1;
		min-width: 0;
	}
	.tabs::-webkit-scrollbar {
		display: none;
	}
	.tab {
		display: inline-flex;
		align-items: center;
		height: var(--nav-h);
		padding: 0 0.6rem;
		font-size: 0.85rem;
		color: var(--fg-muted);
		text-decoration: none;
		border-bottom: 2px solid transparent;
		white-space: nowrap;
	}
	.tab:hover {
		color: var(--fg);
		text-decoration: none;
	}
	.tab[aria-current='page'] {
		color: var(--fg);
		border-bottom-color: var(--orange);
	}
	.right {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	.main {
		max-width: 1600px;
		margin: 0 auto;
		padding: 0.75rem 0.75rem 2rem;
		outline: none;
	}
	.footer {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 1rem;
		max-width: 1600px;
		margin: 0 auto;
		padding: 1rem 0.75rem 2rem;
		font-size: 0.72rem;
		color: var(--fg-dim);
	}
	@media (max-width: 639.98px) {
		.brand span:last-child {
			display: none;
		}
	}
</style>
