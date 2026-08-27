import { Chart } from "chart.js";
import { useCallback, useId, useMemo, useRef, useState } from "react";
import type { CSSProperties } from "react";
import { CampaignCostChart } from "./CampaignCostChart";
import { useChartTheme, useLineChart } from "./charts";
import type { Palette } from "./charts";
import {
	campaignStageCount,
	campaignURL,
	formatCostGain,
	formatJobCircles,
	shortID,
	stateBadgeStyle,
	stateClass,
	stateLabel,
} from "./format";
import { CreateJobIsland } from "./CreateJob";
import { mountIslands } from "./islands";
import { SettingsIsland } from "./Settings";
import { ThemeToggleIsland } from "./ThemeToggle";
import { JobListIsland } from "./JobList";
import { CampaignDetailIsland, CampaignListIsland } from "./Campaigns";
import { JobControlsIsland } from "./JobControls";
import { fetchJSON, useLiveResource } from "./live";
import type { UIEvent } from "./live";
import { LiveStatus } from "./LiveStatus";
import { SkeletonBar } from "./Skeleton";

// DashboardResponse is the shape of both GET /api/v1/dashboard and the
// server-rendered seed in `#dashboard-page`. The Go side keeps those two in
// step (see ui.DashboardPageData); the island simply assumes they agree.
type DashboardResponse = {
	campaigns: CampaignSummary[];
	runningJobs: RunningJob[];
	aggregates: DashboardAggregates;
	hostFacts?: HostFacts;
};

type CampaignSummary = {
	id: string;
	name: string;
	state: string;
	source: string;
	recordedStages: number;
	plannedStages: number;
	campaignSeries: CampaignSeriesPoint[];
	leafJobId?: string;
	circles: number;
	bestCost: number;
	hasBestCost: boolean;
	updatedAt: string;
};

type CampaignSeriesPoint = {
	index: number;
	kind: string;
	circles: number;
	bestCost: number;
	hasBestCost: boolean;
};

type RunningJob = {
	id: string;
	project: string;
	state: string;
	iterations: number;
	maxIters: number;
	circles: number;
	requestedCircles: number;
	bestCost: number;
	initialCost: number;
	cps: number;
	evaluationWidth?: number;
	elapsedSec: number;
	// Absent from the page seed by design: only the JSON endpoint carries it.
	metricHistory?: MetricSample[];
};

type MetricSample = {
	iteration: number;
	cost: number;
};

type DashboardAggregates = {
	running: number;
	pending: number;
	completed: number;
	runningCps: number;
};

type HostFacts = {
	version: string;
	commit: string;
	buildDate: string;
	goos: string;
	goarch: string;
	gomaxProcs: number;
	goVersion: string;
	simd: string;
	activeSSDKernel: string;
	activeSADKernel: string;
	compositingBackend: string;
	fastCompositingBackend: string;
	gpu: {
		state: string;
		error?: string;
	};
};

// ProgressEventPayload is one `data:` frame of /api/v1/stream. The server sends
// more fields than this; the dashboard reads the ones it renders.
type ProgressEventPayload = {
	jobId: string;
	state: string;
	iterations: number;
	bestCost: number;
	cps: number;
};

// A running job emits an event per iteration, so an unbounded history would
// grow without limit in a tab left open overnight. The endpoint caps its seed
// at 100 samples; this is the client's own ceiling on what it keeps after that.
const MAX_HISTORY_POINTS = 1000;

// readDashboardSeed parses the payload the server rendered into the island
// root. It has to run before React's first commit, because that commit clears
// the container the script tag lives in.
function readDashboardSeed(root: HTMLElement): DashboardResponse | null {
	const seed = root.querySelector<HTMLScriptElement>("#dashboard-page");
	if (!seed) {
		return null;
	}
	try {
		return normalizePayload(JSON.parse(seed.textContent || "null"));
	} catch {
		return null;
	}
}

function normalizePayload(payload: DashboardResponse | null): DashboardResponse | null {
	if (!payload) {
		return null;
	}
	return {
		...payload,
		campaigns: payload.campaigns ?? [],
		runningJobs: (payload.runningJobs ?? []).map((job) => ({
			...job,
			metricHistory: normalizeHistory(job.metricHistory ?? []),
		})),
	};
}

