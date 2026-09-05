<script lang="ts">
	// Severity badge with the definitions tooltip. The badge is a button so the
	// tooltip is reachable from the keyboard (focus/Enter/Space); Esc closes it.
	import type { Severity } from '$lib/api/types.gen';
	import { SEVERITIES, severityDef } from '$lib/postmortem';

	let {
		severity,
		size = 'md',
		definitions = true
	}: { severity: Severity; size?: 'sm' | 'md' | 'lg'; definitions?: boolean } = $props();

	const uid = $props.id();
	let tipId = $derived(`sev-tip-${uid}`);
	let def = $derived(severityDef(severity));
	let open = $state(false);
</script>

<span class="wrap">
	<button
		type="button"
		class="sev sev-{def.tone} sev-{size} mono"
		aria-describedby={tipId}
		aria-expanded={open}
		onmouseenter={() => (open = true)}
		onmouseleave={() => (open = false)}
		onfocus={() => (open = true)}
		onblur={() => (open = false)}
		onclick={() => (open = !open)}
		onkeydown={(e) => {
			if (e.key === 'Escape' && open) {
				open = false;
				e.stopPropagation();
			}
		}}
	>
		<span class="sr-only">Severity </span>{severity}
	</button>
	<span role="tooltip" id={tipId} class="tip" hidden={!open}>
		<span class="head"><b class="mono">{def.level}</b> {def.definition}</span>
		{#if definitions}
			<span class="scale">
				{#each SEVERITIES as s (s.level)}
					<span class="row" class:current={s.level === severity}>
						<span class="dot dot-{s.tone}" aria-hidden="true"></span>
						<b class="mono">{s.level}</b>
						<span>{s.definition}</span>
					</span>
				{/each}
			</span>
		{/if}
	</span>
</span>

<style>
	.wrap {
		position: relative;
		display: inline-flex;
	}
	.sev {
		display: inline-flex;
		align-items: center;
		border-radius: 4px;
		border: 1px solid var(--c);
		/* a 10% tint over the page background keeps the coloured text at ≥ 4.5:1 (axe) */
		background: color-mix(in srgb, var(--c) 10%, var(--bg));
		color: var(--c);
		font-weight: 700;
		letter-spacing: 0.04em;
		line-height: 1;
		cursor: help;
	}
	.sev-sm {
		padding: 0.15rem 0.35rem;
		font-size: 0.68rem;
	}
	.sev-md {
		padding: 0.25rem 0.5rem;
		font-size: 0.78rem;
	}
	.sev-lg {
		padding: 0.35rem 0.65rem;
		font-size: 0.95rem;
	}
	.sev-red,
	.dot-red {
		--c: var(--red);
	}
	.sev-orange,
	.dot-orange {
		--c: var(--orange);
	}
	.sev-yellow,
	.dot-yellow {
		--c: var(--yellow);
	}
	.sev-blue,
	.dot-blue {
		--c: var(--blue);
	}
	.tip {
		position: absolute;
		z-index: 40;
		top: calc(100% + 6px);
		left: 0;
		width: max-content;
		max-width: min(24rem, 85vw);
		padding: 0.55rem 0.7rem;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--panel-2);
		color: var(--fg);
		font-size: 0.75rem;
		font-weight: 400;
		line-height: 1.45;
		box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
		text-align: left;
	}
	.tip[hidden] {
		display: none;
	}
	.head {
		display: block;
	}
	.scale {
		display: grid;
		gap: 0.2rem;
		margin-top: 0.5rem;
		padding-top: 0.45rem;
		border-top: 1px solid var(--border);
		color: var(--fg-muted);
	}
	.row {
		display: grid;
		grid-template-columns: 0.5rem 2.6rem 1fr;
		align-items: baseline;
		gap: 0.4rem;
	}
	.row.current {
		color: var(--fg);
	}
	.dot {
		display: inline-block;
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 50%;
		background: var(--c);
		transform: translateY(-1px);
	}
</style>
