import { expect, test } from "./fixtures/seed";

test("the skip link is the first tab stop and moves focus into the content", async ({ page }) => {
	await page.goto("/");
	await page.locator("body").press("Tab");

	const skip = page.locator(".skip-link");
	await expect(skip).toBeFocused();
	// It has to become visible when focused, or it is a trap for sighted
	// keyboard users who cannot see where they are.
	await expect(skip).toBeInViewport();

	await skip.press("Enter");
	// tabindex="-1" on <main> is what makes this move focus rather than only
	// the scroll position; without it the next Tab returns to the navigation.
	await expect(page.locator("#main-content")).toBeFocused();
});

test("every interactive control shows a visible focus indicator", async ({ page, seeded }) => {
	await page.goto(`/jobs/${seeded.jobId}`);

	const controls = page.locator("a[href], button:not([disabled]), input:not([disabled]), select");
	const sampled = Math.min(await controls.count(), 25);
	expect(sampled).toBeGreaterThan(0);

	for (let index = 0; index < sampled; index += 1) {
		const control = controls.nth(index);
		if (!(await control.isVisible())) continue;
		await control.focus();

		const visible = await control.evaluate((node) => {
			const style = getComputedStyle(node);
			// Either the shared outline ring, or the two-tone box-shadow the
			// filled buttons use because an outline disappears on them.
			const outlined = style.outlineStyle !== "none" && parseFloat(style.outlineWidth) > 0;
			return outlined || style.boxShadow !== "none";
		});
		const description = await control.evaluate((node) => `${node.tagName}.${(node as HTMLElement).className}`);
		expect(visible, `${description} has no visible focus indicator`).toBe(true);
	}
});

test("image view modes respond to their number shortcuts", async ({ page, seeded }) => {
	await page.goto(`/jobs/${seeded.jobId}`);
	const viewer = page.locator(".image-viewer").first();
	await expect(viewer).toBeVisible();

	for (const [key, mode] of [["1", "reference"], ["2", "best"], ["4", "difference"]] as const) {
		await page.locator("body").press(key);
		await expect(viewer).toHaveAttribute("data-view-mode", mode);
	}
});

test("a number shortcut does not fire while typing into a field", async ({ page }) => {
	await page.goto("/create");
	const circles = page.locator("#circles");
	await circles.fill("3");
	// The guard exists so a digit typed into a form reaches the form. If the
	// global handler ran here it would also have changed a view mode.
	await expect(circles).toHaveValue("3");
});

test("tabbing through the job detail page never traps focus", async ({ page, seeded }) => {
	await page.goto(`/jobs/${seeded.jobId}`);

	const seen: string[] = [];
	for (let step = 0; step < 40; step += 1) {
		await page.keyboard.press("Tab");
		seen.push(await page.evaluate(() => document.activeElement?.tagName ?? "NONE"));
	}
	// A trap shows up as the same element holding focus forever.
	expect(new Set(seen).size).toBeGreaterThan(1);
	expect(seen).not.toContain("NONE");
});
