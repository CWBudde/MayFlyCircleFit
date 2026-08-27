import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { stateBadgeStyle, stateClass, stateLabel } from "./format";
import { fetchJSON, useLiveResource } from "./live";
import type { UIEvent } from "./live";
import { LiveStatus } from "./LiveStatus";

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

// compareJobOrder mirrors the server's listing order (ListJobSummaries in
// internal/server/job.go): start time descending, ties broken by ID ascending.
// Timestamps are compared as instants rather than as strings because Go omits
// trailing zeros from the fractional second, so two encodings of one instant
// can differ textually; the string comparison only breaks sub-millisecond ties
// that Date.parse cannot see.
function compareJobOrder(a: JobListItem, b: JobListItem): number {
	const left = Date.parse(a.startTime);
	const right = Date.parse(b.startTime);
	if (left !== right) return right - left;
	if (a.startTime !== b.startTime) return a.startTime < b.startTime ? 1 : -1;
	return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

// mergeFirstPage folds an authoritative first page into the pages already
// loaded. The refresh re-reads only the newest JOB_PAGE_SIZE jobs, so it can
// confirm or deny existence inside that window and says nothing beyond it. A
// retained row that falls inside the window but is missing from `fresh` has
// been deleted and must go, otherwise a `job.deleted` event lost to an SSE
// disconnect or sequence gap would keep it on screen for the life of the page
// and the list could outgrow `fresh.total`. Rows past the window are kept
// untouched, because dropping them would reset the reader's scroll on every
// reconciliation.
function mergeFirstPage(current: JobPage, fresh: JobPage): JobPage {
	// Without a next cursor the fresh page is the whole list, so the window
	// covers everything and nothing survives from the previous pages.
	const boundary = fresh.nextCursor ? fresh.jobs[fresh.jobs.length - 1] : undefined;
	const firstIDs = new Set(fresh.jobs.map((job) => job.id));
	const retained = boundary
		? current.jobs.filter((job) => !firstIDs.has(job.id) && compareJobOrder(job, boundary) > 0)
		: [];
	const jobs = [...fresh.jobs, ...retained];
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
	const { value, status, error, update } = useLiveResource({
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
			<div className="row-between" style={{ marginBottom: "2rem" }}>
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
					// aria-busy, not just `disabled`: the button keeps its own label
					// while a page is in flight, so nothing else tells assistive
					// technology that the press was accepted and is still working.
					<button
						className="btn"
						type="button"
						onClick={() => void loadMore()}
						disabled={loadingMore}
						aria-busy={loadingMore}
					>
						{loadingMore ? "Loading more jobs…" : "Load more jobs"}
					</button>
				) : jobs.length > 0 ? (
					<span style={{ color: "var(--text-muted)", fontSize: "0.875rem" }}>All {value.total} jobs loaded</span>
				) : null}
				{pageError ? <div role="alert" style={{ color: "var(--error-text)", marginTop: "0.5rem" }}>{pageError}</div> : null}
			</div>
			<LiveStatus state={status} error={error} style={{ marginTop: "1rem" }}>
				Showing {jobs.length} of {value.total} ·{" "}
			</LiveStatus>
		</div>
	);
}

function JobCard({ job }: { job: JobListItem }) {
	const improvement = job.initialCost > 0 ? (1 - job.bestCost / job.initialCost) * 100 : null;
	return (
		<a href={`/jobs/${job.id}`} className="card-link">
			<div className="card" style={{ cursor: "pointer" }}>
				<div className="row-between row-between-top" style={{ marginBottom: "1rem" }}>
					<div style={{ minWidth: 0 }}>
						<div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: "0.75rem", marginBottom: "0.5rem" }}>
							<h3 style={{ fontSize: "1.125rem", fontWeight: 600, fontFamily: "monospace" }}>{job.id.slice(0, 8)}…</h3>
							<span className={stateClass(job.state)} style={stateBadgeStyle(job.state)}>{stateLabel(job.state)}</span>
						</div>
						<div className="meta-row" style={{ color: "var(--text-muted)", fontSize: "0.875rem" }}>
							<span><strong>Mode:</strong> {job.mode}</span><span><strong>Circles:</strong> {job.circles}</span>
							<span><strong>Iterations:</strong> {job.iterations}</span>
						</div>
					</div>
					{job.state === "running" || job.state === "completed" ? (
						<div className="row-end"><div style={{ color: "var(--text-muted)", fontSize: "0.875rem" }}>Cost</div>
							<div style={{ fontSize: "1.25rem", fontWeight: 600 }}>{job.bestCost.toFixed(2)}</div>
							{improvement !== null ? <div style={{ color: "var(--success-text-strong)", fontSize: "0.75rem" }}>{improvement.toFixed(1)}% improvement</div> : null}
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
