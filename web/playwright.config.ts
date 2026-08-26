import { defineConfig, devices } from "@playwright/test";

// Specs are routed to projects by filename suffix. The two behavior specs
// predate the suffix convention and are matched by name.
const BEHAVIOR = [/live-sync\.spec\.ts/, /job-infinite-scroll\.spec\.ts/];
const A11Y = /.*\.a11y\.spec\.ts/;
const KEYBOARD = /.*\.keyboard\.spec\.ts/;
const LAYOUT = /.*\.layout\.spec\.ts/;
const MOBILE = /.*\.mobile\.spec\.ts/;

export default defineConfig({
	testDir: "./e2e",
	fullyParallel: false,
	forbidOnly: Boolean(process.env.CI),
	retries: process.env.CI ? 2 : 0,
	// One worker: every project shares a single server with one job store and a
	// bounded admission queue, so parallel workers would race the seeded
	// fixtures against each other.
	workers: 1,
	reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : "list",
	use: {
		baseURL: "http://127.0.0.1:19091",
		trace: "retain-on-failure",
		// CI must take the reduced-motion branch: three animations otherwise run
		// indefinitely (the spinner, the running-badge pulse, the download busy
		// ring), which makes stability waits and traces harder to read.
		reducedMotion: "reduce",
	},
	// All four projects share the one webServer below, which is started once per
	// `playwright test` invocation. Do NOT split CI into per-project steps: each
	// invocation re-pays the `go run` compile, which dominates the run.
	projects: [
		// The authoritative engine. Runs every spec.
		{
			name: "chromium",
			use: { ...devices["Desktop Chrome"] },
			// The touch specs need a context with hasTouch, which only the
			// device profiles below provide.
			testIgnore: MOBILE,
		},
		// Safari's engine. Behavior, a11y and keyboard only: the layout sweeps
		// drive their own viewports and learn nothing from a second engine,
		// while pixel-level WebKit differences must never gate a release.
		{
			name: "webkit",
			use: { ...devices["Desktop Safari"] },
			testMatch: [...BEHAVIOR, A11Y, KEYBOARD],
		},
		{
			name: "mobile-safari",
			use: { ...devices["iPhone 14"] },
			testMatch: [A11Y, MOBILE],
		},
		{
			name: "ipad",
			use: { ...devices["iPad (gen 7)"] },
			testMatch: [MOBILE],
		},
		{
			name: "layout",
			use: { ...devices["Desktop Chrome"] },
			testMatch: LAYOUT,
			testIgnore: MOBILE,
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
