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
// Both triggers are needed: the theme switcher stamps data-theme on the root
// element, and the "auto" setting has no attribute at all, so only the media
// query reports a system theme change.
export function useChartTheme(): Palette {
	const [palette, setPalette] = useState<Palette>(readThemePalette);

	useEffect(() => {
		const refresh = () => setPalette(readThemePalette());
		refresh();

		const themeObserver = new MutationObserver(refresh);
		themeObserver.observe(document.documentElement, {
			attributes: true,
			attributeFilter: ["data-theme"],
		});

		const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)");
		mediaQuery.addEventListener("change", refresh);

		return () => {
			themeObserver.disconnect();
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
export function applyAxisTheme(
	chart: ChartInstance,
	palette: Palette,
	extra?: { xTicks?: Record<string, unknown> },
): void {
	for (const [name, scale] of Object.entries(chart.options.scales ?? {})) {
		if (!scale) {
			continue;
		}
		scale.ticks = { ...scale.ticks, color: palette.textMuted, ...(name === "x" ? extra?.xTicks : {}) };
		scale.grid = { ...scale.grid, color: palette.grid };
		if (scale.title) {
			scale.title = { ...scale.title, color: palette.textMuted };
		}
	}
}
