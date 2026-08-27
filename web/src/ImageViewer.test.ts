import { describe, expect, it } from "vitest";
import {
	heatmapGradient,
	initialColormap,
	initialViewMode,
	missingImageMessage,
	shortcutMode,
	withImageParams,
} from "./ImageViewer";
import {
	DEFAULT_OVERLAY_OPACITY,
	normalizeOverlayOpacity,
	OVERLAY_OPACITY_KEY,
	readOverlayOpacity,
	readPreference,
	VIEW_MODES,
	writeOverlayOpacity,
	writePreference,
} from "./prefs";
import type { PreferenceStorage } from "./prefs";

// The same stand-in the other preference tests use: vitest runs in the node
// environment, so there is no real localStorage, and the helpers take the
// storage as an argument precisely so this stays a unit test.
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

// The precedence rule the inline viewer script had, which is easy to get wrong
// in the obvious way: normalizeViewMode(stored) alone would turn "no preference
// stored" into side-by-side and silently discard the mode the server asked for.
describe("initialViewMode", () => {
	it("prefers a stored mode over the server default", () => {
		expect(initialViewMode("overlay", "difference")).toBe("overlay");
	});

	it("falls back to the server default when nothing is stored", () => {
		expect(initialViewMode(null, "difference")).toBe("difference");
	});

	it("falls back to the server default when the stored value is not a mode", () => {
		expect(initialViewMode("sideways", "reference")).toBe("reference");
	});

	it("falls back to side-by-side when neither is usable", () => {
		expect(initialViewMode(null, null)).toBe("side-by-side");
		expect(initialViewMode("", "nonsense")).toBe("side-by-side");
	});

	it("accepts every mode the shared list names", () => {
		for (const mode of VIEW_MODES) expect(initialViewMode(mode, null)).toBe(mode);
	});
});

describe("initialColormap", () => {
	it("prefers a stored colormap", () => {
		expect(initialColormap("magma", "turbo")).toBe("magma");
	});

	it("falls back to turbo", () => {
		expect(initialColormap(null, null)).toBe("turbo");
		expect(initialColormap("viridis", null)).toBe("turbo");
	});
});

// withImageParams mirrors ui.imageViewerSrc, including its rule for a base path
// that already carries a query. These cases are the Go test's cases.
describe("withImageParams", () => {
	it("leaves a bare path alone", () => {
		expect(withImageParams("/api/v1/jobs/abc/best.png")).toBe("/api/v1/jobs/abc/best.png");
	});

	it("appends a revision", () => {
		expect(withImageParams("/api/v1/jobs/abc/best.png", { revision: 7 }))
			.toBe("/api/v1/jobs/abc/best.png?v=7");
	});

	it("keeps an existing query and joins with an ampersand", () => {
		expect(withImageParams("/api/v1/jobs/abc/diff.png?colormap=turbo", { revision: 9 }))
			.toBe("/api/v1/jobs/abc/diff.png?colormap=turbo&v=9");
	});

	it("orders the colormap before the revision, as the server does", () => {
		expect(withImageParams("/api/v1/jobs/abc/diff.png", { colormap: "magma", revision: 3 }))
			.toBe("/api/v1/jobs/abc/diff.png?colormap=magma&v=3");
	});

	it("omits a revision that cannot bust a cache", () => {
		expect(withImageParams("/api/v1/jobs/abc/best.png", { revision: 0 }))
			.toBe("/api/v1/jobs/abc/best.png");
		expect(withImageParams("/api/v1/jobs/abc/best.png", { revision: Number.NaN }))
			.toBe("/api/v1/jobs/abc/best.png");
	});
});

describe("shortcutMode", () => {
	it("maps 1-5 to the five modes in radio order", () => {
		expect(["1", "2", "3", "4", "5"].map(shortcutMode)).toEqual([...VIEW_MODES]);
	});

	it("ignores every other key", () => {
		for (const key of ["0", "6", "9", "a", "Enter", " ", ""]) {
			expect(shortcutMode(key)).toBeNull();
		}
	});
});

