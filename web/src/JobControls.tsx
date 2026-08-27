import { useEffect, useMemo, useState } from "react";
import { fetchJSON, useLiveResource } from "./live";
import type { ProgressEvent, UIEvent } from "./live";
import { LiveStatus } from "./LiveStatus";

type JobActions = { pause: boolean; resume: boolean; cancel: boolean; delete: boolean; polish: boolean };
type JobStatus = ProgressEvent & {
	id: string;
	project: string;
	initialCost: number;
	maxIterations: number;
	elapsed: number;
	actions: JobActions;
	error?: string;
	termination?: string;
	metricHistory?: Array<{
		iteration: number; evaluations: number; cost: number; psnr?: number | null;
		psnrInfinite?: boolean; ssim?: number | null; cps: number; timestamp: string;
	}>;
};

function numberData(root: HTMLElement, name: string): number {
	const value = Number(root.dataset[name]);
	return Number.isFinite(value) ? value : 0;
}

function initialStatus(root: HTMLElement): JobStatus {
	const state = root.dataset.jobState ?? "pending";
	return {
		id: root.dataset.jobId ?? "",
		jobId: root.dataset.jobId ?? "",
		project: "",
		state,
		iterations: numberData(root, "iterations"),
		evaluations: numberData(root, "evaluations"),
		bestCost: numberData(root, "bestCost"),
		bestRevision: numberData(root, "bestRevision"),
		initialCost: numberData(root, "initialCost"),
		maxIterations: numberData(root, "maxIterations"),
		elapsed: 0,
		cps: numberData(root, "cps"),
		timestamp: new Date().toISOString(),
		actions: {
			pause: state === "running",
			resume: state === "paused",
			cancel: state === "running" || state === "pending" || state === "paused",
			delete: state === "completed" || state === "failed" || state === "cancelled",
			polish: root.dataset.canPolish === "true",
		},
	};
}

function reduceStatus(current: JobStatus, event: UIEvent) {
	if (event.type === "job.deleted" && event.jobId === current.id) {
		window.location.assign("/jobs");
		return { value: current };
	}
	if (event.type !== "job.upsert" || event.jobId !== current.id || !event.progress) return { value: current };
	const stateChanged = current.state !== event.progress.state;
	// metricHistory is dropped: only a fresh metrics response may republish the
	// series. Keeping the previous one would let the stale payload overwrite the
	// live sample the progress listener has just appended.
	return {
		value: { ...current, ...event.progress, id: current.id, metricHistory: undefined },
		refresh: stateChanged,
	};
}

export function JobControlsIsland({ root }: { root: HTMLElement }) {
	const initial = useMemo(() => initialStatus(root), [root]);
	const { value: status, status: liveState, error, refresh } = useLiveResource({
		initial,
		load: async (signal) => {
			const [next, metricHistory] = await Promise.all([
				fetchJSON<JobStatus>(`/api/v1/jobs/${encodeURIComponent(initial.id)}/status`, signal),
				fetchJSON<JobStatus["metricHistory"]>(`/api/v1/jobs/${encodeURIComponent(initial.id)}/metrics?limit=1000`, signal),
			]);
			return { ...next, metricHistory };
		},
		reduce: reduceStatus,
	});
	const [busy, setBusy] = useState<string | null>(null);

	useEffect(() => {
		document.dispatchEvent(new CustomEvent("mayflycirclefit:job-status", { detail: status }));
		if (status.metricHistory) {
			document.dispatchEvent(new CustomEvent("mayflycirclefit:job-metrics", { detail: status.metricHistory }));
		}
	}, [status]);

	async function action(name: "pause" | "resume" | "cancel") {
		if ((name === "pause" || name === "cancel") && !window.confirm(`${name[0].toUpperCase() + name.slice(1)} this job?`)) return;
		setBusy(name);
		try {
			await post(`/api/v1/jobs/${encodeURIComponent(status.id)}/${name}`);
			await refresh();
		} catch (reason) {
			window.alert(reason instanceof Error ? reason.message : `Unable to ${name} job`);
		} finally { setBusy(null); }
	}

	async function deleteJob() {
		if (!window.confirm("Delete this job? This cannot be undone.")) return;
		setBusy("delete");
		try {
			const response = await fetch(`/api/v1/jobs/${encodeURIComponent(status.id)}`, { method: "DELETE" });
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
			const response = await fetch(`/api/v1/jobs/${encodeURIComponent(status.id)}/polish`, { method: "POST" });
			if (!response.ok) throw await apiError(response);
			const result = await response.json() as { jobId: string };
			window.location.assign(`/jobs/${result.jobId}`);
		} catch (reason) {
			window.alert(reason instanceof Error ? reason.message : "Unable to start polishing");
			setBusy(null);
		}
	}

	return <div className="action-row">
		<StateBadge state={status.state} />
		{status.actions.pause ? <ActionButton label="Pause job" busy={busy === "pause"} onClick={() => void action("pause")} /> : null}
		{status.actions.resume ? <ActionButton label="Resume job" busy={busy === "resume"} onClick={() => void action("resume")} primary /> : null}
		{status.actions.cancel ? <ActionButton label="Cancel job" busy={busy === "cancel"} onClick={() => void action("cancel")} danger /> : null}
		{status.actions.delete ? <ActionButton label="Delete job" busy={busy === "delete"} onClick={() => void deleteJob()} danger /> : null}
		{status.actions.polish ? <ActionButton label="Polish weak circles" busy={busy === "polish"} onClick={() => void polish()} primary /> : null}
		<ActionButton label="Refresh" glyph="\u27f3" busy={false} onClick={() => void refresh()} />
		<LiveStatus state={liveState} error={error} style={{ fontSize: "0.75rem", marginTop: "0.35rem", flexBasis: "100%", textAlign: "right" }} />
		{status.error ? <div style={{ color: "var(--error-text)", fontSize: "0.75rem" }}>{status.error}</div> : null}
	</div>;
}

function ActionButton({ label, glyph, busy, onClick, danger, primary }: { label: string; glyph?: string; busy: boolean; onClick: () => void; danger?: boolean; primary?: boolean }) {
	// btn-danger pairs the accent background with its own foreground token.
	// The previous inline --error-color background kept btn-primary's white
	// text, which measures 2.3:1 against the dark palette's #f87171.
	const variant = danger ? " btn-danger" : primary ? " btn-primary" : "";
	return (
		<button disabled={busy} aria-busy={busy} onClick={onClick} className={`btn${variant}`}>
			{/* The glyph is decoration; without aria-hidden it joins the
			    button's accessible name as "⟳ Refresh". */}
			{glyph ? <span aria-hidden="true">{glyph} </span> : null}
			{busy ? "Working…" : label}
		</button>
	);
}

function StateBadge({ state }: { state: string }) {
	const kind = state === "completed" ? "badge-success" : state === "failed" ? "badge-error" : state === "paused" || state === "cancelled" ? "badge-warning" : "badge-info";
	return <span className={`badge ${kind}`}>{state[0]?.toUpperCase() + state.slice(1)}</span>;
}

async function post(url: string): Promise<void> {
	const response = await fetch(url, { method: "POST" });
	if (!response.ok) throw await apiError(response);
}

async function apiError(response: Response): Promise<Error> {
	try {
		const payload = await response.json() as { error?: { message?: string }; message?: string };
		return new Error(payload.error?.message ?? payload.message ?? `Request failed: ${response.status}`);
	} catch {
		return new Error(`Request failed: ${response.status}`);
	}
}
