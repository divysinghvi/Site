import { expect, test, type Page } from '@playwright/test';

// The metrics dashboard: every panel from content/panels.yaml renders, the
// range picker drives the query_range step, "View query" shows the PromQL +
// curl, drag/resize persist the layout in the URL hash, #panel= deep links
// focus a panel, empty series say which collector is missing, and the phone
// layout is one column with a <select> range picker.

interface PanelsFile {
	dashboard: { time: { default: string } };
	panels: { id: string; type: string; source: { kind: string } }[];
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

function rangeSteps(page: Page): string[] {
	const steps: string[] = [];
	page.on('request', (r) => {
		const u = new URL(r.url());
		if (u.pathname === '/api/v1/query_range') steps.push(u.searchParams.get('step') ?? '');
	});
	return steps;
}

// The sandbox's Chromium reports navigator.language "en-US@posix", which Intl
// rejects (uPlot builds an Intl.NumberFormat at import); real browsers send a
// valid tag, so the tests pin one.
test.use({ locale: 'en-US' });

test.describe('metrics dashboard', () => {
	test('renders every panel id from panels.yaml with a value or an honest empty state', async ({
		page,
		request
	}) => {
		const file = (await (await request.get('/api/content/panels')).json()) as PanelsFile;
		expect(file.panels.length).toBeGreaterThan(0);
		await ready(page, '/dashboard');
		// wait for the first query round
		await expect(
			page
				.locator(
					'[data-panel-id] [role="status"], [data-panel-id] canvas, [data-panel-id] svg[role="img"], [data-panel-id] .value'
				)
				.first()
		).toBeVisible();
		for (const p of file.panels) {
			const panel = page.locator(`[data-panel-id="${p.id}"]`);
			await expect(panel).toBeVisible();
			await expect(panel.getByRole('heading', { level: 2 })).toBeVisible();
			// never a blank body: a chart, a number, or the empty/error state
			await expect(
				panel.locator('canvas, .value, svg[role="img"], [role="status"], [role="list"]').first()
			).toBeVisible({ timeout: 15_000 });
		}
		// the stat row is computed from queries: years of experience is a live series here
		await expect(page.locator('[data-panel-id="stat-experience"] .value')).toHaveText(
			/^\d+(\.\d+)?$/
		);
		await noHorizontalScroll(page);
	});

	test('the range picker changes the query_range step and the hash', async ({ page }) => {
		const steps = rangeSteps(page);
		await ready(page, '/dashboard#range=24h');
		await expect.poll(() => steps.length).toBeGreaterThan(0);
		expect(steps).toContain('300');
		steps.length = 0;
		await page.getByRole('group', { name: 'Time range' }).getByText('7d', { exact: true }).click();
		await expect.poll(() => steps.length).toBeGreaterThan(0);
		expect(steps).toContain('3600');
		expect(steps).not.toContain('300');
		await expect(page).toHaveURL(/#.*range=7d/);
	});

	test('View query shows the exact PromQL, the resolved range and a copyable curl', async ({
		page
	}) => {
		await ready(page, '/dashboard');
		const panel = page.locator('[data-panel-id="commits-weekly"]');
		await panel.getByRole('button', { name: /menu for/i }).click();
		await page.getByRole('menuitem', { name: 'View query' }).click();
		const dialog = page.getByRole('dialog');
		await expect(dialog).toBeVisible();
		await expect(dialog.getByRole('heading', { level: 2 })).toBeFocused();
		await expect(dialog).toContainText('sum(increase(github_commits_total[7d]))');
		await expect(dialog).toContainText('curl -sG');
		await expect(dialog).toContainText('/api/v1/query_range');
		await expect(dialog).toContainText("--data-urlencode 'step=");
		await expect(dialog.getByRole('button', { name: 'Copy' }).first()).toBeVisible();
		await page.keyboard.press('Escape');
		await expect(dialog).toBeHidden();
		// focus returns to the opener (the menu button)
		await expect(panel.getByRole('button', { name: /menu for/i })).toBeFocused();
	});

	test('drag and resize persist the layout in the hash; Reset layout clears it', async ({
		page
	}) => {
		await ready(page, '/dashboard');
		const panel = page.locator('[data-panel-id="stat-experience"]');
		const grip = panel.getByRole('button', { name: /drag to move/i });
		const g = (await grip.boundingBox())!;
		await page.mouse.move(g.x + g.width / 2, g.y + g.height / 2);
		await page.mouse.down();
		await page.mouse.move(g.x + 200, g.y + 260, { steps: 12 });
		await page.mouse.move(g.x + 420, g.y + 300, { steps: 12 });
		await page.mouse.up();
		await expect(page).toHaveURL(/#.*layout=/);
		const afterDrag = new URL(page.url()).hash;
		await page.waitForTimeout(300); // let the drop transition settle before measuring

		const handle = panel.getByRole('button', { name: /resize/i });
		const h = (await handle.boundingBox())!;
		await page.mouse.move(h.x + h.width / 2, h.y + h.height / 2);
		await page.mouse.down();
		await page.mouse.move(h.x + 120, h.y + 80, { steps: 10 });
		await page.mouse.move(h.x + 240, h.y + 120, { steps: 10 });
		await page.mouse.up();
		await expect.poll(() => new URL(page.url()).hash).not.toBe(afterDrag);
		expect(new URL(page.url()).hash).toContain('layout=');

		// a shared link restores the moved layout
		const shared = page.url();
		await ready(page, shared);
		await expect(page.getByRole('button', { name: 'Reset layout' })).toBeEnabled();
		await page.getByRole('button', { name: 'Reset layout' }).click();
		await expect(page).not.toHaveURL(/layout=/);
		await expect(page.getByRole('button', { name: 'Reset layout' })).toBeDisabled();
	});

	test('keyboard: the panel menu moves a panel and updates the hash', async ({ page }) => {
		await ready(page, '/dashboard');
		const panel = page.locator('[data-panel-id="stat-packages"]');
		await panel.getByRole('button', { name: /menu for/i }).focus();
		await page.keyboard.press('Enter');
		const menu = page.getByRole('menu');
		await expect(menu).toBeVisible();
		await expect(page.getByRole('menuitem', { name: 'View query' })).toBeFocused();
		await page.keyboard.press('End');
		await expect(page.getByRole('menuitem', { name: 'Shorter' })).toBeFocused();
		await page.getByRole('menuitem', { name: 'Move down' }).focus();
		await page.keyboard.press('Enter');
		await expect(menu).toBeHidden();
		await expect(page).toHaveURL(/#.*layout=/);
	});

	test('#panel=<id> focuses that panel (alert runbook deep link)', async ({ page }) => {
		await ready(page, '/dashboard#panel=lfx-pending');
		await expect(page.locator('[data-panel-id="lfx-pending"]')).toBeFocused({ timeout: 15_000 });
	});

	test('a github-sourced panel says which collector is missing instead of drawing zero', async ({
		page
	}) => {
		await ready(page, '/dashboard');
		const panel = page.locator('[data-panel-id="merged-prs-by-org"]');
		const status = panel.getByRole('status');
		await expect(status.or(panel.locator('canvas')).first()).toBeVisible({ timeout: 15_000 });
		if ((await status.count()) > 0) {
			await expect(status).toContainText(/github/);
			await expect(status).toContainText(/collector|token/);
		}
	});

	test('manual metrics carry the source badge with an honest last-updated', async ({ page }) => {
		await ready(page, '/dashboard');
		const badge = page.locator('[data-panel-id="stat-active-users"] .chip.manual');
		await expect(badge).toContainText('source: manual');
		await expect(badge).toContainText(/last updated/);
	});
});

test.describe('390 px phone', () => {
	test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

	test('one column, full-width panels, range picker is a select', async ({ page }) => {
		await ready(page, '/dashboard');
		await expect(page.getByRole('combobox', { name: 'Time range' })).toBeVisible();
		const boxes = await page
			.locator('[data-panel-id]')
			.evaluateAll((els) =>
				els.map((e) => e.getBoundingClientRect()).map((r) => ({ x: r.x, w: r.width }))
			);
		expect(boxes.length).toBeGreaterThan(3);
		for (const b of boxes) {
			expect(b.x).toBeLessThan(20);
			expect(b.w).toBeGreaterThan(340);
		}
		await noHorizontalScroll(page);
		await page.getByRole('combobox', { name: 'Time range' }).selectOption('7d');
		await expect(page).toHaveURL(/#.*range=7d/);
	});
});
