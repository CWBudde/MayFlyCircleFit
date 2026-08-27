import { describe, expect, it } from "vitest";
import {
	DEFAULT_PREFERENCES,
	METRIC_IDS,
	normalizeAutoRefresh,
	normalizeColormap,
	normalizeMetricSelection,
	normalizeViewMode,
	normalizeVisibleMetrics,
	PREFERENCE_KEYS,
	readPreferences,
	resetPreferences,
	writePreferences,
} from "./prefs";
import type { PreferenceStorage } from "./prefs";

// A localStorage stand-in. vitest runs in the node environment, so there is no
// real one, and the helpers take the storage as an argument precisely so this
// stays a unit test rather than a jsdom setup.
class FakeStorage implements PreferenceStorage {
	readonly entries = new Map<string, string>();

	constructor(initial: Record<string, string> = {}) {
		for (const [key, value] of Object.entries(initial)) this.entries.set(key, value);
	}

	getItem(key: string): string | null {
		return this.entries.get(key) ?? null;
	}

	setItem(key: string, value: string): void {
		this.entries.set(key, value);
	}

	removeItem(key: string): void {
		this.entries.delete(key);
	}
}

// Storage that exists but refuses to serve, which is what a browser in a
// privacy-restricted context does once the quota or the policy says no.
const throwingStorage: PreferenceStorage = {
	getItem() {
		throw new Error("storage disabled");
	},
	setItem() {
		throw new Error("storage disabled");
	},
	removeItem() {
		throw new Error("storage disabled");
	},
};

// The keys and the serialized shapes are a compatibility contract: a reader who
// set a preference before the settings page became an island must still get it
// afterwards, and two other pages read the same entries. This case exists to
// make a rename fail loudly rather than silently forget everyone's settings.
describe("the storage contract", () => {
	it("pins the key names", () => {
		expect(PREFERENCE_KEYS).toEqual({
			autoRefresh: "mayflycirclefit.imageRefreshInterval",
			viewMode: "mayflycirclefit.viewMode",
			colormap: "mayflycirclefit.diffColormap",
			visibleMetrics: "mayflycirclefit.visibleMetrics",
		});
	});

	it("pins the serialized value shapes", () => {
		const storage = new FakeStorage();

		expect(writePreferences(
			{ autoRefresh: 5000, viewMode: "difference", colormap: "magma", visibleMetrics: ["psnr", "cost"] },
			storage,
		)).toBe(true);

		expect(Object.fromEntries(storage.entries)).toEqual({
			"mayflycirclefit.imageRefreshInterval": "5000",
			"mayflycirclefit.viewMode": "difference",
			"mayflycirclefit.diffColormap": "magma",
			// Canonical order, not the order the boxes were ticked in.
			"mayflycirclefit.visibleMetrics": '["cost","psnr"]',
		});
	});

	it("reads back what an older build wrote", () => {
		// Written by the inline script this island replaced, verbatim.
		const storage = new FakeStorage({
			"mayflycirclefit.imageRefreshInterval": "2000",
			"mayflycirclefit.viewMode": "overlay",
			"mayflycirclefit.diffColormap": "magma",
			"mayflycirclefit.visibleMetrics": '["cost","ssim"]',
		});

		expect(readPreferences(storage)).toEqual({
			autoRefresh: 2000,
			viewMode: "overlay",
			colormap: "magma",
			visibleMetrics: ["cost", "ssim"],
		});
	});
});

describe("normalizeAutoRefresh", () => {
	it.each<[string | null, number]>([
		["0", 0],
		["30000", 30000],
		["2000", 2000],
		// parseInt's prefix behavior, kept because the previous script had it.
		["2000ms", 2000],
		[null, 0],
		["", 0],
		["-1", 0],
		["not a number", 0],
	])("maps %o to %o", (raw, want) => {
		expect(normalizeAutoRefresh(raw)).toBe(want);
	});
});

describe("normalizeViewMode", () => {
	it("keeps a known mode", () => {
		expect(normalizeViewMode("difference")).toBe("difference");
	});

	it.each([null, "", "sideBySide", "__proto__"])("falls back for %o", (raw) => {
		expect(normalizeViewMode(raw)).toBe("side-by-side");
	});
});

