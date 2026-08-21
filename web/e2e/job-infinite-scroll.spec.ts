import { expect, test } from "@playwright/test";

function job(id: string, refPath: string, startTime: string) {
	return {
		id,
		state: "completed",
		config: { refPath, mode: "joint", circles: 8 },
		iterations: 10,
		bestCost: 4,
		initialCost: 10,
		startTime,
	};
}

test("loads the next jobs page as the sentinel approaches the viewport", async ({ page }) => {
	const requestedCursors: string[] = [];
	await page.route("**/api/v1/jobs?*", async (route) => {
		const cursor = new URL(route.request().url()).searchParams.get("cursor") ?? "";
		requestedCursors.push(cursor);
		const response = cursor === "page-2"
			? {
				jobs: [job("22222222-2222-4222-8222-222222222222", "second.png", "2026-08-20T11:00:00Z")],
				total: 10_000,
			}
			: {
				jobs: [job("11111111-1111-4111-8111-111111111111", "first.png", "2026-08-20T12:00:00Z")],
				nextCursor: "page-2",
				total: 10_000,
			};
		await route.fulfill({ contentType: "application/json", body: JSON.stringify(response) });
	});

	await page.goto("/jobs");
	await expect(page.getByText("first.png")).toBeVisible();
	await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
	await expect(page.getByText("second.png")).toBeVisible();
	await expect.poll(() => requestedCursors).toContain("page-2");
});

test("reconciliation drops rows deleted while later pages were loaded", async ({ page }) => {
	const a = job("aaaaaaaa-1111-4111-8111-111111111111", "alpha.png", "2026-08-20T12:00:00Z");
	const b = job("bbbbbbbb-2222-4222-8222-222222222222", "bravo.png", "2026-08-20T11:00:00Z");
	const c = job("cccccccc-3333-4333-8333-333333333333", "charlie.png", "2026-08-20T10:00:00Z");
	const d = job("dddddddd-4444-4444-8444-444444444444", "delta.png", "2026-08-20T09:00:00Z");

	// After the deletion the first page slides forward, so `alpha` is absent
	// from it without any job.deleted event ever reaching the browser.
	let deleted = false;
	await page.route("**/api/v1/jobs?*", async (route) => {
		const cursor = new URL(route.request().url()).searchParams.get("cursor") ?? "";
		const response = cursor === "page-2"
			? { jobs: [c, d], total: 4 }
			: deleted
				? { jobs: [b, c], nextCursor: "page-2", total: 3 }
				: { jobs: [a, b], nextCursor: "page-2", total: 4 };
		await route.fulfill({ contentType: "application/json", body: JSON.stringify(response) });
	});

	await page.goto("/jobs");
	await expect(page.getByText("alpha.png")).toBeVisible();
	await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
	await expect(page.getByText("delta.png")).toBeVisible();

	deleted = true;
	await page.evaluate(() => window.dispatchEvent(new Event("focus")));

	// The deleted row is inside the refreshed window and goes; the rows beyond
	// it are untouched, because this fetch says nothing about them.
	await expect(page.getByText("alpha.png")).toHaveCount(0);
	await expect(page.getByText("bravo.png")).toBeVisible();
	await expect(page.getByText("charlie.png")).toBeVisible();
	await expect(page.getByText("delta.png")).toBeVisible();
	await expect(page.getByText("Showing 3 of 3")).toBeVisible();
});
