<script lang="ts">
	// The floating query console opened by typing `promql` anywhere (brief §4).
	// A non-modal dialog docked bottom-left (toasts keep the right): a mono output log (commands,
	// tables, red errors) and one input with ↑/↓ history; Esc closes it. The
	// dialog is marked data-keyboard-ignore so the page's single-key shortcuts
	// stay quiet while it has focus.
	import { tick } from 'svelte';
	import { HELP_LINES, runCommand, type Output } from './commands';

	let { open = $bindable(false) }: { open?: boolean } = $props();

	let input = $state('');
	let busy = $state(false);
	let outputs = $state<(Output & { id: number })[]>([]);
	let history = $state<string[]>([]);
	let histIdx = $state(-1);
	let draft = '';
	let seq = 0;
	let field = $state<HTMLInputElement | null>(null);
	let log = $state<HTMLElement | null>(null);

	function push(...items: Output[]) {
		for (const o of items) {
			if (o.kind === 'clear') outputs = [];
			else outputs = [...outputs, { ...o, id: ++seq }];
		}
		void tick().then(() => log?.scrollTo({ top: log.scrollHeight }));
	}

	export function show() {
		open = true;
		if (outputs.length === 0)
			push({ kind: 'text', text: HELP_LINES.slice(0, 1).join('') + ' — type help for commands' });
		void tick().then(() => field?.focus());
	}

	export function hide() {
		open = false;
	}

	async function submit() {
		const line = input.trim();
		if (!line || busy) return;
		input = '';
		histIdx = -1;
		draft = '';
		if (history[history.length - 1] !== line) history = [...history, line];
		push({ kind: 'cmd', text: line });
		busy = true;
		try {
			push(...(await runCommand(line)));
		} finally {
			busy = false;
			void tick().then(() => field?.focus());
		}
	}

	function onKey(e: KeyboardEvent) {
		if (e.key === 'Enter') {
			e.preventDefault();
			void submit();
		} else if (e.key === 'Escape') {
			e.preventDefault();
			e.stopPropagation();
			hide();
		} else if (e.key === 'ArrowUp') {
			if (history.length === 0) return;
			e.preventDefault();
			if (histIdx === -1) {
				draft = input;
				histIdx = history.length - 1;
			} else if (histIdx > 0) histIdx--;
			input = history[histIdx] ?? '';
		} else if (e.key === 'ArrowDown') {
			if (histIdx === -1) return;
			e.preventDefault();
			if (histIdx < history.length - 1) {
				histIdx++;
				input = history[histIdx] ?? '';
			} else {
				histIdx = -1;
				input = draft;
			}
		}
	}

	$effect(() => {
		if (open) void tick().then(() => field?.focus());
	});
</script>

