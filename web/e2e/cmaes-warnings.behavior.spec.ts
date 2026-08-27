import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures/seed";

// The two CMA-ES refusals the creation form anticipates.
//
// internal/server/create_form_cmaes_test.go proves the server refuses these
// configurations, and the Go and unit halves prove the strings are composed
// from the projected limits. What neither can reach is the part that only
// exists once the island has mounted: the warning appearing and clearing as the
// reader edits a *neighbouring* field. That is what is pinned here.
//
// The warnings are advisory by design. app.Validate decides the request, so the
// form stays submittable while one is showing; the last test is what keeps that
// from quietly becoming a client-side block.

const COVARIANCE = /searches \d+ dimensions, above the \d+ full covariance supports/;
const RESTARTS = /schedules its own restarts inside one budget/;

async function openCMAESForm(page: Page) {
	await page.goto("/create");
	// The island has to be up: the CMA-ES section is revealed by it, while the
	// fallback renders that section for every engine.
	await expect(page.locator("[data-island='create-job'] form")).toBeVisible();
	// Located by id rather than by label: every required control's accessible
	// name carries the asterisk's sr-only " (required)" text.
	await page.locator("#optimizer").selectOption("cmaes");
	await expect(page.getByRole("heading", { name: "CMA-ES" })).toBeVisible();
}

test("full covariance warns once the run outgrows it, and clears again", async ({ page }) => {
	await openCMAESForm(page);

	const warning = page.getByText(COVARIANCE);
	// Ten circles in joint mode is seventy dimensions, well inside the limit.
	await expect(warning).toHaveCount(0);

	// The circle count is what crosses the limit, and it is not in the CMA-ES
	// section: the warning has to react to a field two fieldsets above it.
	await page.locator("#circles").fill("100");
	await expect(warning).toBeVisible();

	// Either way out of it clears the warning. Block covariance first.
	await page.locator("#covarianceMode").selectOption("block");
	await expect(warning).toHaveCount(0);

	// Then the mode, which changes how much of the canvas one run searches.
	await page.locator("#covarianceMode").selectOption("full");
	await expect(warning).toBeVisible();
	await page.locator("#mode").selectOption("sequential");
	await expect(warning).toHaveCount(0);
});

test("an internal restart schedule warns beside a fixed attempt count", async ({ page }) => {
	await openCMAESForm(page);

	const warning = page.getByText(RESTARTS);
	await page.locator("#optimizerRestarts").fill("16");
	await expect(warning).toHaveCount(0);

	await page.locator("#restartStrategy").selectOption("ipop");
	await expect(warning).toBeVisible();

	// One is the value IPOP requires, and an emptied field defaults to it.
	await page.locator("#optimizerRestarts").fill("1");
	await expect(warning).toHaveCount(0);
	await page.locator("#optimizerRestarts").fill("");
	await expect(warning).toHaveCount(0);
});

test("a warning advises without disabling the submission", async ({ page }) => {
	await openCMAESForm(page);
	await page.locator("#circles").fill("100");
	await expect(page.getByText(COVARIANCE)).toBeVisible();

	// app.Validate is the only thing that decides a request. The page must not
	// refuse something app would have accepted, so the control stays live.
	await expect(page.getByRole("button", { name: "Create Job" })).toBeEnabled();
});
