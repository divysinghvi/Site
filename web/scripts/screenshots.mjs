// Saves reference screenshots of the hero trace (desktop, phone, drawer) with
// the Playwright Chromium already installed. Usage:
//   SHOTS_BASE_URL=http://127.0.0.1:5173 SHOTS_DIR=/path node scripts/screenshots.mjs
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { chromium } from '@playwright/test';

const base = process.env.SHOTS_BASE_URL ?? 'http://127.0.0.1:5173';
const dir = resolve(process.env.SHOTS_DIR ?? '.playwright/shots');
const prefix = process.env.SHOTS_PREFIX ?? 'w1';
await mkdir(dir, { recursive: true });

const browser = await chromium.launch();
try {
	const desktop = await browser.newContext({ viewport: { width: 1440, height: 900 } });
	const page = await desktop.newPage();
	await page.goto(base + '/', { waitUntil: 'networkidle' });
	await page.getByRole('tree').waitFor();
	await page.waitForTimeout(900); // let the bar-grow animation finish
	await page.screenshot({ path: resolve(dir, `${prefix}-home-1440.png`), fullPage: true });

	await page.getByRole('treeitem').nth(5).click();
	await page.getByRole('dialog').waitFor();
	await page.waitForTimeout(300);
	await page.screenshot({ path: resolve(dir, `${prefix}-drawer-1440.png`), fullPage: false });
	await desktop.close();

	const phone = await browser.newContext({
		viewport: { width: 390, height: 844 },
		deviceScaleFactor: 2,
		isMobile: true,
		hasTouch: true
	});
	const p = await phone.newPage();
	await p.goto(base + '/', { waitUntil: 'networkidle' });
	await p.getByRole('list', { name: /vertical timeline/i }).waitFor();
	await p.screenshot({ path: resolve(dir, `${prefix}-home-390.png`), fullPage: true });

	await p.goto(base + '/trace/career', { waitUntil: 'networkidle' });
	await p.getByRole('list', { name: /vertical timeline/i }).waitFor();
	await p
		.getByRole('list', { name: /vertical timeline/i })
		.getByRole('button')
		.nth(3)
		.tap();
	await p.getByRole('dialog').waitFor();
	await p.waitForTimeout(300);
	await p.screenshot({ path: resolve(dir, `${prefix}-trace-390.png`), fullPage: false });
	await phone.close();
	console.log(`screenshots written to ${dir}`);
} finally {
	await browser.close();
}
