<script lang="ts">
	// Panel frame: header (drag grip, title, description tooltip, source /
	// "source: manual · last updated" badge, loading dot, kebab menu), body
	// slot and the resize handle. Focusable (tabindex=0) and labelled by its
	// title; `flash` highlights it for #panel=<id> deep links.
	import type { Snippet } from 'svelte';
	import type { Panel } from '$lib/api/types.gen';
	import PanelMenu, { type MenuItem } from './PanelMenu.svelte';

	let {
		panel,
		loading = false,
		updated = undefined,
		flash = false,
		draggable = true,
		menuItems,
		onDragStart = undefined,
		onResizeStart = undefined,
		children
	}: {
		panel: Panel;
		loading?: boolean;
		updated?: { ts?: number; unknown: boolean };
		flash?: boolean;
		draggable?: boolean;
		menuItems: MenuItem[];
		onDragStart?: (e: PointerEvent) => void;
		onResizeStart?: (e: PointerEvent) => void;
		children: Snippet;
	} = $props();

	let titleId = $derived(`panel-title-${panel.id}`);
	let descId = $derived(`panel-desc-${panel.id}`);
	let sourceText = $derived.by(() => {
		const k = panel.source.kind;
		const cad = panel.source.cadence ? ` · every ${panel.source.cadence}` : '';
		if (k === 'content') return 'source: content (live)';
		if (k === 'process') return 'source: process (live)';
		return `source: ${k}${cad}`;
	});
	let updatedText = $derived.by(() => {
		if (panel.source.kind !== 'manual') return '';
		if (!updated) return 'source: manual · last updated …';
		if (updated.unknown || updated.ts === undefined)
			return 'source: manual · last updated: unknown';
		return `source: manual · last updated ${new Date(updated.ts * 1000).toISOString().slice(0, 10)}`;
	});
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -- panels are keyboard stops (brief §5), like Grafana's -->
<article
	class="panel frame"
	class:flash
	data-panel-id={panel.id}
	data-panel-type={panel.type}
	tabindex="0"
	aria-labelledby={titleId}
	aria-describedby={descId}