{#if open}
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
	<div
		class="console slide-up"
		role="dialog"
		aria-label="Query console"
		aria-modal="false"
		tabindex="-1"
		data-console
		data-keyboard-ignore
		onkeydown={(e) => {
			if (e.key === 'Escape') {
				e.preventDefault();
				e.stopPropagation();
				hide();
			}
		}}
	>
		<header class="head mono">
			<span class="title"><span class="dot" aria-hidden="true"></span> promql console</span>
			<span class="hint">GET /api/v1/query · kubectl get pods · help</span>
			<button type="button" class="close" aria-label="Close console" onclick={hide}>×</button>
		</header>
		<div class="log mono" bind:this={log} aria-live="polite" aria-busy={busy}>
			{#each outputs as o (o.id)}
				{#if o.kind === 'cmd'}
					<div class="line cmd"><span class="prompt" aria-hidden="true">❯</span>{o.text}</div>
				{:else if o.kind === 'text'}
					<pre class="line text">{o.text}</pre>
				{:else if o.kind === 'error'}
					<pre class="line error" role="alert">{o.text}</pre>
				{:else if o.kind === 'table'}
					<div class="table-wrap">
						<table class="table">
							{#if o.caption}
								<caption>{o.caption}</caption>
							{/if}
							<thead>
								<tr>
									{#each o.table.columns as c (c)}
										<th scope="col">{c}</th>
									{/each}
								</tr>
							</thead>
							<tbody>
								{#each o.table.rows as r, i (i)}
									<tr>
										{#each r as cell, j (j)}
											<td
												class:status-running={o.table.columns[j] === 'STATUS' && cell === 'Running'}
												class:status-pending={o.table.columns[j] === 'STATUS' && cell === 'Pending'}
												class:num={o.table.columns[j] === 'VALUE' || o.table.columns[j] === 'RESTARTS'}
												>{cell}</td
											>
										{/each}
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			{/each}
		</div>
		<form
			class="row mono"
			onsubmit={(e) => {
				e.preventDefault();
				void submit();
			}}
		>
			<span class="prompt" aria-hidden="true">❯</span>
			<label class="sr-only" for="console-input">Console command or PromQL expression</label>
			<input
				id="console-input"
				class="input"
				bind:this={field}
				bind:value={input}
				type="text"
				autocomplete="off"
				autocapitalize="off"
				spellcheck="false"
				placeholder={busy ? 'running…' : 'divy_uptime_seconds, kubectl get pods, help'}
				disabled={busy}
				onkeydown={onKey}
			/>
			<button type="submit" class="btn run" disabled={busy || !input.trim()}>Run</button>
		</form>
	</div>
{/if}

<style>
	.console {
		position: fixed;
		left: 0.75rem;
		bottom: 0.75rem;
		z-index: 70;
		display: flex;
		flex-direction: column;
		width: min(44rem, calc(100vw - 1.5rem));
		max-height: min(24rem, 60vh);
		border: 1px solid var(--border);
		border-radius: 6px;
		background: color-mix(in srgb, var(--panel) 96%, transparent);
		backdrop-filter: blur(6px);
		box-shadow: 0 16px 40px rgba(0, 0, 0, 0.5);
		font-size: 0.78rem;
	}
	.head {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		padding: 0.35rem 0.4rem 0.35rem 0.6rem;
		border-bottom: 1px solid var(--border);
		color: var(--fg-muted);
	}
	.title {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		color: var(--fg);
		font-weight: 600;
		white-space: nowrap;
	}
	.dot {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 50%;
		background: var(--green);
	}
	.hint {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 0.68rem;
		color: var(--fg-dim);
	}
	.close {
		width: 2rem;
		height: 2rem;
		border: 0;
		border-radius: 4px;
		background: transparent;
		color: var(--fg-muted);
		font-size: 1.1rem;
		line-height: 1;
		cursor: pointer;
	}
	.close:hover {
		background: var(--panel-3);
		color: var(--fg);
	}
	.log {
		flex: 1;
		min-height: 6rem;
		padding: 0.4rem 0.6rem;
		overflow: auto;
		background: var(--bg);
	}
	.line {
		margin: 0;
		padding: 0.1rem 0;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		font: inherit;
	}
	.cmd {
		color: var(--fg);
	}
	.text {
		color: var(--fg-muted);
	}
	.error {
		color: var(--red);
	}
	.prompt {
		margin-right: 0.45rem;
		color: var(--green);
	}
	.table-wrap {
		overflow-x: auto;
		margin: 0.2rem 0 0.4rem;
	}
	.table {
		border-collapse: collapse;
		white-space: nowrap;
	}
	.table caption {
		caption-side: bottom;
		padding-top: 0.2rem;
		text-align: left;
		color: var(--fg-dim);
		font-size: 0.68rem;
	}
	.table th,
	.table td {
		padding: 0.1rem 1.2rem 0.1rem 0;
		text-align: left;
		vertical-align: top;
	}
	.table th {
		color: var(--fg-muted);
		font-weight: 600;
	}
	.table td.num {
		font-variant-numeric: tabular-nums;
	}
	.status-running {
		color: var(--green);
	}
	.status-pending {
		color: var(--yellow);
	}
	.row {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		padding: 0.35rem 0.6rem;
		border-top: 1px solid var(--border);
	}
	.row .input {
		flex: 1;
		min-width: 0;
		min-height: 2.2rem;
		font-family: inherit;
	}
	.run {
		min-height: 2.2rem;
	}
	@media (max-width: 639.98px) {
		.console {
			right: 0.5rem;
			left: 0.5rem;
			bottom: 0.5rem;
			width: auto;
			max-height: 70vh;
		}
		.hint {
			display: none;
		}
	}
</style>
