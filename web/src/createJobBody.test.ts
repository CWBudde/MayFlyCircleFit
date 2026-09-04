import { describe, expect, it } from "vitest";
import { buildCreateJobBody, optimizerDimensions } from "./createJobBody";
import contract from "./create-job-parity.json";

// The TypeScript half of the create-page parity check. buildCreateJobBody turns
// a form submission into the JSON body the island posts; the form handler in
// internal/server turns the same submission into a JobConfig directly. Neither
// is checked against the other here — no test process can reach both — so both
// are checked against create-job-parity.json. The Go half is
// TestCreateJobIslandAndFormStoreTheSameConfiguration in
// internal/server/create_job_parity_test.go, which posts each case's form to
// /create and each case's body to POST /api/v1/jobs and compares what the two
// stored.

type ParityCase = (typeof contract)["cases"][number];

describe("create job body parity", () => {
	it("covers every case the contract names", () => {
		expect(contract.cases.map((entry) => entry.name)).toEqual([
			"form defaults",
			"batch run with polishing and early stopping",
			"cmaes with an emptied initial sigma",
			"dragonfly with a canvas path",
			"budget-filling restart cap",
			"unset restart count",
		]);
	});

	it.each(contract.cases.map((entry): [string, ParityCase] => [entry.name, entry]))(
		"builds the contract body for %s", (_name, entry) => {
			expect(buildCreateJobBody(entry.form)).toEqual(entry.body);
		},
	);
});

describe("buildCreateJobBody", () => {
	// The hazard the whole task exists for, stated on its own rather than
	// inside a full submission: a field the defaults would replace has to leave
	// the wire, and a field they would not has to stay on it even at zero.
	it("omits a zero the defaults would replace", () => {
		const body = buildCreateJobBody({ refPath: "a.png", circles: "4", iters: "9", popSize: "20", seed: "1", batchSize: "0" });

		expect(body).not.toHaveProperty("batchSize");
	});

	it("sends a zero the defaults leave alone", () => {
		const body = buildCreateJobBody({ refPath: "a.png", circles: "4", iters: "9", popSize: "20", seed: "0", stopMinIters: "0" });

		expect(body.seed).toBe(0);
		expect(body.stopMinIters).toBe(0);
	});

	it("omits a blank early-stopping field rather than sending zero", () => {
		const body = buildCreateJobBody({ refPath: "a.png", circles: "4", iters: "9", popSize: "20", seed: "1", stopMinIters: "" });

		expect(body).not.toHaveProperty("stopMinIters");
	});

	it("drops the cmaes section for another engine and keeps it for cmaes", () => {
		const shared = { refPath: "a.png", circles: "4", iters: "9", popSize: "20", seed: "1", covarianceMode: "block", activeCMA: "on" };

		expect(buildCreateJobBody({ ...shared, optimizer: "mayfly" })).not.toHaveProperty("covarianceMode");
		expect(buildCreateJobBody({ ...shared, optimizer: "cmaes" }).covarianceMode).toBe("block");
		expect(buildCreateJobBody({ ...shared, optimizer: "cmaes" }).activeCMA).toBe(true);
	});

	// The sign of optimizerRestarts is the request rather than a typo: a
	// negative count caps the stage at that many times its iteration budget and
	// fills the cap with cold attempts. The omit-a-zero rule the defaulted
	// numbers share must not normalize it away, and a zero still has to be
	// omitted so ApplyDefaults can fill in the single attempt.
	it("keeps a negative restart count on the wire with its sign", () => {
		const shared = { refPath: "a.png", circles: "4", iters: "9", popSize: "20", seed: "1" };

		expect(buildCreateJobBody({ ...shared, optimizerRestarts: "-8" }).optimizerRestarts).toBe(-8);
		expect(buildCreateJobBody({ ...shared, optimizerRestarts: "-1" }).optimizerRestarts).toBe(-1);
		expect(buildCreateJobBody({ ...shared, optimizerRestarts: "0" })).not.toHaveProperty("optimizerRestarts");
		expect(buildCreateJobBody({ ...shared, optimizerRestarts: "" })).not.toHaveProperty("optimizerRestarts");
	});

	it("carries the explicit disable flag when convergence is unchecked", () => {
		const shared = { refPath: "a.png", circles: "4", iters: "9", popSize: "20", seed: "1" };

		expect(buildCreateJobBody(shared).disableConvergence).toBe(true);
		expect(buildCreateJobBody({ ...shared, convergenceEnabled: "on" }).disableConvergence).toBe(false);
	});

	it("refuses a number it cannot parse instead of posting a default", () => {
		expect(() => buildCreateJobBody({ refPath: "a.png", circles: "many", iters: "9", popSize: "20", seed: "1" }))
			.toThrow(/circles/);
	});
});