function isRunning(state: string): boolean {
	return state === "running";
}

function isTerminal(state: string): boolean {
	return state === "completed" || state === "failed" || state === "cancelled";
}

function clampProgress(value: number, max: number): number {
	if (!Number.isFinite(value) || max <= 0) {
		return 0;
	}
	return Math.min(100, Math.max(0, (value / max) * 100));
}

function normalizeHistory(samples: MetricSample[]): MetricSample[] {
	if (samples.length === 0) {
		return [];
	}
	const bounded = samples.slice(-MAX_HISTORY_POINTS);
	bounded.sort((left, right) => left.iteration - right.iteration);
	return bounded;
}

function appendHistory(history: MetricSample[], sample: MetricSample): MetricSample[] {
	const last = history[history.length - 1];
	if (last && sample.iteration === last.iteration && sample.cost === last.cost) {
		return history;
	}
	const next = [...history, sample];
	if (next.length <= MAX_HISTORY_POINTS) {
		return next;
	}
	return next.slice(-MAX_HISTORY_POINTS);
}

// mergeJobFromEvent folds one stream frame into the payload.
//
// It updates only what a progress event actually knows: the job's own counters
// and the running totals derived from them. Pending and completed counts stay
// as the endpoint last reported them, because the running-job list this island
// holds is not the whole job table and cannot be counted for them. A terminal
// event drops the job from the list and asks the caller to refetch, which is
// what makes those two counts correct again.
function mergeJobFromEvent(payload: DashboardResponse, event: ProgressEventPayload): DashboardResponse {
	if (isTerminal(event.state)) {
		const runningJobs = payload.runningJobs.filter((job) => job.id !== event.jobId);
		return { ...payload, runningJobs, aggregates: withRunningTotals(payload.aggregates, runningJobs) };
	}

	const runningJobs = [...payload.runningJobs];
	const index = runningJobs.findIndex((job) => job.id === event.jobId);
	const sample: MetricSample = { iteration: event.iterations, cost: event.bestCost };

	if (index < 0) {
		// A job that started after the last fetch. Only the stream's fields are
		// known here; the refetch applyProgress triggers for a first-seen id is
		// what fills in the project, the iteration budget, and the initial cost.
		runningJobs.push({
			id: event.jobId,
			project: "",
			state: event.state,
			iterations: event.iterations,
			maxIters: 0,
			circles: 0,
			requestedCircles: 0,
			bestCost: event.bestCost,
			initialCost: 0,
			cps: event.cps,
			elapsedSec: 0,
			metricHistory: [sample],
		});
	} else {
		const current = runningJobs[index];
		runningJobs[index] = {
			...current,
			state: event.state,
			iterations: Math.max(current.iterations, event.iterations),
			bestCost: event.bestCost,
			cps: event.cps,
			metricHistory: appendHistory(current.metricHistory ?? [], sample),
		};
	}

	return { ...payload, runningJobs, aggregates: withRunningTotals(payload.aggregates, runningJobs) };
}

function withRunningTotals(base: DashboardAggregates, runningJobs: RunningJob[]): DashboardAggregates {
	const running = runningJobs.filter((job) => isRunning(job.state));
	return {
		...base,
		running: running.length,
		runningCps: running.reduce((sum, job) => sum + job.cps, 0),
	};
}

function formatFixed(value: number, digits: number): string {
	if (!Number.isFinite(value)) {
		return "—";
	}
	return value.toFixed(digits);
}

function formatInteger(value: number): string {
	if (!Number.isFinite(value)) {
		return "—";
	}
	return `${Math.round(value)}`;
}

// architectureBadge reads like `amd64 · avx2 · cpu`. The third component is the
// render backend, which is what the GPU probe reports; the compositing kernel
// is already covered by the SIMD component.
function architectureBadge(host: HostFacts | undefined): string {
	if (!host) {
		return "unknown";
	}
	const backend = host.gpu?.state === "available" ? "gpu" : "cpu";
	return `${host.goarch || "unknown"} · ${host.simd || "unknown"} · ${backend}`;
}

