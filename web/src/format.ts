import type { Palette } from "./charts";

// This module holds the pure formatters the islands share. They live here
// rather than inside an island module because every one of them mirrors a Go
// helper in internal/ui, and a mirror is only worth anything if a test can
// import it — the island modules run mountIslands() at load, so importing one
// would mount React instead of returning a function.
//
// The parameter types are deliberately structural and minimal: an island's own
// payload type satisfies them without this module having to import it back,
// which would close an import cycle.

// stateClass mirrors ui.StateBadge in internal/ui/list.templ. The island
// replaces server-rendered rows, so a different mapping here would recolor
// every badge on mount.
export function stateClass(state: string): string {
	switch (state) {
		case "pending":
		case "running":
			return "badge-info";
		case "completed":
			return "badge-success";
		case "failed":
			return "badge-error";
		case "paused":
		case "cancelled":
			return "badge-warning";
		default:
			return "";
	}
}

export function stateLabel(state: string): string {
	if (!state) {
		return "unknown";
	}
	return state.charAt(0).toUpperCase() + state.slice(1);
}

// formatCostGain mirrors formatJobImprovement on the Go side.
export function formatCostGain(initial: number, best: number): string {
	if (!Number.isFinite(initial) || !Number.isFinite(best) || initial <= 0 || best > initial) {
		return "—";
	}
	return `↓ ${((1 - best / initial) * 100).toFixed(1)}%`;
}

// formatJobCircles mirrors formatJobCircles on the Go side: a job that has not
// reached its requested count yet prints both, so the row says how far the
// geometry still has to grow.
export function formatJobCircles(actual: number, requested: number): string {
	if (!Number.isFinite(actual)) return "—";
	if (Number.isFinite(requested) && requested > 0 && requested !== actual) {
		return `${actual} / ${requested}`;
	}
	return `${actual}`;
}

// campaignStageCount mirrors campaignStageCount in schedule.templ.
export function campaignStageCount(campaign: { recordedStages: number; plannedStages: number }): string {
	if (campaign.plannedStages > 0) {
		return `${campaign.recordedStages} / ${campaign.plannedStages}`;
	}
	return `${campaign.recordedStages}`;
}

// campaignURL mirrors dashboardCampaignURL in dashboard.templ, whose default
// arm is the schedule route.
export function campaignURL(campaign: { id: string; source: string }): string {
	return campaign.source === "chain" ? `/chains/${campaign.id}` : `/schedules/${campaign.id}`;
}

// shortID mirrors shortID in schedule.templ.
export function shortID(id: string): string {
	return id.length <= 8 ? id : id.slice(0, 8);
}

// The stage formatters below mirror the templ fallback so mounting the island
// keeps the server-rendered projection instead of shrinking it.
export function formatCost(stage: { hasBestCost: boolean; bestCost: number }): string {
	return stage.hasBestCost ? stage.bestCost.toFixed(3) : "—";
}

export function formatPsnr(stage: { psnrInfinite: boolean; hasPsnr: boolean; psnr: number }): string {
	if (stage.psnrInfinite) return "∞ dB";
	return stage.hasPsnr ? `${stage.psnr.toFixed(2)} dB` : "—";
}

// formatDurationSeconds prints the Go duration form time.Duration.String()
// produces after rounding to whole seconds: the largest non-zero unit leads,
// and every unit below it is printed even when zero.
export function formatDurationSeconds(seconds: number): string {
	const total = Math.round(seconds);
	const hours = Math.floor(total / 3600);
	const minutes = Math.floor((total % 3600) / 60);
	const rest = total % 60;
	if (hours > 0) return `${hours}h${minutes}m${rest}s`;
	if (minutes > 0) return `${minutes}m${rest}s`;
	return `${rest}s`;
}

// formatElapsed mirrors formatCampaignElapsed.
export function formatElapsed(stage: { hasElapsed: boolean; elapsedSec: number }): string {
	return stage.hasElapsed ? formatDurationSeconds(stage.elapsedSec) : "—";
}

// The projection formatters below mirror the ones in schedule.templ, so the
// card the island renders reads exactly like the server-rendered fallback it
// replaces. Their parameter types stay structural for the reason this whole
// module's are: the island's own Campaign type satisfies them without an
// import back the other way.
type ProjectionShape = {
	projected: boolean;
	samples: number;
	recentLegs: number;
	recentCircles: number;
	recentElapsedSec: number;
	recentGainPerCircle: number;
	recentGainPerHour: number;
	latestCircles: number;
	latestCost: number;
	remainingCircles: number;
	costAtPlanEnd: number;
	planEndPsnr: number;
	planEndPsnrInfinite: boolean;
	hasPlanEndPsnr: boolean;
	hasCircleCeiling: boolean;
	remainingElapsedSec: number;
	costAtFinish: number;
	finishPsnr: number;
	finishPsnrInfinite: boolean;
	hasFinishPsnr: boolean;
	hasTimeBudget: boolean;
};

