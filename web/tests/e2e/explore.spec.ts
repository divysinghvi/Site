import { expect, test, type Page } from '@playwright/test';

// Explore: URL-driven PromQL runs, graph/table results, the curl line,
// autocomplete from /api/v1/label/__name__/values, the panel → Explore link,
// API error text, and the Loki tab (the LogQL bar and log list of /logs).

async function ready(page: Page, path: string) {
	await page.goto(path);
	await page.locator('main[data-hydrated="true"]').waitFor();
}

// see dashboard.spec.ts: the sandbox browser's locale tag is invalid for Intl
test.use({ locale: 'en-US' });

test.describe('explore', () => {
	test('runs the query from the URL and shows a graph and the curl', async ({ page }) => {
		await ready(page, '/explore?ds=prom&expr=divy_uptime_seconds&from=now-24h&to=now');
		const box = page.getByRole('combobox', { name: 'PromQL query' });
		await expect(box).toHaveValue('divy_uptime_seconds');
		await expect(page.getByRole('img', { name: /Result of divy_uptime_seconds/ })).toBeVisible({
			timeout: 15_000
		});
		await expect(page.locator('.curl')).toContainText('curl -sG');
		await expect(page.locator('.curl')).toContainText('/api/v1/query_range');
		await expect(page.locator('.curl')).toContainText(
			"--data-urlencode 'query=divy_uptime_seconds'"
		);
	});

	test('an instant query renders a table with labels and values', async ({ page }) => {
		await ready(page, '/explore?ds=prom&expr=divy_experience_years&from=now-24h&to=now&instant=1');
		const table = page.getByRole('table');
		await expect(table).toBeVisible({ timeout: 15_000 });
		await expect(table).toContainText('divy_experience_years');
		await expect(table.locator('td.num').first()).toHaveText(/^\d+(\.\d+)?$/);
		await expect(page.locator('.curl')).toContainText('/api/v1/query');
		await expect(page.locator('.curl')).not.toContainText('query_range');
	});

	test('autocomplete offers metric names and functions', async ({ page }) => {
		await ready(page, '/explore');
		const box = page.getByRole('combobox', { name: 'PromQL query' });
		await box.click();
		await box.pressSequentially('divy_up');
		const list = page.getByRole('listbox', { name: 'Suggestions' });
		await expect(list).toBeVisible();
		await expect(list.getByRole('option', { name: /divy_uptime_seconds/ })).toBeVisible();
		await page.keyboard.press('Enter');
		await expect(box).toHaveValue('divy_uptime_seconds');
		await box.fill('');
		await box.pressSequentially('rat');
		await expect(list.getByRole('option', { name: /^function rate/ })).toBeVisible();
		await page.keyboard.press('Tab');
		await expect(box).toHaveValue('rate(');
	});

	test('the API error text is shown for a bad query', async ({ page }) => {
		await ready(page, '/explore?ds=prom&expr=rate(&from=now-24h&to=now');
		await expect(page.getByRole('status')).toContainText('parse error');
	});

	test('an empty result names the missing collector', async ({ page }) => {
		await ready(page, '/explore?ds=prom&expr=sum(github_merged_prs_total)&from=now-7d&to=now');
		const status = page.getByRole('status');
		await expect(status.or(page.getByRole('img', { name: /Result of/ })).first()).toBeVisible({
			timeout: 15_000
		});
		if ((await status.count()) > 0) await expect(status).toContainText(/github/);
	});

	test('the Explore affordance on a panel lands here with its PromQL', async ({ page }) => {
		await ready(page, '/dashboard');
		const panel = page.locator('[data-panel-id="pypi-downloads"]');
		await panel.getByRole('button', { name: /menu for/i }).click();
		await page.getByRole('menuitem', { name: /Explore/ }).click();
		await expect(page).toHaveURL(/\/explore\?ds=prom&expr=/);
		await page.locator('main[data-hydrated="true"]').waitFor();
		await expect(page.getByRole('combobox', { name: 'PromQL query' })).toHaveValue(
			'rate(pypi_downloads_total{package="codemind-ci"}[2d]) * 86400'
		);
	});

	test('the Loki tab runs LogQL and lists log lines', async ({ page }) => {
		await ready(page, '/explore');
		await page.getByRole('tab', { name: 'Loki' }).click();
		await expect(page).toHaveURL(/ds=loki/);
		const box = page.getByRole('combobox', { name: 'LogQL query' });
		await expect(box).toBeVisible();
		await expect(page.locator('[data-log-row]').first()).toBeVisible({ timeout: 15_000 });
		await box.fill('{service="gradr"} |= "promoted"');
		await page.keyboard.press('Enter');
		await expect(page.locator('[data-log-row]')).toHaveCount(1, { timeout: 15_000 });
		await expect(page.locator('[data-log-row]').first()).toContainText('promoted');
		await expect(page.locator('[data-curl]')).toContainText('/loki/api/v1/query_range');
	});

	test('/ focuses the query bar', async ({ page }) => {
		await ready(page, '/explore');
		await page.locator('main').click({ position: { x: 5, y: 5 } });
		await page.keyboard.press('/');
		await expect(page.getByRole('combobox', { name: 'PromQL query' })).toBeFocused();
	});
});
