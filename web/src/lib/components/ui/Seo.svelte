<script lang="ts">
	// OG / Twitter meta for a route (brief §5). The absolute URLs need the
	// site origin ((site)/+layout.server.ts); without one only the origin-free
	// tags are emitted. Pages with their own og:image (postmortems) pass it in.
	let {
		title,
		description,
		path,
		origin = '',
		image = undefined,
		type = 'website',
		noindex = false
	}: {
		title: string;
		description: string;
		/** Route path (`/logs`) for og:url and the canonical link. */
		path: string;
		origin?: string;
		/** Absolute og:image; default `<origin>/og/default.png`. */
		image?: string;
		type?: 'website' | 'article' | 'profile';
		noindex?: boolean;
	} = $props();

	let canonical = $derived(origin ? origin + path : '');
	let ogImage = $derived(image ?? (origin ? `${origin}/og/default.png` : ''));
</script>

<svelte:head>
	<title>{title}</title>
	<meta name="description" content={description} />
	{#if noindex}
		<meta name="robots" content="noindex" />
	{/if}
	<meta property="og:type" content={type} />
	<meta property="og:site_name" content="divy.dev" />
	<meta property="og:title" content={title} />
	<meta property="og:description" content={description} />
	{#if canonical}
		<link rel="canonical" href={canonical} />
		<meta property="og:url" content={canonical} />
	{/if}
	{#if ogImage}
		<meta property="og:image" content={ogImage} />
		<meta property="og:image:width" content="1200" />
		<meta property="og:image:height" content="630" />
		<meta name="twitter:card" content="summary_large_image" />
		<meta name="twitter:image" content={ogImage} />
	{:else}
		<meta name="twitter:card" content="summary" />
	{/if}
	<meta name="twitter:title" content={title} />
	<meta name="twitter:description" content={description} />
</svelte:head>
