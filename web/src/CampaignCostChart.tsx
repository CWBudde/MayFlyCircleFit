import { Chart } from "chart.js";
import type { TooltipItem } from "chart.js";
import { useId, useMemo, useRef } from "react";
import { applyAxisTheme, useLineChart } from "./charts";
import type { Palette } from "./charts";
import { campaignCostPointColor, formatChartCost } from "./format";

// CampaignCostChartPoint mirrors ui.CampaignSeriesPoint. It is the same shape
// the dashboard endpoint serves and the campaign page seeds, so one component
// draws both.
export type CampaignCostChartPoint = {
	index: number;
	kind: string;
	circles: number;
	bestCost: number;
	hasBestCost: boolean;
};

// A mini chart sits in a dashboard campaign card; a full one replaces the
// server-rendered SVG on the campaign page. The server plot is fixed at
// 960x340 by package constants, which is precisely why the small variant is a
// variant here rather than a second set of constants over there.
export type CampaignCostChartVariant = "mini" | "full";

type CampaignCostChartProps = {
	points: CampaignCostChartPoint[];
	palette: Palette;
	variant?: CampaignCostChartVariant;
};

const chartHeight: Record<CampaignCostChartVariant, number> = { mini: 130, full: 340 };

// The server plot draws campaignPlotTickNum + 1 labelled x ticks. A mini chart
// has a third of the width for the same labels, so it settles for fewer.
const xTickLimit: Record<CampaignCostChartVariant, number> = { mini: 4, full: 6 };

// The SVG draws polish stages as squares because a polish repeats its extend
// stage's circle count, which would otherwise hide one point behind another.
function campaignCostPointStyle(kind: string): "circle" | "rect" {
	return kind === "polish" ? "rect" : "circle";
}

// plottableStages is the rule buildCampaignPlot applies: a stage without a
// recorded cost is skipped, never plotted at zero. A running stage has no
// result yet, and drawing it as a perfect fit would invert the meaning of the
// chart.
function plottableStages(points: CampaignCostChartPoint[]): CampaignCostChartPoint[] {
	return points
		.filter((point) => point.hasBestCost && Number.isFinite(point.bestCost))
		.slice()
		.sort((left, right) => left.index - right.index);
}

// chartLabel repeats the aria-label the server SVG carries, so replacing the
// SVG with a canvas does not take the plot's name away from a screen reader.
const chartLabel = "Best cost against circle count across the campaign";


// CampaignCostChart is the React port of CampaignCostPlot: best cost against
// circle count, one point per stage that recorded one.
export function CampaignCostChart({ points, palette, variant = "mini" }: CampaignCostChartProps) {
	const canvasRef = useRef<HTMLCanvasElement | null>(null);
	const descriptionId = useId();
	const plotted = useMemo(() => plottableStages(points), [points]);
	// The tooltip callback runs outside React's render, so it reads the points
	// through a ref rather than closing over the render they were built in.
	const plottedRef = useRef(plotted);
	plottedRef.current = plotted;

	useLineChart(
		canvasRef,
		() =>
			new Chart(canvasRef.current!.getContext("2d")!, {
				type: "line",
				data: { datasets: [{ label: "best cost", data: [] }] },
				options: {
					responsive: true,
					maintainAspectRatio: false,
					animation: false,
					scales: {
						// Linear, not categorical: circle counts are what the axis
						// means, and stages neither advance by a fixed width nor
						// always advance at all — a polish stage repeats the count
						// its extend stage reached. Even spacing would draw a
						// different curve than the server SVG, whose scaleX
						// interpolates between the smallest and largest count.
						x: { type: "linear", title: { display: true, text: "Circles" } },
						y: {
							title: { display: true, text: "Best cost" },
							ticks: { callback: (value) => formatChartCost(Number(value)) },
						},
					},
					plugins: {
						legend: { display: false },
						tooltip: {
							displayColors: false,
							callbacks: {
								// The same sentence the SVG puts in each point's
								// <title>: stage %d (%s): %d circles, cost %.3f.
								label(item: TooltipItem<"line">) {
									const point = plottedRef.current[item.dataIndex];
									if (!point) {
										return "";
									}
									return `stage ${point.index} (${point.kind}): ${point.circles} circles, cost ${point.bestCost.toFixed(3)}`;
								},
							},
						},
					},
				},
			}),
		(chart) => {
			const current = plottedRef.current;
			chart.data.datasets[0].data = current.map((point) => ({ x: point.circles, y: point.bestCost }));
			Object.assign(chart.data.datasets[0], {
				borderColor: palette.primary,
				backgroundColor: `${palette.primary}22`,
				pointStyle: current.map((point) => campaignCostPointStyle(point.kind)),
				pointBackgroundColor: current.map((point) => campaignCostPointColor(point.kind, palette)),
				pointBorderColor: current.map((point) => campaignCostPointColor(point.kind, palette)),
				pointRadius: 3,
				borderWidth: 2,
				tension: 0,
				fill: false,
			});
			applyAxisTheme(chart, palette, { xTicks: { maxTicksLimit: xTickLimit[variant] } });
		},
		[plotted, palette, variant],
	);

	if (plotted.length === 0) {
		return <p style={{ color: "var(--text-muted)" }}>No stage has recorded a cost yet.</p>;
	}

	const compact = variant === "mini";
	return (
		<div>
			{/* The full variant repeats the SVG's framed box so the swap does not
			    redraw the card around the plot it replaces. */}
			<div
				style={{
					height: `${chartHeight[variant]}px`,
					...(compact
						? {}
						: {
								backgroundColor: "var(--control-bg)",
								border: "1px solid var(--border-color)",
								borderRadius: "0.25rem",
								padding: "0.5rem",
								boxSizing: "border-box",
							}),
				}}
			>
				<canvas ref={canvasRef} role="img" aria-label={chartLabel} aria-describedby={descriptionId} style={{ maxWidth: "100%" }} />
			</div>
			<ul id={descriptionId} className="sr-only">
				{plotted.map((point) => (
					<li key={point.index}>
						{`stage ${point.index} (${point.kind}): ${point.circles} circles, cost ${point.bestCost.toFixed(3)}`}
					</li>
				))}
			</ul>
			<CampaignCostLegend palette={palette} compact={compact} />
		</div>
	);
}

// CampaignCostLegend repeats the SVG's base/extend/polish key. Chart.js draws
// one legend entry per dataset and this chart has a single dataset whose
// points carry the meaning, so the key is markup rather than a chart plugin.
function CampaignCostLegend({ palette, compact }: { palette: Palette; compact: boolean }) {
	return (
		<div
			style={{
				display: "flex",
				gap: compact ? "1rem" : "1.5rem",
				marginTop: compact ? "0.25rem" : "0.5rem",
				fontSize: compact ? "0.75rem" : "0.8125rem",
				color: "var(--text-muted)",
			}}
		>
			<span>
				<svg width="10" height="10" style={{ verticalAlign: "middle" }}>
					<circle cx="5" cy="5" r="4" fill={palette.success} />
				</svg>{" "}
				base
			</span>
			<span>
				<svg width="10" height="10" style={{ verticalAlign: "middle" }}>
					<circle cx="5" cy="5" r="4" fill={palette.primary} />
				</svg>{" "}
				extend
			</span>
			<span>
				<svg width="10" height="10" style={{ verticalAlign: "middle" }}>
					<rect x="1" y="1" width="8" height="8" fill={palette.warning} />
				</svg>{" "}
				polish
			</span>
		</div>
	);
}
