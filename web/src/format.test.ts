import { describe, expect, it } from "vitest";
import {
	acceptedSweepsTitle,
	campaignCostPointColor,
	campaignPerCircleRate,
	campaignPerHourRate,
	campaignProjectedFinish,
	campaignProjectedPlanEnd,
	campaignProjectionBasis,
	campaignProvenance,
	campaignRemainingCircles,
	campaignRemainingElapsed,
	campaignStageCount,
	campaignTitle,
	campaignURL,
	campaignWarningHeading,
	formatAcceptedSweeps,
	formatChartCost,
	formatCost,
	formatCostGain,
	formatElapsed,
	formatJobCircles,
	formatPsnr,
	shortID,
	stateClass,
	stateLabel,
} from "./format";
import type { Palette } from "./charts";

// Every case below was checked against the Go helper it mirrors — the expected
// strings are what fmt/time produce for the same input, not what the TypeScript
// happens to return. Where the two genuinely diverge the case carries a comment
// saying so.

describe("stateClass", () => {
	// Mirrors ui.StateBadge in internal/ui/list.templ.
	const cases: Array<[string, string]> = [
		["pending", "badge-info"],
		["running", "badge-info"],
		["completed", "badge-success"],
		["failed", "badge-error"],
		["paused", "badge-warning"],
		["cancelled", "badge-warning"],
		["skipped", ""],
		["", ""],
		["nonsense", ""],
	];
	it.each(cases)("classes %j as %j", (state, want) => {
		expect(stateClass(state)).toBe(want);
	});
});

describe("stateLabel", () => {
	const cases: Array<[string, string]> = [
		["pending", "Pending"],
		["running", "Running"],
		["completed", "Completed"],
		["failed", "Failed"],
		["paused", "Paused"],
		["cancelled", "Cancelled"],
		// The Go default arm prints the raw state; this one capitalises it. The
		// difference is cosmetic and only reachable for a state neither side
		// enumerates.
		["skipped", "Skipped"],
		// StateBadge renders an empty badge for an empty state; the island has no
		// badge to render, so it names the gap instead.
		["", "unknown"],
	];
	it.each(cases)("labels %j as %j", (state, want) => {
		expect(stateLabel(state)).toBe(want);
	});
});

describe("formatCostGain", () => {
	// Mirrors formatJobImprovement in internal/ui/dashboard.templ.
	const cases: Array<[number, number, string]> = [
		[100, 50, "↓ 50.0%"],
		[100, 100, "↓ 0.0%"],
		[3, 1, "↓ 66.7%"],
		[1000, 1, "↓ 99.9%"],
		// best > initial: the job got worse, which the Go side refuses to print.
		[100, 150, "—"],
		[0, 0, "—"],
		[-1, -2, "—"],
		// Go has no NaN guard because a template never reaches these; the island
		// reads them off the wire, so it does.
		[Number.NaN, 50, "—"],
		[100, Number.NaN, "—"],
		[Number.POSITIVE_INFINITY, 50, "—"],
	];
	it.each(cases)("formats initial=%j best=%j as %j", (initial, best, want) => {
		expect(formatCostGain(initial, best)).toBe(want);
	});
});

describe("formatJobCircles", () => {
	// Mirrors formatJobCircles in internal/ui/dashboard.templ.
	const cases: Array<[number, number, string]> = [
		[64, 0, "64"],
		[64, 64, "64"],
		[32, 64, "32 / 64"],
		// A requested count below the actual one still prints both: the Go
		// condition is inequality, not "short of target".
		[64, 32, "64 / 32"],
		[0, 0, "0"],
		[0, 16, "0 / 16"],
		[64, -1, "64"],
		[Number.NaN, 64, "—"],
	];
	it.each(cases)("formats actual=%j requested=%j as %j", (actual, requested, want) => {
		expect(formatJobCircles(actual, requested)).toBe(want);
	});
});

describe("campaignStageCount", () => {
	// Mirrors campaignStageCount in internal/ui/schedule.templ.
	const cases: Array<[number, number, string]> = [
		[3, 7, "3 / 7"],
		[0, 7, "0 / 7"],
		[7, 7, "7 / 7"],
		// A reconstructed chain has no plan, so only what it recorded is known.
		[4, 0, "4"],
		[0, 0, "0"],
	];
	it.each(cases)("formats %j of %j as %j", (recordedStages, plannedStages, want) => {
		expect(campaignStageCount({ recordedStages, plannedStages })).toBe(want);
	});
});

