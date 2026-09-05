import { expect, test, type Page } from '@playwright/test';

// The contact runbook: escalation steps from the profile, TODO placeholders
// rendered literally, the copyable curl with the live /healthz JSON, the
// open-to chips and a ticking clock in the profile's timezone.

interface Profile {
	tz: string;
	open_to: string[];
	escalation: { step: number; channel: string; target: string }[];
	links: Record<string, string>;
}

async function ready(page: Page, path: string) {
	await page.goto(path);
	await page.locator('main[data-hydrated="true"]').waitFor();
}

test.describe('contact runbook', () => {
	test('shows the escalation table, channels, open-to chips and the healthz JSON', async ({
		page,
		request
	}) => {
		const profile = (await (await request.get('/api/content/profile')).json()) as Profile;
		const healthz = (await request.get('/healthz')).ok();
		expect(healthz).toBe(true);
		await ready(page, '/contact');
		await expect(page).toHaveTitle('Runbook: contact');

		// escalation path: one row per step, in order
		const rows = page.getByRole('table', { name: /escalation/i }).locator('tbody tr');
		expect(await rows.count()).toBe(profile.escalation.length);
		for (const [i, e] of profile.escalation.entries()) {
			await expect(rows.nth(i)).toContainText(String(e.step));
			await expect(rows.nth(i)).toContainText(e.channel);
			if (e.target.startsWith('TODO(divy)')) await expect(rows.nth(i)).toContainText('TODO(divy)');
			else await expect(rows.nth(i).getByRole('link')).toHaveAttribute('href', e.target);
		}

		// channels: a TODO value is a literal placeholder, a real one a link
		for (const [key, value] of Object.entries(profile.links)) {
			const ch = page.locator(`[data-channel="${key}"]`);
			await expect(ch).toBeVisible();
			if (value.startsWith('TODO(divy)')) await expect(ch).toContainText('TODO(divy)');
			else await expect(ch.getByRole('link')).toHaveAttribute('href', value);
		}

		// open-to chips verbatim from the profile
		const chips = page.locator('[data-open-to] .chip');
		await expect(chips).toHaveText(profile.open_to);

		// the curl block and its live response
		await expect(page.locator('[data-curl]')).toHaveText(/^curl https?:\/\/\S+\/healthz$/);
		const out = page.locator('[data-healthz-live="true"]');
		await expect(out).toBeVisible();
		const json = JSON.parse((await out.textContent()) ?? '{}') as Record<string, unknown>;
		expect(json.status).toBe('ok');
		expect(json.open_to).toEqual(profile.open_to);
		expect(json.tz).toBe(profile.tz);

		// the timezone and a running clock
		await expect(page.locator('[data-clock] time')).toHaveText(/^\d{2}:\d{2}:\d{2}$/);
		await expect(page.getByText(profile.tz, { exact: true })).toBeVisible();
		const t1 = await page.locator('[data-clock] time').textContent();
		await page.waitForTimeout(1500);
		const t2 = await page.locator('[data-clock] time').textContent();
		expect(t1).not.toBe(t2);
	});

	test('the copy button copies the curl command', async ({ page, context }) => {
		await context.grantPermissions(['clipboard-read', 'clipboard-write']);
		await ready(page, '/contact');
		await page.getByRole('button', { name: /copy the curl/i }).click();
		const text = await page.evaluate(() => navigator.clipboard.readText());
		expect(text).toMatch(/^curl https?:\/\/\S+\/healthz$/);
	});
});

test.describe('contact on a 390 px phone', () => {
	test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

	test('lays out without horizontal scroll', async ({ page }) => {
		await ready(page, '/contact');
		await expect(page.locator('[data-healthz-live="true"]')).toBeVisible();
		const { scrollWidth, clientWidth } = await page.evaluate(() => ({
			scrollWidth: document.documentElement.scrollWidth,
			clientWidth: document.documentElement.clientWidth
		}));
		expect(scrollWidth).toBeLessThanOrEqual(clientWidth);
	});
});
