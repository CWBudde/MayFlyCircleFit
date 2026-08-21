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
