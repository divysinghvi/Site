import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page } from '@playwright/test';

// Mobile (390×844, touch) pass over every route: no horizontal scroll, the
// controls are at least 44 px tall, and axe reports no serious or critical
// WCAG 2.x A/AA violation.

const ROUTES = [
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

test.use({
	locale: 'en-US',
	viewport: { width: 390, height: 844 },
	isMobile: true,
	hasTouch: true
});

async function ready(page: Page, path: string) {
	await page.goto(path);
	await page.locator('main[data-hydrated="true"]').waitFor();
	await page.waitForTimeout(300);
}

for (const path of ROUTES) {
	test(`${path} at 390px: no horizontal scroll, 44px controls, no serious axe violations`, async ({
		page
	}) => {
		await ready(page, path);
		const { scrollWidth, clientWidth } = await page.evaluate(() => ({
			scrollWidth: document.documentElement.scrollWidth,
			clientWidth: document.documentElement.clientWidth
		}));
		expect(scrollWidth, 'page must not scroll horizontally').toBeLessThanOrEqual(clientWidth);

		const small = await page
			.locator('main .btn, main button.chip, main select.input, header .tab, main .tab')
			.evaluateAll((els) =>
				els
					.filter((e) => {
						const r = e.getBoundingClientRect();
						return r.width > 0 && r.height > 0 && r.height < 44;
					})
					.map((e) => `${e.tagName.toLowerCase()}.${e.className.split(' ')[0]} "${e.textContent?.trim().slice(0, 20)}" ${Math.round(e.getBoundingClientRect().height)}px`)
			);
		expect(small, 'controls shorter than 44px').toEqual([]);

		const results = await new AxeBuilder({ page })
			.withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
			.exclude('.uplot') // canvas charts: uPlot's legend markup is out of scope
			.analyze();
		const serious = results.violations.filter(
			(v) => v.impact === 'serious' || v.impact === 'critical'
		);
		expect(
			serious.map((v) => `${v.id} (${v.impact}): ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`),
			'serious/critical axe violations'
		).toEqual([]);
	});
}
