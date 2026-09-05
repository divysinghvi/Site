import { expect, test, type Page } from '@playwright/test';

// The client-side alert engine: rules from /api/v1/rules listed on /alerts,
// inactive → pending → firing per `for`, a toast on every route when a rule
// fires, Silence hides it for the session (sessionStorage survives a reload),
// and a rule whose metric has no series says so. `?alerts_fast=1` is the test
// hook: 1 s polling and every `for` divided by 10 (DivyAvailableForHire's
// 30 s becomes 3 s).

async function ready(page: Page, path: string) {
	await page.goto(path);
	await page.locator('main[data-hydrated="true"]').waitFor();
}

test.use({ locale: 'en-US' });

test.describe('alerts', () => {
	test('lists every rule with its expression, for and severity', async ({ page }) => {
		await ready(page, '/alerts');
		const rules = page.locator('[data-alert]');
		await expect(rules).toHaveCount(3);
		const hire = page.locator('[data-alert="DivyAvailableForHire"]');
		await expect(hire).toContainText('divy_open_to_work == 1');
		await expect(hire).toContainText('severity=page');
		await expect(hire).toContainText('for 30s');
		await expect(hire.getByRole('link', { name: 'Runbook' })).toHaveAttribute('href', '/contact');
		await expect(hire.getByRole('link', { name: 'Explore' })).toHaveAttribute(
			'href',
			/\/explore\?ds=prom&expr=divy_open_to_work/
		);
		await expect(page.locator('[data-alert="HighContributionRate"]')).toContainText(
			'github_commits_total'
		);
		await expect(page.locator('[data-alert="LFXApplicationPending"]')).toContainText(
			'lfx_applications'
		);
	});

	test('DivyAvailableForHire goes pending, then fires and toasts; Silence hides it for the session', async ({
		page
	}) => {
		await ready(page, '/alerts?alerts_fast=1');
		const hire = page.locator('[data-alert="DivyAvailableForHire"]');
		await expect(hire).toHaveAttribute('data-state', 'pending', { timeout: 10_000 });
		await expect(hire).toContainText(/pending since .* fires in \d+s/);
		await expect(hire).toHaveAttribute('data-state', 'firing', { timeout: 15_000 });
		await expect(hire).toContainText(/firing since \d\d:\d\d:\d\dZ/);
		await expect(hire).toContainText('Open to backend/infra internships');
		await expect(page.locator('[data-count="firing"]')).toContainText(/[1-9]/);

		const toast = page.locator('[data-toast="alert:DivyAvailableForHire"]');
		await expect(toast).toBeVisible();
		await expect(toast).toContainText('DivyAvailableForHire');
		await expect(toast).toContainText('severity=page');
		await expect(toast).toContainText('Runbook: /contact');
		await expect(toast.getByRole('link', { name: /Runbook/ })).toHaveAttribute('href', '/contact');
		await expect(page.locator('[data-toasts]')).toHaveAttribute('aria-live', 'polite');

		await toast.getByRole('button', { name: 'Silence DivyAvailableForHire' }).click();
		await expect(toast).toBeHidden();
		await expect(hire).toContainText('silenced');
		await expect(page.locator('[data-silences]')).toContainText('DivyAvailableForHire');
		expect(await page.evaluate(() => sessionStorage.getItem('divy.alerts.silences'))).toContain(
			'DivyAvailableForHire'
		);

		// the silence survives a reload: the rule fires again but no toast shows
		await page.reload();
		await page.locator('main[data-hydrated="true"]').waitFor();
		await expect(hire).toHaveAttribute('data-state', 'firing', { timeout: 15_000 });
		await page.waitForTimeout(500);
		await expect(toast).toHaveCount(0);
		await expect(hire).toContainText('silenced');

		// Unsilence brings the toast back
		await hire.getByRole('button', { name: /Expire the silence/ }).click();
		await expect(toast).toBeVisible();
		await toast.getByRole('button', { name: /Dismiss/ }).click();
		await expect(toast).toBeHidden();
	});

	test('a firing rule toasts on any route', async ({ page }) => {
		await ready(page, '/dashboard?alerts_fast=1');
		const toast = page.locator('[data-toast="alert:DivyAvailableForHire"]');
		await expect(toast).toBeVisible({ timeout: 15_000 });
		await expect(toast).toContainText('firing');
		await toast.getByRole('link', { name: /Open the alerts page/ }).click();
		await expect(page).toHaveURL(/\/alerts/);
		// client-side navigation keeps the engine (and the toast) alive
		await expect(toast).toBeVisible();
		await expect(page.locator('[data-alert="DivyAvailableForHire"]')).toHaveAttribute(
			'data-state',
			'firing'
		);
	});

	test('a rule whose metric has no series says "no data" and names the source', async ({
		page
	}) => {
		await ready(page, '/alerts?alerts_fast=1');
		const rule = page.locator('[data-alert="HighContributionRate"]');
		await expect(page.locator('.sub')).toContainText('last round', { timeout: 15_000 });
		const state = await rule.getAttribute('data-state');
		if (state === 'nodata') {
			await expect(rule.locator('[data-badge]')).toHaveText('no data');
			await expect(rule).toContainText('github_commits_total: no series');
			await expect(rule).toContainText(/github/);
			await expect(page.locator('[data-toast="alert:HighContributionRate"]')).toHaveCount(0);
		} else {
			// a GitHub token is configured in this environment: the rule evaluates for real
			expect(['pending', 'firing', 'inactive', 'error']).toContain(state);
		}
	});
});