// JobSparkline draws the cost curve of one running job, seeded from the
// endpoint's metric history and grown by the stream.
function JobSparkline({ history, palette, jobId }: { history: MetricSample[]; palette: Palette; jobId: string }) {
	const canvasRef = useRef<HTMLCanvasElement | null>(null);
	const descriptionId = useId();
	const historyRef = useRef(history);
	historyRef.current = history;

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
					scales: { x: { type: "linear", display: false }, y: { display: false } },
					plugins: { legend: { display: false }, tooltip: { enabled: false } },
				},
			}),
		(chart) => {
			const current = historyRef.current;
			chart.data.datasets[0].data = current.map((sample) => ({ x: sample.iteration, y: sample.cost }));
			Object.assign(chart.data.datasets[0], {
				borderColor: palette.primary,
				backgroundColor: `${palette.primary}22`,
				pointRadius: 0,
				borderWidth: 1.5,
				tension: 0,
				fill: true,
			});
		},
		[history, palette],
	);

	// A bare <canvas> is an empty box to a screen reader. The same treatment
	// CampaignCostChart gives its plot applies here: name the drawing, and put
	// the reading of it — first cost, last cost, sample count — in a sibling the
	// canvas points at, since the curve itself announces nothing.
	const first = history[0];
	const last = history[history.length - 1];
	const label = `Best cost over ${history.length} samples for job ${shortID(jobId)}`;
	return (
		// 180px was a hard width in a nine-column table. Capping it instead lets
		// the column give room back to the columns that carry numbers.
		<div style={{ width: "100%", maxWidth: "180px", height: "44px" }}>
			<canvas ref={canvasRef} role="img" aria-label={label} aria-describedby={descriptionId} style={{ maxWidth: "100%" }} />
			<span id={descriptionId} className="sr-only">
				{first && last
					? `Cost fell from ${formatFixed(first.cost, 4)} at iteration ${formatInteger(first.iteration)} to ${formatFixed(last.cost, 4)} at iteration ${formatInteger(last.iteration)}.`
					: "No cost samples recorded yet."}
			</span>
		</div>
	);
}

// The running-job table is sortable by every column that holds a scalar. The
// unsorted state is kept as its own value rather than as a default column,
// because the server's order (the order jobs started) is meaningful and a
// reader who cycles a column past descending should get it back.
type SortKey = "id" | "state" | "circles" | "iterations" | "bestCost" | "gain" | "cps";

type SortState = { key: SortKey; direction: "asc" | "desc" };

function jobSortValue(job: RunningJob, key: SortKey): number | string {
	switch (key) {
		case "id":
			return job.id;
		case "state":
			return job.state;
		case "circles":
			return job.circles;
		case "iterations":
			return job.iterations;
		case "bestCost":
			return job.bestCost;
		case "gain":
			// A job without a usable initial cost prints "—"; sorting it as no gain
			// keeps those rows together at one end instead of interleaving them.
			return job.initialCost > 0 && job.bestCost <= job.initialCost
				? 1 - job.bestCost / job.initialCost
				: -1;
		case "cps":
			return job.cps;
	}
}

function sortJobs(jobs: RunningJob[], sort: SortState | null): RunningJob[] {
	if (!sort) return jobs;
	const factor = sort.direction === "asc" ? 1 : -1;
	return [...jobs].sort((left, right) => {
		const a = jobSortValue(left, sort.key);
		const b = jobSortValue(right, sort.key);
		if (typeof a === "string" || typeof b === "string") {
			return factor * String(a).localeCompare(String(b));
		}
		if (a === b) return 0;
		return factor * (a < b ? -1 : 1);
	});
}

// nextSort cycles one column ascending → descending → unsorted.
function nextSort(current: SortState | null, key: SortKey): SortState | null {
	if (!current || current.key !== key) return { key, direction: "asc" };
	if (current.direction === "asc") return { key, direction: "desc" };
	return null;
}

