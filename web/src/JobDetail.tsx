import { Chart } from "chart.js";
import type { Chart as ChartInstance, TooltipItem } from "chart.js";
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import { applyAxisTheme, useChartTheme, useLineChart } from "./charts";
import type { Palette } from "./charts";
import {
	formatCompactNumber,
	formatElapsedDuration,
	formatFileSize,
	formatReferenceDimensions,
	formatWallClock,
	stateBadgeStyle,
	stateClass,
	stateLabel,
} from "./format";
import { ImageViewer } from "./ImageViewer";
import { fetchJSON, useLiveResource } from "./live";
import type { UIEvent } from "./live";
import { LiveStatus } from "./LiveStatus";
import {
	browserStorage,
	normalizeAutoRefresh,
	normalizeVisibleMetrics,
	PREFERENCE_KEYS,
	readPreference,
} from "./prefs";
import type { Colormap, MetricID } from "./prefs";

// The job detail island. It replaces 865 lines of inline script that lived in
// internal/ui/detail.templ and owned the metric panel, a hand-rolled SVG
// sparkline, the parameter viewer, the ETA and throughput arithmetic and the
// report download -- plus, from beside it, the job-controls island that owned
// the action row. Both are here now, because they were never two things: the
// controls' live resource was the metric panel's only source of live data, and
// they talked to each other through a custom DOM event for want of a common
// parent.
//
// The mount point is the whole detail body, so everything the server rendered
// is the fallback and the hydration seed at once. The seed arrives as
// #job-detail-data rather than as data-* attributes: the panel needs the entire
// job, configuration included, and forty formatted attributes would be a worse
// version of one JSON blob.

export type MetricSample = {
	iteration: number;
	evaluations: number;
	cost: number;
	psnr?: number | null;
	psnrInfinite?: boolean;
	ssim?: number | null;
	cps: number;
	timestamp: string;
};

export type CircleParameter = {
	number: number;
	x: number;
	y: number;
	radius: number;
	red: number;
	green: number;
	blue: number;
	opacity: number;
};

export type JobActions = {
	pause: boolean;
	resume: boolean;
	cancel: boolean;
	delete: boolean;
	polish: boolean;
};

// JobDetailSeed mirrors ui.JobDetail, the view model internal/ui/detail.templ
// renders and serializes into #job-detail-data. Configuration is read from here
// and never refetched: it cannot change while a job runs, and /status carries
// only the raw JobConfig, whose resolved forms (the optimizer a blank field
// defaults to, CMA-ES's normalized sigma) are Go functions this island must not
// reimplement.
export type JobDetailSeed = {
	id: string;
	state: string;
	refPath: string;
	mode: string;
	optimizer: string;
	variant?: string;
	initialSigma: number;
	covarianceMode: string;
	activeCMA: boolean;
	restartStrategy: string;
	evaluationWidth?: number;
	effectiveBackend?: string;
	backendDegraded?: boolean;
	fastCompositing: boolean;
	circles: number;
	iterations: number;
	evaluations: number;
	maxIterations: number;
	itersPerEpoch: number;
	optimizerEpochs: number;
	optimizerRestarts: number;
	popSize: number;
	polishingEnabled: boolean;
	polishingOnly: boolean;
	canPolish: boolean;
	polishingStrategy: string;
	polishingActiveSetSize: number;
	polishingMaxSweeps: number;
	polishingEpochs: number;
	polishingIters: number;
	polishingPopSize: number;
	polishingStagnationIters: number;
	polishingMinImprovement: number;
	bestCost: number;
	candidateCost?: number;
	candidatePsnr?: number;
	candidatePsnrInfinite?: boolean;
	bestRevision: number;
	initialCost: number;
	startTime: string;
	endTime?: string;
	elapsed: number;
	cps: number;
	termination?: string;
	error?: string;
	refWidth?: number;
	refHeight?: number;
	refSize?: number;
	psnr?: number | null;
	psnrInfinite?: boolean;
	ssim?: number | null;
	ssimEnabled: boolean;
	metricHistory: MetricSample[];
	parameters: CircleParameter[];
};

// JobStatusPayload is what GET /api/v1/jobs/{id}/status serves, narrowed to
// what this island reads. refWidth, refHeight and refSize were added for it:
// they were view-model fields with no endpoint behind them, so a refetch used
// to be unable to describe the reference image at all. All three are omitted
// when the file cannot be probed, which is why they are optional here -- a zero
// would claim a real 0x0 image.
export type JobStatusPayload = {
	id: string;
	state: string;
	bestCost: number;
	bestRevision: number;
	candidateCost?: number;
	candidatePsnr?: number;
	candidatePsnrInfinite?: boolean;
	initialCost: number;
	psnr?: number | null;
	psnrInfinite?: boolean;
	ssim?: number | null;
	iterations: number;
	evaluations: number;
	maxIterations?: number;
	actions?: JobActions;
	evaluationWidth?: number;
	effectiveBackend?: string;
	backendDegraded?: boolean;
	refWidth?: number;
	refHeight?: number;
	refSize?: number;
	termination?: string;
	elapsed: number;
	cps: number;
	startTime: string;
	endTime?: string;
	error?: string;
};

// A history sample with its timestamp already resolved to an instant, which is
// the form every derived figure below wants. `instant` is null for a sample
// that never carried a clock; those samples still plot, they just cannot take
// part in a rate.
export type HistorySample = {
	iteration: number;
	evaluations: number;
	cost: number;
	psnr: number | null;
	psnrInfinite: boolean;
	ssim: number | null;
	cps: number;
	instant: number | null;
};

const UNAVAILABLE = "—";

// Go writes a time.Time it never set as this instant. Date.parse is happy to
// turn it into a number, which would make a sample with no clock look like the
// oldest sample there is; the Go mirror recognizes it as IsZero.
const GO_ZERO_TIME = "0001-01-01";

export function parseSampleInstant(value: string | null | undefined): number | null {
	if (!value || value.startsWith(GO_ZERO_TIME)) return null;
	const parsed = Date.parse(value);
	return Number.isNaN(parsed) ? null : parsed;
}

function finiteNumber(value: unknown, fallback: number): number {
	const parsed = typeof value === "number" ? value : Number.parseFloat(String(value ?? ""));
	return Number.isFinite(parsed) ? parsed : fallback;
}

function optionalNumber(value: unknown): number | null {
	const parsed = typeof value === "number" ? value : Number.parseFloat(String(value ?? ""));
	return Number.isFinite(parsed) ? parsed : null;
}

export function normalizeHistorySample(raw: MetricSample): HistorySample {
	return {
		iteration: Math.trunc(finiteNumber(raw.iteration, 0)),
		evaluations: Math.trunc(finiteNumber(raw.evaluations, 0)),
		cost: finiteNumber(raw.cost, 0),
		psnr: optionalNumber(raw.psnr),
		psnrInfinite: raw.psnrInfinite === true,
		ssim: optionalNumber(raw.ssim),
		cps: finiteNumber(raw.cps, 0),
		instant: parseSampleInstant(raw.timestamp),
	};
}

// latestHistorySample is the newest sample carrying an instant, or, when none
// does, simply the newest sample. The distinction matters: throughput needs two
// instants to divide by, while the average it also reports does not.
export function latestHistorySample(history: HistorySample[]): HistorySample | null {
	for (let i = history.length - 1; i >= 0; i--) {
		if (history[i].instant !== null) return history[i];
	}
	return history.length > 0 ? history[history.length - 1] : null;
}

// previousHistorySample walks back from upperExclusive for the newest sample
// strictly older than the target. Two samples may share an instant when the
// clock is coarser than the loop, so an equal instant is only older when its
// iteration is lower.
export function previousHistorySample(
	history: HistorySample[],
	upperExclusive: number,
	target: HistorySample,
): HistorySample | null {
	const limit = Math.min(Math.max(0, upperExclusive), history.length - 1);
	if (limit < 0 || history.length === 0) return null;

	for (let i = limit; i >= 0; i--) {
		const candidate = history[i];
		if (candidate.instant === null) continue;
		if (target.instant === null) return candidate;
		if (
			candidate.instant < target.instant ||
			(candidate.instant === target.instant && candidate.iteration < target.iteration)
		) {
			return candidate;
		}
	}
	return null;
}

