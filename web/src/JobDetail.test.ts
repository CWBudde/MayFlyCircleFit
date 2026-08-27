import { describe, expect, it } from "vitest";
import {
	formatCompactNumber,
	formatElapsedDuration,
	formatFileSize,
	formatReferenceDimensions,
	formatWallClock,
} from "./format";
import contract from "./job-detail-parity.json";
import {
	averageCPS,
	colorChannel,
	costImprovementRate,
	currentCPS,
	etaLabel,
	formatAxisValue,
	formatETA,
	formatMetricValue,
	iterationRate,
	latestHistorySample,
	metricBounds,
	normalizeHistorySample,
	optimizerSchedule,
	parameterDescription,
	parseSampleInstant,
	previousHistorySample,
	progressPercent,
	selectMetricPoints,
} from "./JobDetail";
import type { HistorySample, JobDetailSeed, MetricSample } from "./JobDetail";

// The TypeScript half of Task 18.1's before-and-after-mount check. The Go half
// is internal/ui/job_detail_parity_test.go, which renders the same fixture
// through the templ page; neither language is the source of truth, and a
// formatter that drifts on one side alone fails on both.

type ParityCase = (typeof contract)["cases"][number];

function historyOf(job: ParityCase["job"]): HistorySample[] {
	return (job.metricHistory as MetricSample[]).map(normalizeHistorySample);
}

describe("job detail parity", () => {
	it("covers a running job and a terminal job", () => {
		expect(contract.cases.map((entry) => entry.name)).toEqual(["running", "terminal"]);
	});

	for (const testCase of contract.cases as ParityCase[]) {
		describe(testCase.name, () => {
			// The fixture's job is a serialized ui.JobDetail, which is exactly the
			// blob internal/ui/detail.templ writes into #job-detail-data.
			const job = testCase.job as unknown as JobDetailSeed;
			const want = testCase.expected;
			const history = historyOf(testCase.job);

			it("renders the audited metrics the way the page does", () => {
				expect(job.bestCost.toFixed(4)).toBe(want.bestCost);
				expect(String(job.iterations)).toBe(want.iterations);
				expect(`${progressPercent(job.iterations, job.maxIterations).toFixed(1)}%`).toBe(
					want.iterationProgress,
				);
				expect(formatCompactNumber(job.evaluations)).toBe(want.evaluations);
				expect(optimizerSchedule(job.optimizerRestarts, job.optimizerEpochs, job.itersPerEpoch)).toBe(
					want.optimizerSchedule,
				);
			});

			it("derives the same throughput and ETA", () => {
				expect(formatCompactNumber(averageCPS(history, job.cps))).toBe(want.averageCps);
				expect(formatCompactNumber(currentCPS(history, job.circles, job.cps))).toBe(want.currentCps);
				expect(etaLabel(history, job.maxIterations)).toBe(want.eta);
				expect(costImprovementRate(history)).toBe(want.costImprovementRate);
			});

			it("prints the same elapsed time and start instant", () => {
				expect(formatElapsedDuration(job.elapsed)).toBe(want.elapsed);
				// The wall clock is read out of the RFC 3339 text rather than out of
				// a Date, so this holds in whatever zone the test host is in.
				expect(formatWallClock(job.startTime)).toBe(want.startTime);
				expect(job.termination ?? "").toBe(want.termination);
			});

			it("describes the reference image the same way", () => {
				expect(formatReferenceDimensions(job.refWidth ?? 0, job.refHeight ?? 0)).toBe(
					want.referenceDimensions,
				);
				expect((job.refSize ?? 0) > 0 ? formatFileSize(job.refSize ?? 0) : "").toBe(
					want.referenceFileSize,
				);
			});

			it("writes the same circle rows", () => {
				expect(job.parameters.map(parameterDescription)).toEqual(want.parameters);
			});
		});
	}
});

describe("history sample normalization", () => {
	it("reads Go's zero time as no instant at all", () => {
		// Date.parse is perfectly happy with it, which would make an unstamped
		// sample look like the oldest sample there is.
		expect(parseSampleInstant("0001-01-01T00:00:00Z")).toBeNull();
		expect(parseSampleInstant("")).toBeNull();
		expect(parseSampleInstant(undefined)).toBeNull();
		expect(parseSampleInstant("not a time")).toBeNull();
		expect(parseSampleInstant("2026-08-13T09:00:00Z")).toBe(Date.parse("2026-08-13T09:00:00Z"));
	});

	it("keeps an absent psnr absent rather than turning it into zero", () => {
		const sample = normalizeHistorySample({
			iteration: 3,
			evaluations: 40,
			cost: 1.5,
			psnr: null,
			cps: 12,
			timestamp: "2026-08-13T09:00:00Z",
		});
		expect(sample.psnr).toBeNull();
		expect(sample.ssim).toBeNull();
		expect(sample.psnrInfinite).toBe(false);
		expect(sample.cost).toBe(1.5);
	});
});

function sample(partial: Partial<HistorySample>): HistorySample {
	return {
		iteration: 0,
		evaluations: 0,
		cost: 0,
		psnr: null,
		psnrInfinite: false,
		ssim: null,
		cps: 0,
		instant: null,
		...partial,
	};
}

