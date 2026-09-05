import { expect, test, type Page } from '@playwright/test';

// The easter eggs: typing `promql` outside an input opens the floating
// console (kubectl get pods → the profile's pods as a table, other kubectl
// → kubectl-style errors, anything else → /api/v1/query as a table or the
// API error in red, ↑/↓ history, Esc closes); the Konami code switches to
// the 2017 theme for the session; every route carries OG/Twitter meta.

async function ready(page: Page, path: string) {
	await page.goto(path);
	await page.locator('main[data-hydrated="true"]').waitFor();
	// focus the document, not an input
	await page.locator('main').click({ position: { x: 5, y: 5 } });
}

const KONAMI = [
	'ArrowUp',
	'ArrowUp',
	'ArrowDown',
	'ArrowDown',
	'ArrowLeft',
	'ArrowRight',
	'ArrowLeft',
	'ArrowRight',
	'b',
	'a'
];

test.use({ locale: 'en-US' });

test.describe('promql console', () => {
	test('typing promql opens it and kubectl get pods lists the pods', async ({ page }) => {
		await ready(page, '/dashboard');
		await expect(page.getByRole('dialog', { name: 'Query console' })).toHaveCount(0);
		await page.keyboard.type('promql');
		const dialog = page.getByRole('dialog', { name: 'Query console' });
		await expect(dialog).toBeVisible();
		const input = dialog.getByRole('textbox', { name: /Console command/ });
		await expect(input).toBeFocused();
		await input.fill('kubectl get pods');
		await page.keyboard.press('Enter');
		const table = dialog.getByRole('table').last();
		await expect(table).toBeVisible({ timeout: 10_000 });
		await expect(table.locator('thead')).toHaveText(
			/NAME\s*READY\s*STATUS\s*RESTARTS\s*AGE\s*NOTE/
		);
		const rows = table.locator('tbody tr');
		expect(await rows.count()).toBeGreaterThanOrEqual(3);
		await expect(table).toContainText('gradr-observability');
		await expect(table).toContainText('savely');
		await expect(table).toContainText('1/1');
		await expect(table).toContainText('Running');
		await expect(table).toContainText('lfx-velero');
		await expect(table).toContainText('Pending');
		// AGE is kubectl-shaped
		await expect(rows.first().locator('td').nth(4)).toHaveText(/^\d+[smhdy]$/);
	});

	test('other kubectl commands answer like kubectl; help and clear are local', async ({ page }) => {
		await ready(page, '/uptime');
		await page.keyboard.type('promql');
		const dialog = page.getByRole('dialog', { name: 'Query console' });
		const input = dialog.getByRole('textbox', { name: /Console command/ });
		await input.fill('kubectl get nodes');
		await page.keyboard.press('Enter');
		await expect(dialog.getByRole('alert').last()).toHaveText(
			`error: the server doesn't have a resource type "nodes"`
		);
		await input.fill('kubectl delete pods savely');
		await page.keyboard.press('Enter');
		await expect(dialog.getByRole('alert').last()).toContainText(
			'unknown command "delete" for "kubectl"'
		);
		await input.fill('help');
		await page.keyboard.press('Enter');
		await expect(dialog).toContainText('kubectl get pods');
		await input.fill('clear');
		await page.keyboard.press('Enter');
		await expect(dialog.locator('.log .line')).toHaveCount(0);
	});

	test('anything else is a PromQL instant query: a table, or the API error in red', async ({
		page
	}) => {
		await ready(page, '/contact');
		await page.keyboard.type('promql');
		const dialog = page.getByRole('dialog', { name: 'Query console' });
		const input = dialog.getByRole('textbox', { name: /Console command/ });
		await input.fill('divy_uptime_seconds');
		await page.keyboard.press('Enter');
		const table = dialog.getByRole('table').last();
		await expect(table).toBeVisible({ timeout: 10_000 });
		await expect(table.locator('thead')).toContainText('VALUE');
		await expect(table).toContainText('divy_uptime_seconds');
		await expect(table.locator('tbody td').last()).toHaveText(/^\d+(\.\d+)?$/);
		await input.fill('rate(');
		await page.keyboard.press('Enter');
		const err = dialog.getByRole('alert').last();
		await expect(err).toContainText('parse error');
		await expect(err).toHaveCSS('color', 'rgb(242, 73, 92)');
		// history: ↑ brings the last command back, ↑ again the one before, Esc closes
		await input.press('ArrowUp');
		await expect(input).toHaveValue('rate(');
		await input.press('ArrowUp');
		await expect(input).toHaveValue('divy_uptime_seconds');
		await input.press('ArrowDown');
		await expect(input).toHaveValue('rate(');
		await input.press('Escape');
		await expect(dialog).toHaveCount(0);
	});

	test('typing inside an input does not open the console', async ({ page }) => {
		await ready(page, '/explore');
		const box = page.getByRole('combobox', { name: 'PromQL query' });
		await box.click();
		await box.pressSequentially('promql');
		await expect(page.getByRole('dialog', { name: 'Query console' })).toHaveCount(0);
	});
});

test.describe('konami theme', () => {
	test('the code switches to the 2017 theme for the session and back', async ({ page }) => {
		await ready(page, '/');
		const html = page.locator('html');
		await expect(html).toHaveAttribute('data-theme', 'dark');
		for (const k of KONAMI) await page.keyboard.press(k);
		await expect(html).toHaveAttribute('data-theme', 'grafana2017');
		await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute('content', '#0f1926');
		expect(await page.evaluate(() => sessionStorage.getItem('divy.theme.session'))).toBe(
			'grafana2017'
		);
		// applied before first paint on the next load
		await page.goto('/logs');
		expect(await page.evaluate(() => document.documentElement.dataset.theme)).toBe('grafana2017');
		await page.locator('main[data-hydrated="true"]').waitFor();
		await page.locator('main').click({ position: { x: 5, y: 5 } });
		for (const k of KONAMI) await page.keyboard.press(k);
		await expect(html).toHaveAttribute('data-theme', 'dark');
		expect(await page.evaluate(() => sessionStorage.getItem('divy.theme.session'))).toBeNull();
	});
});

test.describe('meta', () => {
	for (const path of [
		'/',
		'/dashboard',
		'/logs',
		'/uptime',
		'/postmortems',
		'/alerts',
		'/contact',
		'/explore'
	]) {
		test(`${path} has favicon, theme-color and OG/Twitter tags`, async ({ page }) => {
			await page.goto(path);
			// SvelteKit's client absolutizes the icon href after hydration (relative asset paths)
			await expect(page.locator('link[rel="icon"]')).toHaveAttribute('href', /(^|\/)favicon\.svg$/);
			await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute('content', /^#/);
			await expect(page.locator('meta[property="og:title"]')).toHaveAttribute('content', /.+/);
			await expect(page.locator('meta[property="og:description"]')).toHaveAttribute(
				'content',
				/.+/
			);
			await expect(page.locator('meta[name="twitter:card"]')).toHaveAttribute('content', /summary/);
			const image = page.locator('meta[property="og:image"]');
			if ((await image.count()) > 0)
				await expect(image.first()).toHaveAttribute(
					'content',
					/\/og\/(default|postmortems\/INC-\d+)\.png$/
				);
		});
	}
});
