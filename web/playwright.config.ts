import { defineConfig, devices } from '@playwright/test';

// Runs against a Vite dev server on :5173 by default (started here when not
// already running, proxying to the Go API on :8080); set E2E_BASE_URL to test
// a built binary instead (make web-e2e uses that).
const baseURL = process.env.E2E_BASE_URL ?? 'http://127.0.0.1:5173';

export default defineConfig({
	testDir: 'tests/e2e',
	timeout: 30_000,
	fullyParallel: true,
	retries: 0,
	reporter: process.env.CI ? 'github' : 'list',
	use: {
		baseURL,
		trace: 'retain-on-failure'
	},
	projects: [
		{
			name: 'chromium',
			use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } }
		}
	],
	webServer: process.env.E2E_BASE_URL
		? undefined
		: {
				command: 'npm run dev',
				url: 'http://127.0.0.1:5173',
				reuseExistingServer: true,
				timeout: 60_000
			}
});
