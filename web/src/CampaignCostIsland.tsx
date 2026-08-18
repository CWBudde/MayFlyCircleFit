import { useState } from "react";
import { CampaignCostChart } from "./CampaignCostChart";
import type { CampaignCostChartPoint } from "./CampaignCostChart";
import { useChartTheme } from "./charts";

// readSeriesSeed parses the stage series the campaign page rendered into the
// island root. It has to run before React's first commit, because that commit
// clears the container the script tag lives in.
function readSeriesSeed(root: HTMLElement): CampaignCostChartPoint[] {
	const seed = root.querySelector<HTMLScriptElement>("#campaign-cost-series");
	if (!seed) {
		return [];
	}
	try {
		const parsed: unknown = JSON.parse(seed.textContent || "null");
		return Array.isArray(parsed) ? (parsed as CampaignCostChartPoint[]) : [];
	} catch {
		return [];
	}
}

// CampaignCostIsland swaps the campaign page's server-rendered SVG plot for the
// interactive chart. The SVG stays the pre-hydration content: it is what the
// page shows with JavaScript disabled, and what a reader sees before the bundle
// has parsed.
//
// The series is read once. A campaign page is a static view of a stage table —
// nothing on it streams — so unlike the dashboard island there is nothing here
// to refetch.
export function CampaignCostIsland({ root }: { root: HTMLElement }) {
	const palette = useChartTheme();
	// Lazily initialised, not read per render: React's first commit clears the
	// container, taking the seed script with it.
	const [points] = useState<CampaignCostChartPoint[]>(() => readSeriesSeed(root));

	return <CampaignCostChart points={points} palette={palette} variant="full" />;
}
