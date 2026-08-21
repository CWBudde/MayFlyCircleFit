import { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
	config: { ref?: string; refPath?: string; mode: string; circles: number };
	iterations: number;
	bestCost: number;
	initialCost: number;
	startTime: string;
	error?: string;
};

type RawJobPage = {
	jobs: RawJob[];
	nextCursor?: string;
	total: number;
};

type JobPage = {
	jobs: JobListItem[];
	nextCursor?: string;
	total: number;
};

const JOB_PAGE_SIZE = 100;

function readSeed(root: HTMLElement): JobPage {
	const script = root.querySelector<HTMLScriptElement>("#job-list-page");
	if (!script) return { jobs: [], total: 0 };
	try {
		const value = JSON.parse(script.textContent || "{}") as JobPage | JobListItem[];
		if (Array.isArray(value)) return { jobs: value, total: value.length };
		return Array.isArray(value.jobs) && Number.isSafeInteger(value.total)
			? value
			: { jobs: [], total: 0 };
	} catch {
		return { jobs: [], total: 0 };
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

function fromRawPage(page: RawJobPage): JobPage {
	return { jobs: page.jobs.map(fromRaw), nextCursor: page.nextCursor, total: page.total };
}

function mergeFirstPage(current: JobPage, fresh: JobPage): JobPage {
	const firstIDs = new Set(fresh.jobs.map((job) => job.id));
	const jobs = [...fresh.jobs, ...current.jobs.filter((job) => !firstIDs.has(job.id))];
	return {
		jobs,
		total: fresh.total,
		nextCursor: jobs.length >= fresh.total ? undefined : current.nextCursor ?? fresh.nextCursor,
	};
}

function reduceJobs(current: JobPage, event: UIEvent) {
	if (event.type === "job.deleted" && event.jobId) {
		const jobs = current.jobs.filter((job) => job.id !== event.jobId);
		return { value: { ...current, jobs, total: Math.max(0, current.total - (jobs.length < current.jobs.length ? 1 : 0)) } };
	}
	if (event.type === "job.upsert" && event.jobId && event.progress) {
		const index = current.jobs.findIndex((job) => job.id === event.jobId);
		if (index < 0) {
			// A newly-created job belongs at the head. Terminal events for older,
			// unloaded jobs do not need to disturb the user's scroll position.
			return { value: current, refresh: event.progress.state === "pending" };
		}
		const jobs = [...current.jobs];
		jobs[index] = {
			...jobs[index],
			state: event.progress.state,
			iterations: event.progress.iterations,
			bestCost: event.progress.bestCost,
		};
		return { value: { ...current, jobs } };
	}
	return { value: current };
}

export function JobListIsland({ root }: { root: HTMLElement }) {
	const initial = useMemo(() => readSeed(root), [root]);
	const { value, connected, error, update } = useLiveResource({
		initial,
		load: async (signal, current) => mergeFirstPage(
			current,
			fromRawPage(await fetchJSON<RawJobPage>(`/api/v1/jobs?limit=${JOB_PAGE_SIZE}`, signal)),
		),
		reduce: reduceJobs,
	});
	const [loadingMore, setLoadingMore] = useState(false);
	const [pageError, setPageError] = useState<string | null>(null);
	const sentinelRef = useRef<HTMLDivElement | null>(null);
	const pageControllerRef = useRef<AbortController | null>(null);

	const loadMore = useCallback(async () => {
		const cursor = value.nextCursor;
		if (!cursor || pageControllerRef.current) return;
		setLoadingMore(true);
		setPageError(null);
		const controller = new AbortController();
		pageControllerRef.current = controller;
		try {
			const next = fromRawPage(await fetchJSON<RawJobPage>(
				`/api/v1/jobs?limit=${JOB_PAGE_SIZE}&cursor=${encodeURIComponent(cursor)}`,
				controller.signal,
			));
			update((current) => {
				if (current.nextCursor !== cursor) return current;
				const seen = new Set(current.jobs.map((job) => job.id));
				return {
					jobs: [...current.jobs, ...next.jobs.filter((job) => !seen.has(job.id))],
					nextCursor: next.nextCursor,
					total: next.total,
				};
			});
		} catch (reason) {
			if (!controller.signal.aborted) {
				setPageError(reason instanceof Error ? reason.message : "Unable to load more jobs");
			}
		} finally {
			if (pageControllerRef.current === controller) pageControllerRef.current = null;
			if (!controller.signal.aborted) setLoadingMore(false);
		}
	}, [update, value.nextCursor]);

	useEffect(() => {
		const sentinel = sentinelRef.current;
		if (!sentinel || !value.nextCursor || loadingMore) return;
		const observer = new IntersectionObserver((entries) => {
			if (entries.some((entry) => entry.isIntersecting)) void loadMore();
		}, { rootMargin: "500px 0px" });
		observer.observe(sentinel);
		return () => observer.disconnect();
	}, [loadMore, loadingMore, value.nextCursor]);

	useEffect(() => () => pageControllerRef.current?.abort(), []);

	const jobs = value.jobs;
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
			<div ref={sentinelRef} style={{ textAlign: "center", padding: value.nextCursor ? "1.5rem" : "0.5rem" }}>
				{value.nextCursor ? (
					<button className="btn" type="button" onClick={() => void loadMore()} disabled={loadingMore}>
						{loadingMore ? "Loading more jobs…" : "Load more jobs"}
					</button>
				) : jobs.length > 0 ? (
					<span style={{ color: "var(--text-muted)", fontSize: "0.875rem" }}>All {value.total} jobs loaded</span>
				) : null}
				{pageError ? <div style={{ color: "var(--error-text)", marginTop: "0.5rem" }}>{pageError}</div> : null}
			</div>
			<p style={{ color: "var(--text-muted)", fontSize: "0.875rem", marginTop: "1rem" }}>
				Showing {jobs.length} of {value.total} · Live updates: {connected ? "connected" : "reconnecting"}{error ? ` · ${error}` : ""}
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