function SortableHeader({
	label,
	sortKey,
	sort,
	onSort,
	align,
	style,
}: {
	label: string;
	sortKey: SortKey;
	sort: SortState | null;
	onSort: (key: SortKey) => void;
	align?: "left" | "right";
	style?: CSSProperties;
}) {
	const active = sort?.key === sortKey ? sort : null;
	return (
		<th
			scope="col"
			aria-sort={active ? (active.direction === "asc" ? "ascending" : "descending") : "none"}
			style={{ padding: "0.5rem", textAlign: align ?? "left", ...style }}
		>
			<button
				type="button"
				onClick={() => onSort(sortKey)}
				title={`Sort by ${label}`}
				style={{
					background: "none",
					border: "none",
					padding: 0,
					font: "inherit",
					fontWeight: "inherit",
					color: active ? "var(--primary-color)" : "inherit",
					cursor: "pointer",
					display: "inline-flex",
					gap: "0.25rem",
					alignItems: "center",
				}}
			>
				{label}
				<span aria-hidden="true" style={{ fontSize: "0.7rem", opacity: active ? 1 : 0.35 }}>
					{active ? (active.direction === "asc" ? "▲" : "▼") : "↕"}
				</span>
			</button>
		</th>
	);
}

// min() caps the track at the container's own width. Without it a 220px
// minimum is a 220px floor even inside a 180px column, and the grid — not the
// content — is what pushes the page into a horizontal scroll on a phone.
const summaryGridStyle: CSSProperties = {
	display: "grid",
	gridTemplateColumns: "repeat(auto-fill, minmax(min(220px, 100%), 1fr))",
	gap: "1rem",
	marginBottom: "1.5rem",
};

// A skeleton rather than a sentence: the dashboard's shape is known before its
// numbers are, and showing it keeps the page from jumping when they arrive. The
// wording still reaches a screen reader, now from a region that announces it.
function DashboardSkeleton({ error }: { error: string | null }) {
	return (
		<div>
			<div style={{ marginBottom: "2rem" }}>
				<h1 style={{ fontSize: "2rem", fontWeight: 700 }}>Dashboard</h1>
				<p role="status" aria-live="polite" style={{ color: "var(--text-muted)" }}>
					{error ?? "Loading the dashboard…"}
				</p>
			</div>
			{error ? null : (
				<>
					<div style={summaryGridStyle}>
						{["jobs", "throughput", "host"].map((name) => (
							<div key={name} className="card" style={{ display: "grid", gap: "0.75rem" }}>
								<SkeletonBar width="40%" height="1.25rem" />
								<SkeletonBar width="70%" />
								<SkeletonBar width="55%" />
								<SkeletonBar width="62%" />
							</div>
						))}
					</div>
					<div className="card" style={{ display: "grid", gap: "0.75rem" }}>
						<SkeletonBar width="30%" height="1.25rem" />
						<SkeletonBar width="100%" height="2.5rem" />
						<SkeletonBar width="100%" height="2.5rem" />
						<SkeletonBar width="100%" height="2.5rem" />
					</div>
				</>
			)}
		</div>
	);
}

