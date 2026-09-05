<script lang="ts">
	// A copyable curl line (same look as Explore's): the exact query_range call.
	let {
		curl,
		label = 'curl',
		open = $bindable(false),
		id = 'curl'
	}: { curl: string; label?: string; open?: boolean; id?: string } = $props();

	let copied = $state(false);

	async function copy() {
		try {
			await navigator.clipboard.writeText(curl);
			copied = true;
			setTimeout(() => (copied = false), 1500);
		} catch {
			// clipboard blocked: the text is selectable in the <pre>
			copied = false;
		}
	}
</script>

<div class="curl panel" data-curl>
	<div class="head">
		<button
			type="button"
			class="toggle mono dim"
			aria-expanded={open}
			aria-controls="{id}-code"
			onclick={() => (open = !open)}
		>
			<span class="caret" aria-hidden="true">{open ? '▾' : '▸'}</span>
			{label}
		</button>
		<button
			type="button"
			class="btn small"
			onclick={() => void copy()}
			aria-label="Copy the curl command">{copied ? 'Copied' : 'Copy'}</button
		>
	</div>
	<pre class="code" id="{id}-code" hidden={!open}><code>{curl}</code></pre>
</div>

<style>
	.curl {
		padding: 0.35rem 0.6rem 0.5rem;
	}
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}
	.toggle {
		flex: 1;
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		min-height: 2rem;
		padding: 0;
		border: 0;
		background: transparent;
		text-align: left;
		cursor: pointer;
	}
	.caret {
		font-size: 0.7rem;
	}
	.dim {
		color: var(--fg-dim);
		font-size: 0.78rem;
	}
	.btn.small {
		min-height: 1.6rem;
		font-size: 0.72rem;
	}
	.code {
		margin: 0.4rem 0 0;
		padding: 0.5rem 0.65rem;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: var(--bg);
		font-size: 0.75rem;
		line-height: 1.45;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		overflow-x: auto;
		user-select: all;
	}
</style>
