<script lang="ts">
	// A TODO(divy) placeholder rendered literally (never an invented value),
	// muted so it reads as "not filled in yet".
	let {
		value = 'TODO(divy)',
		title = 'Not filled in yet: content/ carries a TODO(divy) placeholder here'
	}: { value?: string; title?: string } = $props();
</script>

<span class="todo mono" {title}>{value}</span>

<style>
	.todo {
		display: inline-block;
		max-width: 100%;
		padding: 0.05rem 0.4rem;
		border: 1px dashed var(--border);
		border-radius: 4px;
		background: var(--panel-2);
		color: var(--fg-dim);
		font-size: 0.72rem;
		line-height: 1.5;
		overflow-wrap: anywhere;
	}
</style>
