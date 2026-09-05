<script lang="ts">
	// Incident history of one target: runs of ≥ 2 consecutive failed probes
	// (newest first, as the API orders them). Wide rows scroll inside the box.
	import type { UptimeIncident } from '$lib/api/types.gen';
	import { errorClass, errorMessage, formatSeconds, formatUtc } from '$lib/uptime/model';

	let { incidents, name }: { incidents: UptimeIncident[]; name: string } = $props();
</script>

{#if incidents.length === 0}
	<p class="none mono" role="status">
		no incidents in the window (an incident is ≥ 2 consecutive failed probes)
	</p>
{:else}
	<div class="scroll">
		<table class="mono">
			<caption class="sr-only">Incidents for {name}</caption>
			<thead>
				<tr>
					<th scope="col">Start (UTC)</th>
					<th scope="col">End (UTC)</th>
					<th scope="col">Duration</th>
					<th scope="col">Probes</th>
					<th scope="col">Class</th>
					<th scope="col">First error</th>
				</tr>
			</thead>
			<tbody>
				{#each incidents as inc (inc.started_at)}
					<tr>
						<td>{formatUtc(inc.started_at)}</td>
						<td
							>{#if inc.ended_at}{formatUtc(inc.ended_at)}{:else}<span class="ongoing">ongoing</span
								>{/if}</td
						>
						<td>{formatSeconds(inc.duration_s)}</td>
						<td>{inc.probes}</td>
						<td><span class="chip cls">{errorClass(inc.first_error) ?? '—'}</span></td>
						<td class="msg">{errorMessage(inc.first_error)}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}

<style>
	.none {
		margin: 0;
		font-size: 0.74rem;
		color: var(--fg-dim);
	}
	.scroll {
		overflow-x: auto;
		max-width: 100%;
	}
	table {
		width: 100%;
		border-collapse: collapse;
		font-size: 0.74rem;
	}
	th,
	td {
		padding: 0.3rem 0.5rem;
		text-align: left;
		white-space: nowrap;
		border-bottom: 1px solid var(--border);
		vertical-align: top;
	}
	th {
		color: var(--fg-dim);
		font-weight: 600;
	}
	.msg {
		white-space: normal;
		min-width: 14rem;
		color: var(--fg-muted);
	}
	.ongoing {
		color: var(--red);
		font-weight: 600;
	}
	.cls {
		border-color: color-mix(in srgb, var(--red) 40%, var(--border));
		color: var(--red);
	}
</style>