describe("campaignURL", () => {
	// Mirrors dashboardCampaignURL in internal/ui/dashboard.templ, whose default
	// arm is the schedule route.
	const cases: Array<[string, string]> = [
		["chain", "/chains/abc123"],
		["schedule", "/schedules/abc123"],
		["", "/schedules/abc123"],
	];
	it.each(cases)("routes source %j to %j", (source, want) => {
		expect(campaignURL({ id: "abc123", source })).toBe(want);
	});
});

describe("shortID", () => {
	// Mirrors shortID in internal/ui/schedule.templ.
	const cases: Array<[string, string]> = [
		["", ""],
		["short", "short"],
		["12345678", "12345678"],
		["123456789", "12345678"],
		["0123456789abcdef", "01234567"],
	];
	it.each(cases)("shortens %j to %j", (id, want) => {
		expect(shortID(id)).toBe(want);
	});
});

describe("formatCost", () => {
	// Mirrors formatCampaignCost in internal/ui/schedule.templ (%.3f).
	const cases: Array<[boolean, number, string]> = [
		[false, 12.5, "—"],
		[true, 0, "0.000"],
		[true, 1.2345, "1.234"],
		[true, 1.2355, "1.236"],
		[true, 99.9995, "99.999"],
		[true, -0.0004, "-0.000"],
	];
	it.each(cases)("formats hasBestCost=%j cost=%j as %j", (hasBestCost, bestCost, want) => {
		expect(formatCost({ hasBestCost, bestCost })).toBe(want);
	});
});

describe("formatPsnr", () => {
	// Mirrors formatCampaignPSNR in internal/ui/schedule.templ. The infinite arm
	// is checked first on both sides: a perfect fit reports no finite PSNR.
	const cases: Array<[boolean, boolean, number, string]> = [
		[true, false, 0, "∞ dB"],
		[true, true, 42, "∞ dB"],
		[false, false, 42, "—"],
		[false, true, 0, "0.00 dB"],
		[false, true, 12.345, "12.35 dB"],
		[false, true, 12.355, "12.36 dB"],
		[false, true, 99.999, "100.00 dB"],
	];
	it.each(cases)("formats infinite=%j has=%j psnr=%j as %j", (psnrInfinite, hasPsnr, psnr, want) => {
		expect(formatPsnr({ psnrInfinite, hasPsnr, psnr })).toBe(want);
	});
});

describe("formatElapsed", () => {
	// Mirrors formatCampaignElapsed, which is time.Duration.Round(time.Second)
	// rendered by Duration.String: the largest non-zero unit leads and every
	// unit below it is printed even when zero.
	const cases: Array<[number, string]> = [
		[0, "0s"],
		[0.4, "0s"],
		[0.6, "1s"],
		[5, "5s"],
		[59.7, "1m0s"],
		[60, "1m0s"],
		[61, "1m1s"],
		[119, "1m59s"],
		[3600, "1h0m0s"],
		[3605, "1h0m5s"],
		[3661, "1h1m1s"],
		[7325.4, "2h2m5s"],
		[86399, "23h59m59s"],
	];
	it.each(cases)("formats %j seconds as %j", (elapsedSec, want) => {
		expect(formatElapsed({ hasElapsed: true, elapsedSec })).toBe(want);
	});

	it("prints an em dash when no elapsed time was recorded", () => {
		expect(formatElapsed({ hasElapsed: false, elapsedSec: 120 })).toBe("—");
	});
});

describe("formatAcceptedSweeps", () => {
	// Mirrors formatAcceptedSweeps in internal/ui/schedule.templ, where the
	// counter is a *int and nil means "not recorded", not zero.
	it("prints a recorded count", () => {
		expect(formatAcceptedSweeps({ acceptedSweeps: 12 })).toBe("12");
	});

	it("prints a recorded zero rather than treating it as absent", () => {
		expect(formatAcceptedSweeps({ acceptedSweeps: 0 })).toBe("0");
	});

	it("prints an em dash for an absent count", () => {
		expect(formatAcceptedSweeps({})).toBe("—");
		expect(formatAcceptedSweeps({ acceptedSweeps: undefined })).toBe("—");
		expect(formatAcceptedSweeps({ acceptedSweeps: null })).toBe("—");
	});
});