// A pending job has never rendered anything, which is a different fact from a
// running job that has not produced a result yet -- and the difference panel
// words it differently again.
describe("missingImageMessage", () => {
	it("explains a pending job", () => {
		expect(missingImageMessage("pending", "best")).toBe("Optimization not started yet");
		expect(missingImageMessage("pending", "difference")).toBe("Not available yet");
	});

	it("says there is nothing yet for every other state", () => {
		for (const state of ["running", "completed", "failed", undefined]) {
			expect(missingImageMessage(state, "best")).toBe("No results yet");
			expect(missingImageMessage(state, "difference")).toBe("No results yet");
		}
	});
});

describe("heatmapGradient", () => {
	it("gives each colormap its own stops", () => {
		expect(heatmapGradient("turbo")).not.toBe(heatmapGradient("magma"));
		expect(heatmapGradient("turbo")).toContain("#23171b");
		expect(heatmapGradient("magma")).toContain("#fcfdbf");
	});
});

// The overlay blend is stored under a key of its own, outside the four the
// settings editor owns. The key name and the decimal-integer shape are a
// compatibility contract with the inline script that wrote them first.
describe("the overlay opacity preference", () => {
	it("pins the key", () => {
		expect(OVERLAY_OPACITY_KEY).toBe("circlefit.overlayOpacity");
	});

	it("clamps to the slider's range", () => {
		expect(normalizeOverlayOpacity("-20")).toBe(0);
		expect(normalizeOverlayOpacity("140")).toBe(100);
		expect(normalizeOverlayOpacity("35")).toBe(35);
	});

	it("parses like the script it replaces, truncating rather than rounding", () => {
		expect(normalizeOverlayOpacity("50.7")).toBe(50);
	});

	it("falls back to half for anything unreadable", () => {
		expect(normalizeOverlayOpacity(null)).toBe(DEFAULT_OVERLAY_OPACITY);
		expect(normalizeOverlayOpacity("")).toBe(DEFAULT_OVERLAY_OPACITY);
		expect(normalizeOverlayOpacity("opaque")).toBe(DEFAULT_OVERLAY_OPACITY);
		expect(readOverlayOpacity(null)).toBe(DEFAULT_OVERLAY_OPACITY);
		expect(readOverlayOpacity(throwingStorage)).toBe(DEFAULT_OVERLAY_OPACITY);
	});

	it("round-trips through storage as a decimal integer", () => {
		const storage = new FakeStorage();
		expect(writeOverlayOpacity(72, storage)).toBe(true);
		expect(storage.getItem(OVERLAY_OPACITY_KEY)).toBe("72");
		expect(readOverlayOpacity(storage)).toBe(72);
	});

	it("reports a storage that refuses rather than throwing", () => {
		expect(writeOverlayOpacity(10, throwingStorage)).toBe(false);
		expect(writeOverlayOpacity(10, null)).toBe(false);
	});
});

// writePreference exists because the viewer changes one preference at a time.
// writePreferences would materialize defaults for keys the reader never
// touched, and an absent key and a key holding the default are deliberately
// indistinguishable to every reader.
describe("writePreference", () => {
	it("leaves the other entries alone", () => {
		const storage = new FakeStorage({ "circlefit.viewMode": "overlay" });
		expect(writePreference("circlefit.diffColormap", "magma", storage)).toBe(true);
		expect(Object.fromEntries(storage.entries)).toEqual({
			"circlefit.viewMode": "overlay",
			"circlefit.diffColormap": "magma",
		});
	});

	it("reports a storage that refuses", () => {
		expect(writePreference("k", "v", throwingStorage)).toBe(false);
		expect(writePreference("k", "v", null)).toBe(false);
	});

	it("reads back nothing rather than throwing", () => {
		expect(readPreference(throwingStorage, "k")).toBeNull();
		expect(readPreference(null, "k")).toBeNull();
	});
});
