<script lang="ts">
	// Jaeger-style "Trace ID" box: accepts `career` or a 32-hex id (an
	// X-Divy-Trace-Id from any response) and opens /trace/[id].
	import { goto } from '$app/navigation';
	import { isTraceId } from '$lib/api/client';

	let { value = '', compact = false }: { value?: string; compact?: boolean } = $props();

	// svelte-ignore state_referenced_locally
	let input = $state(value);
	let error = $state('');

	function submit(e: SubmitEvent) {
		e.preventDefault();
		const id = input.trim().toLowerCase();
		if (!isTraceId(id)) {
			error = 'Want "career" or 32 hex characters';
			return;
		}
		error = '';
		void goto(`/trace/${id}`);
	}
</script>

<form class="flex flex-col gap-1" onsubmit={submit} role="search" aria-label="Open a trace by id">
	<div class="flex items-stretch gap-1">
		<label for="trace-id" class="sr-only">Trace ID</label>
		<input
			id="trace-id"
			class="input mono min-w-0 flex-1 {compact ? '' : 'sm:w-80'}"
			type="text"
			autocomplete="off"
			spellcheck="false"
			placeholder="Trace ID — career or 32 hex"
			bind:value={input}
			aria-invalid={error ? 'true' : undefined}
			aria-describedby={error ? 'trace-id-error' : undefined}
		/>
		<button type="submit" class="btn btn-primary">Open</button>
	</div>
	{#if error}
		<p id="trace-id-error" class="text-xs text-red" role="alert">{error}</p>
	{/if}
</form>
