<script lang="ts">
	// Loki-style log lines: timestamp, level colour, service/component labels,
	// message; click (or Enter) expands the line into its fields, the
	// pretty-printed JSON and the links (span in the career trace, postmortem,
	// runbook). j/k move between lines (roving tabindex); Esc collapses.
	// Used by /logs (also for the live-tail replay, where `typed` limits how
	// much of a message is visible yet) and by Explore's Loki tab.
	import { onMount } from 'svelte';
	import { bindKeys } from '$lib/keyboard';
	import {
		extractedLabels,
		formatPrecise,
		formatTs,
		levelVar,
		lineFields,
		prettyJSON,
		spanHref,
		type LogRow
	} from './lines';

	let {
		rows,
		expanded = $bindable(null),
		typed = {},
		emptyText = 'No lines matched.',
		keyboard = true,
		id = 'logs'
	}: {
		rows: LogRow[];
		expanded?: string | null;
		/** Characters of the message visible per row while the live tail types it. */
		typed?: Record<string, number>;
		emptyText?: string;
		keyboard?: boolean;
		id?: string;
	} = $props();

	let list = $state<HTMLElement | null>(null);
	let focusTs = $state<string | null>(null);

	function toggle(ts: string) {
		expanded = expanded === ts ? null : ts;
	}

	function heads(): HTMLButtonElement[] {
		return list ? [...list.querySelectorAll<HTMLButtonElement>('button.head')] : [];
	}

	function move(delta: number) {
		const hs = heads();
		if (hs.length === 0) return false;
		const i = hs.findIndex((h) => h === document.activeElement);
		const next =
			i < 0 ? (delta > 0 ? 0 : hs.length - 1) : Math.min(hs.length - 1, Math.max(0, i + delta));
		const el = hs[next]!;
		focusTs = el.dataset.ts ?? null;
		el.focus();
		el.scrollIntoView({ block: 'nearest' });
		return true;
	}

	onMount(() => {
		if (!keyboard) return;
		return bindKeys(id, {
			j: () => move(1),
			k: () => move(-1),
			Escape: () => {
				if (expanded === null) return false;
				expanded = null;
				return true;
			}
		});
	});

	function tabIndexOf(row: LogRow, i: number): 0 | -1 {
		if (focusTs) return row.ts === focusTs ? 0 : -1;
		return i === 0 ? 0 : -1;
	}

	function visibleMsg(row: LogRow): string {
		const n = typed[row.ts];
		return n === undefined ? row.msg : row.msg.slice(0, n);
	}

	function typing(row: LogRow): boolean {
		const n = typed[row.ts];
		return n !== undefined && n < row.msg.length;
	}

	function linkOf(row: LogRow, key: string, prefix: string): string | undefined {
		const v = row.json?.[key];
		return typeof v === 'string' && v.startsWith(prefix) ? v : undefined;
	}
</script>

