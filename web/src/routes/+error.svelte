<script lang="ts">
	import { page } from '$app/state';
</script>

<svelte:head>
	<title>{page.status} · divy.dev</title>
</svelte:head>

<section class="panel mx-auto mt-6 max-w-3xl">
	<div class="panel-header"><h1 class="text-sm font-semibold">{page.status}</h1></div>
	<div class="p-4 text-sm">
		<p class="mono">{page.error?.message ?? 'Something went wrong'}</p>
		<p class="mt-3 text-fg-muted">Back to the <a href="/">career trace</a>.</p>
	</div>
</section>
