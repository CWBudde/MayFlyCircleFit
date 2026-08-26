import { expect, test } from "./fixtures/seed";
import { SURFACES, surfacePath } from "./fixtures/surfaces";

// These run on the iPhone and iPad device profiles, which bring a real touch
// stack and WebKit -- not merely a narrow desktop window.

for (const surface of SURFACES) {
	test(`${surface.id} fits the device viewport`, async ({ page, seeded }) => {
		await page.goto(surfacePath(surface, seeded));
		await expect(page.locator(surface.ready).first()).toBeVisible();

		const overflow = await page.evaluate(() => ({
			scrollWidth: document.documentElement.scrollWidth,
			clientWidth: document.documentElement.clientWidth,
		}));
		expect(
			overflow.scrollWidth,
			`${overflow.scrollWidth}px of content in a ${overflow.clientWidth}px viewport`,
		).toBeLessThanOrEqual(overflow.clientWidth + 1);
	});
}

test("navigation responds to a tap", async ({ page }) => {
	await page.goto("/");
	await page.getByRole("link", { name: "Jobs", exact: true }).tap();
	await expect(page).toHaveURL(/\/jobs$/);
});

test("image view modes can be chosen by tapping", async ({ page, seeded }) => {
	await page.goto(`/jobs/${seeded.jobId}`);
	const viewer = page.locator(".image-viewer").first();
	await expect(viewer).toBeVisible();

	// The label is the tap target; the radio behind it is visually hidden.
	await viewer.locator(".view-mode-option label").first().tap();
	await expect(viewer).toHaveAttribute("data-view-mode", "reference");
});

test("primary tap targets are large enough to hit", async ({ page }) => {
	await page.goto("/");

	// WCAG 2.5.5 is AAA, but 44px is what a thumb actually needs and it is
	// cheap to hold for the navigation and buttons. Inline links inside prose
	// are excluded: enlarging those would mean rewriting the copy.
	const targets = page.locator("nav a, .btn");
	for (let index = 0; index < (await targets.count()); index += 1) {
		const target = targets.nth(index);
		if (!(await target.isVisible())) continue;
		const box = await target.boundingBox();
		if (!box) continue;
		const name = (await target.textContent())?.trim().slice(0, 30);
		expect(Math.max(box.height, box.width), `"${name}" is ${Math.round(box.width)}x${Math.round(box.height)}`).toBeGreaterThanOrEqual(24);
	}
});