<div class="list" bind:this={list} data-log-list>
	{#if rows.length === 0}
		<p class="empty dim" role="status">{emptyText}</p>
	{/if}
	{#each rows as row, i (row.ts)}
		{@const open = expanded === row.ts}
		<article
			class="row"
			class:open
			data-log-row={row.ts}
			data-level={row.level}
			style="--lv: {levelVar(row.level)}"
		>
			<div class="line">
				<button
					type="button"
					class="head"
					data-ts={row.ts}
					tabindex={tabIndexOf(row, i)}
					aria-expanded={open}
					aria-controls="{id}-d-{row.ts}"
					onclick={() => toggle(row.ts)}
					onfocus={() => (focusTs = row.ts)}
				>
					<span class="bar" aria-hidden="true"></span>
					<time class="ts mono" datetime={new Date(row.tsMs).toISOString()} class:todo={row.tsTodo}
						>{formatTs(row.tsMs)}</time
					>
					<span class="lvl mono">{row.level}</span>
					<span class="labels">
						<span class="chip svc">{row.service}</span>
						{#if row.component}
							<span class="chip comp">{row.component}</span>
						{/if}
					</span>
					<span class="msg" class:typing={typing(row)}>{visibleMsg(row)}</span>
				</button>
				{#if row.span}
					<a
						class="span-link mono"
						href={spanHref(row.span)}
						title="Open span {row.span} in the career trace"
						aria-label="Open span {row.span} in the career trace">⤴ {row.span}</a
					>
				{/if}
			</div>
			{#if open}
				<div class="detail" id="{id}-d-{row.ts}">
					<div class="cols">
						<section class="fields" aria-label="Fields">
							<h3 class="h3">Stream labels</h3>
							<dl class="kv mono">
								<dt>service</dt>
								<dd>{row.service}</dd>
								<dt>level</dt>
								<dd style="color: {levelVar(row.level)}">{row.level}</dd>
								{#if row.component}
									<dt>component</dt>
									<dd>{row.component}</dd>
								{/if}
							</dl>
							{#if extractedLabels(row).length}
								<h3 class="h3">Extracted labels <span class="dim">(| json)</span></h3>
								<dl class="kv mono">
									{#each extractedLabels(row) as [k, v] (k)}
										<dt>{k}</dt>
										<dd>{v}</dd>
									{/each}
								</dl>
							{/if}
							<h3 class="h3">Line fields</h3>
							<dl class="kv mono">
								{#each lineFields(row) as [k, v] (k)}
									<dt>{k}</dt>
									<dd>
										{#if k === 'span'}
											<a href={spanHref(v)}>{v}</a>
										{:else if k === 'postmortem' && v.startsWith('/postmortems/')}
											<a href={v}>{v}</a>
										{:else if k === 'ts' && row.tsTodo}
											<span
												class="todo-val"
												title="content/logs.ndjson has no date for this line: it is ordered at the linked span's start"
												>{v}</span
											>
										{:else}
											{v}
										{/if}
									</dd>
								{/each}
							</dl>
							<p class="when dim">
								{#if row.tsTodo}
									ts is TODO(divy): shown at the linked span's start ({formatPrecise(
										row.tsMs,
										'day'
									)})
								{:else if row.precision !== 'day'}
									precision {row.precision}: {formatPrecise(row.tsMs, row.precision)}
								{:else}
									{formatPrecise(row.tsMs, 'day')} UTC
								{/if}
							</p>
						</section>
						<section class="json" aria-label="Line JSON">
							<h3 class="h3">JSON</h3>
							<pre class="code"><code>{prettyJSON(row)}</code></pre>
						</section>
					</div>
					<div class="links">
						{#if row.span}
							<a class="btn small" href={spanHref(row.span)}>View span in trace ↗</a>
						{/if}
						{#if linkOf(row, 'postmortem', '/postmortems/')}
							<a class="btn small" href={linkOf(row, 'postmortem', '/postmortems/')}>Postmortem ↗</a
							>
						{/if}
						{#if linkOf(row, 'runbook', '/')}
							<a class="btn small" href={linkOf(row, 'runbook', '/')}>Runbook ↗</a>
						{/if}
					</div>
				</div>
			{/if}
		</article>
	{/each}
</div>

<style>
	.list {
		display: flex;
		flex-direction: column;
	}
	.empty {
		margin: 0;
		padding: 1rem 0.75rem;
		font-size: 0.8rem;
	}
	.dim {
		color: var(--fg-dim);
	}
	.row {
		border-bottom: 1px solid var(--border);
	}
	.row:last-child {
		border-bottom: 0;
	}
	.line {
		display: flex;
		align-items: stretch;
		gap: 0.25rem;
	}
	.head {
		flex: 1;
		min-width: 0;
		display: grid;
		grid-template-columns: 4px 12.6rem 3.2rem auto 1fr;
		gap: 0.5rem;
		align-items: baseline;
		padding: 0.3rem 0.5rem 0.3rem 0;
		border: 0;
		background: transparent;
		color: var(--fg);
		font: inherit;
		font-size: 0.78rem;
		line-height: 1.45;
		text-align: left;
		cursor: pointer;
	}
	.head:hover,
	.row.open .head {
		background: var(--panel-2);
	}
	.bar {
		align-self: stretch;
		width: 4px;
		border-radius: 0 2px 2px 0;
		background: var(--lv);
	}
	.ts {
		color: var(--fg-muted);
		white-space: nowrap;
	}
	.ts.todo {
		color: var(--fg-dim);
		font-style: italic;
	}
	.lvl {
		color: var(--lv);
		text-transform: uppercase;
		font-size: 0.68rem;
		font-weight: 600;
		letter-spacing: 0.03em;
	}
	.labels {
		display: inline-flex;
		gap: 0.25rem;
		white-space: nowrap;
	}
	.chip {
		padding: 0 0.4rem;
		font-size: 0.66rem;
	}
	.chip.comp {
		color: var(--fg-dim);
	}
	.msg {
		overflow-wrap: anywhere;
	}
	.msg.typing::after {
		content: '▌';
		color: var(--fg-dim);
	}
	.span-link {
		display: inline-flex;
		align-items: center;
		padding: 0 0.5rem;
		font-size: 0.68rem;
		color: var(--fg-dim);
		text-decoration: none;
		white-space: nowrap;
	}
	.span-link:hover {
		color: var(--link);
		text-decoration: underline;
	}
	.detail {
		padding: 0.5rem 0.75rem 0.75rem;
		background: color-mix(in srgb, var(--panel-2) 60%, var(--panel));
		border-top: 1px dashed var(--border);
	}
	.cols {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(0, 1.2fr);
		gap: 0.75rem;
	}
	.h3 {
		margin: 0.4rem 0 0.2rem;
		font-size: 0.68rem;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--fg-muted);
	}
	.h3:first-child {
		margin-top: 0;
	}
	.kv {
		display: grid;
		grid-template-columns: max-content minmax(0, 1fr);
		gap: 0.1rem 0.75rem;
		margin: 0;
		font-size: 0.74rem;
	}
	.kv dt {
		color: var(--orange);
	}
	.kv dd {
		margin: 0;
		overflow-wrap: anywhere;
	}
	.todo-val {
		color: var(--fg-dim);
		border-bottom: 1px dashed var(--fg-dim);
	}
	.when {
		margin: 0.4rem 0 0;
		font-size: 0.7rem;
	}
	.code {
		margin: 0;
		padding: 0.5rem 0.65rem;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: var(--bg);
		font-size: 0.72rem;
		line-height: 1.45;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		overflow-x: auto;
		max-height: 24rem;
		overflow-y: auto;
	}
	.links {
		display: flex;
		flex-wrap: wrap;
		gap: 0.4rem;
		margin-top: 0.6rem;
	}
	.btn.small {
		min-height: 1.7rem;
		font-size: 0.72rem;
		text-decoration: none;
	}
	@media (max-width: 639.98px) {
		.line {
			min-width: 0;
		}
		.head {
			grid-template-columns: 4px minmax(0, auto) minmax(0, auto) minmax(0, 1fr);
			grid-template-areas:
				'bar ts lvl labels'
				'bar msg msg msg';
			row-gap: 0.15rem;
			overflow: hidden;
		}
		.labels {
			flex-wrap: wrap;
			justify-content: flex-end;
			min-width: 0;
			white-space: normal;
		}
		.chip {
			max-width: 100%;
			overflow: hidden;
			text-overflow: ellipsis;
		}
		.bar {
			grid-area: bar;
		}
		.ts {
			grid-area: ts;
			font-size: 0.7rem;
		}
		.lvl {
			grid-area: lvl;
		}
		.labels {
			grid-area: labels;
			justify-self: end;
		}
		.msg {
			grid-area: msg;
		}
		.span-link {
			padding: 0 0.35rem;
		}
		.cols {
			grid-template-columns: 1fr;
		}
	}
</style>
