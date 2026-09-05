<script lang="ts">
	// A copyable curl line (same look as Explore's): the exact query_range call.
	let {
		curl,
		label = 'curl',
		open = false
	}: { curl: string; label?: string; open?: boolean } = $props();

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

<details class="curl panel" {open} data-curl>
	<summary class="head">
		<span class="mono dim">{label}</span>
		<button type="button" class="btn small" onclick={(e) => (e.preventDefault(), void copy())}
			>{copied ? 'Copied' : 'Copy'}</button
		>
	</summary>
	<pre class="code"><code>{curl}</code></pre>
</details>

<style>
	.curl {
		padding: 0.35rem 0.6rem 0.5rem;
	}
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		cursor: pointer;
		list-style: none;
	}
	.head::-webkit-details-marker {
		display: none;
	}
	.head::before {
		content: '▸';
		margin-right: 0.35rem;
		color: var(--fg-dim);
		font-size: 0.7rem;
	}
	.curl[open] .head::before {
		content: '▾';
	}
	.head .mono {
		flex: 1;
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
