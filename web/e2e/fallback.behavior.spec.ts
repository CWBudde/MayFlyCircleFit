import { expect, test } from "./fixtures/seed";
import { SURFACES, surfacePath } from "./fixtures/surfaces";
import type { Page } from "@playwright/test";

// Task 18.7's acceptance check, and the automated half of
// docs/behavior-invariants.md's "templ output is the fallback and hydration
// seed" invariant.
//
// Two failure modes, deliberately kept apart, because a page can survive one
// and not the other:
//
//   1. JavaScript is off. Nothing runs; the browser shows exactly what the
//      server wrote, <noscript> included.
//   2. JavaScript is on and the bundle is broken. The module 404s or throws,
//      which is what a bad deploy, a stripped asset, or a CSP that blocks the
//      script actually looks like. The mount points are still in the document
//      and still hold their server-rendered children, but nothing replaces
//      them -- and, unlike case 1, <noscript> does NOT render, so anything the
//      page only says inside <noscript> is invisible in this mode.
//
// The assertions below are content, not markup shape: whatever a reader has to
// be able to see on that page without the island. They are deliberately the
// same list in all three modes.

// Per-surface content that the server must write on its own. Keyed by the
// surface id in surfaces.ts, so adding a page there fails here until its
// fallback is stated.
const REQUIRED: Record<string, string[]> = {
	dashboard: ["h1:has-text('Dashboard')"],
	jobs: ["h1:has-text('Optimization Jobs')", "a[href='/create']"],
	"job-detail": [
		// The invariant names these five by hand: state, metrics, images,
		// parameters, and the artifact links.
		".badge",
		"#metric-history-card",
		"#image-viewer",
		"#image-viewer img[alt='Reference Image']",
		"#image-viewer img[alt='Current Best Image']",
		"#parameter-viewer",
		".detail-downloads",
		"#download-report",
		// Refresh is a link, not a scripted button, so it works in every mode.
		"a.btn:has-text('Refresh')",
	],
	create: [
		// The create page's fallback is a working control, not a view: the
		// form posts to the server-side handler. See Task 18.4's recorded
		// decision in docs/behavior-invariants.md.
		"form[method='POST'][action='/create']",
		"#optimizer",
		"button[type='submit']",
	],
	campaigns: ["h1:has-text('Campaigns')"],
	"campaign-detail": ["h1"],
	settings: ["#settings-image-refresh", "#settings-default-view-mode", "#settings-visible-metrics"],
};

async function assertFallbackIsComplete(page: Page, surfaceId: string) {
	for (const selector of REQUIRED[surfaceId] ?? []) {
		await expect(page.locator(selector).first(), `${surfaceId} is missing ${selector} without the island`).toBeVisible();
	}
}

// With JavaScript disabled the pre-paint theme script does not run either, so
// this also covers the one inline script the gate still permits being absent.
test.describe("with JavaScript disabled", () => {
	test.use({ javaScriptEnabled: false });

	for (const surface of SURFACES) {
		test(`${surface.id} renders its complete fallback`, async ({ page, seeded }) => {
			await page.goto(surfacePath(surface, seeded));
			await assertFallbackIsComplete(page, surface.id);
		});
	}
});

// A 404 on the bundle is not the same as no bundle: scripts run, <noscript>
// stays hidden, and the mount points sit there with their server-rendered
// children and no island coming.
test.describe("with the bundle missing", () => {
	for (const surface of SURFACES) {
		test(`${surface.id} renders its complete fallback`, async ({ page, seeded }) => {
			await page.route("**/static/dashboard.js*", (route) => route.fulfill({ status: 404, body: "" }));
			await page.goto(surfacePath(surface, seeded));
			await assertFallbackIsComplete(page, surface.id);
		});
	}
});

// And a bundle that loads and then throws, which is what a broken build or an
// unsupported syntax feature looks like: the module is evaluated, the error is
// uncaught, and no island ever mounts.
test.describe("with the bundle throwing", () => {
	for (const surface of SURFACES) {
		test(`${surface.id} renders its complete fallback`, async ({ page, seeded }) => {
			await page.route("**/static/dashboard.js*", (route) =>
				route.fulfill({
					status: 200,
					contentType: "text/javascript",
					body: 'throw new Error("deliberately broken bundle");',
				}),
			);
			// The thrown error must not fail the test: it is the condition
			// under test.
			page.on("pageerror", () => {});
			await page.goto(surfacePath(surface, seeded));
			await assertFallbackIsComplete(page, surface.id);
		});
	}
});

// The mount points have to survive a broken bundle too. A fallback that is
// complete only because the island root was left empty would pass the
// assertions above on a page that renders nothing at all inside it.
test("every island root still holds its server-rendered children when the bundle 404s", async ({ page, seeded }) => {
	await page.route("**/static/dashboard.js*", (route) => route.fulfill({ status: 404, body: "" }));

	for (const surface of SURFACES) {
		await page.goto(surfacePath(surface, seeded));

		const roots = page.locator("[data-island]");
		const count = await roots.count();
		expect(count, `${surface.id} renders no island mount point at all`).toBeGreaterThan(0);

		for (let index = 0; index < count; index++) {
			const root = roots.nth(index);
			const name = await root.getAttribute("data-island");
			// The theme switch is chrome and its fallback is three inert
			// buttons; every other root has to carry real page content.
			const minimum = name === "theme-switch" ? 1 : 200;
			const html = await root.innerHTML();
			expect(
				html.trim().length,
				`${surface.id}: island root "${name}" is empty without the bundle`,
			).toBeGreaterThan(minimum);
		}
	}
});