function DashboardIsland({ root }: { root: HTMLElement }) {
	const palette = useChartTheme();
	const [sort, setSort] = useState<SortState | null>(null);
	const initial = readDashboardSeed(root);
	const { value: payload, status: streamStatus, error: errorText } = useLiveResource({
		initial,
		load: async (signal) => normalizePayload(await fetchJSON<DashboardResponse>("/api/v1/dashboard", signal)),
		reduce: reduceDashboardEvent,
	});

	const unsortedJobs = payload?.runningJobs ?? [];
	const campaigns = payload?.campaigns ?? [];
	const aggregates = payload?.aggregates ?? { running: 0, pending: 0, completed: 0, runningCps: 0 };
	const hostFacts = payload?.hostFacts;
	const jobs = useMemo(() => sortJobs(unsortedJobs, sort), [unsortedJobs, sort]);
	const onSort = useCallback((key: SortKey) => setSort((current) => nextSort(current, key)), []);

	if (!payload) {
		return <DashboardSkeleton error={errorText} />;
	}

	return (
		<div>
			<div style={{ marginBottom: "2rem" }}>
				<h1 style={{ fontSize: "2rem", fontWeight: 700 }}>Dashboard</h1>
				<p style={{ color: "var(--text-muted)" }}>Monitor jobs, campaigns, and host state from one view.</p>
			</div>

			<div style={summaryGridStyle}>
				<div className="card">
					<h2 style={{ fontSize: "1.25rem", fontWeight: 600, marginBottom: "0.5rem" }}>Jobs</h2>
					<div style={{ display: "grid", gap: "0.5rem", color: "var(--text-muted)" }}>
						<p><strong>Running:</strong> {aggregates.running}</p>
						<p><strong>Pending:</strong> {aggregates.pending}</p>
						<p><strong>Completed:</strong> {aggregates.completed}</p>
					</div>
				</div>
				<div className="card">
					<h2 style={{ fontSize: "1.25rem", fontWeight: 600, marginBottom: "0.5rem" }}>Throughput</h2>
					<div style={{ display: "grid", gap: "0.5rem", color: "var(--text-muted)" }}>
						<p><strong>Circles/sec:</strong> {formatFixed(aggregates.runningCps, 2)}</p>
						<p><strong>Running jobs:</strong> {aggregates.running}</p>
					</div>
				</div>
				<div className="card">
					<h2 style={{ fontSize: "1.25rem", fontWeight: 600, marginBottom: "0.5rem" }}>Host</h2>
					<div style={{ display: "grid", gap: "0.5rem", color: "var(--text-muted)" }}>
						<p><strong>Runtime:</strong> {hostFacts ? `${hostFacts.goos}/${hostFacts.goarch}` : "—"}</p>
						<p><strong>Architecture:</strong> <span style={{ fontFamily: "monospace" }}>{architectureBadge(hostFacts)}</span></p>
						<p><strong>GPU:</strong> {hostFacts?.gpu?.state ?? "—"}</p>
						<p><strong>Build:</strong> {hostFacts?.version ?? "—"}</p>
					</div>
				</div>
			</div>

			<div className="card" style={{ marginBottom: "1.5rem" }}>
				<div className="row-between" style={{ marginBottom: "1rem" }}>
					<h2 style={{ fontSize: "1.25rem", fontWeight: 600, margin: 0 }}>Running jobs</h2>
					<a href="/jobs" className="btn" style={{ backgroundColor: "var(--border-color)" }}>
						Open jobs page
					</a>
				</div>
				{jobs.length === 0 ? (
					<p style={{ color: "var(--text-muted)" }}>No jobs are running right now.</p>
				) : (
					// A named, focusable region: the table scrolls sideways on a narrow
					// screen, and without tabindex a keyboard could never reach the
					// columns that scrolled out of view.
					<div className="table-scroll" role="region" aria-label="Running jobs" tabIndex={0}>
						<table style={{ width: "100%", borderCollapse: "collapse", fontSize: "0.875rem" }}>
							<thead>
								<tr style={{ textAlign: "left", borderBottom: "1px solid var(--border-color)" }}>
									<SortableHeader label="Job" sortKey="id" sort={sort} onSort={onSort} style={{ padding: "0.5rem 0.5rem 0.5rem 0" }} />
									<SortableHeader label="State" sortKey="state" sort={sort} onSort={onSort} />
									<SortableHeader label="Circles" sortKey="circles" sort={sort} onSort={onSort} align="right" />
									<SortableHeader label="Iter" sortKey="iterations" sort={sort} onSort={onSort} align="right" />
									<SortableHeader label="Best cost" sortKey="bestCost" sort={sort} onSort={onSort} align="right" />
									<SortableHeader label="Gain" sortKey="gain" sort={sort} onSort={onSort} align="right" />
									<SortableHeader label="CPS" sortKey="cps" sort={sort} onSort={onSort} align="right" />
									<th scope="col" style={{ padding: "0.5rem" }}>Progress</th>
									<th scope="col" style={{ padding: "0.5rem" }}>Cost</th>
								</tr>
							</thead>
							<tbody>
								{jobs.map((job) => (
									<JobRow key={job.id} job={job} palette={palette} />
								))}
							</tbody>
						</table>
					</div>
				)}
				<a href="/api/v1/events" style={{ display: "inline-block", marginTop: "0.75rem", color: "var(--primary-color)" }}>
					Stream updates
				</a>
			</div>

			<div className="card" style={{ marginBottom: "1.5rem" }}>
				<div className="row-between" style={{ marginBottom: "1rem" }}>
					<h2 style={{ fontSize: "1.25rem", fontWeight: 600, margin: 0 }}>Campaigns</h2>
					<a href="/schedules" className="btn" style={{ backgroundColor: "var(--border-color)" }}>
						Open campaigns
					</a>
				</div>
				{campaigns.length === 0 ? (
					<p style={{ color: "var(--text-muted)" }}>
						No campaigns yet. Start one via <code>POST /api/v1/schedules</code>.
					</p>
				) : (
					<div style={{ display: "grid", gap: "1rem" }}>
						{campaigns.map((campaign) => (
							<CampaignCard key={`${campaign.source}:${campaign.id}`} campaign={campaign} palette={palette} />
						))}
					</div>
				)}
			</div>

			<LiveStatus state={streamStatus} error={errorText} />
		</div>
	);
}