// Number("") is 0, so a blank required field would otherwise be sent as an
// explicit zero. That is not a harmless default: the server honours an explicit
// seed of 0, while the form path defaults a blank one, so the two admission
// paths would store different configurations for the same submission.
describe("required numbers reject a blank value", () => {
	const complete = {
		refPath: "assets/ref.png",
		circles: "8",
		iters: "100",
		popSize: "30",
		seed: "0",
	};

	for (const name of ["circles", "iters", "popSize", "seed"]) {
		it(`throws when ${name} is blank rather than sending 0`, () => {
			expect(() => buildCreateJobBody({ ...complete, [name]: "" })).toThrow(
				`${name} is required and was left blank`,
			);
		});

		it(`throws when ${name} is absent rather than sending 0`, () => {
			const partial = { ...complete };
			delete (partial as Record<string, string>)[name];

			expect(() => buildCreateJobBody(partial)).toThrow(
				`${name} is required and was left blank`,
			);
		});
	}

	it("still sends an explicitly typed zero seed", () => {
		expect(buildCreateJobBody(complete).seed).toBe(0);
	});
});

// The mirror of TestOptimizerDimensions in internal/app/config_test.go. Both
// tables name the same cases, against the configuration ApplyDefaults produces
// rather than the one submitted: the island recomputes the rule so it can warn
// that full covariance will refuse the run, and a mirror that drifts warns about
// the wrong configuration rather than failing.
describe("optimizer dimensions", () => {
	const PARAMETERS_PER_CIRCLE = 7;
	const DEFAULT_BATCH_SIZE = 5;

	for (const testCase of [
		{ name: "joint searches every circle", form: { mode: "joint", circles: "10" }, want: 10 },
		{ name: "joint ignores a batch size", form: { mode: "joint", circles: "10", batchSize: "3" }, want: 10 },
		{ name: "joint above the full covariance limit", form: { mode: "joint", circles: "100" }, want: 100 },
		{ name: "sequential searches one circle", form: { mode: "sequential", circles: "40", batchSize: "8" }, want: 1 },
		{ name: "batch searches one batch", form: { mode: "batch", circles: "40", batchSize: "8" }, want: 8 },
		{
			name: "an automatic batch searches the default batch",
			form: { mode: "batch", circles: "100", batchSize: "0" },
			want: DEFAULT_BATCH_SIZE,
		},
		{
			name: "an automatic batch narrower than the default searches every circle",
			form: { mode: "batch", circles: "3", batchSize: "0" },
			want: 3,
		},
		{ name: "a wide batch searches every circle", form: { mode: "batch", circles: "80", batchSize: "100" }, want: 80 },
	]) {
		it(testCase.name, () => {
			expect(optimizerDimensions(testCase.form, PARAMETERS_PER_CIRCLE, DEFAULT_BATCH_SIZE)).toBe(
				testCase.want * PARAMETERS_PER_CIRCLE,
			);
		});
	}

	// Only reachable from a programmatic caller: the control is required with a
	// minimum of one, so the browser will not submit a blank, and an emptied
	// batch size reads exactly as the automatic zero the form ships.
	it("an emptied batch size resolves like the automatic one", () => {
		expect(optimizerDimensions({ mode: "batch", circles: "100", batchSize: "" }, 7, 5)).toBe(35);
	});

	it("an unset circle count still searches one", () => {
		expect(optimizerDimensions({ mode: "joint", circles: "" }, 7, 5)).toBe(7);
	});
});