>
	<header class="panel-header head">
		{#if draggable}
			<button
				type="button"
				class="grip"
				aria-label="Drag to move {panel.title} (use the panel menu to move with the keyboard)"
				title="Drag to move"
				onpointerdown={onDragStart}
			>
				<svg viewBox="0 0 10 16" width="10" height="16" aria-hidden="true">
					{#each [2, 8, 14] as y (y)}
						<circle cx="3" cy={y} r="1.3" fill="currentColor" />
						<circle cx="7" cy={y} r="1.3" fill="currentColor" />
					{/each}
				</svg>
			</button>
		{/if}
		<h2 id={titleId} class="title">{panel.title}</h2>
		<span class="info-wrap">
			<button type="button" class="info" aria-label="About {panel.title}" aria-describedby={descId}>
				i
			</button>
			<span id={descId} role="tooltip" class="desc">{panel.description}</span>
		</span>
		{#if panel.source.kind === 'manual'}
			<span
				class="chip manual"
				title={updatedText + (panel.source.note ? ' — ' + panel.source.note : '')}
			>
				<span class="long">{updatedText}</span>
				<span class="short" aria-hidden="true"
					>{updatedText.replace('source: manual · last updated', 'manual · updated')}</span
				>
			</span>
		{:else}
			<span class="chip src" title={panel.source.note ?? ''}>{sourceText}</span>
		{/if}
		{#if loading}
			<span class="loading" role="status" aria-label="Loading {panel.title}"></span>
		{/if}
		<span class="spacer"></span>
		<PanelMenu items={menuItems} label="Menu for {panel.title}" />
	</header>
	<div class="body">
		{@render children()}
	</div>
	{#if draggable}
		<button
			type="button"
			class="resize"
			aria-label="Resize {panel.title} (use the panel menu to resize with the keyboard)"
			title="Drag to resize"
			onpointerdown={onResizeStart}
		>
			<svg viewBox="0 0 10 10" width="10" height="10" aria-hidden="true">
				<path d="M9 1 1 9M9 5 5 9M9 9" stroke="currentColor" stroke-width="1.4" fill="none" />
			</svg>
		</button>
	{/if}
</article>

<style>
	.frame {
		position: relative;
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
		overflow: hidden;
	}
	.frame:focus-visible {
		outline-offset: 0;
	}
	.frame.flash {
		animation: panel-flash 1.6s ease-out 1;
	}
	@keyframes panel-flash {
		0%,
		60% {
			box-shadow: 0 0 0 2px var(--orange);
		}
		100% {
			box-shadow: 0 0 0 0 transparent;
		}
	}
	.head {
		flex: none;
		gap: 0.4rem;
		padding-left: 0.4rem;
		padding-right: 0.25rem;
		user-select: none;
	}
	.grip {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 1.25rem;
		height: 1.75rem;
		border: 0;
		background: transparent;
		color: var(--fg-dim);
		cursor: grab;
		touch-action: none;
	}
	.grip:hover {
		color: var(--fg);
	}
	.title {
		margin: 0;
		font-size: 0.8125rem;
		font-weight: 600;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		min-width: 0;
	}
	.info-wrap {
		position: relative;
		display: inline-flex;
	}
	.info {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 1.1rem;
		height: 1.1rem;
		border: 1px solid var(--border);
		border-radius: 50%;
		background: transparent;
		color: var(--fg-dim);
		font-family: var(--font-mono);
		font-size: 0.65rem;
		cursor: help;
	}
	.desc {
		display: none;
		position: absolute;
		top: calc(100% + 6px);
		left: 0;
		z-index: 25;
		width: max-content;
		max-width: 22rem;
		padding: 0.45rem 0.6rem;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: var(--panel-2);
		color: var(--fg);
		font-size: 0.72rem;
		font-weight: 400;
		line-height: 1.4;
		white-space: normal;
		box-shadow: 0 6px 20px rgba(0, 0, 0, 0.35);
	}
	.info:hover + .desc,
	.info:focus-visible + .desc,
	.info-wrap:hover .desc {
		display: block;
	}
	.chip {
		font-weight: 400;
		overflow: hidden;
		text-overflow: ellipsis;
		min-width: 0;
	}
	.chip.manual {
		color: var(--yellow);
		border-color: color-mix(in srgb, var(--yellow) 50%, var(--border));
	}
	.loading {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 50%;
		background: var(--blue);
		flex: none;
	}
	@media (prefers-reduced-motion: no-preference) {
		.loading {
			animation: pulse 900ms ease-in-out infinite alternate;
		}
	}
	@keyframes pulse {
		from {
			opacity: 0.3;
		}
		to {
			opacity: 1;
		}
	}
	.spacer {
		flex: 1;
	}
	.body {
		flex: 1;
		min-height: 0;
		position: relative;
	}
	.resize {
		position: absolute;
		right: 0;
		bottom: 0;
		width: 1.25rem;
		height: 1.25rem;
		border: 0;
		background: transparent;
		color: var(--fg-dim);
		cursor: nwse-resize;
		touch-action: none;
		display: inline-flex;
		align-items: flex-end;
		justify-content: flex-end;
		padding: 0 3px 3px 0;
	}
	.resize:hover {
		color: var(--fg);
	}
	.frame {
		container-type: inline-size;
	}
	.chip .short {
		display: none;
	}
	/* narrow panels: keep the title readable, drop the source chip, shorten the manual badge */
	@container (max-width: 400px) {
		.chip.src {
			display: none;
		}
		.chip .long {
			display: none;
		}
		.chip .short {
			display: inline;
		}
	}
	@media (max-width: 639.98px) {
		.chip.src {
			display: none;
		}
	}
</style>