describe("normalizeColormap", () => {
	it("keeps a known colormap", () => {
		expect(normalizeColormap("magma")).toBe("magma");
	});

	it.each([null, "", "viridis"])("falls back for %o", (raw) => {
		expect(normalizeColormap(raw)).toBe("turbo");
	});
});

describe("normalizeVisibleMetrics", () => {
	it("keeps the canonical order regardless of how the value was written", () => {
		expect(normalizeVisibleMetrics('["cps","cost"]')).toEqual(["cost", "cps"]);
	});

	it("drops metrics it does not know", () => {
		expect(normalizeVisibleMetrics('["cost","entropy"]')).toEqual(["cost"]);
	});

	it.each<[string, string | null]>([
		["nothing stored", null],
		["an empty string", ""],
		["an empty selection", "[]"],
		["a selection of unknown names only", '["entropy"]'],
		["malformed JSON", "{"],
		["a JSON value that is not an array", '"cost"'],
	])("restores every metric for %s", (_case, raw) => {
		expect(normalizeVisibleMetrics(raw)).toEqual([...METRIC_IDS]);
	});
});

describe("normalizeMetricSelection", () => {
	it("restores every metric when the last box is cleared", () => {
		expect(normalizeMetricSelection([])).toEqual([...METRIC_IDS]);
	});

	it("de-duplicates and orders", () => {
		expect(normalizeMetricSelection(["ssim", "cost", "ssim"])).toEqual(["cost", "ssim"]);
	});
});

describe("readPreferences", () => {
	it("returns the defaults for an empty storage", () => {
		expect(readPreferences(new FakeStorage())).toEqual(DEFAULT_PREFERENCES);
	});

	it("returns the defaults when there is no storage at all", () => {
		expect(readPreferences(null)).toEqual(DEFAULT_PREFERENCES);
	});

	it("returns the defaults when reading throws", () => {
		expect(readPreferences(throwingStorage)).toEqual(DEFAULT_PREFERENCES);
	});

	it("does not hand out the shared default array", () => {
		const first = readPreferences(new FakeStorage());
		first.visibleMetrics.push("cost");
		expect(DEFAULT_PREFERENCES.visibleMetrics).toEqual([...METRIC_IDS]);
	});
});

describe("writePreferences", () => {
	it("stores a value the readers accept even when the editor offers a bad one", () => {
		const storage = new FakeStorage();

		expect(writePreferences(
			{ autoRefresh: -5, viewMode: "nope" as never, colormap: "nope" as never, visibleMetrics: [] },
			storage,
		)).toBe(true);

		expect(readPreferences(storage)).toEqual(DEFAULT_PREFERENCES);
	});

	it("reports failure rather than throwing when storage refuses", () => {
		expect(writePreferences(DEFAULT_PREFERENCES, throwingStorage)).toBe(false);
	});

	it("reports failure when there is no storage", () => {
		expect(writePreferences(DEFAULT_PREFERENCES, null)).toBe(false);
	});
});

describe("resetPreferences", () => {
	it("removes exactly the four keys it owns", () => {
		const storage = new FakeStorage({
			"mayflycirclefit.imageRefreshInterval": "5000",
			"mayflycirclefit.viewMode": "overlay",
			"mayflycirclefit.diffColormap": "magma",
			"mayflycirclefit.visibleMetrics": '["cost"]',
			// Owned by the layout's pre-paint script, and not this page's to clear.
			"mayflycirclefit.theme": "dark",
			// Owned by the image viewer.
			"mayflycirclefit.overlayOpacity": "70",
		});

		expect(resetPreferences(storage)).toBe(true);
		expect([...storage.entries.keys()]).toEqual([
			"mayflycirclefit.theme",
			"mayflycirclefit.overlayOpacity",
		]);
		expect(readPreferences(storage)).toEqual(DEFAULT_PREFERENCES);
	});

	it("reports failure rather than throwing when storage refuses", () => {
		expect(resetPreferences(throwingStorage)).toBe(false);
	});

	it("reports failure when there is no storage", () => {
		expect(resetPreferences(null)).toBe(false);
	});
});
