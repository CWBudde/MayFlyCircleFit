import {
	Chart,
	Filler,
	LinearScale,
	LineController,
	LineElement,
	PointElement,
	Tooltip,
} from "chart.js";
import type { Chart as ChartInstance } from "chart.js";
import { useEffect, useRef, useState } from "react";
import type { RefObject } from "react";

// Register only what the islands' charts draw. Chart.js ships every
// controller, scale, and plugin, and the tree shaking is driven by these
// registrations, so importing the `auto` bundle instead would cost the binary
// a chart library it does not use. No category scale: every chart here has a
// numeric x axis, and a category scale would space its points evenly
// regardless of their value.
Chart.register(LinearScale, LineController, LineElement, PointElement, Tooltip, Filler);

// Palette is the set of theme tokens the charts paint with. Chart.js resolves
// no CSS variables of its own, so every one it needs is read out here first.
export type Palette = {
	primary: string;
	success: string;
	warning: string;
	text: string;
	textMuted: string;
	border: string;
	grid: string;
	background: string;
};

function readThemePalette(): Palette {
	const style = getComputedStyle(document.documentElement);
	return {
		primary: style.getPropertyValue("--primary-color").trim(),
		success: style.getPropertyValue("--success-color").trim(),
		warning: style.getPropertyValue("--warning-color").trim(),
		text: style.getPropertyValue("--text-color").trim(),
		textMuted: style.getPropertyValue("--text-muted").trim(),
		border: style.getPropertyValue("--border-color").trim(),
		grid: style.getPropertyValue("--grid-color").trim(),
		background: style.getPropertyValue("--control-bg").trim(),
	};
}

// useChartTheme resolves the page's palette tokens and re-resolves them when
// the theme changes. Chart.js bakes colors into its own state at draw time, so
// a CSS variable alone would leave a mounted chart painted for the old theme;
// the components that consume this palette call chart.update() when it changes.
//
// Both triggers are needed, and the first one watches <head> rather than the
// root element. The theme controller (the pre-paint script in
// internal/ui/layout.templ) deliberately never sets an attribute on <html> --
// WebKit then fails to inherit the custom properties into elements already
// parsed -- so an explicit choice appears as the #theme-override stylesheet
// being appended, having its text replaced, or being removed. A
// MutationObserver on documentElement filtered to data-theme, which is what
// this used to be, saw none of those and never fired. The "auto" setting adds
// no stylesheet at all, so only the media query reports a system theme change.
export function useChartTheme(): Palette {
	const [palette, setPalette] = useState<Palette>(readThemePalette);

	useEffect(() => {
		// The observer below watches all of <head>, so it can fire for reasons
		// that are not a theme change. readThemePalette allocates a fresh
		// object every call, and handing React a new object it would compare by
		// identity means a re-render, a new `palette` dependency, and a
		// chart.update() for every one of them. Only a changed token counts.
		const refresh = () =>
			setPalette((current) => {
				const next = readThemePalette();
				const changed = (Object.keys(next) as Array<keyof Palette>).some((key) => next[key] !== current[key]);
				return changed ? next : current;
			});
		refresh();

		// subtree and characterData are both load bearing: apply() reuses the
		// existing <style> and assigns textContent, which replaces its child
		// text node rather than touching <head> itself.
		const overrideObserver = new MutationObserver(refresh);
		overrideObserver.observe(document.head, {
			childList: true,
			subtree: true,
			characterData: true,
		});

		const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)");
		mediaQuery.addEventListener("change", refresh);

		return () => {
			overrideObserver.disconnect();
			mediaQuery.removeEventListener("change", refresh);
		};
	}, []);

	return palette;
}

// useLineChart owns one Chart.js instance for the lifetime of the component.
//
// The chart is built once and updated in place afterwards. Rebuilding it
// whenever its data changed would mean a canvas reallocation per stream frame,
// which is exactly the cost the imperative chart.update() path exists to avoid.
export function useLineChart(
	canvasRef: RefObject<HTMLCanvasElement | null>,
	build: () => ChartInstance,
	apply: (chart: ChartInstance) => void,
	deps: unknown[],
): void {
	const chartRef = useRef<ChartInstance | null>(null);
	const buildRef = useRef(build);
	const applyRef = useRef(apply);
	buildRef.current = build;
	applyRef.current = apply;

	useEffect(() => {
		const context = canvasRef.current?.getContext("2d");
		if (!context) {
			return;
		}
		const chart = buildRef.current();
		chartRef.current = chart;
		applyRef.current(chart);
		chart.update("none");

		return () => {
			chart.destroy();
			chartRef.current = null;
		};
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, []);

	useEffect(() => {
		const chart = chartRef.current;
		if (!chart) {
			return;
		}
		applyRef.current(chart);
		chart.update("none");
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, deps);
}

// applyAxisTheme repaints every configured scale in the current palette.
//
// It assigns leaf properties and never replaces `ticks`, `grid` or `title` with
// a spread of its current value. The two read the same on the page and are not
// the same: from the first update onward chart.options is a chain of resolved
// Chart.js proxies, so `{ ...scale.ticks }` copies nested option proxies
// (ticks.minor, ticks.major) into the raw config. Chart.js re-resolves those
// copies on the next pass, finds its own descriptor key `_scriptable` on one of
// them, and calls that function as though it were a scriptable option value --
// "name.startsWith is not a function". Thrown from a render, it takes the whole
// island down and leaves the page blank.
export function applyAxisTheme(
	chart: ChartInstance,
	palette: Palette,
	extra?: { xTicks?: Record<string, unknown> },
): void {
	for (const [name, scale] of Object.entries(chart.options.scales ?? {})) {
		if (!scale) {
			continue;
		}
		const themed = scale as typeof scale & {
			ticks?: Record<string, unknown>;
			grid?: Record<string, unknown>;
			title?: Record<string, unknown>;
		};
		if (themed.ticks) {
			themed.ticks.color = palette.textMuted;
			if (name === "x" && extra?.xTicks) {
				Object.assign(themed.ticks, extra.xTicks);
			}
		}
		if (themed.grid) {
			themed.grid.color = palette.grid;
		}
		if (themed.title) {
			themed.title.color = palette.textMuted;
		}
	}
}
