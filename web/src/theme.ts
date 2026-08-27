// The stored half of the color theme.
//
// The palette itself is owned by the pre-paint script in internal/ui/layout.templ,
// which has to run before the first paint and therefore stays inline, ahead of
// this bundle. It publishes window.circlefitTheme, and everything here is either
// storage (which the toggle needs and the pre-paint script only reads) or a
// typed view of that controller.

import type { PreferenceStorage } from "./prefs";

/** The key the pre-paint script reads before the first paint. */
export const THEME_STORAGE_KEY = "circlefit.theme";

// "auto" is the absence of the key rather than a stored word: the pre-paint
// script treats anything that is not "light" or "dark" as auto, so writing
// "auto" would work by accident and then differ from what the reset path does.
export type ThemeChoice = "auto" | "light" | "dark";

export const THEME_CHOICES: readonly ThemeChoice[] = ["auto", "light", "dark"];

// ThemeController is what the pre-paint script publishes. apply swaps the one
// stylesheet that carries the override; it never mutates <html>, because WebKit
// then fails to inherit the custom properties into elements already parsed.
export interface ThemeController {
	apply(theme: ThemeChoice): void;
	selected(): ThemeChoice;
	storageKey: string;
}

declare global {
	interface Window {
		circlefitTheme?: ThemeController;
	}
}

export function themeController(): ThemeController | null {
	if (typeof window === "undefined") return null;
	return window.circlefitTheme ?? null;
}

export function normalizeThemeChoice(value: string | null | undefined): ThemeChoice {
	return value === "light" || value === "dark" ? value : "auto";
}

export function readThemeChoice(storage: PreferenceStorage | null): ThemeChoice {
	if (!storage) return "auto";
	try {
		return normalizeThemeChoice(storage.getItem(THEME_STORAGE_KEY));
	} catch {
		return "auto";
	}
}

// storeThemeChoice persists a choice, removing the key for "auto" so a reader
// cannot tell "follow the system" from "never chose". Failure is silent on
// purpose: the choice still applies to the page in front of the reader, it just
// will not survive the next load.
export function storeThemeChoice(next: ThemeChoice, storage: PreferenceStorage | null): void {
	if (!storage) return;

	try {
		if (next === "auto") {
			storage.removeItem(THEME_STORAGE_KEY);
			return;
		}
		storage.setItem(THEME_STORAGE_KEY, next);
	} catch {
		// Storage can be unavailable in privacy-restricted contexts.
	}
}
