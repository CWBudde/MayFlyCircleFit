import { useMemo, useState } from "react";
import type { FormEvent } from "react";
import { buildCreateJobBody } from "./createJobBody";
import type { CreateJobFormValues } from "./createJobBody";

// The job creation form, posting POST /api/v1/jobs instead of the server-side
// /create form.
//
// Both admission paths are kept. The templ form inside the mount point is the
// no-JavaScript fallback and stays exactly as functional as it was; this island
// replaces it when the bundle loads. What it must not do is create a different
// job from the same input, which is what buildCreateJobBody and
// web/src/create-job-parity.json exist to prevent.
//
// Nothing here states a bound of its own. The page seeds the mount point with
// the limits the server projects from internal/app, and the controls below
// carry them as min/max so the browser's own constraint validation enforces the
// server's numbers; app.Validate still decides the request.

/** The bounds the server projects onto this page. Mirrors ui.CreateJobLimits. */
interface CreateJobLimits {
	maxCircles: number;
	maxIterations: number;
	minPopulation: number;
	maxPopulation: number;
	maxOptimizerEpochs: number;
	maxBatchSize: number;
	maxPolishingSweeps: number;
	maxConvergencePatience: number;
	minConvergenceThreshold: number;
	maxConvergenceThreshold: number;
	minPolishingMinImprovement: number;
	defaultInitialSigma: number;
}

/** The page seed. Mirrors ui.CreateJobPageData. */
interface CreateJobSeed {
	project: string;
	limits: CreateJobLimits;
}

/** One `<option>` of a fallback `<select>`, read from the markup it replaces. */
interface Choice {
	value: string;
	label: string;
}

const sectionStyle = { minInlineSize: 0, border: 0, padding: 0, margin: "0 0 2rem" } as const;
const gridStyle = {
	display: "grid",
	gridTemplateColumns: "repeat(auto-fit, minmax(min(200px, 100%), 1fr))",
	gap: "1rem",
} as const;
const inputStyle = {
	width: "100%",
	padding: "0.5rem",
	border: "1px solid var(--border-color)",
	borderRadius: "0.375rem",
	fontSize: "0.875rem",
} as const;
const selectStyle = { ...inputStyle, backgroundColor: "var(--control-bg)" } as const;
const labelStyle = { display: "block", fontWeight: 500, marginBottom: "0.5rem" } as const;
const helpStyle = { fontSize: "0.75rem", color: "var(--text-muted)", marginTop: "0.25rem" } as const;
const headingStyle = { fontSize: "1.25rem", fontWeight: 600, marginBottom: "1rem" } as const;
const checkboxStyle = { marginRight: "0.5rem", width: "1rem", height: "1rem", cursor: "pointer" } as const;

/**
 * readFallbackValues serializes the server-rendered form the island is about to
 * replace. Its current values are the initial state, so the defaults the page
 * ships live in exactly one place — the templ markup — and a checkbox is "on"
 * or absent here just as it is in a real submission.
 */
function readFallbackValues(root: HTMLElement): CreateJobFormValues {
	const form = root.querySelector("form");
	const values: CreateJobFormValues = {};
	if (!form) return values;

	for (const [name, value] of new FormData(form).entries()) {
		if (typeof value === "string") values[name] = value;
	}

	return values;
}

/**
 * readFallbackChoices reads each fallback `<select>`'s options. The option sets
 * are the server's enumerations — modes, engines, covariance modes, polishing
 * strategies — so taking them from the markup keeps this file from carrying a
 * second copy that could name a value the server refuses.
 */
function readFallbackChoices(root: HTMLElement): Record<string, Choice[]> {
	const choices: Record<string, Choice[]> = {};

	for (const select of root.querySelectorAll("select")) {
		if (!select.name) continue;
		choices[select.name] = Array.from(select.options).map((option) => ({
			value: option.value,
			label: option.textContent?.trim() ?? option.value,
		}));
	}

	return choices;
}

