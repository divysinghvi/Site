<script lang="ts">
	// The honest empty state (brief §7): says which collector/source is missing
	// instead of drawing a flat zero line.
	let {
		message,
		expr = undefined,
		tone = 'muted'
	}: { message: string; expr?: string; tone?: 'muted' | 'error' } = $props();
</script>

<div class="empty" class:error={tone === 'error'} role="status">
	<svg class="icon" viewBox="0 0 16 16" aria-hidden="true">
		<path
			d="M1.5 11.5 5 7l3 3 3.5-5 3 3.5"
			fill="none"
			stroke="currentColor"
			stroke-width="1.4"
			stroke-dasharray="2 2"
		/>
		<circle cx="8" cy="10" r="1.2" fill="currentColor" />
	</svg>
	<p class="msg">{message}</p>
	{#if expr}
		<code class="expr">{expr}</code>
	{/if}
</div>

<style>
	.empty {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 0.35rem;
		height: 100%;
		padding: 0.75rem;
		border: 1px dashed var(--border);
		border-radius: 4px;
		margin: 0.5rem;
		color: var(--fg-muted);
		text-align: center;
		font-size: 0.78rem;
	}
	.empty.error {
		color: var(--red);
		border-color: color-mix(in srgb, var(--red) 40%, var(--border));
	}
	.icon {
		width: 22px;
		height: 22px;
		opacity: 0.7;
	}
	.msg {
		margin: 0;
		max-width: 36rem;
		overflow-wrap: anywhere;
	}
	.expr {
		max-width: 100%;
		padding: 0.1rem 0.4rem;
		border-radius: 3px;
		background: var(--panel-2);
		font-size: 0.7rem;
		color: var(--fg-dim);
		overflow-wrap: anywhere;
	}
</style>