describe("history walking", () => {
	it("prefers the newest stamped sample but falls back to the newest one", () => {
		const stamped = sample({ iteration: 1, instant: 1000 });
		const unstamped = sample({ iteration: 2 });
		expect(latestHistorySample([stamped, unstamped])).toBe(stamped);
		expect(latestHistorySample([unstamped])).toBe(unstamped);
		expect(latestHistorySample([])).toBeNull();
	});

	it("treats an equal instant as older only when the iteration is lower", () => {
		// A clock coarser than the loop stamps two samples identically; the
		// iteration is then the only thing that orders them.
		const older = sample({ iteration: 1, instant: 1000 });
		const target = sample({ iteration: 2, instant: 1000 });
		expect(previousHistorySample([older, target], 0, target)).toBe(older);
		expect(previousHistorySample([target], 0, target)).toBeNull();
	});

	it("reports no rate from a single sample", () => {
		expect(iterationRate([sample({ iteration: 5, instant: 1000 })])).toBe(0);
		expect(iterationRate([])).toBe(0);
	});

	it("falls back to the job figure when two samples cannot be differenced", () => {
		const only = [sample({ iteration: 5, evaluations: 10, cps: 3, instant: 1000 })];
		expect(currentCPS(only, 64, 42)).toBe(42);
		// A run with no circles cannot convert evaluations into circles per
		// second at all.
		expect(currentCPS(only, 0, 42)).toBe(42);
		expect(averageCPS([], 42)).toBe(42);
	});
});

describe("eta", () => {
	it("prints at the resolution a reader can act on", () => {
		expect(formatETA(0)).toBe("0s");
		expect(formatETA(45.4)).toBe("45s");
		expect(formatETA(300)).toBe("5m 0s");
		expect(formatETA(3725)).toBe("1h 2m");
		expect(formatETA(-1)).toBe("—");
		expect(formatETA(Number.POSITIVE_INFINITY)).toBe("—");
	});

	it("says nothing rather than guessing when there is no basis", () => {
		const history = [sample({ iteration: 10, instant: 1000 }), sample({ iteration: 20, instant: 2000 })];
		expect(etaLabel(history, 0)).toBe("—");
		expect(etaLabel([], 100)).toBe("—");
		// The planned count reached is zero remaining, not an unknown.
		expect(etaLabel([sample({ iteration: 100, instant: 1 })], 100)).toBe("0s");
		expect(etaLabel(history, 40)).toBe("2s");
	});
});

describe("cost improvement rate", () => {
	it("distinguishes a stalled run from an unmeasurable one", () => {
		const flat = [sample({ iteration: 1, cost: 5 }), sample({ iteration: 2, cost: 5 })];
		expect(costImprovementRate(flat)).toBe("→ 0.0000 / iter");
		expect(costImprovementRate([sample({ iteration: 1, cost: 5 })])).toBe("—");
		// Two samples at one iteration cannot be divided by an iteration count.
		expect(costImprovementRate([sample({ iteration: 1, cost: 5 }), sample({ iteration: 1, cost: 4 })])).toBe("—");
	});

	it("points the arrow at the direction the cost moved", () => {
		expect(costImprovementRate([sample({ iteration: 0, cost: 5 }), sample({ iteration: 10, cost: 4 })])).toBe(
			"↓ 0.1000 / iter",
		);
		expect(costImprovementRate([sample({ iteration: 0, cost: 4 }), sample({ iteration: 10, cost: 5 })])).toBe(
			"↑ 0.1000 / iter",
		);
	});
});

describe("chart series selection", () => {
	const history = [
		sample({ iteration: 1, cost: 5, psnr: 20, cps: 100 }),
		sample({ iteration: 2, cost: 4, psnr: null, cps: 110 }),
		sample({ iteration: 3, cost: 3, psnr: 22, cps: 120 }),
	];

	it("drops the samples a series has no value for", () => {
		// PSNR before the first audit is absent, not zero: plotting it at zero
		// would draw a cliff the run never had.
		const psnr = selectMetricPoints(history, "psnr", "all");
		expect(psnr.points.map((point) => point.iteration)).toEqual([1, 3]);
		expect(psnr.total).toBe(2);
	});

	it("cuts the window off the tail and still reports the whole total", () => {
		const cost = selectMetricPoints(history, "cost", "100");
		expect(cost.points).toHaveLength(3);

		const windowed = selectMetricPoints(history, "cost", "1000");
		expect(windowed.total).toBe(3);
	});

	it("pads a flat series so it is not drawn against the frame", () => {
		expect(metricBounds([])).toBeNull();
		// A single sample has no spread, so the padding is 5% of its magnitude
		// or one, whichever is larger -- 0.5 here, so one wins.
		expect(metricBounds([{ iteration: 1, value: 10 }])).toEqual({ min: 9, max: 11 });
		expect(metricBounds([{ iteration: 1, value: 100 }])).toEqual({ min: 95, max: 105 });
		expect(metricBounds([{ iteration: 1, value: 0 }])).toEqual({ min: -1, max: 1 });
		expect(metricBounds([
			{ iteration: 1, value: 0 },
			{ iteration: 2, value: 100 },
		])).toEqual({ min: -5, max: 105 });
	});

	it("labels a value by what the series means", () => {
		expect(formatMetricValue("psnr", 31.256)).toBe("31.26 dB");
		expect(formatMetricValue("cps", 1234.5)).toBe("1234.50 cps");
		expect(formatMetricValue("cost", 1.23456)).toBe("1.2346");
	});

	it("keeps an axis tick readable at any magnitude", () => {
		expect(formatAxisValue("cost", 123456)).toBe("1.23e+5");
		expect(formatAxisValue("cost", 0.0001)).toBe("1.00e-4");
		expect(formatAxisValue("ssim", 0.91234)).toBe("0.912");
		expect(formatAxisValue("psnr", 31.25)).toBe("31.3");
		expect(formatAxisValue("cost", 0)).toBe("0.000");
	});
});

describe("circle rows", () => {
	it("clamps a channel to the byte range the swatch needs", () => {
		expect(colorChannel(-1)).toBe(0);
		expect(colorChannel(0.5)).toBe(128);
		expect(colorChannel(2)).toBe(255);
	});
});
