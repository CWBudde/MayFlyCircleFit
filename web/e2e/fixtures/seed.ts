import { test as base, type APIRequestContext } from "@playwright/test";
import type { SeededIds } from "./surfaces";

// Seeding is worker-scoped, not test-scoped. With workers: 1 that means one
// optimizer run for the entire suite, shared by every spec and every project.
// Per-test seeding would add real optimizer time to each of the ~40 cases for
// no additional coverage.
//
// These have to be real POSTs rather than page.route stubs: /jobs/{id} and
// /schedules/{id} are rendered server-side from templ, so intercepting a
// browser fetch cannot produce the page an accessibility sweep needs to audit.

const REFERENCE = "assets/test.png";
const TERMINAL = new Set(["completed", "failed", "cancelled"]);

async function pollUntilTerminal(
	request: APIRequestContext,
	url: string,
	budgetMs = 60_000,
): Promise<Record<string, unknown>> {
	const deadline = Date.now() + budgetMs;
	for (;;) {
		const response = await request.get(url, { headers: { Accept: "application/json" } });
		if (!response.ok()) throw new Error(`${url} returned ${response.status()}`);
		const body = (await response.json()) as Record<string, unknown>;
		if (TERMINAL.has(String(body.state))) return body;
		if (Date.now() > deadline) {
			throw new Error(`${url} still ${String(body.state)} after ${budgetMs}ms`);
		}
		await new Promise((resolve) => setTimeout(resolve, 250));
	}
}

async function seedCompletedJob(request: APIRequestContext): Promise<string> {
	const response = await request.post("/api/v1/jobs", {
		data: { refPath: REFERENCE, mode: "joint", circles: 2, iters: 30, popSize: 20, seed: 42 },
	});
	if (response.status() !== 201) {
		throw new Error(`seeding a job returned ${response.status()}: ${await response.text()}`);
	}
	const { id } = (await response.json()) as { id: string };
	await pollUntilTerminal(request, `/api/v1/jobs/${id}`);
	return id;
}

async function seedCampaign(request: APIRequestContext): Promise<string> {
	const response = await request.post("/api/v1/schedules", {
		// The body is the schedule document itself, in the format
		// app.ParseSchedule validates -- see docs/examples/512-circle-campaign.json.
		// Kept deliberately tiny: this exists to give the campaign pages real
		// stages to render, not to measure anything.
		data: {
			schemaVersion: 1,
			name: "browser matrix fixture",
			seed: 7,
			base: { refPath: REFERENCE, mode: "batch", circles: 2, batchSize: 2, iters: 10, popSize: 20 },
			steps: [{ type: "extend", repeat: 1, additionalCircles: 1 }],
		},
	});
	if (response.status() !== 201) {
		throw new Error(`seeding a campaign returned ${response.status()}: ${await response.text()}`);
	}
	// Schedules return scheduleId, not id -- jobs are the ones that return id.
	const { scheduleId } = (await response.json()) as { scheduleId: string };
	return scheduleId;
}

export const test = base.extend<Record<string, never>, { seeded: SeededIds }>({
	seeded: [
		async ({ playwright }, use) => {
			const request = await playwright.request.newContext({
				baseURL: "http://127.0.0.1:19091",
			});
			try {
				const jobId = await seedCompletedJob(request);
				const campaignId = await seedCampaign(request);
				await use({ jobId, campaignId });
			} finally {
				await request.dispose();
			}
		},
		{ scope: "worker" },
	],
});

export { expect } from "@playwright/test";

// The report is served with Content-Disposition: attachment, so navigating to
// it triggers a download rather than rendering. It is fetched and injected via
// setContent instead -- which works offline because every image in it is a
// data: URI and it carries no <script> or <link> at all.
export async function fetchReportHtml(request: APIRequestContext, jobId: string): Promise<string> {
	const response = await request.get(`/api/v1/jobs/${jobId}/report.html`);
	if (!response.ok()) throw new Error(`report.html returned ${response.status()}`);
	return response.text();
}