// averageCPS is the run's own average as the newest sample recorded it. The
// job-level figure is the fallback for a run that has recorded nothing yet.
export function averageCPS(history: HistorySample[], fallback: number): number {
	const latest = latestHistorySample(history);
	if (!latest) return fallback;
	return Number.isFinite(latest.cps) ? latest.cps : 0;
}

// currentCPS is the instantaneous rate across the two newest samples, which is
// what says whether a run is speeding up or slowing down; the average cannot.
// It falls back to the job-level figure whenever the two samples cannot be
// differenced -- no second sample, no clock, no advance in evaluations.
export function currentCPS(history: HistorySample[], circles: number, fallback: number): number {
	const latest = latestHistorySample(history);
	if (!latest || latest.instant === null || circles <= 0) return fallback;

	const previous = previousHistorySample(history, history.length - 2, latest);
	if (!previous || previous.evaluations === latest.evaluations || previous.instant === null) {
		return fallback;
	}

	const deltaEvaluations = latest.evaluations - previous.evaluations;
	const deltaMilliseconds = latest.instant - previous.instant;
	if (deltaEvaluations <= 0 || deltaMilliseconds <= 0) return fallback;

	return (deltaEvaluations * circles) / (deltaMilliseconds / 1000);
}

// iterationRate is iterations per second across the two newest samples, and the
// only rate an ETA may be built from: the average would keep promising the
// speed of a warm-up that is long over.
export function iterationRate(history: HistorySample[]): number {
	const latest = latestHistorySample(history);
	if (!latest || latest.instant === null) return 0;

	const previous = previousHistorySample(history, history.length - 2, latest);
	if (!previous || previous.instant === null) return 0;

	const deltaIterations = latest.iteration - previous.iteration;
	const deltaMilliseconds = latest.instant - previous.instant;
	if (deltaIterations <= 0 || deltaMilliseconds <= 0) return 0;

	return (deltaIterations * 1000) / deltaMilliseconds;
}

// costImprovementRate is the cost change per iteration between the two newest
// samples, with the arrow saying which way it went. An em dash means the two
// samples do not support the division, not that the rate is zero -- a zero rate
// is a stalled run and reads "→ 0.0000 / iter".
export function costImprovementRate(history: HistorySample[]): string {
	if (history.length < 2) return UNAVAILABLE;

	const latest = history[history.length - 1];
	const previous = history[history.length - 2];
	if (!Number.isFinite(latest.cost) || !Number.isFinite(previous.cost)) return UNAVAILABLE;

	const deltaIterations = latest.iteration - previous.iteration;
	if (deltaIterations <= 0) return UNAVAILABLE;

	const rate = (latest.cost - previous.cost) / deltaIterations;
	const arrow = rate < 0 ? "↓" : rate > 0 ? "↑" : "→";
	return `${arrow} ${formatCompactNumber(Math.abs(rate), 4)} / iter`;
}

