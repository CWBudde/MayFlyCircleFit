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

// The list is empty, and the story of what used to be in it is worth keeping,
// because the defect it recorded is unfixed -- it was escaped, not closed.
//
// WebKit on the mobile device profile finished this document's initial style
// pass with the root's custom properties resolved but inherited by nothing:
// body and every server-rendered element beneath it saw no --text-color at all.
// Light mode hid it, because the fallback colour is near enough to #111827 to
// look right; dark mode painted black text on the dark surface, which axe
// measured at 1.17:1 against a 4.5:1 threshold. Desktop WebKit was clean, so
// viewport or device-scale state was part of the trigger. It was not the theme
// script -- it reproduced with scripts stripped and the palette chosen purely by
// prefers-color-scheme -- and it did not reproduce on a minimal document with
// the same rule shape, so it was a style-caching problem rather than a cascade
// one. Disabling any one stylesheet forced a recalculation and the correct
// values appeared immediately.
//
// Tried and rejected at the time: moving the theme script after the
// stylesheets, replacing the data-theme attribute with a swapped stylesheet so
// no element is mutated mid-parse, consolidating every :root declaration into a
// single sheet, and forcing an invalidation at the end of the body. Each helped
// in some runs and none was deterministic.
//
// What did close it was Phase 18, and not on purpose. The two surfaces that
// tripped it -- settings and create -- were the last two pages rendering their
// controls as server-rendered markup with no island over them. Both are islands
// now (`settings` and `create-job`; see mountIslands in web/src/dashboard.tsx),
// and mounting calls createRoot().render(), which replaces the server-rendered
// <select>s with freshly created elements. Fresh elements inherit correctly.
// The stale-entry half of the ratchet is what reported this: with the Phase 18
// bundle in place both entries stopped firing on mobile-safari, in dark, and
// the run failed until they were deleted.
//
// So the browser defect is still there. Anything that reintroduces a control
// painted outside every island root -- a new page with no island, or a fallback
// a reader interacts with before the bundle arrives -- can bring it back, and
// it will come back as a real contrast failure rather than as an allowlist
// entry. docs/browser-support.md carries the manual Safari check that decides
// whether the same defect exists outside Playwright's Linux WebKit build.

// Keyed by the surface id declared in surfaces.ts.
export const KNOWN_VIOLATIONS: Record<string, KnownViolation[]> = {};

// The total number of allowlisted violations across every surface.
// This may only ever be lowered. Raising it means a regression shipped; fix the
// regression rather than the number.
export const MAX_KNOWN_VIOLATIONS = 0;
