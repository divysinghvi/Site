// Reference screenshots of the postmortem, contact and uptime pages. Usage:
//   SHOTS_BASE_URL=http://127.0.0.1:5173 SHOTS_DIR=/path node tests/shots/w3.mjs
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { chromium } from '@playwright/test';

const base = process.env.SHOTS_BASE_URL ?? 'http://127.0.0.1:5173';
const dir = resolve(process.env.SHOTS_DIR ?? '.playwright/shots');
const prefix = process.env.SHOTS_PREFIX ?? 'w3';
await mkdir(dir, { recursive: true });

const hydrated = 'main[data-hydrated="true"]';
const browser = await chromium.launch();
try {
	const desktop = await browser.newContext({
		viewport: { width: 1440, height: 900 },
		locale: 'en-US'
	});
	const page = await desktop.newPage();
	await page.goto(base + '/postmortems/INC-001', { waitUntil: 'networkidle' });
	await page.locator(hydrated).waitFor();
	await page.waitForTimeout(400);
	await page.screenshot({ path: resolve(dir, `${prefix}-postmortem-1440.png`), fullPage: true });

	await page.goto(base + '/postmortems', { waitUntil: 'networkidle' });
	await page.locator(hydrated).waitFor();
	await page.screenshot({
		path: resolve(dir, `${prefix}-postmortems-list-1440.png`),
		fullPage: true
	});

	await page.goto(base + '/contact', { waitUntil: 'networkidle' });
	await page.locator(hydrated).waitFor();
	await page.locator('[data-healthz-live="true"]').waitFor();
	await page.waitForTimeout(300);
	await page.screenshot({ path: resolve(dir, `${prefix}-contact-1440.png`), fullPage: true });

	await page.goto(base + '/uptime', { waitUntil: 'networkidle' });
	await page.locator(hydrated).waitFor();
	await page.getByText(/live · refreshed/).waitFor();
	await page.waitForTimeout(300);
	await page.screenshot({ path: resolve(dir, `${prefix}-uptime-1440.png`), fullPage: true });
	await desktop.close();

	const phone = await browser.newContext({
		viewport: { width: 390, height: 844 },
		locale: 'en-US',
		deviceScaleFactor: 2,
		isMobile: true,
		hasTouch: true
	});
	const p = await phone.newPage();
	await p.goto(base + '/postmortems/INC-001', { waitUntil: 'networkidle' });
	await p.locator(hydrated).waitFor();
	await p.waitForTimeout(300);
	await p.screenshot({ path: resolve(dir, `${prefix}-postmortem-390.png`), fullPage: true });

	await p.goto(base + '/uptime', { waitUntil: 'networkidle' });
	await p.locator(hydrated).waitFor();
	await p.getByText(/live · refreshed/).waitFor();
	await p.waitForTimeout(300);
	await p.screenshot({ path: resolve(dir, `${prefix}-uptime-390.png`), fullPage: true });
	await phone.close();
	console.log(`screenshots written to ${dir}`);
} finally {
	await browser.close();
}
