<script lang="ts">
	// /contact as a runbook (brief §3.7): escalation path, channels, a
	// copyable `curl <origin>/healthz` with its live response, scope chips and
	// the on-call timezone with the current local time. Every value comes
	// from /api/content/profile; TODO(divy) placeholders render literally.
	import { onMount } from 'svelte';
	import { api, isTodo } from '$lib/api/client';
	import TodoBadge from '$lib/postmortem/TodoBadge.svelte';

	let { data } = $props();

	let profile = $derived(data.profile);

	// ---- curl block: the site origin baked at build time; the browser's origin as a fallback ----
	let browserOrigin = $state('');
	let origin = $derived(data.siteOrigin || browserOrigin);
	let curl = $derived(`curl ${origin || '<site-origin>'}/healthz`);
	let snapshot = $derived(data.healthz ? JSON.stringify(data.healthz) : '');

	let liveText = $state('');
	let liveStatus = $state(0);
	let liveTraceId = $state('');
	let liveError = $state('');
	let fetchedAt = $state<number | null>(null);

	async function refresh() {
		try {
			const res = await fetch(`${api.base}/healthz`, { headers: { Accept: 'application/json' } });
			liveText = (await res.text()).trim();
			liveStatus = res.status;
			liveTraceId = res.headers.get('x-divy-trace-id') ?? '';
			liveError = res.ok ? '' : `HTTP ${res.status}`;
			fetchedAt = Date.now();
		} catch (e) {
			liveError = e instanceof Error ? e.message : String(e);
		}
	}

	// ---- copy ----
	let copied = $state(false);
	let copyTimer: ReturnType<typeof setTimeout> | undefined;
	async function copy() {
		try {
			await navigator.clipboard.writeText(curl);
			copied = true;
			clearTimeout(copyTimer);
			copyTimer = setTimeout(() => (copied = false), 1600);
		} catch {
			// clipboard unavailable (insecure context): the text is selectable
			copied = false;
		}
	}

	// ---- clock in the profile's timezone ----
	let nowMs = $state<number | null>(null);
	function inTz(ms: number, opts: Intl.DateTimeFormatOptions): string {
		try {
			return new Intl.DateTimeFormat('en-GB', { timeZone: profile.tz, ...opts }).format(ms);
		} catch {
			return '—';
		}
	}
	function offsetOf(ms: number): string {
		try {
			const parts = new Intl.DateTimeFormat('en-GB', {
				timeZone: profile.tz,
				timeZoneName: 'shortOffset'
			}).formatToParts(ms);
			return parts.find((p) => p.type === 'timeZoneName')?.value.replace('GMT', 'UTC') ?? '';
		} catch {
			return '';
		}
	}
	let localTime = $derived(
		nowMs === null
			? '--:--:--'
			: inTz(nowMs, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
	);
	let localDate = $derived(
		nowMs === null
			? ''
			: inTz(nowMs, { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric' })
	);
	let offset = $derived(nowMs === null ? '' : offsetOf(nowMs));

	onMount(() => {
		browserOrigin = location.origin;
		nowMs = Date.now();
		void refresh();
		const clock = setInterval(() => (nowMs = Date.now()), 1000);
		return () => {
			clearInterval(clock);
			clearTimeout(copyTimer);
		};
	});

	type LinkKind = 'todo' | 'http' | 'mailto' | 'path' | 'text';
	function kindOf(v: string): LinkKind {
		if (isTodo(v)) return 'todo';
		if (/^https?:\/\//i.test(v)) return 'http';
		if (/^mailto:/i.test(v)) return 'mailto';
		if (v.startsWith('/')) return 'path';
		return 'text';
	}
	function pretty(v: string): string {
		return v
			.replace(/^https?:\/\//i, '')
			.replace(/^mailto:/i, '')
			.replace(/\/$/, '');
	}

	const channels = $derived([
		{ key: 'email', label: 'Email', value: profile.links.email },
		{ key: 'github', label: 'GitHub', value: profile.links.github },
		{ key: 'linkedin', label: 'LinkedIn', value: profile.links.linkedin },
		{ key: 'resume', label: 'Resume (PDF)', value: profile.links.resume },
		{ key: 'calendar', label: 'Book a slot', value: profile.links.calendar }
	]);

	let description = $derived(
		`Runbook for reaching ${profile.name} (@${profile.handle}): escalation path, channels, a liveness check and the on-call timezone (${profile.tz}).`
	);
	let canonical = $derived(data.siteOrigin ? `${data.siteOrigin}/contact` : '');
</script>

{#snippet target(value: string)}
	{@const k = kindOf(value)}
	{#if k === 'todo'}
		<TodoBadge {value} />
	{:else if k === 'http'}
		<a href={value} rel="external noopener" target="_blank">{pretty(value)}</a>
	{:else if k === 'mailto'}
		<a href={value}>{pretty(value)}</a>
	{:else if k === 'path'}
		<a href={value} rel="external">{value}</a>
	{:else}
		<span>{value}</span>
	{/if}
{/snippet}

<svelte:head>
	<title>Runbook: contact</title>
	<meta name="description" content={description} />
	<meta property="og:type" content="profile" />
	<meta property="og:title" content="Runbook: contact · divy.dev" />
	<meta property="og:description" content={description} />
	{#if canonical}
		<link rel="canonical" href={canonical} />
		<meta property="og:url" content={canonical} />
	{/if}
	<meta name="twitter:card" content="summary" />
</svelte:head>

<article class="runbook" aria-labelledby="rb-title">
	<header class="head">
		<p class="kicker mono"><span class="chip">runbook</span><span>contact</span></p>
		<h1 id="rb-title" class="title mono">Runbook: contact</h1>
		<p class="sub">
			How to reach the service owner. Values are read from
			<a href="/api/content/profile" rel="external" class="mono">/api/content/profile</a>;
			placeholders are shown as they are, never guessed.
		</p>
	</header>

	<section class="panel block" aria-labelledby="rb-owner">
		<div class="panel-header"><h2 id="rb-owner" class="h">0. On-call</h2></div>
		<dl class="kv mono">
			<div>
				<dt>Owner</dt>
				<dd>
					{profile.name}
					<a href={profile.links.github} rel="external noopener" target="_blank"
						>@{profile.handle}</a
					>
				</dd>
			</div>
			<div>
				<dt>Location</dt>
				<dd>{profile.location}</dd>
			</div>
			<div>
				<dt>Timezone</dt>
				<dd class="tz">
					<span>{profile.tz}</span>
					<span class="clock" aria-live="off" data-clock
						><time datetime={nowMs === null ? undefined : new Date(nowMs).toISOString()}
							>{localTime}</time
						>{#if offset}<span class="dim"> {offset}</span>{/if}</span
					>
					{#if localDate}<span class="dim">{localDate}</span>{/if}
				</dd>
			</div>
			<div>
				<dt>Status</dt>
				<dd class="chips">
					{#if profile.open_to_work}
						<span class="chip open" title="divy_open_to_work == 1 fires DivyAvailableForHire"
							>open to work</span
						>
					{:else}
						<span class="chip">not looking</span>
					{/if}
				</dd>
			</div>
			<div class="wide">
				<dt>Open to</dt>
				<dd class="chips" data-open-to>
					{#each profile.open_to as o (o)}
						<span class="chip scope">{o}</span>
					{/each}
				</dd>
			</div>
		</dl>
	</section>

	<section class="panel block" aria-labelledby="rb-esc">
		<div class="panel-header"><h2 id="rb-esc" class="h">1. Escalation path</h2></div>
		<div class="scroll">
			<table class="esc mono">
				<caption class="sr-only">Escalation steps in order</caption>
				<thead>
					<tr>
						<th scope="col">Step</th>
						<th scope="col">Channel</th>
						<th scope="col">Target</th>
						<th scope="col">Response time</th>
						<th scope="col">Note</th>
					</tr>
				</thead>
				<tbody>
					{#each profile.escalation as e (e.step)}
						<tr>
							<td class="step">{e.step}</td>
							<td>{e.channel}</td>
							<td class="target">{@render target(e.target)}</td>
							<td>
								{#if isTodo(e.response_time)}<TodoBadge
										value={e.response_time}
									/>{:else}{e.response_time}{/if}
							</td>
							<td class="note">{e.note ?? ''}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	</section>

	<section class="panel block" aria-labelledby="rb-ch">
		<div class="panel-header"><h2 id="rb-ch" class="h">2. Channels</h2></div>
		<ul class="channels" aria-label="Contact channels">
			{#each channels as c (c.key)}
				<li class="ch" data-channel={c.key}>
					<span class="label">{c.label}</span>
					<span class="value mono">
						{#if c.key === 'resume' && kindOf(c.value) !== 'todo' && kindOf(c.value) !== 'text'}
							<a href={c.value} rel="external" download>Download {pretty(c.value)}</a>
						{:else}
							{@render target(c.value)}
						{/if}
					</span>
				</li>
			{/each}
		</ul>
	</section>

	<section class="panel block" aria-labelledby="rb-live">
		<div class="panel-header">
			<h2 id="rb-live" class="h">3. Verify the service is alive</h2>
			<span class="spacer"></span>
			<button type="button" class="btn" onclick={copy} aria-label="Copy the curl command"
				>{copied ? 'Copied' : 'Copy'}</button
			>
			<span class="sr-only" aria-live="polite"
				>{copied ? 'Command copied to the clipboard' : ''}</span
			>
		</div>
		<pre class="term" data-keyboard-ignore><code class="cmd" data-curl>{curl}</code>
<code class="out" data-healthz-live={liveText ? 'true' : 'false'}
				>{#if liveText}{liveText}{:else if snapshot}{snapshot}{:else}(no response yet){/if}</code
			></pre>
		<p class="meta mono" aria-live="polite">
			{#if liveText}
				<span class="ok">●</span> live from <code>/healthz</code>
				{#if liveStatus}(HTTP {liveStatus}){/if}
				{#if fetchedAt}at {new Date(fetchedAt).toISOString().slice(11, 19)}Z{/if}
				{#if liveTraceId}
					· trace <a href="/trace/{liveTraceId}">{liveTraceId}</a>
				{/if}
			{:else if liveError}
				<span class="warn">●</span> snapshot from build time · live fetch failed: {liveError}
			{:else if snapshot}
				<span class="dim">●</span> snapshot from build time
			{:else}
				<span class="warn">●</span> no healthz response at build time
			{/if}
			<button type="button" class="btn small" onclick={refresh}>Re-run</button>
		</p>
	</section>
</article>

<style>
	.runbook {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
		max-width: 960px;
		margin: 0 auto;
	}
	.head {
		padding: 0.5rem 0.25rem 0;
	}
	.kicker {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		margin: 0 0 0.25rem;
		font-size: 0.72rem;
		color: var(--fg-dim);
	}
	.title {
		margin: 0;
		font-size: clamp(1.4rem, 3.5vw, 2rem);
		font-weight: 700;
		letter-spacing: -0.01em;
	}
	.sub {
		margin: 0.35rem 0 0;
		max-width: 75ch;
		color: var(--fg-muted);
		font-size: 0.9rem;
	}
	.h {
		margin: 0;
		font-size: 0.8125rem;
		font-weight: 600;
	}
	.spacer {
		flex: 1;
	}
	.kv {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
		gap: 0.6rem 1.25rem;
		margin: 0;
		padding: 0.75rem 0.9rem;
		font-size: 0.8rem;
	}
	.kv div {
		display: flex;
		flex-direction: column;
		gap: 0.2rem;
		min-width: 0;
	}
	.kv .wide {
		grid-column: 1 / -1;
	}
	.kv dt {
		font-size: 0.66rem;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--fg-dim);
	}
	.kv dd {
		margin: 0;
		overflow-wrap: anywhere;
	}
	.tz {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 0.6rem;
		align-items: baseline;
	}
	.clock {
		font-variant-numeric: tabular-nums;
		color: var(--green);
		font-weight: 600;
	}
	.dim {
		color: var(--fg-dim);
	}
	.clock .dim {
		margin-left: 0.35rem;
	}
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: 0.3rem;
	}
	.chip.open {
		border-color: var(--green);
		color: var(--green);
	}
	.chip.scope {
		color: var(--fg);
	}
	.scroll {
		overflow-x: auto;
	}
	.esc {
		width: 100%;
		border-collapse: collapse;
		font-size: 0.78rem;
	}
	.esc th,
	.esc td {
		padding: 0.45rem 0.75rem;
		text-align: left;
		vertical-align: top;
		border-bottom: 1px solid var(--border);
	}
	.esc th {
		font-size: 0.66rem;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--fg-dim);
		white-space: nowrap;
	}
	.esc tbody tr:last-child td {
		border-bottom: 0;
	}
	.step {
		color: var(--orange);
		font-weight: 700;
	}
	.target {
		min-width: 10rem;
		overflow-wrap: anywhere;
	}
	.note {
		min-width: 14rem;
		color: var(--fg-muted);
	}
	.channels {
		list-style: none;
		margin: 0;
		padding: 0.25rem 0;
	}
	.ch {
		display: grid;
		grid-template-columns: 9rem 1fr;
		gap: 0.75rem;
		padding: 0.45rem 0.9rem;
		border-bottom: 1px solid var(--border);
		font-size: 0.82rem;
	}
	.ch:last-child {
		border-bottom: 0;
	}
	.label {
		color: var(--fg-muted);
	}
	.value {
		overflow-wrap: anywhere;
	}
	.term {
		margin: 0;
		padding: 0.75rem 0.9rem;
		overflow-x: auto;
		background: var(--bg);
		border-top: 1px solid var(--border);
		font-size: 0.8rem;
		line-height: 1.5;
	}
	.cmd::before {
		content: '$ ';
		color: var(--fg-dim);
	}
	.cmd {
		color: var(--fg);
	}
	.out {
		display: block;
		color: var(--green);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.meta {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.35rem;
		margin: 0;
		padding: 0.5rem 0.9rem;
		border-top: 1px solid var(--border);
		font-size: 0.72rem;
		color: var(--fg-dim);
	}
	.meta code {
		color: var(--fg-muted);
	}
	.ok {
		color: var(--green);
	}
	.warn {
		color: var(--yellow);
	}
	.btn.small {
		min-height: 1.5rem;
		min-width: 0;
		padding: 0 0.45rem;
		font-size: 0.7rem;
		margin-left: auto;
	}
	@media (max-width: 639.98px) {
		.ch {
			grid-template-columns: 1fr;
			gap: 0.15rem;
		}
		.kv {
			grid-template-columns: 1fr 1fr;
		}
	}
</style>
