<script lang="ts">
	// LogQL query bar: a textarea combobox (aria-expanded / aria-activedescendant)
	// over a listbox fed by $lib/logql/complete — label names/values from the
	// Loki API inside {…}, pipeline stages after | or a selector, functions and
	// aggregations elsewhere. Enter runs, Shift+Enter inserts a newline, ↑/↓
	// navigate, Tab/Enter accept, Esc closes the list and then clears the query,
	// Ctrl+Space forces the list open.
	import { tick } from 'svelte';
	import { apply, contextAt, suggest, type Completion, type Context } from './complete';

	let {
		value = $bindable(''),
		labels = [],
		fields = [],
		fetchValues,
		onrun,
		onclear = undefined,
		id = 'logql',
		placeholder = 'LogQL, e.g. {service="gradr"} |= "sentry" | json | level="warn"',
		disabled = false
	}: {
		value?: string;
		labels?: readonly string[];
		fields?: readonly string[];
		fetchValues: (label: string) => Promise<string[]>;
		onrun: () => void;
		onclear?: () => void;
		id?: string;
		placeholder?: string;
		disabled?: boolean;
	} = $props();

	let area = $state<HTMLTextAreaElement | null>(null);
	let open = $state(false);
	let items = $state<Completion[]>([]);
	let activeIdx = $state(0);
	let ctx: Context | null = null;
	let values: string[] = [];
	let valueKey = '';
	let seq = 0;

	export function focus() {
		area?.focus();
	}

	async function refresh(force = false) {
		if (!area) return;
		const caret = area.selectionStart ?? value.length;
		const c = contextAt(value, caret, force);
		ctx = c;
		if (!c || (!force && (c.kind === 'name' || c.kind === 'label') && c.prefix.length === 0)) {
			open = false;
			return;
		}
		const my = ++seq;
		if (c.kind === 'value' && c.label) {
			if (c.label !== valueKey) {
				valueKey = c.label;
				values = await fetchValues(c.label).catch(() => []);
				if (my !== seq) return;
			}
		}
		items = suggest(c, { labels, values, fields });
		activeIdx = 0;
		open = items.length > 0;
	}

	function accept(c: Completion) {
		if (!ctx) return;
		const r = apply(value, ctx, c);
		value = r.text;
		open = false;
		void tick().then(() => {
			area?.focus();
			area?.setSelectionRange(r.caret, r.caret);
			// a label completion opens the value list straight away
			if (c.kind === 'label' && ctx?.kind === 'label') void refresh();
		});
	}

	function onKey(e: KeyboardEvent) {
		if (open) {
			if (e.key === 'ArrowDown') {
				e.preventDefault();
				activeIdx = (activeIdx + 1) % items.length;
				return;
			}
			if (e.key === 'ArrowUp') {
				e.preventDefault();
				activeIdx = (activeIdx - 1 + items.length) % items.length;
				return;
			}
			if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) {
				e.preventDefault();
				const c = items[activeIdx];
				if (c) accept(c);
				return;
			}
			if (e.key === 'Escape') {
				e.preventDefault();
				e.stopPropagation();
				open = false;
				return;
			}
		}
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			open = false;
			onrun();
		} else if (e.key === 'Escape') {
			e.preventDefault();
			e.stopPropagation();
			if (value) {
				value = '';
				onclear?.();
			}
		} else if (e.key === ' ' && e.ctrlKey) {
			e.preventDefault();
			void refresh(true);
		}
	}

	function onBlur() {
		// let a click on an option land first
		setTimeout(() => (open = false), 120);
	}

	let listId = $derived(`${id}-listbox`);
	let activeId = $derived(open && items[activeIdx] ? `${id}-opt-${activeIdx}` : undefined);

	$effect(() => {
		if (!open || !area) return;
		const el = document.getElementById(`${id}-opt-${activeIdx}`);
		el?.scrollIntoView({ block: 'nearest' });
	});
</script>

<div class="bar">
	<textarea
		{id}
		class="input area mono"
		bind:this={area}
		bind:value
		rows="2"
		spellcheck="false"
		autocomplete="off"
		autocapitalize="off"
		{placeholder}
		{disabled}
		role="combobox"
		aria-label="LogQL query"
		aria-autocomplete="list"
		aria-expanded={open}
		aria-controls={listId}
		aria-activedescendant={activeId}
		aria-haspopup="listbox"
		onkeydown={onKey}
		oninput={() => void refresh()}
		onblur={onBlur}
		onclick={() => void refresh()}></textarea>
	<ul class="list" id={listId} role="listbox" aria-label="Suggestions" hidden={!open}>
		{#each items as c, i (c.kind + c.label)}
			<li
				id="{id}-opt-{i}"
				role="option"
				aria-selected={i === activeIdx}
				class="opt"
				class:active={i === activeIdx}
				onpointerdown={(e) => {
					e.preventDefault();
					accept(c);
				}}
				onpointerenter={() => (activeIdx = i)}
			>
				<span class="kind kind-{c.kind}">{c.kind}</span>
				<span class="lbl mono">{c.label}</span>
				{#if c.detail}
					<span class="detail">{c.detail}</span>
				{/if}
			</li>
		{/each}
	</ul>
</div>

<style>
	.bar {
		position: relative;
		flex: 1;
		min-width: 0;
	}
	.area {
		display: block;
		width: 100%;
		min-height: 3.2rem;
		padding: 0.45rem 0.6rem;
		resize: vertical;
		line-height: 1.45;
		font-size: 0.85rem;
	}
	.list {
		position: absolute;
		left: 0;
		right: 0;
		top: 100%;
		z-index: 30;
		max-height: 18rem;
		margin: 0.15rem 0 0;
		padding: 0.25rem;
		list-style: none;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--panel-2);
		box-shadow: 0 10px 30px rgba(0, 0, 0, 0.45);
		overflow-y: auto;
	}
	.opt {
		display: grid;
		grid-template-columns: 5.2rem 1fr;
		gap: 0 0.5rem;
		align-items: baseline;
		padding: 0.3rem 0.5rem;
		border-radius: 4px;
		font-size: 0.78rem;
		cursor: pointer;
	}
	.opt.active {
		background: var(--panel-3);
	}
	.kind {
		font-size: 0.62rem;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--fg-dim);
	}
	.kind-selector,
	.kind-parser {
		color: var(--green);
	}
	.kind-function,
	.kind-aggregation {
		color: var(--blue);
	}
	.kind-label,
	.kind-field {
		color: var(--orange);
	}
	.kind-value {
		color: var(--purple);
	}
	.kind-stage {
		color: var(--yellow);
	}
	.detail {
		grid-column: 2;
		font-size: 0.68rem;
		color: var(--fg-dim);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
</style>
