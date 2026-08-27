import { assertAllowlistBudget, runAxe, useTheme } from "./fixtures/axe";
import { expect, fetchReportHtml, test } from "./fixtures/seed";
import { SURFACES, THEMES, surfacePath } from "./fixtures/surfaces";

// One case per (surface x theme). Both themes matter because the palettes are
// independent: a pairing that passes on white can fail badly on #0f172a, and
// the dark palette is the one that shipped the 2.54:1 button.
for (const surface of SURFACES) {
	for (const theme of THEMES) {
		test(`${surface.id} has no WCAG 2.1 AA violations (${theme})`, async ({ page, seeded }, testInfo) => {
			await useTheme(page, theme);
			await page.goto(surfacePath(surface, seeded));
			// Never a network wait: every one of these pages holds an open SSE
			// connection, so networkidle never resolves.
			await expect(page.locator(surface.ready).first()).toBeVisible();
			await runAxe(page, surface.id, theme, testInfo);
		});
	}
}

// The downloadable report is served as an attachment, so navigating to it
// downloads rather than renders. It is fetched and injected instead, which
// works offline because every image in it is a data: URI.
test("the downloadable report has no WCAG 2.1 AA violations", async ({ page, request, seeded }, testInfo) => {
	const html = await fetchReportHtml(request, seeded.jobId);
	await page.setContent(html);
	await runAxe(page, "report", "light", testInfo);
});

test("the allowlist stays within its agreed budget", async () => {
	assertAllowlistBudget();
});
