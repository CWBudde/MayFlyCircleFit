import { useMemo } from "react";
import { fetchJSON, useLiveResource } from "./live";
import type { UIEvent } from "./live";

type JobListItem = {
	id: string;
	state: string;
	refPath: string;
	mode: string;
	circles: number;
	iterations: number;
	bestCost: number;
	initialCost: number;
	startTime: string;
	error?: string;
};

type RawJob = {
	id: string;
	state: string;
	config: { ref: string; refPath?: string; mode: string; circles: number };
	iterations: number;
	bestCost: number;
	initialCost: number;
	startTime: string;
	error?: string;
};

function readSeed(root: HTMLElement): JobListItem[] {
	const script = root.querySelector<HTMLScriptElement>("#job-list-page");
	if (!script) return [];
	try {
		const value = JSON.parse(script.textContent || "[]") as JobListItem[];
		return Array.isArray(value) ? value : [];
	} catch {
		return [];
	}
}

function fromRaw(job: RawJob): JobListItem {
	return {
		id: job.id,
		state: job.state,
		refPath: job.config.refPath ?? job.config.ref ?? "",
		mode: job.config.mode,
		circles: job.config.circles,
		iterations: job.iterations,
		bestCost: job.bestCost,
		initialCost: job.initialCost,
		startTime: job.startTime,
		error: job.error,
	};
}

function reduceJobs(current: JobListItem[], event: UIEvent) {
	if (event.type === "job.upsert" || event.type === "job.deleted") {
		return { value: current, refresh: true };
	}
	return { value: current };
}

export function JobListIsland({ root }: { root: HTMLElement }) {
	const initial = useMemo(() => readSeed(root), [root]);
	const { value: jobs, connected, error } = useLiveResource({
		initial,
		load: async (signal) => (await fetchJSON<RawJob[]>("/api/v1/jobs", signal)).map(fromRaw),
		reduce: reduceJobs,
	});
	return (
		<div>
			<div style={{ marginBottom: "2rem", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
				<h1 style={{ fontSize: "2rem", fontWeight: 700 }}>Optimization Jobs</h1>
				<a href="/create" className="btn btn-primary">+ Create New Job</a>
			</div>
			{jobs.length === 0 ? (
				<div className="card" style={{ textAlign: "center", padding: "3rem" }}>
					<h2 style={{ fontSize: "1.25rem", fontWeight: 600 }}>No jobs yet</h2>
					<p style={{ color: "var(--text-muted)", marginBottom: "1.5rem" }}>Create your first optimization job to get started.</p>
					<a href="/create" className="btn btn-primary">Create Job</a>
				</div>
			) : (
				<div style={{ display: "grid", gap: "1rem" }}>
					{jobs.map((job) => <JobCard key={job.id} job={job} />)}
				</div>
			)}
			<p style={{ color: "var(--text-muted)", fontSize: "0.875rem", marginTop: "1rem" }}>
				Live updates: {connected ? "connected" : "reconnecting"}{error ? ` · ${error}` : ""}
			</p>
		</div>
	);
}

function JobCard({ job }: { job: JobListItem }) {
	const improvement = job.initialCost > 0 ? (1 - job.bestCost / job.initialCost) * 100 : null;
	return (
		<a href={`/jobs/${job.id}`} style={{ textDecoration: "none", color: "inherit" }}>
			<div className="card" style={{ cursor: "pointer" }}>
				<div style={{ display: "flex", justifyContent: "space-between", gap: "1rem", marginBottom: "1rem" }}>
					<div>
						<div style={{ display: "flex", alignItems: "center", gap: "0.75rem", marginBottom: "0.5rem" }}>
							<h3 style={{ fontSize: "1.125rem", fontWeight: 600, fontFamily: "monospace" }}>{job.id.slice(0, 8)}…</h3>
							<StateBadge state={job.state} />
						</div>
						<div style={{ display: "flex", gap: "1.5rem", color: "var(--text-muted)", fontSize: "0.875rem" }}>
							<span><strong>Mode:</strong> {job.mode}</span><span><strong>Circles:</strong> {job.circles}</span>
							<span><strong>Iterations:</strong> {job.iterations}</span>
						</div>
					</div>
					{job.state === "running" || job.state === "completed" ? (
						<div style={{ textAlign: "right" }}><div style={{ color: "var(--text-muted)", fontSize: "0.875rem" }}>Cost</div>
							<div style={{ fontSize: "1.25rem", fontWeight: 600 }}>{job.bestCost.toFixed(2)}</div>
							{improvement !== null ? <div style={{ color: "var(--success-color)", fontSize: "0.75rem" }}>{improvement.toFixed(1)}% improvement</div> : null}
						</div>
					) : null}
				</div>
				<div style={{ borderTop: "1px solid var(--border-color)", paddingTop: "1rem", color: "var(--text-muted)", fontSize: "0.875rem" }}>
					<strong>Reference:</strong> {job.refPath}
				</div>
				{job.error ? <div style={{ marginTop: "0.75rem", color: "var(--error-text)" }}><strong>Error:</strong> {job.error}</div> : null}
			</div>
		</a>
	);
}

function StateBadge({ state }: { state: string }) {
	const kind = state === "completed" ? "badge-success" : state === "failed" ? "badge-error" : state === "cancelled" || state === "paused" ? "badge-warning" : "badge-info";
	return <span className={`badge ${kind}`}>{state ? state[0].toUpperCase() + state.slice(1) : "Unknown"}</span>;
}
