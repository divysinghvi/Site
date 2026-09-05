<script lang="ts">
	// Placeholder chrome for routes a later step fills in. UI labels only.
	let { title }: { title: string } = $props();
</script>

<svelte:head>
	<title>{title}</title>
	<meta name="robots" content="noindex" />
</svelte:head>

<section class="panel mx-auto mt-6 max-w-3xl" aria-labelledby="page-title">
	<div class="panel-header">
		<h1 id="page-title" class="text-sm font-semibold">{title}</h1>
	</div>
	<div class="p-4 text-sm text-fg-muted">
		<p>This page is coming in a later step.</p>
		<p class="mt-2">
			Until then, the <a href="/">career trace</a> is live, and the API it reads from answers at
			<a href="/api/traces/career" rel="external" class="mono">/api/traces/career</a>.
		</p>
	</div>
</section>
