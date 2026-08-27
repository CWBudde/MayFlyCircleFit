import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures/seed";

// The refusals the creation form anticipates: two CMA-ES ones and the polishing
// engine restriction, which covers Dragonfly as well.
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
// Deliberately not the "runs its own MayFly population" clause: the checkbox's
// help text carries that sentence too, and matching it would pass whether or
// not the warning ever appeared.
const POLISHING = /unavailable under \w+ and this job will be refused/;

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

// The batch case is the one the browser can get wrong in the direction that
// matters: telling a reader to change a configuration the server accepts. A
// batch job leaving Batch Size at its automatic 0 searches the *default* batch,
// not the whole canvas, because Validate reads the normalized configuration.
test("an automatic batch size is resolved before the warning is decided", async ({ page }) => {
	await openCMAESForm(page);

	const warning = page.getByText(COVARIANCE);
	await page.locator("#circles").fill("100");
	await expect(warning).toBeVisible();

	// Same hundred circles, but one batch at a time and no batch size given.
	await page.locator("#mode").selectOption("batch");
	await expect(warning).toHaveCount(0);

	// A batch that is actually wide enough does cross the limit, so the mode is
	// not simply exempt.
	await page.locator("#batchSize").fill("100");
	await expect(warning).toBeVisible();
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

// Polishing is the one anticipated refusal that is not CMA-ES-specific: a sweep
// runs its own MayFly population whatever engine the job names, so Dragonfly is
// refused for the same reason and the note has to name whichever engine is
// selected. See "Polishing is MayFly-only" in docs/behavior-invariants.md.
test("polishing warns under an engine that cannot polish, and names it", async ({ page }) => {
	await openCMAESForm(page);

	const warning = page.getByText(POLISHING);
	// The engine alone is not a conflict; nothing has asked for a polish yet.
	await expect(warning).toHaveCount(0);

	await page.locator("#polishingEnabled").check();
	await expect(warning).toBeVisible();
	await expect(page.getByText(/unavailable under cmaes/)).toBeVisible();

	// Either way out clears it: the engine that can polish, or no polish.
	await page.locator("#optimizer").selectOption("mayfly");
	await expect(warning).toHaveCount(0);

	await page.locator("#optimizer").selectOption("dragonfly");
	await expect(page.getByText(/unavailable under dragonfly/)).toBeVisible();

	await page.locator("#polishingEnabled").uncheck();
	await expect(warning).toHaveCount(0);
});
