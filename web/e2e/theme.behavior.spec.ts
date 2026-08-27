import { expect, test } from "./fixtures/seed";

// The theme controller was rewritten to swap one stylesheet rather than set an
// attribute on <html>, so the switch itself needs covering: the Go tests can
// only assert that the markers are present, not that clicking one works.

test("the theme switch applies, persists, and returns to auto", async ({ page }) => {
	await page.goto("/settings");

	const body = page.locator("body");
	const background = () => body.evaluate((node) => getComputedStyle(node).backgroundColor);

	await page.getByRole("button", { name: "Use dark theme" }).click();
	await expect(page.getByRole("button", { name: "Use dark theme" })).toHaveAttribute("aria-pressed", "true");
	expect(await background()).toBe("rgb(15, 23, 42)");

	// It has to survive a reload, applied before the first paint.
	await page.reload();
	expect(await background()).toBe("rgb(15, 23, 42)");
	await expect(page.getByRole("button", { name: "Use dark theme" })).toHaveAttribute("aria-pressed", "true");

	await page.getByRole("button", { name: "Use light theme" }).click();
	expect(await background()).toBe("rgb(249, 250, 251)");

	await page.getByRole("button", { name: "Use system theme" }).click();
	await expect(page.getByRole("button", { name: "Use system theme" })).toHaveAttribute("aria-pressed", "true");
	expect(await page.evaluate(() => localStorage.getItem("mayflycirclefit.theme"))).toBeNull();
});

test("a stored theme survives a page with no island", async ({ page }) => {
	await page.goto("/create");
	await page.getByRole("button", { name: "Use dark theme" }).click();
	await page.reload();
	// create has no React island, so nothing replaces the server-rendered
	// markup after load: whatever the first paint produced is what stays.
	expect(await page.locator("body").evaluate((n) => getComputedStyle(n).color)).toBe("rgb(243, 244, 246)");
});

// The explicit choice has to beat the system preference, which is only true if
// its selector matches the specificity of the prefers-color-scheme rule.
// :root:not([data-theme]) computes to (0,2,0), so an override written as a bare
// :root lost to it however late it was appended: on a dark-preferring OS the
// Light button reported itself pressed and nothing changed.
test.describe("on an OS that prefers dark", () => {
	test.use({ colorScheme: "dark" });

	test("an explicit light choice still wins", async ({ page }) => {
		await page.goto("/settings");

		const background = () => page.locator("body").evaluate((node) => getComputedStyle(node).backgroundColor);
		// Auto follows the preference.
		expect(await background()).toBe("rgb(15, 23, 42)");

		await page.getByRole("button", { name: "Use light theme" }).click();
		expect(await background()).toBe("rgb(249, 250, 251)");

		// And it has to survive the reload, where the preload script reapplies
		// it before the first paint rather than after a click.
		await page.reload();
		await expect(page.getByRole("button", { name: "Use light theme" })).toHaveAttribute("aria-pressed", "true");
		expect(await background()).toBe("rgb(249, 250, 251)");
	});
});
