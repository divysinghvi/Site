import { expect, test, type Page } from '@playwright/test';

// Smoke: the hero trace loads, rows are keyboard-navigable (j/k/Enter/Esc)
// and the 390 px layout is the vertical timeline without horizontal scroll.

/** Waits until the SvelteKit app has hydrated (the root layout flips this attribute). */
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

test.describe('hero career trace', () => {
	test('renders at least 10 span rows from the API', async ({ page }) => {
		await ready(page, '/');
		await expect(page.getByRole('tree')).toBeVisible();
		const rows = page.getByRole('treeitem');
		expect(await rows.count()).toBeGreaterThanOrEqual(10);
		// the row labels are data, not literals: every row names a service
		await expect(rows.first()).toHaveAttribute('aria-level', '1');
		await noHorizontalScroll(page);
	});

	test('j/k move focus, Enter opens the drawer, Esc closes it', async ({ page }) => {
		await ready(page, '/');
		await expect(page.getByRole('tree')).toBeVisible();
		await page.locator('main').click({ position: { x: 5, y: 5 } });
		await page.keyboard.press('j');
		const first = page.getByRole('treeitem').nth(1);
		await expect(first).toBeFocused();
		await page.keyboard.press('j');
		await expect(page.getByRole('treeitem').nth(2)).toBeFocused();
		await page.keyboard.press('k');
		await expect(first).toBeFocused();
		const rowId = await first.getAttribute('data-span-key');

		await page.keyboard.press('Enter');
		const dialog = page.getByRole('dialog');
		await expect(dialog).toBeVisible();
		await expect(dialog.getByRole('heading', { level: 2 })).toBeFocused();
		expect(page.url()).toContain(`#span=${encodeURIComponent(rowId ?? '')}`.replace(/%2E/g, '.'));

		await page.keyboard.press('Escape');
		await expect(dialog).toBeHidden();
		await expect(first).toBeFocused();
		expect(page.url()).not.toContain('#span=');
	});

	test('clicking a row opens the drawer with tags and process', async ({ page }) => {
		await ready(page, '/');
		const row = page.getByRole('treeitem').nth(3);
		await row.click();
		const dialog = page.getByRole('dialog');
		await expect(dialog).toBeVisible();
		await expect(dialog.getByRole('heading', { name: /tags/i })).toBeVisible();
		await expect(dialog.getByRole('heading', { name: /process/i })).toBeVisible();
		await dialog.getByRole('button', { name: /close span details/i }).click();
		await expect(dialog).toBeHidden();
	});

	test('a #span= deep link opens that span', async ({ page }) => {
		await ready(page, '/');
		const key = await page.getByRole('treeitem').nth(2).getAttribute('data-span-key');
		await ready(page, `/#trace?span=${key}`);
		const dialog = page.getByRole('dialog');
		await expect(dialog).toBeVisible();
		await expect(dialog.getByRole('heading', { level: 2 })).toHaveText(key ?? '');
	});

	test('the Trace ID box opens /trace/[id] and rejects bad ids', async ({ page }) => {
		await ready(page, '/');
		const box = page.getByLabel('Trace ID');
		await box.fill('nope');
		await page.getByRole('button', { name: 'Open' }).click();
		await expect(page.getByRole('alert')).toContainText('32 hex');
		await box.fill('career');
		await page.getByRole('button', { name: 'Open' }).click();
		await expect(page).toHaveURL(/\/trace\/career$/);
		await expect(page.getByRole('tree')).toBeVisible();
	});

	test('/trace/[id] shows the API error text for an unknown id', async ({ page }) => {
		await ready(page, '/trace/00000000000000000000000000000000');
		const alert = page.getByRole('alert');
		await expect(alert).toBeVisible();
		await expect(alert).toContainText('trace not found');
	});
});

test.describe('390 px phone', () => {
	test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

	test('collapses to the vertical timeline, opens the sheet, no horizontal scroll', async ({
		page
	}) => {
		await ready(page, '/');
		const list = page.getByRole('list', { name: /vertical timeline/i });
		await expect(list).toBeVisible();
		await expect(page.getByRole('tree')).toBeHidden();
		expect(await list.getByRole('listitem').count()).toBeGreaterThanOrEqual(10);
		await noHorizontalScroll(page);

		await list.getByRole('button').nth(2).tap();
		const dialog = page.getByRole('dialog');
		await expect(dialog).toBeVisible();
		await noHorizontalScroll(page);
		await page.keyboard.press('Escape');
		await expect(dialog).toBeHidden();
	});

	test('/trace/career works on a phone', async ({ page }) => {
		await ready(page, '/trace/career');
		await expect(page.getByRole('list', { name: /vertical timeline/i })).toBeVisible();
		await noHorizontalScroll(page);
	});
});