function reduceDashboardEvent(
	current: DashboardResponse | null,
	event: UIEvent,
): { value: DashboardResponse | null; refresh?: boolean } {
	if (!current) return { value: current, refresh: true };
	if (event.type === "campaign.changed" || event.type === "job.deleted") {
		return { value: current, refresh: true };
	}
	if (event.type !== "job.upsert" || !event.progress) return { value: current };
	const progress = event.progress as ProgressEventPayload;
	const known = current.runningJobs.some((job) => job.id === progress.jobId);
	if (progress.state !== "running") {
		return { value: mergeJobFromEvent(current, progress), refresh: true };
	}
	if (!known) return { value: current, refresh: true };
	return { value: mergeJobFromEvent(current, progress) };
}

function JobRow({ job, palette }: { job: RunningJob; palette: Palette }) {
	const history = job.metricHistory ?? [];
	const progress = clampProgress(job.iterations, job.maxIters);

	return (
		<tr style={{ borderBottom: "1px solid var(--border-color)" }}>
			<td style={{ padding: "0.75rem 0.5rem 0.75rem 0" }}>
				<a href={`/jobs/${job.id}`} style={{ fontFamily: "monospace", color: "var(--text-color)", textDecoration: "none" }}>
					{shortID(job.id)}
				</a>
				<div style={{ fontSize: "0.75rem", color: "var(--text-muted)" }}>{job.project}</div>
			</td>
			<td style={{ padding: "0.75rem 0.5rem" }}>
				<span className={stateClass(job.state)} style={stateBadgeStyle(job.state)}>{stateLabel(job.state)}</span>
			</td>
			<td style={{ padding: "0.75rem 0.5rem", textAlign: "right" }}>{formatJobCircles(job.circles, job.requestedCircles)}</td>
			<td style={{ padding: "0.75rem 0.5rem", textAlign: "right" }}>{formatInteger(job.iterations)}</td>
			<td style={{ padding: "0.75rem 0.5rem", textAlign: "right" }}>{formatFixed(job.bestCost, 4)}</td>
			{/* --success-color is a fill, not a foreground: at 2.54:1 on white it
			    fails AA as text. --success-text-strong is the same hue darkened
			    for light mode and left as-is for dark. */}
			<td style={{ padding: "0.75rem 0.5rem", textAlign: "right", color: "var(--success-text-strong)" }}>
				{formatCostGain(job.initialCost, job.bestCost)}
			</td>
			<td style={{ padding: "0.75rem 0.5rem", textAlign: "right" }}>{formatFixed(job.cps, 2)}</td>
			{/* 9rem plus the sparkline's old 180px claimed a quarter of a
			    nine-column table before any content did. */}
			<td style={{ padding: "0.75rem 0.5rem", minWidth: "7rem" }}>
				<div style={{ fontSize: "0.7rem", color: "var(--text-muted)", marginBottom: "0.25rem" }}>
					{job.maxIters > 0 ? `${progress.toFixed(1)}%` : "—"}
				</div>
				<div
					role="progressbar"
					aria-label={`Iteration progress for job ${shortID(job.id)}`}
					aria-valuemin={0}
					aria-valuemax={100}
					// A job with no iteration budget has no determinate progress, and
					// omitting aria-valuenow is how that is said.
					aria-valuenow={job.maxIters > 0 ? Math.round(progress) : undefined}
					aria-valuetext={job.maxIters > 0 ? `${progress.toFixed(1)}%` : "unknown"}
					style={{ height: "6px", backgroundColor: "var(--border-color)", borderRadius: "3px", overflow: "hidden" }}
				>
					<div style={{ height: "100%", width: `${progress}%`, backgroundColor: "var(--primary-color)" }} />
				</div>
			</td>
			<td style={{ padding: "0.75rem 0.5rem" }}>
				{history.length > 1 ? (
					<JobSparkline history={history} palette={palette} jobId={job.id} />
				) : (
					<span style={{ color: "var(--text-muted)", fontSize: "0.75rem" }}>—</span>
				)}
			</td>
		</tr>
	);
}