function readSeed(root: HTMLElement): CreateJobSeed | null {
	const script = root.querySelector("#create-job-page");
	if (!script) return null;

	try {
		return JSON.parse(script.textContent || "null") as CreateJobSeed;
	} catch {
		return null;
	}
}

/** The island's form state, as the controls below see it. */
interface FormBinding {
	value: (name: string) => string;
	set: (name: string, value: string) => void;
}

/**
 * attr renders a bound as an HTML attribute value. JavaScript prints a small
 * float in exponent notation and `min="1e-9"` is not a valid HTML floating
 * point number, so the browser drops the constraint without saying anything.
 */
function attr(value: number): string {
	const text = String(value);
	if (!text.includes("e")) return text;

	return value.toFixed(20).replace(/0+$/, "");
}

interface FieldProps {
	form: FormBinding;
	name: string;
	label: string;
	help?: string;
	required?: boolean;
}

// The red asterisk carries the fact in colour and glyph only, which WCAG 1.3.1
// does not accept, so it is hidden from the accessibility tree and the word is
// supplied beside it; the control itself carries aria-required.
function Required() {
	return (
		<>
			<span style={{ color: "var(--error-color)" }} aria-hidden="true">*</span>
			<span className="sr-only"> (required)</span>
		</>
	);
}

function FieldLabel({ name, label, required }: { name: string; label: string; required?: boolean }) {
	return (
		<label htmlFor={name} style={labelStyle}>
			{label}
			{required ? <Required /> : null}
		</label>
	);
}

function Help({ help }: { help?: string }) {
	return help ? <p style={helpStyle}>{help}</p> : null;
}

function Text({ form, name, label, help, required }: FieldProps) {
	return (
		<div style={{ marginBottom: "1rem" }}>
			<FieldLabel name={name} label={label} required={required} />
			<input
				type="text"
				id={name}
				name={name}
				value={form.value(name)}
				required={required}
				aria-required={required ? "true" : undefined}
				onChange={(event) => form.set(name, event.target.value)}
				style={inputStyle}
			/>
			<Help help={help} />
		</div>
	);
}

function Num({ form, name, label, help, required, min, max, step, placeholder }: FieldProps & {
	min?: number;
	max?: number;
	step?: string;
	placeholder?: string;
}) {
	return (
		<div>
			<FieldLabel name={name} label={label} required={required} />
			<input
				type="number"
				id={name}
				name={name}
				value={form.value(name)}
				required={required}
				aria-required={required ? "true" : undefined}
				min={min === undefined ? undefined : attr(min)}
				max={max === undefined ? undefined : attr(max)}
				step={step}
				placeholder={placeholder}
				onChange={(event) => form.set(name, event.target.value)}
				style={inputStyle}
			/>
			<Help help={help} />
		</div>
	);
}

function Select({ form, name, label, help, required, choices }: FieldProps & { choices: Choice[] }) {
	return (
		<div>
			<FieldLabel name={name} label={label} required={required} />
			<select
				id={name}
				name={name}
				value={form.value(name)}
				required={required}
				aria-required={required ? "true" : undefined}
				onChange={(event) => form.set(name, event.target.value)}
				style={selectStyle}
			>
				{choices.map((choice) => <option key={choice.value} value={choice.value}>{choice.label}</option>)}
			</select>
			<Help help={help} />
		</div>
	);
}

function Check({ form, name, label, help }: FieldProps) {
	return (
		<div style={{ marginBottom: "0.5rem" }}>
			<label htmlFor={name} style={{ display: "flex", alignItems: "center", cursor: "pointer" }}>
				<input
					type="checkbox"
					id={name}
					name={name}
					checked={form.value(name) === "on"}
					onChange={(event) => form.set(name, event.target.checked ? "on" : "")}
					style={checkboxStyle}
				/>
				<span style={{ fontWeight: 500 }}>{label}</span>
			</label>
			{help ? <p style={{ ...helpStyle, marginLeft: "1.5rem" }}>{help}</p> : null}
		</div>
	);
}

