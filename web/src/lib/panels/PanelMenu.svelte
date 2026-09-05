<script lang="ts">
	// Panel kebab menu: <button aria-haspopup="menu"> + role="menu" with
	// arrow/Home/End/Esc keyboard handling; closes on outside click or blur.
	import { tick } from 'svelte';

	export interface MenuItem {
		id: string;
		label: string;
		/** Rendered as a link when set (Explore). */
		href?: string;
		action?: () => void;
		separator?: boolean;
		hidden?: boolean;
	}

	let { items, label = 'Panel menu' }: { items: MenuItem[]; label?: string } = $props();

	let open = $state(false);
	let btn = $state<HTMLButtonElement | null>(null);
	let menu = $state<HTMLDivElement | null>(null);
	let visible = $derived(items.filter((i) => !i.hidden));

	function itemEls(): HTMLElement[] {
		return menu ? [...menu.querySelectorAll<HTMLElement>('[role="menuitem"]')] : [];
	}

	async function show(focusIndex: number | 'last' = 0) {
		open = true;
		await tick();
		const els = itemEls();
		const el = focusIndex === 'last' ? els[els.length - 1] : els[focusIndex];
		el?.focus();
	}

	function close(restore = true) {
		if (!open) return;
		open = false;
		if (restore) btn?.focus();
	}

	function onButtonKey(e: KeyboardEvent) {
		if (e.key === 'ArrowDown' || e.key === 'Enter' || e.key === ' ') {
			e.preventDefault();
			void show(0);
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			void show('last');
		}
	}

	function onMenuKey(e: KeyboardEvent) {
		const els = itemEls();
		const cur = els.indexOf(document.activeElement as HTMLElement);
		const go = (i: number) => {
			e.preventDefault();
			els[(i + els.length) % els.length]?.focus();
		};
		switch (e.key) {
			case 'ArrowDown':
				return go(cur + 1);
			case 'ArrowUp':
				return go(cur - 1);
			case 'Home':
				return go(0);
			case 'End':
				return go(els.length - 1);
			case 'Escape':
				e.preventDefault();
				e.stopPropagation();
				return close();
			case 'Tab':
				return close(false);
		}
	}

	function run(item: MenuItem) {
		close();
		item.action?.();
	}

	function onDocumentPointer(e: PointerEvent) {
		if (!open) return;
		const t = e.target as Node;
		if (menu?.contains(t) || btn?.contains(t)) return;
		close(false);
	}
</script>

<svelte:document onpointerdown={onDocumentPointer} />

<div class="menu-root">
	<button
		type="button"
		class="kebab"
		bind:this={btn}
		aria-haspopup="menu"
		aria-expanded={open}
		aria-label={label}
		title={label}
		onclick={() => (open ? close() : void show(0))}
		onkeydown={onButtonKey}
	>
		<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
			<circle cx="8" cy="3" r="1.4" fill="currentColor" />
			<circle cx="8" cy="8" r="1.4" fill="currentColor" />
			<circle cx="8" cy="13" r="1.4" fill="currentColor" />
		</svg>
	</button>
	{#if open}
		<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
		<div
			class="menu fade-in"
			role="menu"
			tabindex="-1"
			aria-label={label}
			bind:this={menu}
			onkeydown={onMenuKey}
		>
			{#each visible as item (item.id)}
				{#if item.separator}
					<div class="sep" role="separator"></div>
				{/if}
				{#if item.href}
					<a
						class="item"
						role="menuitem"
						href={item.href}
						tabindex="-1"
						onclick={() => close(false)}
					>
						{item.label}
					</a>
				{:else}
					<button
						class="item"
						role="menuitem"
						type="button"
						tabindex="-1"
						onclick={() => run(item)}
					>
						{item.label}
					</button>
				{/if}
			{/each}
		</div>
	{/if}
</div>

<style>
	.menu-root {
		position: relative;
		display: inline-flex;
	}
	.kebab {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 1.75rem;
		height: 1.75rem;
		border: 0;
		border-radius: 4px;
		background: transparent;
		color: var(--fg-muted);
		cursor: pointer;
	}
	.kebab:hover,
	.kebab[aria-expanded='true'] {
		background: var(--panel-3);
		color: var(--fg);
	}
	.menu {
		position: absolute;
		top: 100%;
		right: 0;
		z-index: 20;
		min-width: 12rem;
		padding: 0.25rem;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--panel-2);
		box-shadow: 0 8px 24px rgba(0, 0, 0, 0.4);
	}
	.item {
		display: block;
		width: 100%;
		padding: 0.4rem 0.6rem;
		border: 0;
		border-radius: 4px;
		background: transparent;
		color: var(--fg);
		font: inherit;
		font-size: 0.8rem;
		text-align: left;
		text-decoration: none;
		cursor: pointer;
	}
	.item:hover,
	.item:focus-visible {
		background: var(--panel-3);
		outline: none;
		text-decoration: none;
	}
	.sep {
		height: 1px;
		margin: 0.25rem 0;
		background: var(--border);
	}
</style>