// formatETA prints a remaining duration at the resolution a reader can act on:
// seconds under a minute, minutes and seconds under an hour, hours and minutes
// above it. It is deliberately not formatElapsedDuration, which is an elapsed
// time and keeps a decimal second.
export function formatETA(seconds: number): string {
	if (!Number.isFinite(seconds) || seconds < 0) return UNAVAILABLE;
	if (seconds < 60) return `${Math.round(seconds)}s`;

	const minutes = Math.floor(seconds / 60);
	if (seconds < 3600) return `${minutes}m ${Math.floor(seconds % 60)}s`;

	return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

// etaLabel is the projected time to the planned iteration count. It is an em
// dash rather than a guess wherever the projection has no basis: no planned
// count, no recorded history, or a rate of zero.
export function etaLabel(history: HistorySample[], maxIterations: number): string {
	if (maxIterations <= 0 || history.length === 0) return UNAVAILABLE;

	// The iteration the projection counts down from is the newest sample's,
	// clock or no clock; only the rate needs an instant to divide by.
	const completed = history[history.length - 1].iteration;
	if (completed < 0) return UNAVAILABLE;
	if (completed >= maxIterations) return "0s";

	const rate = iterationRate(history);
	if (rate <= 0) return UNAVAILABLE;

	return formatETA((maxIterations - completed) / rate);
}

// colorChannel and parameterDescription mirror the Go helpers of the same names
// in internal/ui/detail.templ, so a circle row rewritten by a live refresh
// reads exactly as the server-rendered one it replaces.
export function colorChannel(value: number): number {
	return Math.round(Math.min(1, Math.max(0, value)) * 255);
}

export function parameterDescription(circle: CircleParameter): string {
	return (
		`Circle ${circle.number}: (${circle.x.toFixed(2)}, ${circle.y.toFixed(2)}, ` +
		`${circle.radius.toFixed(2)}) RGB(${colorChannel(circle.red)}, ` +
		`${colorChannel(circle.green)}, ${colorChannel(circle.blue)}) α=${circle.opacity.toFixed(3)}`
	);
}

/**
 * optimizerSchedule states the budget one stage actually spent. Mirrors
 * optimizerSchedule in internal/ui/detail.templ, and the pair is pinned by
 * job-detail-parity.json: the restart clause is dropped at a single attempt so
 * an ordinary job does not read as though something extra happened to it.
 */
export function optimizerSchedule(restarts: number, epochs: number, itersPerEpoch: number): string {
	const schedule = `${epochs} × ${itersPerEpoch} iterations`;

	return restarts > 1 ? `${restarts} restarts × ${schedule}` : schedule;
}

export function progressPercent(iterations: number, maximum: number): number {
	if (maximum <= 0) return 0;
	return Math.min(100, Math.max(0, (iterations / maximum) * 100));
}

// The chart's series and the windows a reader can cut them to. Both lists are
// the ones the inline script offered, values included, because a page reloaded
// mid-run must not silently change what it is showing.
const SERIES = ["cost", "psnr", "ssim", "cps"] as const;
type Series = (typeof SERIES)[number];

const WINDOWS = ["all", "100", "250", "500", "1000"] as const;
type WindowChoice = (typeof WINDOWS)[number];

const SERIES_LABELS: Record<Series, string> = {
	cost: "Cost",
	psnr: "PSNR",
	ssim: "SSIM",
	cps: "CPS",
};

const AXIS_TITLES: Record<Series, string> = {
	cost: "COST",
	psnr: "PSNR (dB)",
	ssim: "SSIM",
	cps: "CPS (circles/sec)",
};

export type ChartPoint = { iteration: number; value: number };

export function seriesValue(sample: HistorySample, series: Series): number | null {
	const value = series === "cost" ? sample.cost : series === "cps" ? sample.cps : sample[series];
	return typeof value === "number" && Number.isFinite(value) ? value : null;
}

// selectMetricPoints drops the samples that have no value for the chosen series
// -- PSNR before the first audit, SSIM on a job that never enabled it -- and
// then cuts the tail the window asks for. The total it reports is the count
// before the cut, which is what "showing 100 of 4 812" means.
export function selectMetricPoints(
	history: HistorySample[],
	series: Series,
	window: WindowChoice,
): { points: ChartPoint[]; total: number } {
	const all: ChartPoint[] = [];
	for (const sample of history) {
		const value = seriesValue(sample, series);
		if (value !== null) all.push({ iteration: sample.iteration, value });
	}
	const size = Number.parseInt(window, 10);
	return { points: Number.isFinite(size) ? all.slice(-size) : all, total: all.length };
}

export function formatMetricValue(series: Series, value: number): string {
	if (series === "psnr") return `${value.toFixed(2)} dB`;
	if (series === "cps") return `${value.toFixed(2)} cps`;
	return value.toFixed(4);
}

export function formatAxisValue(series: Series, value: number): string {
	const magnitude = Math.abs(value);
	if (magnitude !== 0 && (magnitude >= 10000 || magnitude < 0.001)) return value.toExponential(2);
	if (series === "ssim") return value.toFixed(3);
	if (series === "psnr") return value.toFixed(1);
	if (series === "cps") return value.toFixed(2);
	return value.toFixed(magnitude < 10 ? 3 : 1);
}

// metricBounds is the 5% padding the hand-rolled SVG applied, kept because it
// is what stops a flat series from being drawn as a line hugging the frame. A
// series with no spread at all is padded by 5% of its own magnitude, or by 1
// when that is zero too.
export function metricBounds(points: ChartPoint[]): { min: number; max: number } | null {
	if (points.length === 0) return null;
	let minimum = points[0].value;
	let maximum = points[0].value;
	for (const point of points) {
		if (point.value < minimum) minimum = point.value;
		if (point.value > maximum) maximum = point.value;
	}
	const range = maximum - minimum;
	const padding = range === 0 ? Math.max(Math.abs(maximum) * 0.05, 1) : range * 0.05;
	return { min: minimum - padding, max: maximum + padding };
}

// ---------------------------------------------------------------------------
// Live state
// ---------------------------------------------------------------------------

type DetailState = {
	seed: JobDetailSeed;
	status: JobStatusPayload | null;
	history: HistorySample[];
};

function readSeed(): JobDetailSeed | null {
	const script = document.getElementById("job-detail-data");
	if (!script) return null;
	try {
		const parsed = JSON.parse(script.textContent || "null") as JobDetailSeed | null;
		return parsed && typeof parsed.id === "string" ? parsed : null;
	} catch {
		return null;
	}
}

// seedActions is what the server's own view of the job implies about which
// actions are offered. /status computes the same set with the store and the
// schedule in hand, so this is the first paint's answer and the endpoint's is
// the authoritative one.
function seedActions(seed: JobDetailSeed): JobActions {
	return {
		pause: seed.state === "running",
		resume: seed.state === "paused",
		cancel: seed.state === "pending" || seed.state === "running" || seed.state === "paused",
		delete: seed.state === "completed" || seed.state === "failed" || seed.state === "cancelled",
		polish: seed.canPolish,
	};
}

// statusFromSeed is the first value of the live resource: the server's own
// numbers, in the shape the endpoint serves them, so the panel renders the same
// figures before the first fetch as after it.
function statusFromSeed(seed: JobDetailSeed): JobStatusPayload {
	return {
		id: seed.id,
		state: seed.state,
		bestCost: seed.bestCost,
		bestRevision: seed.bestRevision,
		candidateCost: seed.candidateCost,
		candidatePsnr: seed.candidatePsnr,
		candidatePsnrInfinite: seed.candidatePsnrInfinite,
		initialCost: seed.initialCost,
		psnr: seed.psnr,
		psnrInfinite: seed.psnrInfinite,
		ssim: seed.ssim,
		iterations: seed.iterations,
		evaluations: seed.evaluations,
		maxIterations: seed.maxIterations,
		actions: seedActions(seed),
		evaluationWidth: seed.evaluationWidth,
		effectiveBackend: seed.effectiveBackend,
		backendDegraded: seed.backendDegraded,
		refWidth: seed.refWidth,
		refHeight: seed.refHeight,
		refSize: seed.refSize,
		termination: seed.termination,
		elapsed: seed.elapsed,
		cps: seed.cps,
		startTime: seed.startTime,
		endTime: seed.endTime,
		error: seed.error,
	};
}

// appendProgress folds one live frame into the recorded history, which is what
// keeps the chart moving between the thirty-second reconciliations. A frame
// with no clock of its own is stamped on arrival: an unstamped sample would
// take no part in the rates it is the newest evidence for.
function appendProgress(
	history: HistorySample[],
	status: JobStatusPayload,
	timestamp: string | undefined,
): HistorySample[] {
	const sample: HistorySample = {
		iteration: Math.trunc(finiteNumber(status.iterations, 0)),
		evaluations: Math.trunc(finiteNumber(status.evaluations, 0)),
		cost: finiteNumber(status.bestCost, 0),
		psnr: optionalNumber(status.psnr),
		psnrInfinite: status.psnrInfinite === true,
		ssim: optionalNumber(status.ssim),
		cps: finiteNumber(status.cps, 0),
		instant: parseSampleInstant(timestamp) ?? Date.now(),
	};
	return [...history, sample];
}

function reduceDetail(current: DetailState, event: UIEvent) {
	const id = current.seed.id;
	if (event.type === "job.deleted" && event.jobId === id) {
		window.location.assign("/jobs");
		return { value: current };
	}
	if (event.type !== "job.upsert" || event.jobId !== id || !event.progress) {
		return { value: current };
	}

	const previous = current.status ?? statusFromSeed(current.seed);
	const status: JobStatusPayload = { ...previous, ...event.progress, id };
	return {
		value: {
			...current,
			status,
			history: appendProgress(current.history, status, event.progress.timestamp),
		},
		refresh: previous.state !== status.state,
	};
}

// ---------------------------------------------------------------------------
// The island
// ---------------------------------------------------------------------------

export function JobDetailIsland({ root }: { root: HTMLElement }) {
	const seed = useMemo(readSeed, []);
	if (!seed) {
		// Without a seed there is nothing to mount over: leave the server's
		// markup where it is rather than replacing a complete page with an empty
		// one. React has already emptied the root by the time this renders, so
		// say why instead of rendering nothing.
		return (
			<p style={{ color: "var(--text-muted)" }}>
				The job data this page was seeded with could not be read. Reload the page.
			</p>
		);
	}
	// The mount point itself carries no props: everything this island needs is
	// in the seed, and the root is only where React attaches.
	void root;
	return <JobDetailBody seed={seed} />;
}

function JobDetailBody({ seed }: { seed: JobDetailSeed }) {
	const initial = useMemo<DetailState>(
		() => ({
			seed,
			status: statusFromSeed(seed),
			history: (seed.metricHistory ?? []).map(normalizeHistorySample),
		}),
		[seed],
	);

	const { value, status: liveState, error: liveError, refresh } = useLiveResource<DetailState>({
		initial,
		load: async (signal, current) => {
			const [status, history] = await Promise.all([
				fetchJSON<JobStatusPayload>(`/api/v1/jobs/${encodeURIComponent(seed.id)}/status`, signal),
				fetchJSON<MetricSample[] | null>(
					`/api/v1/jobs/${encodeURIComponent(seed.id)}/metrics?limit=1000`,
					signal,
				),
			]);
			return {
				seed: current.seed,
				status,
				history: Array.isArray(history) ? history.map(normalizeHistorySample) : current.history,
			};
		},
		reduce: reduceDetail,
	});

	const status = value.status ?? statusFromSeed(seed);
	const history = value.history;
	const jobState = status.state;

	// Monotonic, because two things advance it: the live frames and the /status
	// refetch. A response that started before the newest frame must not walk the
	// images back to an older render.
	const [revision, setRevision] = useState(seed.bestRevision);
	useEffect(() => {
		const next = Number(status.bestRevision);
		if (Number.isFinite(next)) setRevision((current) => (next > current ? next : current));
	}, [status.bestRevision]);

	// The heatmap colormap is this island's now. It used to travel from the
	// image viewer to the detail script through a data attribute on the viewer's
	// root, because the two lived in different islands; the viewer is a child
	// here, so it reports through a callback and the difference download link
	// and the report render read the same state.
	const [colormap, setColormap] = useState<Colormap>("turbo");

	// The metric cards a reader has switched off in Settings. The storage event
	// only fires in *other* tabs, which is exactly when this page cannot know on
	// its own that the set changed.
	const [visibleMetrics, setVisibleMetrics] = useState<MetricID[]>(() =>
		normalizeVisibleMetrics(readPreference(browserStorage(), PREFERENCE_KEYS.visibleMetrics)),
	);
	useEffect(() => {
		const onStorage = (event: StorageEvent) => {
			if (event.key === PREFERENCE_KEYS.visibleMetrics) {
				setVisibleMetrics(normalizeVisibleMetrics(readPreference(browserStorage(), PREFERENCE_KEYS.visibleMetrics)));
			}
		};
		window.addEventListener("storage", onStorage);
		return () => window.removeEventListener("storage", onStorage);
	}, []);

	// Opt-in polling for a reader whose event stream a buffering proxy has
	// swallowed. It moved here from the image-viewer island together with the
	// rest of that island's job-page duties; zero, the default, leaves the page
	// on the live stream alone.
	useEffect(() => {
		if (!ACTIVE_STATES.includes(jobState)) return;
		const interval = normalizeAutoRefresh(readPreference(browserStorage(), PREFERENCE_KEYS.autoRefresh));
		if (interval <= 0) return;
		const timer = window.setInterval(() => void refresh(), interval);
		const stop = () => window.clearInterval(timer);
		window.addEventListener("pagehide", stop);
		return () => {
			stop();
			window.removeEventListener("pagehide", stop);
		};
	}, [jobState, refresh]);

	const [parameters, setParameters] = useState<CircleParameter[]>(seed.parameters ?? []);
	const [parametersOpen, setParametersOpen] = useState(false);
	const refreshingParameters = useRef(false);

	const refreshParameters = useCallback(async () => {
		if (refreshingParameters.current) return;
		refreshingParameters.current = true;
		try {
			const response = await fetch(`/api/v1/jobs/${encodeURIComponent(seed.id)}/params.json`, {
				cache: "no-store",
				headers: { Accept: "application/json" },
			});
			// A job that has not committed a result yet answers 404, which is a
			// state and not a failure: keep whatever is on screen.
			if (response.status === 404 || !response.ok) return;
			const snapshot = (await response.json()) as { circles?: CircleParameter[] };
			setParameters(snapshot.circles ?? []);
		} catch (reason) {
			console.error("Unable to refresh parameters", reason);
		} finally {
			refreshingParameters.current = false;
		}
	}, [seed.id]);

	// The viewer is only refreshed while it is open, exactly as the inline
	// script did: the list is the one part of this page whose refresh costs a
	// request nobody asked for.
	useEffect(() => {
		if (parametersOpen) void refreshParameters();
	}, [parametersOpen, refreshParameters, status.bestRevision]);

	const hasParameters = parameters.length > 0;
	const actions = status.actions ?? seedActions(seed);

	return (
		<>
			<div style={{ marginBottom: "2rem" }}>
				<div className="row-between" style={{ marginBottom: "1rem" }}>
					<div>
						<a
							href="/jobs"
							style={{
								color: "var(--text-muted)",
								textDecoration: "none",
								fontSize: "0.875rem",
								marginBottom: "0.5rem",
								display: "inline-block",
							}}
						>
							← Back to Jobs
						</a>
						<h1 style={{ fontSize: "2rem", fontWeight: 700, fontFamily: "monospace" }}>
							{seed.id.slice(0, 8)}...
						</h1>
					</div>
					<JobActionRow
						jobId={seed.id}
						state={jobState}
						actions={actions}
						liveState={liveState}
						liveError={liveError}
						statusError={status.error}
						refresh={refresh}
					/>
				</div>
			</div>
			{status.error ? (
				<div
					className="card"
					style={{
						backgroundColor: "var(--error-bg)",
						border: "1px solid var(--error-border)",
						marginBottom: "1.5rem",
					}}
				>
					<h3 style={{ color: "var(--error-text)", fontWeight: 600, marginBottom: "0.5rem" }}>Error</h3>
					<p style={{ color: "var(--error-text)", fontFamily: "monospace", fontSize: "0.875rem" }}>
						{status.error}
					</p>
				</div>
			) : null}
			<div className="detail-stack">
				<MetricSummary
					seed={seed}
					status={status}
					history={history}
					visibleMetrics={visibleMetrics}
				/>
				<ConfigurationCard seed={seed} status={status} />
				<DownloadsCard
					jobId={seed.id}
					colormap={colormap}
					available={hasParameters}
				/>
				<ParametersCard
					jobId={seed.id}
					circles={seed.circles}
					parameters={parameters}
					onToggle={setParametersOpen}
				/>
				<ImageViewer
					jobId={seed.id}
					revision={revision}
					jobState={jobState}
					defaultMode="side-by-side"
					referenceURL={`/api/v1/jobs/${encodeURIComponent(seed.id)}/ref.png`}
					bestURL={`/api/v1/jobs/${encodeURIComponent(seed.id)}/best.png`}
					diffURL={`/api/v1/jobs/${encodeURIComponent(seed.id)}/diff.png`}
					metadata={{
						dimensions: formatReferenceDimensions(status.refWidth ?? 0, status.refHeight ?? 0),
						fileSize: (status.refSize ?? 0) > 0 ? formatFileSize(status.refSize ?? 0) : "",
						bytes: (status.refSize ?? 0) > 0 ? `${status.refSize} bytes` : "",
					}}
					extraClass="detail-images"
					onColormap={setColormap}
				/>
				<MetricHistoryCard history={history} ssimEnabled={seed.ssimEnabled} />
			</div>
		</>
	);
}

const ACTIVE_STATES = ["running", "pending", "paused"];

// ---------------------------------------------------------------------------
// Action row
// ---------------------------------------------------------------------------

function JobActionRow({
	jobId,
	state,
	actions,
	liveState,
	liveError,
	statusError,
	refresh,
}: {
	jobId: string;
	state: string;
	actions: JobActions;
	liveState: "connecting" | "connected" | "reconnecting";
	liveError: string | null;
	statusError?: string;
	refresh: () => Promise<void>;
}) {
	const [busy, setBusy] = useState<string | null>(null);

	async function action(name: "pause" | "resume" | "cancel") {
		if ((name === "pause" || name === "cancel") && !window.confirm(`${name[0].toUpperCase() + name.slice(1)} this job?`)) return;
		setBusy(name);
		try {
			await post(`/api/v1/jobs/${encodeURIComponent(jobId)}/${name}`);
			await refresh();
		} catch (reason) {
			window.alert(reason instanceof Error ? reason.message : `Unable to ${name} job`);
		} finally {
			setBusy(null);
		}
	}

	async function deleteJob() {
		if (!window.confirm("Delete this job? This cannot be undone.")) return;
		setBusy("delete");
		try {
			const response = await fetch(`/api/v1/jobs/${encodeURIComponent(jobId)}`, { method: "DELETE" });
			if (!response.ok) throw await apiError(response);
			window.location.assign("/jobs");
		} catch (reason) {
			window.alert(reason instanceof Error ? reason.message : "Unable to delete job");
			setBusy(null);
		}
	}

	async function polish() {
		setBusy("polish");
		try {
			const response = await fetch(`/api/v1/jobs/${encodeURIComponent(jobId)}/polish`, { method: "POST" });
			if (!response.ok) throw await apiError(response);
			const result = (await response.json()) as { jobId: string };
			window.location.assign(`/jobs/${result.jobId}`);
		} catch (reason) {
			window.alert(reason instanceof Error ? reason.message : "Unable to start polishing");
			setBusy(null);
		}
	}

	return (
		<div className="action-row">
			<span className={stateClass(state)} style={stateBadgeStyle(state)}>
				{stateLabel(state)}
			</span>
			{actions.pause ? <ActionButton label="Pause job" busy={busy === "pause"} onClick={() => void action("pause")} warning /> : null}
			{actions.resume ? <ActionButton label="Resume job" busy={busy === "resume"} onClick={() => void action("resume")} primary /> : null}
			{actions.cancel ? <ActionButton label="Cancel job" busy={busy === "cancel"} onClick={() => void action("cancel")} danger /> : null}
			{actions.delete ? <ActionButton label="Delete job" busy={busy === "delete"} onClick={() => void deleteJob()} danger /> : null}
			{actions.polish ? <ActionButton label="Polish weak circles" busy={busy === "polish"} onClick={() => void polish()} primary /> : null}
			<ActionButton label="Refresh" glyph="⟳" busy={false} onClick={() => void refresh()} />
			<LiveStatus
				state={liveState}
				error={liveError}
				style={{ fontSize: "0.75rem", marginTop: "0.35rem", flexBasis: "100%", textAlign: "right" }}
			/>
			{statusError ? <div style={{ color: "var(--error-text)", fontSize: "0.75rem" }}>{statusError}</div> : null}
		</div>
	);
}

function ActionButton({
	label,
	glyph,
	busy,
	onClick,
	danger,
	warning,
	primary,
}: {
	label: string;
	glyph?: string;
	busy: boolean;
	onClick: () => void;
	danger?: boolean;
	warning?: boolean;
	primary?: boolean;
}) {
	// btn-danger and btn-warning pair the accent background with their own
	// foreground token. An inline --error-color background keeps btn-primary's
	// white text, which measures 2.3:1 against the dark palette's #f87171.
	//
	// The three variants are the ones the templ fallback uses for the same four
	// buttons (internal/ui/detail.templ), and they have to stay the same three:
	// the fallback is what a reader sees until this island mounts, and a Pause
	// button that is amber before mount and grey after it is a flicker with no
	// meaning behind it.
	const variant = danger ? " btn-danger" : warning ? " btn-warning" : primary ? " btn-primary" : "";
	return (
		<button disabled={busy} aria-busy={busy} onClick={onClick} className={`btn${variant}`}>
			{/* The glyph is decoration; without aria-hidden it joins the button's
			    accessible name as "⟳ Refresh". */}
			{glyph ? <span aria-hidden="true">{glyph} </span> : null}
			{busy ? "Working…" : label}
		</button>
	);
}

async function post(url: string): Promise<void> {
	const response = await fetch(url, { method: "POST" });
	if (!response.ok) throw await apiError(response);
}

async function apiError(response: Response): Promise<Error> {
	try {
		const payload = (await response.json()) as { error?: { message?: string }; message?: string };
		return new Error(payload.error?.message ?? payload.message ?? `Request failed: ${response.status}`);
	} catch {
		return new Error(`Request failed: ${response.status}`);
	}
}

// ---------------------------------------------------------------------------
// Metric summary
// ---------------------------------------------------------------------------

const MUTED = { fontSize: "0.875rem", color: "var(--text-muted)", marginBottom: "0.25rem" } as const;
const FIGURE = { fontSize: "1.5rem", fontWeight: 600 } as const;
const NOTE = { fontSize: "0.75rem", color: "var(--text-muted)", marginTop: "0.25rem" } as const;

function statusBorderColor(state: string): string {
	switch (state) {
		case "running":
			return "var(--info-text)";
		case "paused":
		case "cancelled":
			return "var(--warning-text)";
		case "completed":
			return "var(--success-text)";
		case "failed":
			return "var(--error-text)";
		default:
			return "var(--text-muted)";
	}
}

function psnrText(psnr: number | null | undefined, infinite: boolean | undefined): string {
	if (infinite) return "∞";
	return typeof psnr === "number" && Number.isFinite(psnr) ? psnr.toFixed(2) : UNAVAILABLE;
}

function MetricSummary({
	seed,
	status,
	history,
	visibleMetrics,
}: {
	seed: JobDetailSeed;
	status: JobStatusPayload;
	history: HistorySample[];
	visibleMetrics: MetricID[];
}) {
	const shown = (metric: MetricID) => visibleMetrics.includes(metric);
	const maxIterations = status.maxIterations ?? seed.maxIterations;
	const percent = progressPercent(status.iterations, maxIterations);
	const candidate = status.candidateCost;

	return (
		<div
			className="card detail-summary"
			style={{ marginBottom: "1.5rem", borderLeft: `6px solid ${statusBorderColor(status.state)}` }}
		>
			<h2 style={{ fontSize: "1.25rem", fontWeight: 600, marginBottom: "1rem" }}>Metrics</h2>
			<div
				style={{
					display: "grid",
					gridTemplateColumns: "repeat(auto-fit, minmax(min(160px, 100%), 1fr))",
					gap: "1rem",
				}}
			>
				{shown("cost") ? (
					<div data-metric-card="cost">
						<div style={MUTED}>Audited Best Cost</div>
						<div style={FIGURE} data-metric="best-cost">
							{status.bestCost.toFixed(4)}
						</div>
						<div style={{ fontSize: "0.75rem", color: "var(--text-muted)", marginTop: "0.25rem" }}>
							RGB mean squared error · committed and checkpoint-safe · lower is better
						</div>
						{status.initialCost > 0 && status.bestCost < status.initialCost ? (
							<div style={{ fontSize: "0.75rem", color: "var(--success-text-strong)", marginTop: "0.25rem" }}>
								↓ {((1 - status.bestCost / status.initialCost) * 100).toFixed(1)}% improvement
							</div>
						) : null}
						<div style={NOTE}>
							<span>Cost change / iter:</span>
							<span data-metric="cost-improvement-rate" style={{ marginLeft: "0.35rem" }}>
								{costImprovementRate(history)}
							</span>
						</div>
					</div>
				) : null}
				{typeof candidate === "number" ? (
					<div
						id="candidate-metrics"
						style={{
							display: "block",
							padding: "0.75rem",
							border: "1px solid var(--primary-color)",
							borderRadius: "0.5rem",
							background: "color-mix(in srgb, var(--primary-color) 8%, transparent)",
						}}
					>
						<div style={MUTED}>In-flight Candidate</div>
						<div style={FIGURE} data-metric="candidate-cost">
							{candidate.toFixed(4)}
						</div>
						<div
							style={{ fontSize: "0.75rem", color: "var(--success-text-strong)", marginTop: "0.25rem" }}
							data-metric="candidate-gain"
						>
							{candidate < status.bestCost
								? `↓ ${(status.bestCost - candidate).toFixed(4)} (${(
										(1 - candidate / status.bestCost) *
										100
									).toFixed(2)}%) provisional gain`
								: ""}
						</div>
						<div style={NOTE}>
							<span data-metric="candidate-psnr">
								{psnrText(status.candidatePsnr, status.candidatePsnrInfinite)}
							</span>{" "}
							dB · pending full-image usefulness audit
						</div>
					</div>
				) : null}
				{shown("psnr") ? (
					<div data-metric-card="psnr">
						<div style={MUTED}>Audited PSNR</div>
						<div style={FIGURE}>
							<span data-metric="psnr">{psnrText(status.psnr, status.psnrInfinite)}</span> dB
						</div>
						<div style={NOTE}>Peak signal-to-noise ratio · higher is better</div>
					</div>
				) : null}
				{seed.ssimEnabled && shown("ssim") ? (
					<div data-metric-card="ssim">
						<div style={MUTED}>SSIM</div>
						<div style={FIGURE} data-metric="ssim">
							{typeof status.ssim === "number" ? status.ssim.toFixed(4) : "Calculating…"}
						</div>
						<div style={NOTE}>Structural similarity, higher is better</div>
					</div>
				) : null}
				<div>
					<div style={MUTED}>Iterations</div>
					<div style={FIGURE}>
						<span data-metric="iterations">{status.iterations}</span>
						{maxIterations > 0 ? (
							<span style={{ fontSize: "1rem", color: "var(--text-muted)" }}> / {maxIterations}</span>
						) : null}
					</div>
					{maxIterations > 0 ? (
						<>
							<div style={NOTE}>
								<span data-metric="iteration-progress">{percent.toFixed(1)}%</span> of planned optimizer steps
							</div>
							<div
								id="iteration-progress-track"
								role="progressbar"
								aria-label="Optimizer iteration progress"
								aria-valuemin={0}
								aria-valuemax={100}
								// The width and the exposed value move together: a fill
								// that advances beside a frozen aria-valuenow leaves a
								// screen reader on the figure the page was served with.
								aria-valuenow={Number(percent.toFixed(1))}
								style={{
									marginTop: "0.5rem",
									backgroundColor: "var(--border-color)",
									height: "4px",
									borderRadius: "2px",
									overflow: "hidden",
								}}
							>
								<div
									id="iteration-progress-bar"
									style={{ width: `${percent.toFixed(1)}%`, height: "100%", backgroundColor: "var(--primary-color)" }}
								/>
							</div>
						</>
					) : null}
				</div>
				<div>
					<div style={MUTED}>Evaluations</div>
					<div style={FIGURE} data-metric="evaluations" title={`${status.evaluations} objective evaluations`}>
						{formatCompactNumber(status.evaluations)}
					</div>
					<div style={NOTE}>Objective function calls</div>
				</div>
				{shown("cps") ? (
					<div data-metric-card="cps">
						<div style={MUTED}>Throughput</div>
						<div style={FIGURE} data-metric="cps">
							{formatCompactNumber(averageCPS(history, status.cps))}
						</div>
						<div style={NOTE}>avg circles/sec</div>
						<div style={{ fontSize: "0.75rem", color: "var(--text-muted)" }}>
							Current:{" "}
							<span data-metric="cps-current">
								{formatCompactNumber(currentCPS(history, seed.circles, status.cps))}
							</span>{" "}
							circles/sec
						</div>
						<div style={NOTE}>
							ETA:
							<span data-metric="eta" style={{ marginLeft: "0.35rem" }}>
								{etaLabel(history, maxIterations)}
							</span>
						</div>
					</div>
				) : null}
				<div>
					<div style={MUTED}>Elapsed Time</div>
					<div style={FIGURE} data-metric="elapsed">
						{formatElapsedDuration(status.elapsed)}
					</div>
					<div style={NOTE}>{formatWallClock(status.startTime)}</div>
					{status.termination ? (
						<div style={NOTE} data-metric="termination">
							stopped: {status.termination}
						</div>
					) : null}
				</div>
			</div>
		</div>
	);
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

function ConfigurationCard({ seed, status }: { seed: JobDetailSeed; status: JobStatusPayload }) {
	// Everything here comes from the seed except the evaluation width, which is
	// measured from the renderer while the job runs and so is the one
	// configuration figure a refetch can improve.
	const workers = status.evaluationWidth ?? seed.evaluationWidth ?? 0;
	// The backend is measured the same way and for a sharper reason: a degraded
	// run finished on the CPU, and OpenCL costs are not comparable with CPU
	// costs, so the label has to say which arithmetic produced the number.
	const backend = status.effectiveBackend ?? seed.effectiveBackend ?? "";
	const degraded = status.backendDegraded ?? seed.backendDegraded ?? false;
	return (
		<div className="card detail-configuration" style={{ marginBottom: "1.5rem" }}>
			<h2 style={{ fontSize: "1.25rem", fontWeight: 600, marginBottom: "1rem" }}>Configuration</h2>
			<div
				style={{
					display: "grid",
					gridTemplateColumns: "repeat(auto-fit, minmax(min(200px, 100%), 1fr))",
					gap: "1rem",
				}}
			>
				<Fact label="Mode" value={seed.mode} capitalize />
				<Fact label="Optimizer" value={seed.optimizer} capitalize />
				{seed.variant ? <Fact label="Variant" value={seed.variant} uppercase /> : null}
				{seed.optimizer === "cmaes" ? (
					<>
						<Fact label="Initial Sigma" value={String(seed.initialSigma)} />
						<Fact label="Covariance" value={seed.covarianceMode} capitalize />
						<Fact label="Active CMA" value={String(seed.activeCMA)} />
						<Fact label="Restart Strategy" value={seed.restartStrategy} uppercase />
					</>
				) : null}
				{workers > 1 ? <Fact label="Parallel Evaluation" value={`${workers} workers`} /> : null}
				{backend ? <Fact label="Backend" value={degraded ? `${backend} (degraded to CPU mid-run)` : backend} /> : null}
				{seed.fastCompositing ? <Fact label="Compositing" value="Fast (+/-1 per channel)" /> : null}
				<Fact label="Circles" value={String(seed.circles)} />
				<Fact label="Population Size" value={String(seed.popSize)} />
				<Fact
					label="Optimizer Schedule"
					value={optimizerSchedule(seed.optimizerRestarts, seed.optimizerEpochs, seed.itersPerEpoch)}
				/>
				<div>
					<div style={{ fontSize: "0.875rem", color: "var(--text-muted)" }}>Active-set Polishing</div>
					{seed.polishingEnabled ? (
						<>
							<div style={{ fontWeight: 500 }}>
								{seed.polishingOnly ? "Continuation only" : "Enabled"} ·{" "}
								{`up to ${seed.polishingMaxSweeps} sweeps of ${seed.polishingActiveSetSize} circles`}
							</div>
							<div style={{ fontSize: "0.75rem", color: "var(--text-muted)" }}>
								{`${seed.polishingStrategy} · population ${seed.polishingPopSize} · ` +
									`${seed.polishingEpochs} × ${seed.polishingIters} iterations · ` +
									`stagnation ${seed.polishingStagnationIters} · ` +
									`progress threshold ${formatShortFloat(seed.polishingMinImprovement)}`}
							</div>
						</>
					) : (
						<div style={{ fontWeight: 500 }}>Disabled</div>
					)}
				</div>
				<div style={{ gridColumn: "1 / -1" }}>
					<div style={{ fontSize: "0.875rem", color: "var(--text-muted)" }}>Reference Image</div>
					<div style={{ fontWeight: 500, fontFamily: "monospace", fontSize: "0.875rem" }}>{seed.refPath}</div>
				</div>
			</div>
		</div>
	);
}

// formatShortFloat is Go's %.4g for the values this page shows it: the
// polishing improvement threshold, which is a small decimal like 0.001.
// Number#toPrecision keeps the same four significant digits but pads with
// zeros, which %g strips.
export function formatShortFloat(value: number): string {
	if (!Number.isFinite(value)) return String(value);
	if (value === 0) return "0";

	const exponent = Math.floor(Math.log10(Math.abs(value)));
	if (exponent < -4 || exponent >= 4) {
		const mantissa = Number((value / 10 ** exponent).toPrecision(4));
		const sign = exponent < 0 ? "-" : "+";
		return `${mantissa}e${sign}${String(Math.abs(exponent)).padStart(2, "0")}`;
	}
	return String(Number(value.toPrecision(4)));
}

function Fact({
	label,
	value,
	capitalize,
	uppercase,
}: {
	label: string;
	value: string;
	capitalize?: boolean;
	uppercase?: boolean;
}) {
	return (
		<div>
			<div style={{ fontSize: "0.875rem", color: "var(--text-muted)" }}>{label}</div>
			<div
				style={{
					fontWeight: 500,
					textTransform: capitalize ? "capitalize" : uppercase ? "uppercase" : undefined,
				}}
			>
				{value}
			</div>
		</div>
	);
}

// ---------------------------------------------------------------------------
// Downloads
// ---------------------------------------------------------------------------

function DownloadsCard({
	jobId,
	colormap,
	available,
}: {
	jobId: string;
	colormap: Colormap;
	available: boolean;
}) {
	const [busy, setBusy] = useState(false);
	const [note, setNote] = useState("");
	const encoded = encodeURIComponent(jobId);

	async function downloadReport() {
		setBusy(true);
		setNote("Rendering images and assembling report…");
		try {
			const response = await fetch(
				`/api/v1/jobs/${encoded}/report.html?colormap=${encodeURIComponent(colormap)}`,
				{ cache: "no-store" },
			);
			if (!response.ok) throw new Error(`HTTP ${response.status}`);
			const objectURL = URL.createObjectURL(await response.blob());
			const link = document.createElement("a");
			link.href = objectURL;
			link.download = `job-${jobId}-report.html`;
			document.body.appendChild(link);
			link.click();
			link.remove();
			window.setTimeout(() => URL.revokeObjectURL(objectURL), 1000);
			setNote("Report download ready.");
		} catch (reason) {
			console.error("Unable to generate report", reason);
			setNote("Report generation failed. Please try again.");
		} finally {
			setBusy(false);
		}
	}

	return (
		<div className="card detail-downloads download-card" style={{ marginBottom: "1.5rem" }}>
			<div className="download-header">
				<h2 style={{ fontSize: "1.125rem", fontWeight: 600 }}>Downloads</h2>
				<p style={{ color: "var(--text-muted)", fontSize: "0.8125rem" }}>Current immutable result artifacts</p>
			</div>
			<div className="download-grid">
				<DownloadLink
					href={`/api/v1/jobs/${encoded}/best.png?download=1`}
					download={`job-${jobId}-best.png`}
					available={available}
				>
					Best PNG
				</DownloadLink>
				<DownloadLink
					href={`/api/v1/jobs/${encoded}/params.json`}
					download={`job-${jobId}-params.json`}
					available={available}
				>
					Parameters JSON
				</DownloadLink>
				<DownloadLink
					id="download-difference"
					href={`/api/v1/jobs/${encoded}/diff.png?colormap=${encodeURIComponent(colormap)}&download=1`}
					download={`job-${jobId}-diff.png`}
					available={available}
				>
					Difference PNG
				</DownloadLink>
				<button
					id="download-report"
					className="btn download-button"
					type="button"
					disabled={!available || busy}
					aria-busy={busy}
					onClick={() => void downloadReport()}
				>
					{busy ? "Generating report…" : "HTML Report"}
				</button>
			</div>
			<p
				id="report-download-status"
				role="status"
				aria-live="polite"
				style={{ minHeight: "1rem", marginTop: "0.5rem", color: "var(--text-muted)", fontSize: "0.75rem" }}
			>
				{note}
			</p>
		</div>
	);
}

function DownloadLink({
	id,
	href,
	download,
	available,
	children,
}: {
	id?: string;
	href: string;
	download: string;
	available: boolean;
	children: string;
}) {
	return (
		<a
			id={id}
			className="btn download-button"
			href={href}
			download={download}
			aria-disabled={available ? "false" : "true"}
			tabIndex={available ? 0 : -1}
		>
			{children}
		</a>
	);
}

// ---------------------------------------------------------------------------
// Parameters
// ---------------------------------------------------------------------------

function ParametersCard({
	jobId,
	circles,
	parameters,
	onToggle,
}: {
	jobId: string;
	circles: number;
	parameters: CircleParameter[];
	// The <details> element keeps its own open state; this only reports it, so
	// that a refresh can be skipped while the list is closed.
	onToggle: (open: boolean) => void;
}) {
	const available = parameters.length > 0;
	return (
		<div className="card detail-parameters" style={{ marginBottom: "1.5rem" }}>
			<div
				style={{
					display: "flex",
					flexWrap: "wrap",
					justifyContent: "space-between",
					alignItems: "center",
					gap: "1rem",
				}}
			>
				<div>
					<h2 style={{ fontSize: "1.25rem", fontWeight: 600 }}>Current Best Parameters</h2>
					<p style={{ fontSize: "0.875rem", color: "var(--text-muted)" }}>
						<span id="parameter-count">{parameters.length}</span> of {circles} circles available
					</p>
				</div>
				<a
					id="parameter-export"
					className="btn parameter-export"
					style={{ backgroundColor: "var(--border-color)", fontSize: "0.875rem" }}
					href={`/api/v1/jobs/${encodeURIComponent(jobId)}/params.json`}
					download={`job-${jobId}-params.json`}
					aria-disabled={available ? "false" : "true"}
					tabIndex={available ? 0 : -1}
				>
					Download params.json
				</a>
			</div>
			<details
				id="parameter-viewer"
				className="parameter-viewer"
				style={{ marginTop: "1rem" }}
				onToggle={(event) => onToggle((event.currentTarget as HTMLDetailsElement).open)}
			>
				<summary>
					<span style={{ fontWeight: 600 }}>Inspect circles</span>
					<span style={{ fontSize: "0.75rem", color: "var(--text-muted)" }}>X, Y, radius, RGB, opacity</span>
				</summary>
				{available ? (
					<ol id="parameter-list" className="parameter-list">
						{parameters.map((circle) => {
							const description = parameterDescription(circle);
							return (
								<li key={circle.number} title={description}>
									<span
										className="parameter-color"
										style={{
											backgroundColor: `rgba(${colorChannel(circle.red)}, ${colorChannel(circle.green)}, ${colorChannel(
												circle.blue,
											)}, ${circle.opacity.toFixed(3)})`,
										}}
									/>
									<span>{description}</span>
								</li>
							);
						})}
					</ol>
				) : (
					<p
						id="parameter-empty"
						style={{ marginTop: "1rem", color: "var(--text-muted)", fontSize: "0.875rem" }}
					>
						No best parameters available yet.
					</p>
				)}
			</details>
		</div>
	);
}

// ---------------------------------------------------------------------------
// Metric history chart
// ---------------------------------------------------------------------------

function MetricHistoryCard({ history, ssimEnabled }: { history: HistorySample[]; ssimEnabled: boolean }) {
	const [series, setSeries] = useState<Series>("cost");
	const [window_, setWindow] = useState<WindowChoice>("all");
	const [visible, setVisible] = useState(history.length > 0);
	const ids = useId();
	const palette = useChartTheme();

	// The first sample a run records is what turns the card from its empty state
	// into a chart, exactly as addMetricSample did.
	const hadSamples = useRef(history.length > 0);
	useEffect(() => {
		if (history.length > 0 && !hadSamples.current) {
			hadSamples.current = true;
			setVisible(true);
		}
	}, [history.length]);

	const selected = useMemo(() => selectMetricPoints(history, series, window_), [history, series, window_]);

	return (
		<div id="metric-history-card" className="card detail-history" style={{ marginBottom: "1.5rem" }}>
			<div className="row-between" style={{ marginBottom: "1rem" }}>
				<div>
					<h2 style={{ fontSize: "1.25rem", fontWeight: 600 }}>Metric History</h2>
					<p style={{ fontSize: "0.8125rem", color: "var(--text-muted)" }}>Quality over optimizer iterations</p>
				</div>
				{history.length > 0 ? (
					<button
						id="sparkline-toggle"
						className="btn"
						style={{ fontSize: "0.8125rem", padding: "0.35rem 0.7rem", backgroundColor: "var(--border-color)" }}
						aria-expanded={visible}
						onClick={() => setVisible((current) => !current)}
					>
						{visible ? "Hide History" : "Show History"}
					</button>
				) : null}
			</div>
			{history.length === 0 ? (
				<p id="metric-history-empty" style={{ color: "var(--text-muted)", fontSize: "0.875rem" }}>
					No metric samples yet. History will appear when optimization begins.
				</p>
			) : null}
			{history.length > 0 && visible ? (
				<div
					id="cost-sparkline-container"
					style={{
						display: "block",
						padding: "1rem",
						backgroundColor: "var(--bg-color)",
						borderRadius: "0.375rem",
						position: "relative",
					}}
				>
					<div
						style={{
							display: "flex",
							flexWrap: "wrap",
							justifyContent: "space-between",
							alignItems: "center",
							gap: "0.75rem",
							marginBottom: "0.75rem",
						}}
					>
						<div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: "0.75rem" }}>
							<label
								htmlFor={`${ids}-series`}
								style={{ fontSize: "0.875rem", fontWeight: 600, color: "var(--text-muted)" }}
							>
								Metric
								<select
									id={`${ids}-series`}
									value={series}
									onChange={(event) => setSeries(event.target.value as Series)}
									style={selectStyle}
								>
									{SERIES.filter((option) => option !== "ssim" || ssimEnabled).map((option) => (
										<option value={option} key={option}>
											{SERIES_LABELS[option]}
										</option>
									))}
								</select>
							</label>
							<label
								htmlFor={`${ids}-window`}
								style={{ fontSize: "0.875rem", fontWeight: 600, color: "var(--text-muted)" }}
							>
								Window
								<select
									id={`${ids}-window`}
									value={window_}
									onChange={(event) => setWindow(event.target.value as WindowChoice)}
									style={selectStyle}
								>
									<option value="all">All samples</option>
									<option value="100">Last 100</option>
									<option value="250">Last 250</option>
									<option value="500">Last 500</option>
									<option value="1000">Last 1,000</option>
								</select>
							</label>
						</div>
						<div id="sparkline-stats" style={{ fontSize: "0.75rem", color: "var(--text-muted)" }}>
							Showing <span id="sparkline-samples">{selected.points.length}</span> of{" "}
							<span id="sparkline-total-samples">{selected.total}</span> samples
						</div>
					</div>
					{/* The empty case is decided here rather than inside the chart:
					    useLineChart builds its Chart.js instance once, on mount, so
					    a component that returned early on the first render would
					    never build one when the series later filled up. */}
					{selected.points.length === 0 ? (
						<p id="sparkline-empty" style={{ color: "var(--text-muted)", fontSize: "0.8125rem" }}>
							No samples available
						</p>
					) : (
						<MetricChart series={series} points={selected.points} palette={palette} />
					)}
				</div>
			) : null}
		</div>
	);
}

