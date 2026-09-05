// Reference screenshots of the dashboard and Explore with the preinstalled
// Playwright Chromium. Usage:
//   SHOTS_BASE_URL=http://127.0.0.1:5173 SHOTS_DIR=/path node tests/shots/w2.mjs
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { chromium } from '@playwright/test';

const base = process.env.SHOTS_BASE_URL ?? 'http://127.0.0.1:5173';
const dir = resolve(process.env.SHOTS_DIR ?? '.playwright/shots');
const prefix = process.env.SHOTS_PREFIX ?? 'w2';
await mkdir(dir, { recursive: true });

const browser = await chromium.launch();
try {
	// locale pinned: the sandbox browser reports "en-US@posix", which Intl rejects
	const desktop = await browser.newContext({
		viewport: { width: 1440, height: 900 },
		locale: 'en-US'
	});
	const page = await desktop.newPage();
	await page.goto(base + '/dashboard', { waitUntil: 'networkidle' });
	await page.locator('main[data-hydrated="true"]').waitFor();
	await page.waitForTimeout(1500); // first query round + draw-in
	await page.screenshot({ path: resolve(dir, `${prefix}-dashboard-1440.png`), fullPage: true });

	await page
		.locator('[data-panel-id="commits-weekly"]')
		.getByRole('button', { name: /menu for/i })
		.click();
	await page.getByRole('menuitem', { name: 'View query' }).click();
	await page.getByRole('dialog').waitFor();
	await page.waitForTimeout(300);
	await page.screenshot({ path: resolve(dir, `${prefix}-viewquery-1440.png`), fullPage: false });
	await page.keyboard.press('Escape');

	await page.goto(base + '/explore?ds=prom&expr=divy_uptime_seconds&from=now-24h&to=now', {
		waitUntil: 'networkidle'
	});
	await page.locator('main[data-hydrated="true"]').waitFor();
	await page.waitForTimeout(1500);
	await page.screenshot({ path: resolve(dir, `${prefix}-explore-1440.png`), fullPage: true });
	await desktop.close();

	const phone = await browser.newContext({
		viewport: { width: 390, height: 844 },
		locale: 'en-US',
		deviceScaleFactor: 2,
		isMobile: true,
		hasTouch: true
	});
	const p = await phone.newPage();
	await p.goto(base + '/dashboard', { waitUntil: 'networkidle' });
	await p.locator('main[data-hydrated="true"]').waitFor();
	await p.waitForTimeout(1500);
	await p.screenshot({ path: resolve(dir, `${prefix}-dashboard-390.png`), fullPage: true });
	await phone.close();
	console.log(`screenshots written to ${dir}`);
} finally {
	await browser.close();
}
