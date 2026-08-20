import { expect, test } from "@playwright/test";

test("reconciles after a network outage without reloading", async ({
	context,
	page,
	request,
}) => {
	await page.goto("/jobs");
	await expect(page.getByText("Live updates: connected")).toBeVisible();

	const navigationStartedAt = await page.evaluate(() => performance.getEntriesByType("navigation")[0]?.startTime);
	await context.setOffline(true);

	const response = await request.post("/api/v1/jobs", {
		data: {
			refPath: "assets/test.png",
			mode: "joint",
			circles: 1,
			iters: 25,
			popSize: 20,
			seed: 42,
		},
	});
	expect(response.status(), await response.text()).toBe(201);
	const job = (await response.json()) as { id: string };
	// Going offline does not by itself make the page fetch anything, and the
	// reconciliation interval is far longer than the expect timeout. Ask for a
	// refresh explicitly so the offline failure is observed deterministically
	// rather than depending on the initial request still being in flight.
	await page.evaluate(() => window.dispatchEvent(new Event("focus")));
	await expect(page.getByText(/Failed to fetch/)).toBeVisible();

	await context.setOffline(false);
	await page.evaluate(() => window.dispatchEvent(new Event("focus")));
	await expect(page.getByText("Live updates: connected")).toBeVisible();
	await expect(page.locator(`a[href="/jobs/${job.id}"]`)).toBeVisible();
	expect(await page.evaluate(() => performance.getEntriesByType("navigation")[0]?.startTime)).toBe(navigationStartedAt);
});
