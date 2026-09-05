// Reference screenshots of the logs, alerts, toast, console, Konami theme
// and the phone layout of every route. Usage:
//   SHOTS_BASE_URL=http://127.0.0.1:5173 SHOTS_DIR=/path node tests/shots/w5.mjs
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { chromium } from '@playwright/test';

const base = process.env.SHOTS_BASE_URL ?? 'http://127.0.0.1:5173';
const dir = resolve(process.env.SHOTS_DIR ?? '.playwright/shots');
const prefix = process.env.SHOTS_PREFIX ?? 'w5';
await mkdir(dir, { recursive: true });

const hydrated = 'main[data-hydrated="true"]';
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
const browser = await chromium.launch();
try {
	const desktop = await browser.newContext({
		viewport: { width: 1440, height: 900 },
		locale: 'en-US'
	});
	const page = await desktop.newPage();

	await page.goto(base + '/logs', { waitUntil: 'networkidle' });
	await page.locator(hydrated).waitFor();
	await page.locator('[data-log-row]').first().waitFor();
	await page.locator('[data-log-row]').first().locator('button.head').click();
	await page.waitForTimeout(400);
	await page.screenshot({ path: resolve(dir, `${prefix}-logs-1440.png`), fullPage: false });

	await page.goto(base + '/alerts?alerts_fast=1', { waitUntil: 'networkidle' });
	await page.locator(hydrated).waitFor();
	await page.locator('[data-toast="alert:DivyAvailableForHire"]').waitFor({ timeout: 20_000 });
	await page.waitForTimeout(500);
	await page.screenshot({ path: resolve(dir, `${prefix}-alerts-1440.png`), fullPage: false });

	await page.goto(base + '/dashboard?alerts_fast=1', { waitUntil: 'networkidle' });
	await page.locator(hydrated).waitFor();
	await page.locator('[data-toast="alert:DivyAvailableForHire"]').waitFor({ timeout: 20_000 });
	await page.waitForTimeout(500);
	await page.screenshot({ path: resolve(dir, `${prefix}-toast-1440.png`), fullPage: false });

	await page.locator('main').click({ position: { x: 5, y: 5 } });
	await page.keyboard.type('promql');
	const dialog = page.getByRole('dialog', { name: 'Query console' });
	await dialog.waitFor();
	const input = dialog.getByRole('textbox');
	for (const cmd of ['kubectl get pods', 'divy_uptime_seconds', 'kubectl get nodes']) {
		await input.fill(cmd);
		await page.keyboard.press('Enter');
		await page.waitForTimeout(600);
	}
	await page.screenshot({ path: resolve(dir, `${prefix}-console-1440.png`), fullPage: false });
	await page.keyboard.press('Escape');

	for (const k of KONAMI) await page.keyboard.press(k);
	await page.waitForTimeout(300);
	await page.screenshot({ path: resolve(dir, `${prefix}-konami-1440.png`), fullPage: false });
	await desktop.close();

	const phone = await browser.newContext({
		viewport: { width: 390, height: 844 },
		locale: 'en-US',
		deviceScaleFactor: 2,
		isMobile: true,
		hasTouch: true
	});
	const p = await phone.newPage();
	const routes = [
		'/',
		'/dashboard',
		'/logs',
		'/uptime',
		'/postmortems',
		'/postmortems/INC-001',
		'/alerts',
		'/contact',
		'/explore'
	];
	for (const r of routes) {
		await p.goto(base + r, { waitUntil: 'networkidle' });
		await p.locator(hydrated).waitFor();
		await p.waitForTimeout(500);
		const name = r === '/' ? 'home' : r.slice(1).replace(/\//g, '-').toLowerCase();
		await p.screenshot({ path: resolve(dir, `${prefix}-mobile-${name}-390.png`), fullPage: false });
		if (r === '/logs')
			await p.screenshot({ path: resolve(dir, `${prefix}-logs-390.png`), fullPage: false });
	}
	await phone.close();
} finally {
	await browser.close();
}
console.log('screenshots written to', dir);
