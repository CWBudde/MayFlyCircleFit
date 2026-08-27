import { describe, expect, it } from "vitest";
import { buildCreateJobBody } from "./createJobBody";
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
