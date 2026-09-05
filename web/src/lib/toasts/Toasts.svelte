<script lang="ts">
	// The global toast mount: a polite live region at the bottom-right (full
	// width at ≤ 640 px) rendering $lib/toasts/store. Each toast has a dismiss
	// button and its owner's actions (Silence, Runbook); new ones slide in
	// (CSS, gated on prefers-reduced-motion).
	import { toasts, type Toast } from './store.svelte';

	function icon(t: Toast): string {
		switch (t.tone) {
			case 'error':
				return '!';
			case 'warning':
				return '△';
			case 'success':
				return '✓';
			default:
				return 'i';
		}
	}
</script>

<div class="toasts" role="status" aria-live="polite" aria-atomic="false" data-toasts>
	{#each toasts.items as t (t.id)}
		<section class="toast slide-in tone-{t.tone}" data-toast={t.id} aria-label={t.title}>
			<span class="badge mono" aria-hidden="true">{icon(t)}</span>
			<div class="body">
				<h2 class="title">{t.title}</h2>
				{#if t.meta}
					<p class="meta mono">{t.meta}</p>
				{/if}
				{#if t.body}
					<p class="text">{t.body}</p>
				{/if}
				{#if t.actions.length}
					<div class="actions">
						{#each t.actions as a (a.label)}
							{#if a.href}
								<a
									class="btn small"
									class:btn-primary={a.primary}
									href={a.href}
									aria-label={a.ariaLabel ?? a.label}
									onclick={() => a.onclick?.()}>{a.label}</a
								>
							{:else}
								<button
									type="button"
									class="btn small"
									class:btn-primary={a.primary}
									aria-label={a.ariaLabel ?? a.label}
									onclick={() => a.onclick?.()}>{a.label}</button
								>
							{/if}
						{/each}
					</div>
				{/if}
			</div>
			<button
				type="button"
				class="close"
				aria-label="Dismiss {t.title}"
				onclick={() => toasts.dismiss(t.id)}>×</button
			>
		</section>
	{/each}
</div>

<style>
	.toasts {
		position: fixed;
		right: 0.75rem;
		bottom: calc(0.75rem + env(safe-area-inset-bottom, 0px) + var(--toast-offset, 0px));
		z-index: 80;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
		width: min(24rem, calc(100vw - 1.5rem));
		pointer-events: none;
	}
	.toast {
		display: grid;
		grid-template-columns: auto 1fr auto;
		gap: 0.6rem;
		padding: 0.6rem 0.6rem 0.6rem 0.7rem;
		border: 1px solid var(--border);
		border-left: 4px solid var(--blue);
		border-radius: 6px;
		background: var(--panel);
		box-shadow: 0 12px 32px rgba(0, 0, 0, 0.45);
		pointer-events: auto;
	}
	.tone-error {
		border-left-color: var(--red);
	}
	.tone-warning {
		border-left-color: var(--yellow);
	}
	.tone-success {
		border-left-color: var(--green);
	}
	.badge {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 1.4rem;
		height: 1.4rem;
		border-radius: 50%;
		background: var(--panel-3);
		color: var(--fg);
		font-size: 0.75rem;
		font-weight: 700;
	}
	.tone-error .badge {
		background: color-mix(in srgb, var(--red) 25%, var(--panel-3));
		color: var(--red);
	}
	.tone-warning .badge {
		background: color-mix(in srgb, var(--yellow) 25%, var(--panel-3));
		color: var(--yellow);
	}
	.body {
		min-width: 0;
	}
	.title {
		margin: 0;
		font-size: 0.85rem;
		font-weight: 600;
		overflow-wrap: anywhere;
	}
	.meta {
		margin: 0.1rem 0 0;
		font-size: 0.68rem;
		color: var(--fg-dim);
		overflow-wrap: anywhere;
	}
	.text {
		margin: 0.25rem 0 0;
		font-size: 0.78rem;
		color: var(--fg-muted);
		overflow-wrap: anywhere;
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
		margin-top: 0.45rem;
	}
	.btn.small {
		min-height: 1.7rem;
		font-size: 0.72rem;
		text-decoration: none;
	}
	.close {
		align-self: start;
		width: 1.6rem;
		height: 1.6rem;
		border: 0;
		border-radius: 4px;
		background: transparent;
		color: var(--fg-muted);
		font-size: 1.1rem;
		line-height: 1;
		cursor: pointer;
	}
	.close:hover {
		background: var(--panel-3);
		color: var(--fg);
	}
	@media (max-width: 639.98px) {
		.toasts {
			right: 0.5rem;
			left: 0.5rem;
			width: auto;
		}
	}
</style>
