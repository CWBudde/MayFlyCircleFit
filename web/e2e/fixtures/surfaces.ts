// The canonical list of auditable UI surfaces.
//
// One place, so a11y, responsive and keyboard specs cannot drift out of sync
// about what "every page" means, and so adding a page to the UI is a one-line
// change here rather than an edit to four specs.

export type Surface = {
	/** Stable id, used to key the allowlist and name attachments. */
	id: string;
	/** Path, or a builder when the URL needs a seeded id. */
	path: string | ((ids: SeededIds) => string);
	/**
	 * A locator that is only present once the page has finished hydrating.
	 *
	 * Readiness is never a network wait on this app: every page holds an open
	 * SSE connection, so waitForLoadState("networkidle") never resolves.
	 */
	ready: string;
};

export type SeededIds = { jobId: string; campaignId: string };

export const SURFACES: Surface[] = [
	{ id: "dashboard", path: "/", ready: "[data-live-state]" },
	{ id: "jobs", path: "/jobs", ready: "[data-live-state]" },
	{ id: "job-detail", path: (ids) => `/jobs/${ids.jobId}`, ready: "[data-live-state]" },
	{ id: "create", path: "/create", ready: "form" },
	{ id: "campaigns", path: "/schedules", ready: "[data-live-state]" },
	{ id: "campaign-detail", path: (ids) => `/schedules/${ids.campaignId}`, ready: "[data-live-state]" },
	{ id: "settings", path: "/settings", ready: "h1" },
];

export function surfacePath(surface: Surface, ids: SeededIds): string {
	return typeof surface.path === "function" ? surface.path(ids) : surface.path;
}

export const THEMES = ["light", "dark"] as const;
export type Theme = (typeof THEMES)[number];