function CampaignCard({ campaign, palette }: { campaign: CampaignSummary; palette: Palette }) {
	const plotted = useMemo(
		() => (campaign.campaignSeries ?? []).filter((point) => point.hasBestCost),
		[campaign.campaignSeries],
	);

	return (
		<div className="card" style={{ borderLeft: "4px solid var(--primary-color)" }}>
			<div className="row-between row-between-top">
				<div style={{ flex: "1 1 260px", minWidth: 0 }}>
					<div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: "0.75rem", marginBottom: "0.5rem" }}>
						<a href={campaignURL(campaign)} style={{ textDecoration: "none", color: "inherit" }}>
							<h3 style={{ fontSize: "1.125rem", fontWeight: 600, fontFamily: "monospace" }}>{shortID(campaign.id)}</h3>
						</a>
						{campaign.name ? <span style={{ color: "var(--text-muted)" }}>{campaign.name}</span> : null}
						{campaign.state ? <span className={stateClass(campaign.state)} style={stateBadgeStyle(campaign.state)}>{stateLabel(campaign.state)}</span> : null}
					</div>
					<div className="meta-row" style={{ color: "var(--text-muted)", fontSize: "0.875rem" }}>
						<span><strong>Stages:</strong> {campaignStageCount(campaign)}</span>
						<span><strong>Circles:</strong> {campaign.circles}</span>
					</div>
					{campaign.hasBestCost ? (
						<>
							<div style={{ fontSize: "0.875rem", color: "var(--text-muted)", marginTop: "0.5rem" }}>Best cost</div>
							<div style={{ fontSize: "1.125rem", fontWeight: 600 }}>{formatFixed(campaign.bestCost, 2)}</div>
						</>
					) : null}
				</div>
				{/* Wraps because the thumbnail is a fixed 140px and the chart needs
				    room of its own: side by side they do not fit a phone-width card,
				    and without wrapping the card would scroll sideways instead. */}
				<div style={{ flex: "0 1 420px", minWidth: 0, display: "flex", flexWrap: "wrap", gap: "0.75rem", justifyContent: "flex-end", alignItems: "stretch" }}>
					{campaign.leafJobId ? (
						<a
							href={`/jobs/${campaign.leafJobId}`}
							style={{ display: "block", textDecoration: "none", flex: "0 0 auto" }}
							title={`Open campaign leaf job ${campaign.leafJobId}`}
						>
							<img
								src={`/api/v1/jobs/${campaign.leafJobId}/best.png`}
								alt="Latest campaign best result"
								style={{
									display: "block",
									// Fixed at 140x100 the thumbnail never gave ground, so on a
									// phone it and the chart competed for a card narrower than
									// either. It may now shrink; the aspect ratio holds it.
									width: "140px",
									maxWidth: "100%",
									height: "100px",
									objectFit: "cover",
									objectPosition: "center",
									borderRadius: "0.375rem",
									border: "1px solid var(--border-color)",
									backgroundColor: "var(--bg-color)",
								}}
							/>
						</a>
					) : null}
					{plotted.length > 0 ? (
						<div style={{ flex: "1 1 180px", minWidth: "min(180px, 100%)", minHeight: "120px" }}>
							<CampaignCostChart points={plotted} palette={palette} />
						</div>
					) : null}
				</div>
			</div>
		</div>
	);
}

mountIslands({
	dashboard: DashboardIsland,
	"create-job": CreateJobIsland,
	"job-list": JobListIsland,
	"campaign-list": CampaignListIsland,
	"campaign-detail": CampaignDetailIsland,
	"job-controls": JobControlsIsland,
	settings: SettingsIsland,
	// Rendered by the layout on every page, so this one mounts everywhere.
	"theme-switch": ThemeToggleIsland,
});