async function apiError(response: Response): Promise<string> {
	try {
		const payload = await response.json() as { error?: { message?: string }; message?: string };
		return payload.error?.message ?? payload.message ?? `Request failed: ${response.status}`;
	} catch {
		return `Request failed: ${response.status}`;
	}
}

export function CreateJobIsland({ root }: { root: HTMLElement }) {
	// Read during the first render, before React commits and clears the mount
	// point: after that the fallback markup is gone.
	const initial = useMemo(() => ({
		values: readFallbackValues(root),
		choices: readFallbackChoices(root),
		seed: readSeed(root),
	}), [root]);

	const [values, setValues] = useState<CreateJobFormValues>(initial.values);
	const [error, setError] = useState("");
	const [busy, setBusy] = useState(false);

	const limits = initial.seed?.limits;
	if (!limits) {
		// Unreachable while the page renders its seed, which templ does
		// unconditionally inside this mount point. It is handled anyway because
		// mounting has already replaced the fallback form by the time this runs:
		// rendering nothing would leave a blank page with no way back, so the
		// page says what happened and offers the reload that restores the
		// server-rendered form.
		return (
			<div className="card" role="alert">
				<p>The creation form could not be initialized because the page carries no limits.</p>
				<p><a href="/create">Reload the page</a> to use the server-rendered form.</p>
			</div>
		);
	}

	// One binding handed to every control below. The controls are module-level
	// components rather than closures declared here: a component type recreated
	// on each render is a different type to React, which would remount every
	// input and take the caret out of it on every keystroke.
	const form: FormBinding = {
		value: (name) => values[name] ?? "",
		set: (name, next) => setValues((current) => ({ ...current, [name]: next })),
	};

	const choices = (name: string): Choice[] => initial.choices[name] ?? [];

	const optimizer = form.value("optimizer");

	async function submit(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		setError("");
		setBusy(true);

		try {
			const body: Record<string, unknown> = buildCreateJobBody(values);

			// The project is a field of the request envelope, not of the
			// configuration; an absent one means the default project.
			const project = initial.seed?.project ?? "";
			if (project !== "") body.project = project;

			const response = await fetch("/api/v1/jobs", {
				method: "POST",
				headers: { "Content-Type": "application/json", Accept: "application/json" },
				body: JSON.stringify(body),
			});

			if (!response.ok) {
				setError(await apiError(response));
				return;
			}

			const job = await response.json() as { id?: string };
			if (!job.id) {
				setError("The server created a job but did not name it.");
				return;
			}

			window.location.assign(`/jobs/${job.id}`);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : "Could not reach the server.");
		} finally {
			setBusy(false);
		}
	}

	return (
		<form className="card" onSubmit={submit}>
			{error ? (
				<div
					role="alert"
					className="card"
					style={{ backgroundColor: "var(--error-bg)", border: "1px solid var(--error-border)", marginBottom: "1.5rem" }}
				>
					<h3 style={{ color: "var(--error-text)", fontWeight: 600, marginBottom: "0.5rem" }}>Error</h3>
					<p style={{ color: "var(--error-text)", fontSize: "0.875rem" }}>{error}</p>
				</div>
			) : null}

			<fieldset style={sectionStyle}>
				<legend style={{ padding: 0 }}><h2 style={headingStyle}>Reference Image</h2></legend>
				<Text form={form} name="refPath" label="Image Path" required help="Enter the path to your reference image file on the server." />
				<Text
					form={form}
					name="canvasPath"
					label="Canvas Path (Optional)"
					help="Load existing canvas to continue from a previous result. Leave empty for blank canvas."
				/>
			</fieldset>

			<fieldset style={sectionStyle}>
				<legend style={{ padding: 0 }}><h2 style={headingStyle}>Optimization Parameters</h2></legend>
				<div style={gridStyle}>
					<Select
						form={form}
						choices={choices("optimizer")}
						name="optimizer"
						label="Optimizer"
						required
						help="CMA-ES is configured in the CMA-ES section, which appears when it is selected."
					/>
					<Select form={form} choices={choices("mode")} name="mode" label="Mode" required help="Optimization strategy" />
					<Num
						form={form}
						name="circles"
						label="Circles"
						required
						min={1}
						max={limits.maxCircles}
						help={`Number of circles (1-${limits.maxCircles})`}
					/>
					<Num
						form={form}
						name="iters"
						label="Iterations"
						required
						min={1}
						max={limits.maxIterations}
						help={`Max iterations (1-${limits.maxIterations})`}
					/>
					<Num
						form={form}
						name="popSize"
						label="Population Size"
						required
						min={limits.minPopulation}
						max={limits.maxPopulation}
						help={`Population size (${limits.minPopulation}-${limits.maxPopulation})`}
					/>
					<Num
						form={form}
						name="batchSize"
						label="Batch Size"
						min={0}
						max={limits.maxBatchSize}
						help="Batch mode only. Set equal to Circles to optimize them all together; 0 selects the automatic default."
					/>
					<Num form={form} name="seed" label="Random Seed" required help="0 for random, or set for reproducibility" />
					<Num
						form={form}
						name="optimizerEpochs"
						label="Optimizer Epochs"
						min={1}
						max={limits.maxOptimizerEpochs}
						help="Repeat each optimizer stage, retaining the best and reseeding with fresh diversity."
					/>
				</div>
			</fieldset>

			{optimizer === "cmaes" ? (
				<fieldset style={sectionStyle}>
					<legend style={{ padding: 0 }}><h2 style={headingStyle}>CMA-ES</h2></legend>
					<div style={gridStyle}>
						<Num
							form={form}
							name="initialSigma"
							label="Initial Sigma"
							step="any"
							placeholder={attr(limits.defaultInitialSigma)}
							help="Initial step size in the normalized [0,1] search box. Must be finite and positive; leave empty for the default."
						/>
						<Select
							form={form}
							choices={choices("covarianceMode")}
							name="covarianceMode"
							label="Covariance"
							help="Full covariance is limited in the number of optimizer dimensions it supports; choose block or separable above that."
						/>
						<Select
							form={form}
							choices={choices("restartStrategy")}
							name="restartStrategy"
							label="Restart Strategy"
							help="CMA-ES's own restart schedule, spending one stage budget across several runs."
						/>
					</div>
					<Check
						form={form}
						name="activeCMA"
						label="Active covariance adaptation"
						help="Negative rank-mu updates, which also learn from the worst candidates of each generation. Enabled by default."
					/>
				</fieldset>
			) : null}

			<fieldset style={sectionStyle}>
				<legend style={{ padding: 0 }}><h2 style={headingStyle}>Active-set Polishing</h2></legend>
				<Check
					form={form}
					name="polishingEnabled"
					label="Polish selected circles after the batch run"
					help="Batch mode only. Every strategy preserves draw order, and a sweep is kept only when the complete image improves."
				/>
				<div style={gridStyle}>
					<Select form={form} choices={choices("polishingStrategy")} name="polishingStrategy" label="Strategy" />
					<Num
						form={form}
						name="polishingActiveSetSize"
						label="Active Set Size"
						min={1}
						max={limits.maxBatchSize}
						help="Circles optimized together"
					/>
					<Num
						form={form}
						name="polishingMaxSweeps"
						label="Maximum Sweeps"
						min={1}
						max={limits.maxPolishingSweeps}
						help="Regional polishing continues to the next tile after a rejection"
					/>
					<Num form={form} name="polishingEpochs" label="Epochs per Sweep" min={1} max={limits.maxOptimizerEpochs} />
					<Num form={form} name="polishingIters" label="Iterations per Epoch" min={1} max={limits.maxIterations} />
					<Num
						form={form}
						name="polishingPopSize"
						label="Sweep Population"
						min={limits.minPopulation}
						max={limits.maxPopulation}
						help="Population for the active set, independent of the population size above"
					/>
					<Num
						form={form}
						name="polishingStagnationIters"
						label="Stagnation Iterations"
						min={1}
						max={limits.maxIterations}
						help="Must not exceed iterations per epoch"
					/>
					<Num
						form={form}
						name="polishingMinImprovement"
						label="Progress Threshold"
						min={limits.minPolishingMinImprovement}
						step="any"
						help="Absolute optimizer cost reduction that resets stagnation"
					/>
				</div>
			</fieldset>

			<fieldset style={sectionStyle}>
				<legend style={{ padding: 0 }}><h2 style={headingStyle}>Convergence Settings</h2></legend>
				<Check
					form={form}
					name="convergenceEnabled"
					label="Enable Convergence Detection"
					help="Stop early when optimizer can't improve further (sequential/batch modes only)."
				/>
				<div style={gridStyle}>
					<Num
						form={form}
						name="convergencePatience"
						label="Patience"
						min={1}
						max={limits.maxConvergencePatience}
						help="Iterations with no improvement before stopping"
					/>
					<Num
						form={form}
						name="convergenceThreshold"
						label="Threshold"
						min={limits.minConvergenceThreshold}
						max={limits.maxConvergenceThreshold}
						step={attr(limits.minConvergenceThreshold)}
						help="Minimum relative improvement (0.001 = 0.1%)"
					/>
				</div>
			</fieldset>

			<fieldset style={sectionStyle}>
				<legend style={{ padding: 0 }}><h2 style={headingStyle}>Early Stopping (Optimizer)</h2></legend>
				<p style={{ ...helpStyle, marginTop: 0, marginBottom: "1rem" }}>
					Applied per iteration inside a single optimizer run, in every mode. The convergence settings above instead
					count whole circles or batches. Leave these empty to disable early stopping and keep runs reproducible.
				</p>
				<div style={gridStyle}>
					<Num
						form={form}
						name="stopTargetCost"
						label="Target Cost"
						min={0}
						step="any"
						placeholder="disabled"
						help="Stop once the best cost reaches this absolute value"
					/>
					<Num
						form={form}
						name="stopStagnationIters"
						label="Stagnation Iterations"
						min={0}
						placeholder="disabled"
						help="Stop after N consecutive iterations without progress"
					/>
					<Num
						form={form}
						name="stopMinImprovement"
						label="Minimum Improvement"
						min={0}
						step="any"
						placeholder="any improvement"
						help="ABSOLUTE cost reduction counted as progress; requires stagnation iterations"
					/>
					<Num
						form={form}
						name="stopMinIters"
						label="Minimum Iterations"
						min={0}
						placeholder="0"
						help="Iterations completed before any early stop can fire"
					/>
				</div>
			</fieldset>

			<fieldset style={sectionStyle}>
				<legend style={{ padding: 0 }}><h2 style={headingStyle}>Advanced Metrics</h2></legend>
				<Check
					form={form}
					name="enableSSIM"
					label="Enable SSIM"
					help="Track structural similarity during the run. This adds periodic image rendering and metric work."
				/>
			</fieldset>

			<div className="action-row" style={{ paddingTop: "1rem", borderTop: "1px solid var(--border-color)" }}>
				<a href="/jobs" className="btn" style={{ backgroundColor: "var(--border-color)", textDecoration: "none" }}>
					Cancel
				</a>
				<button type="submit" className="btn btn-primary" disabled={busy}>
					{busy ? "Creating…" : "Create Job"}
				</button>
			</div>
		</form>
	);
}
