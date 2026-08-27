import type { ReactElement } from "react";
import { useState } from "react";
import { browserStorage } from "./prefs";
import { readThemeChoice, storeThemeChoice, themeController } from "./theme";
import type { ThemeChoice } from "./theme";

// The theme switch is page chrome: the layout renders it on every page, and
// this island takes it over so the chrome itself stops carrying a script.
//
// It does not own the palette. The pre-paint script in <head> applies the
// stored theme before the first paint and publishes window.mayflyTheme; this
// only wires the buttons to it, so a click swaps one stylesheet rather than
// mutating <html>. Everything below the mount point is server-rendered first,
// so a page without the bundle still shows the three buttons -- inert, exactly
// as they were before this island existed.

const OPTIONS: Array<{ value: ThemeChoice; label: string; title: string; icon: ReactElement }> = [
	{
		value: "auto",
		label: "Use system theme",
		title: "Auto theme",
		icon: (
			<svg aria-hidden="true" width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
				<rect x="3" y="4" width="18" height="13" rx="2"></rect>
				<path d="M8 21h8M12 17v4"></path>
			</svg>
		),
	},
	{
		value: "light",
		label: "Use light theme",
		title: "Light theme",
		icon: (
			<svg aria-hidden="true" width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
				<circle cx="12" cy="12" r="4"></circle>
				<path d="M12 2v2M12 20v2M4.93 4.93l1.42 1.42M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.42-1.42M17.66 6.34l1.41-1.41"></path>
			</svg>
		),
	},
	{
		value: "dark",
		label: "Use dark theme",
		title: "Dark theme",
		icon: (
			<svg aria-hidden="true" width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
				<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"></path>
			</svg>
		),
	},
];

// The mount point keeps the group role and its label, so only the buttons are
// rendered here.
export function ThemeToggleIsland() {
	const [choice, setChoice] = useState<ThemeChoice>(() => {
		const controller = themeController();
		// The controller is the authority while it exists: it has already
		// decided what the first paint used.
		return controller ? controller.selected() : readThemeChoice(browserStorage());
	});

	const select = (next: ThemeChoice) => {
		themeController()?.apply(next);
		storeThemeChoice(next, browserStorage());
		setChoice(next);
	};

	return (
		<>
			{OPTIONS.map((option) => (
				<button
					key={option.value}
					className="theme-option"
					type="button"
					data-theme-value={option.value}
					aria-label={option.label}
					aria-pressed={option.value === choice}
					title={option.title}
					onClick={() => select(option.value)}
				>
					{option.icon}
				</button>
			))}
		</>
	);
}
