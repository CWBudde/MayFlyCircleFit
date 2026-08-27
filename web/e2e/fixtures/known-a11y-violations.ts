// Accessibility violations that are known, triaged, and not yet fixed.
//
// This list is a ratchet, not a mute button. Two assertions in axe.ts keep it
// honest: a violation missing from this list fails the run, and an entry here
// that no longer fires ALSO fails the run, with instructions to delete it. So
// the list cannot silently grow, and it cannot rot once a fix lands.
//
// Every entry states what it is and what closing it needs.

export type KnownViolation = {
	/** axe rule id, e.g. "color-contrast". */
	rule: string;
	/**
	 * Playwright project names this applies to. An entry with no engines
	 * listed applies everywhere. Scoping matters: a violation that fires on
	 * one engine and not another would otherwise be reported as a stale entry
	 * on the engine where it passes.
	 */
	engines?: string[];
	/**
	 * CSS selectors for the nodes this entry excuses, and only those.
	 *
	 * Allowlisting a bare rule id would discard every node axe grouped under
	 * it, so a fresh contrast failure anywhere on the surface would pass for as
	 * long as the known one kept firing. A node that is not listed here fails
	 * the run even when its rule is allowed.
	 */
	nodes: string[];
	/** What it is, and what closing it needs. */
	why: string;
};

// Narrower than "WebKit": desktop WebKit renders this document correctly, so
// whatever the trigger is, it needs the mobile device profile as well as the
// engine. That was found by the stale-entry half of the ratchet rejecting a
// broader guess.
const AFFECTED_PROJECTS = ["mobile-safari"];

// WebKit finishes this document's initial style pass with the root's custom
// properties resolved but inherited by nothing: body and every server-rendered
// element beneath it see no --text-color at all. Light mode hides it, because
// the initial color is near enough to #111827 to look correct; dark mode paints
// black text on the dark surface, which axe measures at 1.17:1 against a 4.5:1
// threshold.
//
// It needs the mobile device profile: the same page on desktop WebKit is clean,
// so viewport or device-scale state is part of the trigger.
//
// It is not caused by the theme script -- it reproduces with scripts stripped
// and the palette selected purely by prefers-color-scheme -- and it does not
// reproduce on a minimal document with the same rule shape, so it is a caching
// problem rather than a cascade one. Disabling any one stylesheet forces a
// recalculation and the correct values appear immediately.
//
// Only these two surfaces are affected because every other page mounts a React
// island, and mounting replaces the server-rendered markup with freshly created
// elements, which inherit correctly. Settings and create have no island.
//
// Tried and rejected: moving the theme script after the stylesheets, replacing
// the data-theme attribute with a swapped stylesheet so no element is mutated
// mid-parse, consolidating every :root declaration into a single sheet, and
// forcing an invalidation at the end of the body. Each helped in some runs and
// none was deterministic.
//
// Whether real Safari shares this is the open question, and no Linux runner can
// answer it -- Playwright ships WebKit built for Linux, not Safari. The manual
// dark-mode check in docs/browser-support.md is what settles it. If Safari is
// clean, these two entries come out and the WebKit projects keep guarding the
// rest; if it is not, this becomes a release blocker with a known reproduction.
// What axe reports is narrower than the mechanism: on both surfaces every
// flagged node is a <select>, measured at #000000 on #0f172a. Probing the live
// page shows the mechanism is nonetheless the one described above -- before
// anything touches it, body itself computes --text-color as the empty string
// and color as black, and setting any inline style on a single element makes
// the whole subtree resolve correctly. The selectors below are therefore where
// the defect surfaces, not the extent of it.
const customPropertyInheritance = (nodes: string[]): KnownViolation => ({
	rule: "color-contrast",
	engines: AFFECTED_PROJECTS,
	nodes,
	why: "WebKit does not inherit :root custom properties into this document's server-rendered elements on the initial style pass; dark mode therefore renders near-black text, which axe catches on the select controls. Pages with a React island escape it because mounting replaces the markup. See docs/browser-support.md for the manual Safari check that decides whether this is real.",
});

// Keyed by the surface id declared in surfaces.ts.
export const KNOWN_VIOLATIONS: Record<string, KnownViolation[]> = {
	settings: [
		customPropertyInheritance([
			"#settings-image-refresh",
			"#settings-default-view-mode",
			"#settings-default-colormap",
		]),
	],
	create: [
		customPropertyInheritance([
			"#optimizer",
			"#mode",
			"#covarianceMode",
			"#restartStrategy",
			"#polishingStrategy",
		]),
	],
};

// The total number of allowlisted violations across every surface.
// This may only ever be lowered. Raising it means a regression shipped; fix the
// regression rather than the number.
export const MAX_KNOWN_VIOLATIONS = 2;
