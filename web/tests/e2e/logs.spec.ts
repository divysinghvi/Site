import { expect, test, type Page } from '@playwright/test';

// The logs explorer: the default query lists lines newest first, the level
// chips rewrite the selector's level matcher, a line expands into its JSON
// with a link to its span, autocomplete offers label names and values from
// the Loki API, the curl block reproduces the query_range call, and the live
// tail replays the result with a typewriter cadence (off under reduced motion).

async function ready(page: Page, path: string) {
	await page.goto(path);
	await page.locator('main[data-hydrated="true"]').waitFor();
}

test.use({ locale: 'en-US' });

test.describe('logs', () => {
	test('the default query shows lines newest first with level, labels and message', async ({
		page
	}) => {
		await ready(page, '/logs');
		const rows = page.locator('[data-log-row]');
		await expect(rows.first()).toBeVisible({ timeout: 15_000 });
		expect(await rows.count()).toBeGreaterThan(10);
		await expect(page.getByRole('combobox', { name: 'LogQL query' })).toHaveValue('{service=~".+"}');
		// newest first: the first row's ns timestamp ≥ the last row's
		const ts = await rows.evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.logRow!));
		expect(BigInt(ts[0]!) >= BigInt(ts[ts.length - 1]!)).toBe(true);
		const first = rows.first();
		await expect(first.locator('.lvl')).toHaveText(/^(info|warn|error|debug)$/i);
		await expect(first.locator('.chip.svc')).not.toBeEmpty();
		await expect(first.locator('.msg')).not.toBeEmpty();
		await expect(page.locator('.res-head')).toContainText('newest first');
	});

	test('level chips rewrite the level matcher of the selector', async ({ page }) => {
		await ready(page, '/logs');
		await expect(page.locator('[data-log-row]').first()).toBeVisible({ timeout: 15_000 });
		const box = page.getByRole('combobox', { name: 'LogQL query' });
		const warn = page.getByRole('button', { name: /^warn/ });
		await warn.click();
		await expect(box).toHaveValue('{service=~".+", level="warn"}');
		await expect(page.locator('[data-log-row]').first()).toBeVisible({ timeout: 15_000 });
		const levels = await page
			.locator('[data-log-row]')
			.evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.level));
		expect(levels.length).toBeGreaterThan(0);
		expect(new Set(levels)).toEqual(new Set(['warn']));
		await page.getByRole('button', { name: /^error/ }).click();
		await expect(box).toHaveValue('{service=~".+", level=~"error|warn"}');
		await expect(page.getByRole('button', { name: /^error/ })).toHaveAttribute('aria-pressed', 'true');
		await expect(page.getByRole('button', { name: /^info/ })).toHaveAttribute('aria-pressed', 'false');
		await page.getByRole('button', { name: 'all' }).click();
		await expect(box).toHaveValue('{service=~".+"}');
		await expect(page).toHaveURL(/\/logs$/);
	});

	test('a line expands into pretty JSON and links to its span in the trace', async ({ page }) => {
		await ready(page, '/logs?q=%7Bservice%3D%22gradr%22%7D%20%7C%3D%20%22promoted%22');
		const row = page.locator('[data-log-row]').first();
		await expect(row).toBeVisible({ timeout: 15_000 });
		await expect(page.locator('[data-log-row]')).toHaveCount(1);
		const head = row.locator('button.head');
		await expect(head).toHaveAttribute('aria-expanded', 'false');
		await head.click();
		await expect(head).toHaveAttribute('aria-expanded', 'true');
		const json = row.locator('section[aria-label="Line JSON"] pre');
		await expect(json).toBeVisible();
		const text = await json.textContent();
		expect(text).toContain('"msg"');
		expect(text!.split('\n').length).toBeGreaterThan(3); // pretty-printed, one field per line
		const link = row.getByRole('link', { name: /View span in trace/ });
		await expect(link).toHaveAttribute('href', /^\/trace\/career\?span=/);
		await page.keyboard.press('Escape');
		await expect(head).toHaveAttribute('aria-expanded', 'false');
	});

	test('autocomplete offers label names, values and pipeline stages', async ({ page }) => {
		await ready(page, '/logs');
		const box = page.getByRole('combobox', { name: 'LogQL query' });
		await box.fill('');
		await box.pressSequentially('{ser');
		const list = page.getByRole('listbox', { name: 'Suggestions' });
		await expect(list).toBeVisible();
		await expect(list.getByRole('option', { name: /label service/ })).toBeVisible();
		await page.keyboard.press('Enter');
		await expect(box).toHaveValue('{service=""}');
		await expect(list.getByRole('option', { name: /value gradr/ })).toBeVisible({ timeout: 10_000 });
		await page.keyboard.press('Enter');
		await expect(box).toHaveValue('{service="gradr"}');
		await page.keyboard.press('End');
		await box.pressSequentially('} ');
		await expect(list.getByRole('option', { name: /\| json/ })).toBeVisible();
		await expect(list.getByRole('option', { name: /\|= "…"/ })).toBeVisible();
	});

	test('the curl block reproduces the query_range call', async ({ page }) => {
		await ready(page, '/logs?q=%7Blevel%3D%22debug%22%7D');
		await expect(page.locator('[data-log-row]').first()).toBeVisible({ timeout: 15_000 });
		const curl = page.locator('[data-curl]');
		await expect(curl).toContainText('curl -sG');
		await expect(curl).toContainText('/loki/api/v1/query_range');
		await expect(curl).toContainText(`--data-urlencode 'query={level="debug"}'`);
		await expect(curl).toContainText('direction=backward');
	});

	test('the live tail replays matching lines with a typewriter cadence', async ({ page }) => {
		await ready(page, '/logs?q=%7Bservice%3D%22gradr%22%7D');
		await expect(page.locator('[data-log-row]').first()).toBeVisible({ timeout: 15_000 });
		const total = await page.locator('[data-log-row]').count();
		await page.getByRole('button', { name: 'Live tail' }).click();
		const bar = page.locator('.tail-bar');
		await expect(bar).toBeVisible();
		await expect(bar).toContainText('live tail');
		// lines arrive one by one, characters in steps
		await expect(page.locator('[data-log-row]')).toHaveCount(1, { timeout: 5_000 });
		const typed = page.locator('[data-log-row]').first().locator('.msg');
		await expect(typed).toHaveClass(/typing/);
		await expect
			.poll(async () => Number(await bar.getAttribute('data-tail-count')), { timeout: 10_000 })
			.toBeGreaterThan(2);
		await page.getByRole('button', { name: 'Pause' }).click();
		await expect(bar).toContainText('paused');
		await page.getByRole('button', { name: 'Resume' }).click();
		await page.getByRole('button', { name: 'Stop tail' }).click();
		await expect(bar).toBeHidden();
		await expect(page.locator('[data-log-row]')).toHaveCount(total);
	});

	test('under prefers-reduced-motion the tail shows the whole replay at once', async ({
		browser
	}) => {
		const ctx = await browser.newContext({ reducedMotion: 'reduce', locale: 'en-US' });
		const page = await ctx.newPage();
		await ready(page, '/logs?q=%7Bservice%3D%22gradr%22%7D');
		await expect(page.locator('[data-log-row]').first()).toBeVisible({ timeout: 15_000 });
		const total = await page.locator('[data-log-row]').count();
		await page.getByRole('button', { name: 'Live tail' }).click();
		const bar = page.locator('.tail-bar');
		await expect(bar).toContainText('cadence off');
		await expect(bar).toHaveAttribute('data-tail-done', 'true');
		await expect(page.locator('[data-log-row]')).toHaveCount(total);
		await ctx.close();
	});

	test('a metric query renders a table and a bad query the API error', async ({ page }) => {
		await ready(
			page,
			'/logs?q=' + encodeURIComponent('sum by (service) (count_over_time({service=~".+"}[10y]))')
		);
		await expect(page.getByRole('table')).toBeVisible({ timeout: 15_000 });
		await expect(page.locator('.res-head')).toContainText('matrix');
		const box = page.getByRole('combobox', { name: 'LogQL query' });
		await box.fill('{service="gradr"} |= ');
		await page.keyboard.press('Enter');
		await expect(page.getByRole('status').first()).toContainText(/parse error|syntax error/, {
			timeout: 15_000
		});
	});
});
