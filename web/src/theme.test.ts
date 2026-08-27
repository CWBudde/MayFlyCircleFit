import { describe, expect, it } from "vitest";
import {
	normalizeThemeChoice,
	readThemeChoice,
	storeThemeChoice,
	THEME_STORAGE_KEY,
} from "./theme";
import type { PreferenceStorage } from "./prefs";

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

// The pre-paint script in internal/ui/layout.templ reads this exact key before
// the first paint, and it is not built from this module. A rename here would
// leave the toggle writing somewhere the first paint never looks, which shows
// up as a theme that flashes back on every navigation.
it("pins the key the pre-paint script reads", () => {
	expect(THEME_STORAGE_KEY).toBe("mayflycirclefit.theme");
});

describe("normalizeThemeChoice", () => {
	it.each(["light", "dark"])("keeps %s", (raw) => {
		expect(normalizeThemeChoice(raw)).toBe(raw);
	});

	it.each<string | null | undefined>([null, undefined, "", "auto", "Dark", "system"])(
		"reads %o as auto",
		(raw) => {
			expect(normalizeThemeChoice(raw)).toBe("auto");
		},
	);
});

describe("readThemeChoice", () => {
	it("returns the stored choice", () => {
		expect(readThemeChoice(new FakeStorage({ "mayflycirclefit.theme": "dark" }))).toBe("dark");
	});

	it("returns auto for an empty storage", () => {
		expect(readThemeChoice(new FakeStorage())).toBe("auto");
	});

	it("returns auto when there is no storage or reading throws", () => {
		expect(readThemeChoice(null)).toBe("auto");
		expect(readThemeChoice(throwingStorage)).toBe("auto");
	});
});

describe("storeThemeChoice", () => {
	it("writes an explicit choice verbatim", () => {
		const storage = new FakeStorage();
		storeThemeChoice("light", storage);
		expect(storage.getItem(THEME_STORAGE_KEY)).toBe("light");
	});

	// auto is the absence of the key, not the word: the e2e spec asserts
	// localStorage.getItem returns null after choosing the system theme.
	it("removes the key for auto", () => {
		const storage = new FakeStorage({ "mayflycirclefit.theme": "dark" });
		storeThemeChoice("auto", storage);
		expect(storage.getItem(THEME_STORAGE_KEY)).toBeNull();
	});

	it("does not throw when storage refuses or is absent", () => {
		expect(() => storeThemeChoice("dark", throwingStorage)).not.toThrow();
		expect(() => storeThemeChoice("auto", throwingStorage)).not.toThrow();
		expect(() => storeThemeChoice("dark", null)).not.toThrow();
	});
});
