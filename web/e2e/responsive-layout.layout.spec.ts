import { expect, test } from "./fixtures/seed";
import { SURFACES, surfacePath } from "./fixtures/surfaces";

// The layout contract in one line. Everything else on a page can be argued
// about; a page that scrolls sideways on a phone is simply broken, and this is
// the assertion that catches every cause of it at once -- a grid track wider
// than its container, an unwrapped flex row, a fixed-width image.
const WIDTHS = [320, 375, 768, 1024, 1440];

for (const surface of SURFACES) {
	for (const width of WIDTHS) {
		test(`${surface.id} does not scroll sideways at ${width}px`, async ({ page, seeded }) => {
			await page.setViewportSize({ width, height: 900 });
			await page.goto(surfacePath(surface, seeded));
			await expect(page.locator(surface.ready).first()).toBeVisible();

			const overflow = await page.evaluate(() => ({
				scrollWidth: document.documentElement.scrollWidth,
				clientWidth: document.documentElement.clientWidth,
			}));
			// One pixel of slack: sub-pixel layout rounding is not a defect.
			expect(
				overflow.scrollWidth,
				`${overflow.scrollWidth}px of content in a ${overflow.clientWidth}px viewport`,
			).toBeLessThanOrEqual(overflow.clientWidth + 1);
		});
	}
}

test("the navigation stacks below the tablet breakpoint", async ({ page }) => {
	await page.setViewportSize({ width: 375, height: 900 });
	await page.goto("/");
	const direction = await page
		.locator(".nav-container")
		.evaluate((node) => getComputedStyle(node).flexDirection);
	expect(direction).toBe("column");

	// Stacking is only useful if every link is still reachable.
	for (const label of ["Dashboard", "Jobs", "Campaigns", "Create Job", "Settings"]) {
		await expect(page.getByRole("link", { name: label, exact: true })).toBeVisible();
	}
});

test("side-by-side comparison stacks on a phone and pairs on a desktop", async ({ page, seeded }) => {
	await page.goto(`/jobs/${seeded.jobId}`);
	await expect(page.locator(".image-view-panels")).toBeVisible();

	// Asserting on the geometry of the visible panes rather than on the grid's
	// track count: auto-fit reports however many tracks the container fits,
	// which says nothing about whether the reader sees the two images beside
	// each other. Sharing a top edge is what "side by side" actually means.
	const paneTops = () =>
		page.locator(".image-view-panel:visible").evaluateAll((nodes) =>
			nodes.map((node) => Math.round(node.getBoundingClientRect().top)),
		);

	await page.setViewportSize({ width: 320, height: 900 });
	const stacked = await paneTops();
	expect(stacked.length).toBe(2);
	expect(stacked[0], "panes share a row on a phone").not.toBe(stacked[1]);

	await page.setViewportSize({ width: 1440, height: 900 });
	const paired = await paneTops();
	expect(paired.length).toBe(2);
	expect(paired[0], "panes are stacked on a desktop").toBe(paired[1]);
});

test("a wide table scrolls inside a reachable region rather than the page", async ({ page }) => {
	await page.setViewportSize({ width: 320, height: 900 });
	await page.goto("/");
	const regions = page.locator(".table-scroll");

	for (let index = 0; index < (await regions.count()); index += 1) {
		const region = regions.nth(index);
		// Focusable and named: a scroll container a keyboard cannot reach is
		// content a keyboard cannot read.
		await expect(region).toHaveAttribute("tabindex", "0");
		await expect(region).toHaveAttribute("aria-label", /\S/);
		expect(await region.evaluate((node) => getComputedStyle(node).overflowX)).toBe("auto");
	}
});