describe("acceptedSweepsTitle", () => {
	// Mirrors acceptedSweepsTitle in internal/ui/schedule.templ.
	const cases: Array<[{ acceptedSweeps?: number | null; kind: string }, string]> = [
		[{ acceptedSweeps: 12, kind: "polish" }, ""],
		[{ acceptedSweeps: 0, kind: "extend" }, ""],
		[{ kind: "extend" }, "Only a polish stage runs sweeps"],
		[{ kind: "base" }, "Only a polish stage runs sweeps"],
		[{ acceptedSweeps: null, kind: "extend" }, "Only a polish stage runs sweeps"],
		[{ kind: "polish" }, "The polisher does not persist its accepted-sweep count"],
	];
	it.each(cases)("titles %j as %j", (stage, want) => {
		expect(acceptedSweepsTitle(stage)).toBe(want);
	});
});

describe("campaignTitle", () => {
	// Mirrors campaignTitle in internal/ui/schedule.templ.
	const cases: Array<[{ name: string; id: string; source: string }, string]> = [
		[{ name: "mayfly-3000-v2", id: "0123456789", source: "schedule" }, "mayfly-3000-v2"],
		[{ name: "mayfly-3000-v2", id: "0123456789", source: "chain" }, "mayfly-3000-v2"],
		[{ name: "", id: "0123456789abcdef", source: "chain" }, "Imported chain 01234567"],
		[{ name: "", id: "0123456789abcdef", source: "schedule" }, "Campaign 01234567"],
		// The Go default arm covers an unset source, and it is the schedule arm.
		[{ name: "", id: "0123456789abcdef", source: "" }, "Campaign 01234567"],
		// shortID leaves an already-short id alone.
		[{ name: "", id: "abc", source: "schedule" }, "Campaign abc"],
	];
	it.each(cases)("titles %j as %j", (campaign, want) => {
		expect(campaignTitle(campaign)).toBe(want);
	});
});

describe("campaignProvenance", () => {
	// Mirrors campaignProvenance in internal/ui/schedule.templ.
	const base = { plannedStages: 7, hasSeed: false, campaignSeed: 0, stages: [] as unknown[] };

	it("describes a reconstructed chain by its recorded stages alone", () => {
		expect(campaignProvenance({ ...base, source: "chain", stages: [1, 2, 3] })).toBe(
			"Reconstructed from checkpoint lineage · 3 stages",
		);
	});

	it("describes a schedule against its plan", () => {
		expect(campaignProvenance({ ...base, source: "schedule", stages: [1, 2, 3] })).toBe(
			"Schedule · 3 of 7 stages recorded",
		);
	});

	it("appends the seed when the campaign carries one", () => {
		expect(
			campaignProvenance({ ...base, source: "schedule", stages: [1], hasSeed: true, campaignSeed: 4242 }),
		).toBe("Schedule · 1 of 7 stages recorded · seed 4242");
	});

	it("treats an unset source as a schedule, matching the Go default arm", () => {
		expect(campaignProvenance({ ...base, source: "", stages: [] })).toBe("Schedule · 0 of 7 stages recorded");
	});

	it("keeps a chain's seed out of the sentence", () => {
		expect(
			campaignProvenance({ ...base, source: "chain", stages: [1], hasSeed: true, campaignSeed: 9 }),
		).toBe("Reconstructed from checkpoint lineage · 1 stages");
	});
});

describe("formatChartCost", () => {
	// Mirrors formatPlotCost in internal/ui/schedule.templ.
	const cases: Array<[number, string]> = [
		[0, "0.00"],
		[1.005, "1.00"],
		[12.3456, "12.35"],
		[999.994, "999.99"],
		[999.995, "1000.00"],
		[1000, "1000"],
		[1234.56, "1235"],
		[-12.345, "-12.35"],
		// Go rounds a tie to even and JavaScript rounds it away from zero, so
		// these three are the one place the mirror does not hold: Go prints
		// "1500", "2500" and "-1234". The gap only reaches a y-axis tick label
		// whose cost lands on an exact half above 1000, and it is recorded here
		// rather than papered over.
		[1500.5, "1501"],
		[2500.5, "2501"],
		[-1234.5, "-1235"],
	];
	it.each(cases)("formats %j as %j", (cost, want) => {
		expect(formatChartCost(cost)).toBe(want);
	});
});

describe("campaignCostPointColor", () => {
	// Mirrors campaignPointFill in internal/ui/schedule.templ, which returns the
	// CSS variables this palette resolves.
	const palette: Palette = {
		primary: "#primary",
		success: "#success",
		warning: "#warning",
		text: "#text",
		textMuted: "#muted",
		border: "#border",
		grid: "#grid",
		background: "#background",
	};
	const cases: Array<[string, string]> = [
		["base", palette.success],
		["polish", palette.warning],
		["extend", palette.primary],
		["", palette.primary],
		["nonsense", palette.primary],
	];
	it.each(cases)("paints a %j stage %j", (kind, want) => {
		expect(campaignCostPointColor(kind, palette)).toBe(want);
	});
});

