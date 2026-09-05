<script lang="ts">
	// Time range picker: a radio group of presets on wide screens, a <select>
	// at ≤ 640 px (CSS decides, so the static HTML carries both and the
	// browser shows one). Presets come from content/panels.yaml.
	import type { Preset } from '$lib/timerange';
	import { PRESET_LABELS } from '$lib/timerange';

	let {
		value = $bindable(),
		options,
		name = 'range',
		label = 'Time range'
	}: { value: Preset; options: readonly Preset[]; name?: string; label?: string } = $props();
</script>

<fieldset class="picker" aria-label={label}>
	<legend class="sr-only">{label}</legend>
	<div class="segments" role="presentation">
		{#each options as opt (opt)}
			<label class="seg" class:on={value === opt} title={PRESET_LABELS[opt]}>
				<input type="radio" {name} value={opt} bind:group={value} class="sr-only" />
				<span>{opt}</span>
			</label>
		{/each}
	</div>
	<select class="input select" aria-label={label} bind:value>
		{#each options as opt (opt)}
			<option value={opt}>{PRESET_LABELS[opt]}</option>
		{/each}
	</select>
</fieldset>

<style>
	.picker {
		display: inline-flex;
		margin: 0;
		padding: 0;
		border: 0;
	}
	.segments {
		display: inline-flex;
		border: 1px solid var(--border);
		border-radius: 4px;
		overflow: hidden;
		background: var(--panel-2);
	}
	.seg {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: 2.6rem;
		min-height: 2rem;
		padding: 0 0.55rem;
		font-family: var(--font-mono);
		font-size: 0.78rem;
		color: var(--fg-muted);
		cursor: pointer;
		border-right: 1px solid var(--border);
		user-select: none;
	}
	.seg:last-child {
		border-right: 0;
	}
	.seg:hover {
		color: var(--fg);
		background: var(--panel-3);
	}
	.seg.on {
		color: var(--fg);
		background: var(--panel-3);
		box-shadow: inset 0 -2px 0 var(--orange);
	}
	.seg:has(input:focus-visible) {
		outline: 2px solid var(--focus);
		outline-offset: -2px;
	}
	.select {
		display: none;
		min-width: 9rem;
	}
	@media (max-width: 639.98px) {
		.segments {
			display: none;
		}
		.select {
			display: inline-block;
		}
	}
</style>
