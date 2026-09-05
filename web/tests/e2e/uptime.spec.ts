import { expect, test, type Page } from '@playwright/test';

// The uptime status page over /api/uptime: one card per target, 90 heartbeat
// cells each, unconfigured targets grey with the TODO url, down targets red,
// the self-probe note, the honest banner, and a 60 s auto-refresh.

interface Heartbeats {
	targets: {
		target: string;
		url: string;
		status: 'up' | 'down' | 'unconfigured' | 'unknown';
		note: string | null;
		last: { error: string | null } | null;
	}[];
}

async function ready(page: Page, path: string) {
	await page.goto(path);
	await page.locator('main[data-hydrated="true"]').waitFor();
}

async function noHorizontalScroll(page: Page) {
	const { scrollWidth, clientWidth } = await page.evaluate(() => ({
		scrollWidth: document.documentElement.scrollWidth,
		clientWidth: document.documentElement.clientWidth
	}));
	expect(scrollWidth, 'page must not scroll horizontally').toBeLessThanOrEqual(clientWidth);
}

test.describe('uptime status page', () => {
	test('renders every target with 90 cells, the unconfigured state and the red-when-down rule', async ({
		page,
		request
	}) => {
		const hb = (await (await request.get('/api/uptime')).json()) as Heartbeats;
		expect(hb.targets.length).toBeGreaterThan(0);
		await ready(page, '/uptime');
		await expect(page).toHaveTitle('Uptime');
		// the live fetch replaced the snapshot
		await expect(page.getByText(/live · refreshed/)).toBeVisible();

		for (const t of hb.targets) {
			const card = page.locator(`[data-target="${t.target}"]`);
			await expect(card).toBeVisible();
			await expect(card).toHaveAttribute('data-status', t.status);
			const bar = card.getByRole('img', { name: /90-day heartbeat/ });
			await expect(bar).toHaveAttribute('data-cells', '90');
			expect(await bar.locator('.cell').count()).toBe(90);
			if (t.status === 'unconfigured') {
				await expect(card).toContainText('unconfigured');
				await expect(card).toContainText('TODO(divy)');
				// nothing green for a target that was never probed
				expect(await bar.locator('.cell-up').count()).toBe(0);
				await expect(card.getByRole('status')).toContainText(/not probed/i);
			} else {
				await expect(card.getByRole('link', { name: t.url })).toHaveAttribute('href', t.url);
			}
			if (t.status === 'down') {
				await expect(card.locator('.status')).toHaveText('down');
				await expect(card.getByRole('alert')).toContainText(t.last?.error?.split(':')[0] ?? '');
				const color = await card.locator('.status').evaluate((el) => getComputedStyle(el).color);
				expect(color).not.toBe('rgb(115, 191, 105)'); // never the green token
			}
			if (t.status === 'up') {
				await expect(card.locator('.status')).toHaveText('up');
				expect(await bar.locator('.cell-up, .cell-partial').count()).toBeGreaterThan(0);
			}
			if (t.note) await expect(card.locator('[data-note]')).toContainText(t.note);
		}

		// the banner is honest: red whenever a monitored target is down, and it lists the unconfigured ones
		const banner = page.locator('[data-overall]');
		const down = hb.targets.filter((t) => t.status === 'down').length;
		const unconf = hb.targets.filter((t) => t.status === 'unconfigured');
		if (down > 0) await expect(banner).toHaveAttribute('data-overall', 'degraded');
		else await expect(banner).not.toHaveAttribute('data-overall', 'degraded');
		for (const u of unconf) await expect(banner).toContainText(u.target);
		await expect(page.locator('[data-collector-state]')).toHaveAttribute(
			'data-collector-state',
			/ok|never|stale|disabled|missing/
		);
		await noHorizontalScroll(page);
	});

	test('auto-refresh re-fetches /api/uptime and can be switched off', async ({ page }) => {
		let calls = 0;
		page.on('request', (r) => {
			if (new URL(r.url()).pathname.startsWith('/api/uptime')) calls++;
		});
		await page.clock.install();
		await ready(page, '/uptime');
		await expect.poll(() => calls).toBeGreaterThanOrEqual(1);
		const before = calls;
		await page.clock.runFor(61_000);
		await expect.poll(() => calls).toBeGreaterThan(before);
		await page.getByRole('checkbox', { name: /auto-refresh/i }).uncheck();
		const stopped = calls;
		await page.clock.runFor(61_000);
		expect(calls).toBe(stopped);
		await page.getByRole('button', { name: 'Refresh' }).click();
		await expect.poll(() => calls).toBeGreaterThan(stopped);
	});
});

test.describe('uptime on a 390 px phone', () => {
	test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

	test('keeps 90 cells per target without horizontal scroll', async ({ page, request }) => {
		const hb = (await (await request.get('/api/uptime')).json()) as Heartbeats;
		await ready(page, '/uptime');
		for (const t of hb.targets) {
			const bar = page.locator(`[data-target="${t.target}"]`).getByRole('img');
			expect(await bar.locator('.cell').count()).toBe(90);
		}
		await noHorizontalScroll(page);
	});
});
