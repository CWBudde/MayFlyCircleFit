// Browser-local UI preferences.
//
// The settings island owns the editor, but the keys and the serialized shapes
// below are a compatibility contract rather than an implementation detail: the
// job detail page and the image viewer read the same entries. The serialized
// values were carried over verbatim from the inline script that used to live in
// internal/ui/settings.templ; changing how one is serialized needs a migration,
// not an edit here.
//
// The prefix was renamed once, from mayflycirclefit. to circlefit., when the
// project was renamed, and deliberately without a migration: a reader who had
// stored preferences under the old prefix falls back to the defaults the first
// time afterwards. That is the only key rename this file has taken, and the
// rule above stands for the next one.

export const PREFERENCE_KEYS = {
	/** Milliseconds between forced image refreshes, decimal; "0" is SSE-driven. */
	autoRefresh: "circlefit.imageRefreshInterval",
	/** One member of VIEW_MODES, stored verbatim. */
	viewMode: "circlefit.viewMode",
	/** One member of COLORMAPS, stored verbatim. */
	colormap: "circlefit.diffColormap",
	/** A JSON array of METRIC_IDS members, in METRIC_IDS order. */
	visibleMetrics: "circlefit.visibleMetrics",
} as const;

export const VIEW_MODES = ["reference", "best", "side-by-side", "difference", "overlay"] as const;
export const COLORMAPS = ["turbo", "magma"] as const;
export const METRIC_IDS = ["cost", "psnr", "ssim", "cps"] as const;

export type ViewMode = (typeof VIEW_MODES)[number];
export type Colormap = (typeof COLORMAPS)[number];
export type MetricID = (typeof METRIC_IDS)[number];

export type Preferences = {
	autoRefresh: number;
	viewMode: ViewMode;
	colormap: Colormap;
	visibleMetrics: MetricID[];
};

// Defaults a page falls back to when nothing is stored. They are also what the
// reset button restores, by removing the keys rather than by writing these
// values back: an absent key and a key holding the default have to stay
// indistinguishable, because that is what the readers assume.
export const DEFAULT_PREFERENCES: Preferences = {
	autoRefresh: 0,
	viewMode: "side-by-side",
	colormap: "turbo",
	visibleMetrics: [...METRIC_IDS],
};

// The subset of Storage this module uses. Naming it keeps the helpers testable
// off a fake in vitest's node environment, where there is no window at all.
export interface PreferenceStorage {
	getItem(key: string): string | null;
	setItem(key: string, value: string): void;
	removeItem(key: string): void;
}

// browserStorage returns localStorage, or null where reading it throws. Privacy
// modes make the property itself throw, not just its methods, so this has to be
// guarded rather than assumed.
export function browserStorage(): PreferenceStorage | null {
	try {
		return typeof window === "undefined" ? null : window.localStorage;
	} catch {
		return null;
	}
}

function readRaw(storage: PreferenceStorage | null, key: string): string | null {
	if (!storage) return null;
	try {
		return storage.getItem(key);
	} catch {
		return null;
	}
}

// readPreference is readRaw under an exported name. The image viewer needs one
// entry at a time rather than the whole editor bundle: it reads the view mode
// before it knows whether the server default or the stored value wins, and it
// reads the refresh interval without caring what the metric checkboxes say.
export function readPreference(storage: PreferenceStorage | null, key: string): string | null {
	return readRaw(storage, key);
}

// writePreference stores one entry and leaves the rest alone. writePreferences
// writes all four keys at once, which is right for the editor and wrong for a
// job page: switching the view mode there must not also materialize defaults
// for keys the reader has never touched, because an absent key and a key
// holding the default are deliberately indistinguishable to every reader.
export function writePreference(
	key: string,
	value: string,
	storage: PreferenceStorage | null,
): boolean {
	if (!storage) return false;

	try {
		storage.setItem(key, value);
		return true;
	} catch {
		return false;
	}
}

// The image viewer's overlay blend, as a whole percentage. It sits outside
// PREFERENCE_KEYS on purpose: the settings editor does not offer it, so it is
// not part of the set that editor writes or that its reset button clears. The
// key name and the decimal-integer shape are still a compatibility contract --
// the inline viewer script stored them under exactly this name before any of
// this was TypeScript.
export const OVERLAY_OPACITY_KEY = "circlefit.overlayOpacity";
export const DEFAULT_OVERLAY_OPACITY = 50;