// The projection formatters mirror the ones in schedule.templ. The fixture is
// the measured growth campaign the estimator was built from: 2000 circles at
// cost 64.602, a trailing rate of 0.017697 cost per circle, and 1000 circles
// left to the plan's 3000 ceiling.
const projectionFixture = {
	projected: true,
	samples: 3,
	recentLegs: 1,
	recentCircles: 1000,
	recentElapsedSec: 3360,
	recentGainPerCircle: 0.017697,
	recentGainPerHour: 18.96,
	latestCircles: 2000,
	latestCost: 64.602,
	remainingCircles: 1000,
	costAtPlanEnd: 46.905,
	planEndPsnr: 31.42,
	planEndPsnrInfinite: false,
	hasPlanEndPsnr: true,
	hasCircleCeiling: true,
	remainingElapsedSec: 5760,
	costAtFinish: 46.905,
	finishPsnr: 31.42,
	finishPsnrInfinite: false,
	hasFinishPsnr: true,
	hasTimeBudget: true,
};

describe("campaignWarningHeading", () => {
	it("agrees with the list under it", () => {
		expect(campaignWarningHeading(["one"])).toBe("Advisory:");
		expect(campaignWarningHeading(["one", "two"])).toBe("Advisories:");
	});
});

describe("the projection formatters", () => {
	it("prints the trailing rates the projections were extrapolated with", () => {
		expect(campaignPerCircleRate(projectionFixture)).toBe(
			"0.017697 cost/circle over the last 1000 circles");
		expect(campaignPerHourRate(projectionFixture)).toBe("18.96 cost/hour over the last 56m0s");
	});

	it("prints a measured zero rate rather than hiding it behind a dash", () => {
		// A trailing window that added circles and spent wall clock but removed
		// no cost measured a rate of zero. That is a finding about the campaign,
		// and the CLI prints it as 0.000000; a dash would say the number is
		// missing. The denominators are what decide presence.
		const flat = { ...projectionFixture, recentGainPerCircle: 0, recentGainPerHour: 0 };
		expect(campaignPerCircleRate(flat)).toBe("0.000000 cost/circle over the last 1000 circles");
		expect(campaignPerHourRate(flat)).toBe("0.00 cost/hour over the last 56m0s");
	});

	it("dashes only when the window has no denominator to divide by", () => {
		const unmeasured = { ...projectionFixture, recentCircles: 0, recentElapsedSec: 0 };
		expect(campaignPerCircleRate(unmeasured)).toBe("—");
		expect(campaignPerHourRate(unmeasured)).toBe("—");
	});

	it("names the ceiling as well as the shortfall", () => {
		expect(campaignRemainingCircles(projectionFixture)).toBe("1000 circles to 3000");
		expect(campaignRemainingElapsed(projectionFixture)).toBe("1h36m0s");
	});

	it("parenthesises the PSNR beside the cost it restates", () => {
		expect(campaignProjectedPlanEnd(projectionFixture)).toBe("46.905 (PSNR 31.42 dB)");
		expect(campaignProjectedFinish(projectionFixture)).toBe("46.905 (PSNR 31.42 dB)");
	});

	// A projected cost that is absent must not be printed as the zero the field
	// carries: that would report a perfect fit for a campaign nobody estimated.
	it("prints an absent projection as a dash rather than as zero", () => {
		const absent = { ...projectionFixture, hasCircleCeiling: false, hasTimeBudget: false, costAtPlanEnd: 0, costAtFinish: 0 };
		expect(campaignProjectedPlanEnd(absent)).toBe("—");
		expect(campaignProjectedFinish(absent)).toBe("—");
	});

	it("says what the estimate rests on, and when it rests on nothing", () => {
		expect(campaignProjectionBasis(projectionFixture)).toBe(
			"From 3 measured stages alone, at 2000 circles and cost 64.602. " +
			"The projections below extrapolate the trailing leg, not the whole campaign.");
		expect(campaignProjectionBasis({ ...projectionFixture, projected: false })).toBe(
			"Not enough completed stages to project a cost yet.");
		expect(campaignPerCircleRate({ ...projectionFixture, projected: false })).toBe("—");
	});
});
