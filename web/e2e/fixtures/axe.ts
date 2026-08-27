import AxeBuilder from "@axe-core/playwright";
import { expect, type Page, type TestInfo } from "@playwright/test";
import { KNOWN_VIOLATIONS, MAX_KNOWN_VIOLATIONS } from "./known-a11y-violations";
import type { Theme } from "./surfaces";

// WCAG 2.1 level A and AA. These are the tags Task 12.9 committed to; axe's
// "best-practice" rules are collected separately and asserted on by nothing,
// so they inform without gating.
const WCAG = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"];

// The theme is applied through localStorage rather than by setting data-theme
// after load, because layout.templ runs an inline script in <head> that reads
// exactly this key before first paint. Writing the attribute afterwards races
// that script and can leave the document half-themed.
export async function useTheme(page: Page, theme: Theme): Promise<void> {
	await page.addInitScript((value) => {
		try {
			window.localStorage.setItem("mayflycirclefit.theme", value as string);
		} catch {
			// Storage is unavailable in some privacy contexts; the run is still
			// meaningful, it just audits whatever theme the browser defaults to.
		}
	}, theme);
}

// axe reports a node's target as a selector, or as an array of selectors when
// the node sits inside frames. Nothing audited here is framed, so joining is
// only about producing one stable string to match the allowlist against.
function selectorOf(target: unknown): string {
	return [target].flat(3).filter((part) => typeof part === "string").join(" ");
}

export async function runAxe(
	page: Page,
	surfaceId: string,
	theme: Theme,
	testInfo: TestInfo,
): Promise<void> {
	const results = await new AxeBuilder({ page }).withTags(WCAG).analyze();

	// Attach before asserting: a failure is unreadable from the GitHub
	// annotation alone, and this is the artifact that makes it diagnosable.
	await testInfo.attach(`axe-${surfaceId}-${theme}.json`, {
		body: JSON.stringify(results.violations, null, 2),
		contentType: "application/json",
	});

	// Scope the allowlist to the engine running: an entry that fires only on
	// WebKit must not be reported as stale when Chromium passes, and a WebKit
	// entry must not excuse a Chromium regression.
	const project = testInfo.project.name;
	const applicable = (KNOWN_VIOLATIONS[surfaceId] ?? []).filter(
		(entry) => !entry.engines || entry.engines.includes(project),
	);
	// The known WebKit defect is invisible in light mode, where the colour it
	// falls back to is close enough to the light palette's own text colour, so
	// the entry only applies to the dark run.
	//
	// Allowlisting is per node, not per rule: axe groups every failing element
	// under one violation, so excusing the rule id would discard nodes nobody
	// triaged and let a fresh contrast regression on the same surface pass for
	// as long as the known one kept firing.
	const allowed = new Map<string, Set<string>>();
	if (theme === "dark") {
		for (const entry of applicable) {
			const nodes = allowed.get(entry.rule) ?? new Set<string>();
			for (const node of entry.nodes) nodes.add(node);
			allowed.set(entry.rule, nodes);
		}
	}
	const fired = new Set(results.violations.map((violation) => violation.id));

	const unexpected = results.violations.flatMap((violation) => {
		const nodes = allowed.get(violation.id);
		if (!nodes) {
			return [`${violation.id}: ${violation.help} (${violation.nodes.length} nodes)`];
		}
		return violation.nodes
			.filter((node) => !nodes.has(selectorOf(node.target)))
			.map((node) => `${violation.id} on ${selectorOf(node.target)}: ${violation.help}`);
	});
	expect(
		unexpected,
		`unallowlisted WCAG 2.1 AA violations on ${surfaceId} (${theme})`,
	).toEqual([]);

	// The other half of the ratchet, kept at rule granularity. An allowlist
	// entry that stopped firing is a fix nobody recorded, and leaving it in
	// place would let the same defect return unnoticed. Per-node staleness is
	// deliberately not asserted: the defect resolves non-deterministically, so
	// which of the listed controls axe catches varies between runs, and only
	// the rule falling silent altogether means it is gone.
	const stale = [...allowed.keys()].filter((rule) => !fired.has(rule));
	expect(
		stale,
		`these allowlist entries for ${surfaceId} on ${project} no longer fire -- delete them from known-a11y-violations.ts`,
	).toEqual([]);
}

// Guards the budget across the whole suite, so the per-surface lists cannot
// grow past what was agreed even if each one looks individually reasonable.
export function assertAllowlistBudget(): void {
	const total = Object.values(KNOWN_VIOLATIONS).reduce((sum, list) => sum + list.length, 0);
	expect(
		total,
		"MAX_KNOWN_VIOLATIONS may only be lowered; a regression is not a reason to raise it",
	).toBeLessThanOrEqual(MAX_KNOWN_VIOLATIONS);
}