const selectStyle = {
	marginLeft: "0.35rem",
	padding: "0.3rem 0.5rem",
	border: "1px solid var(--border-color)",
	borderRadius: "0.25rem",
	background: "var(--control-bg)",
} as const;

// MetricChart is the sparkline, drawn by Chart.js instead of by hand. The
// hand-rolled SVG it replaces built its own scales, ticks, hover line, tooltip
// and keyboard selection out of createElementNS calls; all of that except the
// keyboard route is what a chart library is for. The keyboard route is not, so
// it is still here: a canvas is a black box to a screen reader, and the
// selection the arrow keys move drives both the tooltip and the live readout,
// which is the same pairing the SVG had.
function MetricChart({
	series,
	points,
	palette,
}: {
	series: Series;
	points: ChartPoint[];
	palette: Palette;
}) {
	const canvasRef = useRef<HTMLCanvasElement | null>(null);
	const descriptionId = useId();
	const pointsRef = useRef(points);
	const seriesRef = useRef(series);
	pointsRef.current = points;
	seriesRef.current = series;

	// null means "nothing is selected": the readout then reports the latest
	// sample as a latest rather than as a selection, which is what the pointer
	// leaving the chart used to do.
	const [selected, setSelected] = useState<number | null>(points.length - 1);
	const selectedRef = useRef(selected);
	selectedRef.current = selected;

	// A redraw re-selects the newest point, as updateSparkline's closing
	// showLatestSparklinePoint() did: a chart that has just grown a sample
	// should be reading that sample out.
	useEffect(() => {
		setSelected(points.length > 0 ? points.length - 1 : null);
	}, [points]);

	const active = selected ?? (points.length > 0 ? points.length - 1 : null);

	useLineChart(
		canvasRef,
		() =>
			new Chart(canvasRef.current!.getContext("2d")!, {
				type: "line",
				data: { datasets: [{ label: "metric", data: [] }] },
				options: {
					responsive: true,
					maintainAspectRatio: false,
					animation: false,
					// Nearest along x, without needing to be on the line: the same
					// snap the pointer handler hand-rolled by scanning every point.
					interaction: { mode: "index", intersect: false, axis: "x" },
					onHover: (_event, elements) => {
						const index = elements[0]?.index;
						if (typeof index === "number" && index !== selectedRef.current) setSelected(index);
					},
					scales: {
						x: {
							type: "linear",
							title: { display: true, text: "Iteration" },
							ticks: { callback: (value) => Math.round(Number(value)).toLocaleString() },
						},
						y: { title: { display: true, text: AXIS_TITLES[series] } },
					},
					plugins: {
						legend: { display: false },
						tooltip: {
							displayColors: false,
							callbacks: {
								label(item: TooltipItem<"line">) {
									const point = pointsRef.current[item.dataIndex];
									if (!point) return "";
									return `Iteration ${point.iteration.toLocaleString()} · ${formatMetricValue(
										seriesRef.current,
										point.value,
									)}`;
								},
							},
						},
					},
				},
			}),
		(chart: ChartInstance) => {
			const current = pointsRef.current;
			chart.data.datasets[0].data = current.map((point) => ({ x: point.iteration, y: point.value }));
			Object.assign(chart.data.datasets[0], {
				borderColor: palette.primary,
				pointBackgroundColor: palette.primary,
				pointBorderColor: palette.primary,
				pointRadius: current.length > 200 ? 0 : 2,
				pointHitRadius: 12,
				borderWidth: 2,
				tension: 0,
				fill: false,
			});

			// Chart.js types a scale as the union of every registered scale, so
			// the three fields this chart drives are reached through one narrow
			// cast rather than by asserting the whole scale is linear.
			// Every write below sets one leaf property. Replacing `ticks` or
			// `title` wholesale with a spread of its current value looks
			// equivalent and is not: after the first update chart.options is a
			// resolved proxy chain, so the spread copies Chart.js's own nested
			// option proxies (ticks.minor, ticks.major) back into the raw
			// config. Chart.js then re-resolves those copies, reads the
			// descriptor key _scriptable off one of them, finds a function,
			// and calls it as if it were a scriptable option -- "name.startsWith
			// is not a function", thrown during render, which unmounts the whole
			// island and leaves the page blank.
			const y = chart.options.scales?.y as
				| { min?: number; max?: number; ticks?: Record<string, unknown>; title?: Record<string, unknown> }
				| undefined;
			if (y) {
				const bounds = metricBounds(current);
				y.min = bounds?.min;
				y.max = bounds?.max;
				if (y.title) {
					y.title.display = true;
					y.title.text = AXIS_TITLES[series];
				}
				if (y.ticks) {
					y.ticks.callback = (value: unknown) => formatAxisValue(series, Number(value));
				}
			}
			applyAxisTheme(chart, palette);

			// The selection is pushed into the chart rather than left to the
			// pointer, so the arrow keys and the pointer light up the same point.
			//
			// It is guarded on the element existing because useLineChart calls
			// this before its first chart.update(): the dataset has data by then
			// but the controller has not built the point elements yet, and
			// setActiveElements reaches straight into them
			// ("Cannot set properties of undefined (setting 'active')", thrown
			// from an effect, which unmounts the island). The deps effect runs
			// again immediately after that first update, and that pass has the
			// elements and sets the selection.
			const drawn = chart.getDatasetMeta(0)?.data ?? [];
			const elements = active === null || !drawn[active] ? [] : [{ datasetIndex: 0, index: active }];
			chart.setActiveElements(elements);
			chart.tooltip?.setActiveElements(elements, { x: 0, y: 0 });
		},
		[points, series, palette, active],
	);

	function moveSelection(event: ReactKeyboardEvent<HTMLCanvasElement>) {
		if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
		if (points.length === 0) return;
		const last = points.length - 1;
		let index = selected ?? last;
		switch (event.key) {
			case "ArrowLeft":
				index = Math.max(0, index - 1);
				break;
			case "ArrowRight":
				index = Math.min(last, index + 1);
				break;
			case "Home":
				index = 0;
				break;
			case "End":
				index = last;
				break;
			default:
				return;
		}
		event.preventDefault();
		setSelected(index);
	}

	const readoutPoint = points[active ?? points.length - 1];
	const readout =
		selected === null
			? `Latest: iteration ${readoutPoint.iteration.toLocaleString()} · ${formatMetricValue(series, readoutPoint.value)}`
			: `Iteration ${readoutPoint.iteration.toLocaleString()} · ${formatMetricValue(series, readoutPoint.value)}`;
	const first = points[0];
	const last = points[points.length - 1];
	const bestValue = points.reduce(
		(carried, point) => (series === "cost" ? Math.min(carried, point.value) : Math.max(carried, point.value)),
		first.value,
	);

	return (
		<>
			{/* The frame carries the size, not the canvas. A responsive Chart.js
			    chart writes its own inline width and height onto the canvas
			    every time it resizes, so a canvas that is also sized from CSS
			    (width: 100%; height: 280px) resizes the element that its own
			    ResizeObserver is watching: the chart re-measured, redrew and
			    re-measured for as long as the page was open, which cost roughly
			    a third of the main thread and made the page slower the longer
			    it stayed open. Sizing a positioned parent instead is the
			    library's documented arrangement, and it is what the dashboard
			    and campaign charts already did. */}
			<div className="metric-chart-frame">
				<canvas
					id="cost-sparkline"
					ref={canvasRef}
					className="metric-chart-canvas"
					role="img"
					tabIndex={0}
					aria-label="Metric history chart"
					aria-describedby={descriptionId}
					onKeyDown={moveSelection}
					onFocus={() => setSelected(points.length - 1)}
					onPointerLeave={() => setSelected(null)}
				/>
			</div>
			<div
				style={{
					display: "flex",
					flexWrap: "wrap",
					justifyContent: "space-between",
					gap: "0.5rem 1rem",
					marginTop: "0.5rem",
					fontSize: "0.75rem",
					color: "var(--text-muted)",
				}}
			>
				<span>
					Start: <span id="sparkline-start">{formatMetricValue(series, first.value)}</span>
				</span>
				<span>
					Current: <span id="sparkline-current">{formatMetricValue(series, last.value)}</span>
				</span>
				<span>
					<span id="sparkline-best-label">{series === "cost" ? "Min" : "Max"}</span>:{" "}
					<span id="sparkline-min">{formatMetricValue(series, bestValue)}</span>
				</span>
				<span id="sparkline-hover-readout" aria-live="polite">
					{readout}
				</span>
			</div>
			{/* A canvas says nothing to a screen reader. The described-by text is
			    the same sentence the tooltip shows, for the selected point. */}
			<p id={descriptionId} className="sr-only">
				{`${points.length} samples, from iteration ${first.iteration.toLocaleString()} to ${last.iteration.toLocaleString()}. ` +
					`Selected: ${readout}. Use the arrow keys, Home and End to move through the series.`}
			</p>
		</>
	);
}
