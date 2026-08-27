// Turning the job creation form into a POST /api/v1/jobs body.
//
// The two admission paths do not read a blank field the same way. The templ
// form posts strings to /create, where the handler resolves an empty one
// against the defaults and only then builds a JobConfig. The JSON API reads the
// raw body to see which keys the caller actually wrote and refuses any value
// ApplyDefaults would replace, so `"batchSize": 0` is a 400 rather than the
// automatic batch size the same form field means. See the "A written field is
// used as written" invariant in docs/behavior-invariants.md.
//
// So the island cannot marshal its state and post it: a field the user left
// alone has to leave the key out entirely. The tables below say which fields
// that applies to, and web/src/create-job-parity.json pins the result against
// what the form produces for the same input.

/** A form submission: field name to the string the browser would send. An
 * unchecked checkbox is an absent key and an emptied number is "", which is
 * what the island stores as its state so the two cannot drift. */
export type CreateJobFormValues = Record<string, string | undefined>;

export type CreateJobBody = Record<string, string | number | boolean>;

// Sent always. The form renders each with a value and marks it required, so
// there is no blank to interpret: circles, iters and popSize are refused below
// their minimum by both paths, and seed is the field whose zero means "draw
// one", which ApplyDefaults reads off EffectiveSeed and leaves alone here.
const REQUIRED_NUMBERS = ["circles", "iters", "popSize", "seed"] as const;

// Omitted when blank *and* when zero. ApplyDefaults replaces the zero in each
// of these with a real default, so an explicit zero on the wire is refused as a
// default override, while the form field means "decide for me" — batchSize is
// rendered as 0 and documented that way.
const DEFAULTED_NUMBERS = [
	"batchSize",
	"optimizerEpochs",
	"polishingActiveSetSize",
	"polishingMaxSweeps",
	"polishingEpochs",
	"polishingIters",
	"polishingPopSize",
	"polishingStagnationIters",
	"polishingMinImprovement",
	"convergencePatience",
	"convergenceThreshold",
] as const;

// Omitted only when blank. ApplyDefaults never fills the early-stopping fields
// — an unconfigured run has to reproduce exactly — so zero is a value the API
// accepts as written, and a blank field and an explicit zero mean the same
// thing on both paths.
const OPTIONAL_NUMBERS = ["stopTargetCost", "stopStagnationIters", "stopMinImprovement", "stopMinIters"] as const;

// The optimizer that reads the CMA-ES section. Every other engine refuses those
// fields at validation rather than ignoring them, and the fallback form submits
// them whatever engine is selected, so both paths drop them the same way.
const CMAES = "cmaes";

/** Reads a form field, trimmed. An absent field is blank. */
function text(form: CreateJobFormValues, name: string): string {
	return (form[name] ?? "").trim();
}

/** A checkbox is submitted as "on" when checked and not at all otherwise. */
function checked(form: CreateJobFormValues, name: string): boolean {
	return text(form, name) === "on";
}

function toNumber(name: string, raw: string): number {
	const value = Number(raw);
	if (!Number.isFinite(value)) throw new Error(`${name} must be a number`);
	return value;
}

/**
 * buildCreateJobBody translates one form submission into the JSON body that
 * produces the same stored configuration as posting the form to /create.
 *
 * It throws when a number does not parse; the form's own `type="number"`
 * controls make that unreachable from the island, which submits only after the
 * browser's constraint validation has passed.
 */
export function buildCreateJobBody(form: CreateJobFormValues): CreateJobBody {
	const body: CreateJobBody = {};

	body.refPath = text(form, "refPath");

	const canvasPath = text(form, "canvasPath");
	if (canvasPath !== "") body.canvasPath = canvasPath;

	const optimizer = text(form, "optimizer");
	if (optimizer !== "") body.optimizer = optimizer;

	const mode = text(form, "mode");
	if (mode !== "") body.mode = mode;

	for (const name of REQUIRED_NUMBERS) {
		body[name] = toNumber(name, text(form, name));
	}

	for (const name of DEFAULTED_NUMBERS) {
		const raw = text(form, name);
		if (raw === "") continue;

		const value = toNumber(name, raw);
		if (value === 0) continue;

		body[name] = value;
	}

	for (const name of OPTIONAL_NUMBERS) {
		const raw = text(form, name);
		if (raw === "") continue;

		body[name] = toNumber(name, raw);
	}

	body.polishingEnabled = checked(form, "polishingEnabled");

	const strategy = text(form, "polishingStrategy");
	if (strategy !== "") body.polishingStrategy = strategy;

	// Both keys travel together. An unchecked box is the only way the form can
	// ask for convergence detection to be off, and a lone `convergenceEnabled:
	// false` would be defaulted straight back to true, so the explicit disable
	// flag is what carries the request.
	const convergence = checked(form, "convergenceEnabled");
	body.convergenceEnabled = convergence;
	body.disableConvergence = !convergence;

	body.enableSSIM = checked(form, "enableSSIM");

	if (optimizer === CMAES) {
		// An emptied step size is "use the CMA-ES default", not zero, which
		// validation refuses as a non-positive step. Omitting the key is how the
		// JSON path asks for the same default the form handler substitutes.
		const sigma = text(form, "initialSigma");
		if (sigma !== "") body.initialSigma = toNumber("initialSigma", sigma);

		body.activeCMA = checked(form, "activeCMA");

		const covariance = text(form, "covarianceMode");
		if (covariance !== "") body.covarianceMode = covariance;

		const restart = text(form, "restartStrategy");
		if (restart !== "") body.restartStrategy = restart;
	}

	return body;
}