// normalizeOverlayOpacity parses with parseInt rather than Number, matching the
// inline script it replaces: a stored "50.7" is 50, not 51.
export function normalizeOverlayOpacity(raw: string | number | null): number {
	const parsed = typeof raw === "number" ? raw : Number.parseInt(raw ?? "", 10);
	if (!Number.isFinite(parsed)) return DEFAULT_OVERLAY_OPACITY;
	return Math.min(100, Math.max(0, Math.round(parsed)));
}

export function readOverlayOpacity(storage: PreferenceStorage | null): number {
	return normalizeOverlayOpacity(readRaw(storage, OVERLAY_OPACITY_KEY));
}

export function writeOverlayOpacity(percent: number, storage: PreferenceStorage | null): boolean {
	return writePreference(OVERLAY_OPACITY_KEY, String(normalizeOverlayOpacity(percent)), storage);
}

export function normalizeAutoRefresh(raw: string | null): number {
	const parsed = Number.parseInt(raw ?? "", 10);
	return Number.isFinite(parsed) && parsed >= 0 ? parsed : DEFAULT_PREFERENCES.autoRefresh;
}

export function normalizeViewMode(raw: string | null): ViewMode {
	return VIEW_MODES.includes(raw as ViewMode) ? (raw as ViewMode) : DEFAULT_PREFERENCES.viewMode;
}

export function normalizeColormap(raw: string | null): Colormap {
	return COLORMAPS.includes(raw as Colormap) ? (raw as Colormap) : DEFAULT_PREFERENCES.colormap;
}

// normalizeMetricSelection is the "clearing every box restores all of them"
// rule, kept in one place because both reading and writing need it: an empty
// selection would otherwise hide every metric card with no way back.
export function normalizeMetricSelection(selected: readonly string[]): MetricID[] {
	const enabled = new Set(selected);
	const kept = METRIC_IDS.filter((metric) => enabled.has(metric));
	return kept.length > 0 ? kept : [...DEFAULT_PREFERENCES.visibleMetrics];
}

export function normalizeVisibleMetrics(raw: string | null): MetricID[] {
	if (!raw) return [...DEFAULT_PREFERENCES.visibleMetrics];
	try {
		const parsed: unknown = JSON.parse(raw);
		if (!Array.isArray(parsed)) return [...DEFAULT_PREFERENCES.visibleMetrics];
		return normalizeMetricSelection(parsed.filter((entry): entry is string => typeof entry === "string"));
	} catch {
		return [...DEFAULT_PREFERENCES.visibleMetrics];
	}
}

export function readPreferences(storage: PreferenceStorage | null): Preferences {
	return {
		autoRefresh: normalizeAutoRefresh(readRaw(storage, PREFERENCE_KEYS.autoRefresh)),
		viewMode: normalizeViewMode(readRaw(storage, PREFERENCE_KEYS.viewMode)),
		colormap: normalizeColormap(readRaw(storage, PREFERENCE_KEYS.colormap)),
		visibleMetrics: normalizeVisibleMetrics(readRaw(storage, PREFERENCE_KEYS.visibleMetrics)),
	};
}

// normalizePreferences puts an editor's in-flight values back inside the range
// the readers accept, so a write is never the thing that stores nonsense.
export function normalizePreferences(input: Preferences): Preferences {
	return {
		autoRefresh: normalizeAutoRefresh(String(input.autoRefresh)),
		viewMode: normalizeViewMode(input.viewMode),
		colormap: normalizeColormap(input.colormap),
		visibleMetrics: normalizeMetricSelection(input.visibleMetrics),
	};
}

// writePreferences reports whether the values reached storage. The caller turns
// false into the "local storage is not available" notice; it is not an error,
// because the page still works, it just forgets.
export function writePreferences(input: Preferences, storage: PreferenceStorage | null): boolean {
	if (!storage) return false;

	const prefs = normalizePreferences(input);
	try {
		storage.setItem(PREFERENCE_KEYS.autoRefresh, String(prefs.autoRefresh));
		storage.setItem(PREFERENCE_KEYS.viewMode, prefs.viewMode);
		storage.setItem(PREFERENCE_KEYS.colormap, prefs.colormap);
		storage.setItem(PREFERENCE_KEYS.visibleMetrics, JSON.stringify(prefs.visibleMetrics));
		return true;
	} catch {
		return false;
	}
}

// resetPreferences removes the four keys this editor owns. The theme is not one
// of them: it is stored under its own key by the pre-paint script in the layout
// and is not part of what this page resets.
export function resetPreferences(storage: PreferenceStorage | null): boolean {
	if (!storage) return false;

	try {
		for (const key of Object.values(PREFERENCE_KEYS)) {
			storage.removeItem(key);
		}
		return true;
	} catch {
		return false;
	}
}