// campaignWarningHeading mirrors the Go helper of the same name.
export function campaignWarningHeading(warnings: unknown[]): string {
	return warnings.length === 1 ? "Advisory:" : "Advisories:";
}

export function campaignProjectionBasis(projection: ProjectionShape): string {
	if (!projection.projected) return "Not enough completed stages to project a cost yet.";
	const stages = projection.samples === 1 ? "1 measured stage" : `${projection.samples} measured stages`;
	const legs = projection.recentLegs === 1 ? "leg" : `${projection.recentLegs} legs`;
	return `From ${stages} alone, at ${projection.latestCircles} circles and cost ${projection.latestCost.toFixed(3)}. ` +
		`The projections below extrapolate the trailing ${legs}, not the whole campaign.`;
}

export function campaignPerCircleRate(projection: ProjectionShape): string {
	if (!projection.projected || projection.recentGainPerCircle === 0) return "—";
	return `${projection.recentGainPerCircle.toFixed(6)} cost/circle over the last ${projection.recentCircles} circles`;
}

export function campaignPerHourRate(projection: ProjectionShape): string {
	if (!projection.projected || projection.recentGainPerHour === 0) return "—";
	return `${projection.recentGainPerHour.toFixed(2)} cost/hour over the last ${formatDurationSeconds(projection.recentElapsedSec)}`;
}

export function campaignRemainingCircles(projection: ProjectionShape): string {
	if (projection.remainingCircles <= 0) return "—";
	return `${projection.remainingCircles} circles to ${projection.latestCircles + projection.remainingCircles}`;
}

export function campaignRemainingElapsed(projection: ProjectionShape): string {
	if (projection.remainingElapsedSec <= 0) return "—";
	return formatDurationSeconds(projection.remainingElapsedSec);
}

export function campaignProjectedPlanEnd(projection: ProjectionShape): string {
	return formatProjectedCost(projection.hasCircleCeiling, projection.costAtPlanEnd,
		projection.hasPlanEndPsnr, projection.planEndPsnrInfinite, projection.planEndPsnr);
}

export function campaignProjectedFinish(projection: ProjectionShape): string {
	return formatProjectedCost(projection.hasTimeBudget, projection.costAtFinish,
		projection.hasFinishPsnr, projection.finishPsnrInfinite, projection.finishPsnr);
}

// formatProjectedCost mirrors the Go helper: the PSNR restates the cost beside
// it, so it is parenthesised rather than given a figure of its own.
export function formatProjectedCost(
	present: boolean, cost: number, hasPsnr: boolean, infinite: boolean, psnr: number,
): string {
	if (!present) return "—";
	if (infinite) return `${cost.toFixed(3)} (PSNR ∞ dB)`;
	return hasPsnr ? `${cost.toFixed(3)} (PSNR ${psnr.toFixed(2)} dB)` : cost.toFixed(3);
}

export function formatAcceptedSweeps(stage: { acceptedSweeps?: number | null }): string {
	return stage.acceptedSweeps === undefined || stage.acceptedSweeps === null ? "—" : `${stage.acceptedSweeps}`;
}

export function acceptedSweepsTitle(stage: { acceptedSweeps?: number | null; kind: string }): string {
	if (stage.acceptedSweeps !== undefined && stage.acceptedSweeps !== null) return "";
	return stage.kind !== "polish"
		? "Only a polish stage runs sweeps"
		: "The polisher does not persist its accepted-sweep count";
}

export function campaignTitle(campaign: { name: string; id: string; source: string }): string {
	if (campaign.name) return campaign.name;
	const short = shortID(campaign.id);
	return campaign.source === "chain" ? `Imported chain ${short}` : `Campaign ${short}`;
}

export function campaignProvenance(campaign: {
	source: string;
	stages: unknown[];
	plannedStages: number;
	hasSeed: boolean;
	campaignSeed: number;
}): string {
	const recorded = campaign.stages.length;
	if (campaign.source === "chain") return `Reconstructed from checkpoint lineage · ${recorded} stages`;
	const text = `Schedule · ${recorded} of ${campaign.plannedStages} stages recorded`;
	return campaign.hasSeed ? `${text} · seed ${campaign.campaignSeed}` : text;
}

// formatChartCost mirrors formatPlotCost in schedule.templ.
export function formatChartCost(cost: number): string {
	if (Math.abs(cost) >= 1000) {
		return cost.toFixed(0);
	}
	return cost.toFixed(2);
}

// campaignCostPointColor matches campaignPointFill in the server-rendered SVG,
// so a campaign keeps its stage colors when React swaps in.
export function campaignCostPointColor(kind: string, palette: Palette): string {
	switch (kind) {
		case "base":
			return palette.success;
		case "polish":
			return palette.warning;
		default:
			return palette.primary;
	}
}
