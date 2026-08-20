import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
	testDir: "./e2e",
	fullyParallel: false,
	forbidOnly: Boolean(process.env.CI),
	retries: process.env.CI ? 2 : 0,
	workers: 1,
	reporter: process.env.CI ? "github" : "list",
	use: {
		baseURL: "http://127.0.0.1:19091",
		trace: "retain-on-failure",
	},
	projects: [
		{
			name: "chromium",
			use: { ...devices["Desktop Chrome"] },
		},
	],
	webServer: {
		command: "env GOCACHE=/tmp/mayfly-playwright-go-cache go run . serve --addr 127.0.0.1 --port 19091 --input-root . --data-root /tmp/mayflycirclefit-playwright-data",
		cwd: "..",
		url: "http://127.0.0.1:19091/",
		reuseExistingServer: !process.env.CI,
		timeout: 120_000,
	},
});
