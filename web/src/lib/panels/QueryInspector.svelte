<script lang="ts">
	// "View query" dialog: the exact PromQL of every target, the resolved
	// start/end/step, and a copyable curl for /api/v1/query_range (GET form)
	// or /api/v1/query for instant targets. Focus is trapped; Esc closes and
	// focus returns to the opener.
	import { onMount, tick } from 'svelte';
	import type { ResolvedRange } from '$lib/timerange';
	import { humanStep } from '$lib/timerange';
	import { curlInstant, curlRange } from './prom';

	export interface InspectorEntry {
		refId: string;
		expr: string;
		kind: 'instant' | 'range';
		hidden: boolean;
	}

	let {
		open = $bindable(false),
		title,
		entries,
		range,
		origin,
		exploreHref = undefined
	}: {
		open?: boolean;
		title: string;
		entries: InspectorEntry[];
		range: ResolvedRange;
		origin: string;
		exploreHref?: string;
	} = $props();

	let dialog = $state<HTMLDivElement | null>(null);
	let opener: HTMLElement | null = null;
	let copied = $state<string | null>(null);

	function curlFor(e: InspectorEntry): string {
		return e.kind === 'range'
			? curlRange(origin, e.expr, range.from, range.to, range.step)
			: curlInstant(origin, e.expr);
	}

	function iso(s: number): string {
		return new Date(s * 1000).toISOString();
	}

	async function copy(key: string, text: string) {
		try {
			await navigator.clipboard.writeText(text);
			copied = key;
			setTimeout(() => (copied = null), 1500);
		} catch {
			// clipboard blocked: the text is selectable in the <pre>
			copied = null;
		}
	}

	function close() {
		open = false;
	}

	function focusables(): HTMLElement[] {
		return dialog
			? [
					...dialog.querySelectorAll<HTMLElement>(
						'button, [href], input, textarea, [tabindex]:not([tabindex="-1"])'
					)
				]
			: [];
	}

	function onKey(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.preventDefault();
			e.stopPropagation();
			close();
			return;
		}
		if (e.key === 'Tab') {
			const els = focusables();
			if (els.length === 0) return;
			const first = els[0]!;
			const last = els[els.length - 1]!;
			if (e.shiftKey && document.activeElement === first) {
				e.preventDefault();
				last.focus();
			} else if (!e.shiftKey && document.activeElement === last) {
				e.preventDefault();
				first.focus();
			}
		}
	}

	$effect(() => {
		if (open) {
			opener = document.activeElement as HTMLElement | null;
			void tick().then(() => dialog?.querySelector<HTMLElement>('h2')?.focus());
		} else if (opener) {
			opener.focus();
			opener = null;
		}
	});

	onMount(() => () => (open = false));
</script>

{#if open}
	<div
		class="backdrop fade-in"
		role="presentation"
		onpointerdown={(e) => e.target === e.currentTarget && close()}
	>
		<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
		<div
			class="dialog slide-up panel"
			role="dialog"
			tabindex="-1"
			aria-modal="true"
			aria-labelledby="inspector-title"
			bind:this={dialog}
			onkeydown={onKey}
		>
			<header class="head">
				<h2 id="inspector-title" tabindex="-1">Query · {title}</h2>
				<button type="button" class="btn" onclick={close} aria-label="Close query inspector"
					>Close</button
				>
			</header>
			<div class="content">
				<dl class="range mono">
					<dt>start</dt>
					<dd>{range.from} <span class="dim">({iso(range.from)})</span></dd>
					<dt>end</dt>
					<dd>{range.to} <span class="dim">({iso(range.to)})</span></dd>
					<dt>step</dt>
					<dd>
						{range.step}s
						<span class="dim"
							>({humanStep(range.step)}, {Math.floor((range.to - range.from) / range.step) + 1} points)</span
						>
					</dd>
					<dt>lookback</dt>
					<dd><span class="dim">API default (QUERY_LOOKBACK_DELTA, 26h): not sent</span></dd>
				</dl>
				{#each entries as e (e.refId)}
					{@const curl = curlFor(e)}
					<section class="entry" aria-label="Target {e.refId}">
						<div class="entry-head">
							<span class="chip">{e.refId}</span>
							<span class="chip"
								>{e.kind === 'range' ? '/api/v1/query_range' : '/api/v1/query'}</span
							>
							{#if e.hidden}
								<span
									class="chip hidden-chip"
									title="Runs, is not drawn; its latest value feeds the panel">hidden</span
								>
							{/if}
						</div>
						<div class="block">
							<div class="block-head">
								<span>PromQL</span>
								<button type="button" class="btn small" onclick={() => copy('q' + e.refId, e.expr)}>
									{copied === 'q' + e.refId ? 'Copied' : 'Copy'}
								</button>
							</div>
							<pre class="code"><code>{e.expr}</code></pre>
						</div>
						<div class="block">
							<div class="block-head">
								<span>curl</span>
								<button type="button" class="btn small" onclick={() => copy('c' + e.refId, curl)}>
									{copied === 'c' + e.refId ? 'Copied' : 'Copy'}
								</button>
							</div>
							<pre class="code"><code>{curl}</code></pre>
						</div>
					</section>
				{/each}
			</div>
			<footer class="foot">
				{#if exploreHref}
					<a class="btn btn-primary" href={exploreHref}>Open in Explore ↗</a>
				{/if}
				<span class="hint mono"
					>Same origin, no auth: add it to Grafana as a Prometheus data source.</span
				>
			</footer>
		</div>
	</div>
{/if}

<style>
	.backdrop {
		position: fixed;
		inset: 0;
		z-index: 60;
		display: flex;
		align-items: flex-start;
		justify-content: center;
		padding: 4rem 0.75rem 1rem;
		background: rgba(0, 0, 0, 0.55);
		overflow-y: auto;
	}
	.dialog {
		width: min(60rem, 100%);
		max-height: calc(100dvh - 5rem);
		display: flex;
		flex-direction: column;
		box-shadow: 0 20px 60px rgba(0, 0, 0, 0.5);
	}
	.head,
	.foot {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		padding: 0.6rem 0.9rem;
		border-bottom: 1px solid var(--border);
	}
	.foot {
		border-bottom: 0;
		border-top: 1px solid var(--border);
		flex-wrap: wrap;
	}
	.head h2 {
		margin: 0;
		flex: 1;
		font-size: 0.9rem;
		outline: none;
	}
	.content {
		padding: 0.75rem 0.9rem;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 0.9rem;
	}
	.range {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: 0.15rem 0.9rem;
		margin: 0;
		font-size: 0.75rem;
	}
	.range dt {
		color: var(--fg-dim);
	}
	.range dd {
		margin: 0;
		overflow-wrap: anywhere;
	}
	.dim {
		color: var(--fg-dim);
	}
	.entry {
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
		padding-top: 0.6rem;
		border-top: 1px dashed var(--border);
	}
	.entry-head {
		display: flex;
		gap: 0.35rem;
	}
	.hidden-chip {
		color: var(--yellow);
	}
	.block-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 0.25rem;
		font-size: 0.72rem;
		color: var(--fg-muted);
	}
	.btn.small {
		min-height: 1.6rem;
		font-size: 0.72rem;
	}
	.code {
		margin: 0;
		padding: 0.5rem 0.65rem;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: var(--bg);
		font-size: 0.75rem;
		line-height: 1.45;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		overflow-x: auto;
		user-select: all;
	}
	.hint {
		font-size: 0.7rem;
		color: var(--fg-dim);
	}
</style>
