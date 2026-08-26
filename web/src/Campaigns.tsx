import { Fragment, useMemo } from "react";
import { CampaignCostChart } from "./CampaignCostChart";
import { useChartTheme } from "./charts";
import {
	acceptedSweepsTitle,
	campaignPerCircleRate,
	campaignPerHourRate,
	campaignProjectedFinish,
	campaignProjectedPlanEnd,
	campaignProjectionBasis,
	campaignProvenance,
	campaignRemainingCircles,
	campaignRemainingElapsed,
	campaignTitle,
	campaignWarningHeading,
	formatAcceptedSweeps,
	formatCost,
	formatElapsed,
	formatPsnr,
} from "./format";
import { fetchJSON, useLiveResource } from "./live";
import type { UIEvent } from "./live";
import { ImageViewer } from "./ImageViewer";

type CampaignPoint = { index: number; kind: string; circles: number; bestCost: number; hasBestCost: boolean };
type CampaignSummary = {
	id: string; name: string; state: string; source: "schedule" | "chain";
	recordedStages: number; plannedStages: number; campaignSeries: CampaignPoint[];
	leafJobId?: string; circles: number; bestCost: number; hasBestCost: boolean; updatedAt: string;
};
type CampaignList = { schedules: CampaignSummary[]; chains: CampaignSummary[] };

type CampaignStage = CampaignPoint & {
	state: string; psnr: number; psnrInfinite: boolean; hasPsnr: boolean;
	iterations: number; evaluations: number; elapsedSec: number; hasElapsed: boolean;
	elapsedAbsent: string; acceptedSweeps?: number; jobId: string; parentJobId: string; note: string;
};
// CampaignProjection mirrors ui.CampaignProjection. It is optional on the wire
// twice over: an imported chain has no plan to project, and a schedule that
// will not advance has nothing left to project towards.
type CampaignProjection = {
	projected: boolean; samples: number; recentLegs: number;
	recentCircles: number; recentElapsedSec: number;
	gainPerCircle: number; gainPerHour: number;
	recentGainPerCircle: number; recentGainPerHour: number;
	latestCircles: number; latestCost: number;
	remainingCircles: number; costAtPlanEnd: number;
	planEndPsnr: number; planEndPsnrInfinite: boolean; hasPlanEndPsnr: boolean; hasCircleCeiling: boolean;
	remainingElapsedSec: number; costAtFinish: number;
	finishPsnr: number; finishPsnrInfinite: boolean; hasFinishPsnr: boolean; hasTimeBudget: boolean;
	note: string;
};
type Campaign = {
	id: string; name: string; state: string; source: "schedule" | "chain";
	campaignSeed: number; hasSeed: boolean; plannedStages: number; stages: CampaignStage[]; error: string;
	warnings?: string[] | null; projection?: CampaignProjection | null;
};

function seed<T>(root: HTMLElement, id: string, fallback: T): T {
	const script = root.querySelector<HTMLScriptElement>(`#${id}`);
	if (!script) return fallback;
	try { return JSON.parse(script.textContent || "null") as T; } catch { return fallback; }
}

function campaignEvent<T>(current: T, event: UIEvent) {
	return event.type === "campaign.changed" || event.type === "job.deleted"
		? { value: current, refresh: true }
		: { value: current };
}

export function CampaignListIsland({ root }: { root: HTMLElement }) {
	const initial = useMemo(() => {
		const seeded = seed<Partial<CampaignList>>(root, "campaign-list-page", {});
		return { schedules: seeded.schedules ?? [], chains: seeded.chains ?? [] };
	}, [root]);
	const { value, connected, error } = useLiveResource({
		initial,
		load: async (signal) => {
			const loaded = await fetchJSON<Partial<CampaignList>>("/api/v1/campaigns", signal);
			return { schedules: loaded.schedules ?? [], chains: loaded.chains ?? [] };
		},
		reduce: campaignEvent<CampaignList>,
	});
	return <div>
		<div style={{ marginBottom: "2rem" }}><h1 style={{ fontSize: "2rem", fontWeight: 700 }}>Campaigns</h1>
			<p style={{ color: "var(--text-muted)" }}>A campaign is a chain of stages read as one run rather than as unrelated jobs.</p></div>
		<CampaignSection title="Schedules" empty="No schedules yet." campaigns={value.schedules} />
		<CampaignSection title="Imported chains" empty="No multi-stage chains outside a schedule." campaigns={value.chains} />
		<p style={{ color: "var(--text-muted)", fontSize: "0.875rem" }}>Live updates: {connected ? "connected" : "reconnecting"}{error ? ` · ${error}` : ""}</p>
	</div>;
}

function CampaignSection({ title, empty, campaigns }: { title: string; empty: string; campaigns: CampaignSummary[] }) {
	return <section style={{ marginBottom: "2rem" }}><h2 style={{ fontSize: "1.25rem", fontWeight: 600, marginBottom: "0.75rem" }}>{title}</h2>
		{campaigns.length === 0 ? <div className="card" style={{ color: "var(--text-muted)" }}>{empty}</div> :
			<div style={{ display: "grid", gap: "1rem" }}>{campaigns.map((item) => <CampaignCard key={`${item.source}:${item.id}`} item={item} />)}</div>}
	</section>;
}

function CampaignCard({ item }: { item: CampaignSummary }) {
	const href = item.source === "chain" ? `/chains/${item.id}` : `/schedules/${item.id}`;
	return <a href={href} style={{ textDecoration: "none", color: "inherit" }}><div className="card">
		<div style={{ display: "flex", justifyContent: "space-between", gap: "1rem" }}><div>
			<div style={{ display: "flex", gap: "0.75rem", alignItems: "center" }}><h3 style={{ fontFamily: "monospace" }}>{item.id.slice(0, 8)}</h3><Badge state={item.state} />{item.name ? <span>{item.name}</span> : null}</div>
			<div style={{ color: "var(--text-muted)", fontSize: "0.875rem" }}><strong>Stages:</strong> {item.recordedStages}{item.plannedStages ? ` / ${item.plannedStages}` : ""} · <strong>Circles:</strong> {item.circles}</div>
		</div>{item.hasBestCost ? <div style={{ textAlign: "right" }}><small>Best cost</small><div style={{ fontSize: "1.25rem", fontWeight: 600 }}>{item.bestCost.toFixed(2)}</div></div> : null}</div>
	</div></a>;
}

export function CampaignDetailIsland({ root }: { root: HTMLElement }) {
	const palette = useChartTheme();
	const initial = useMemo(() => seed<Campaign | null>(root, "campaign-detail-page", null), [root]);
	const source = initial?.source ?? (window.location.pathname.startsWith("/chains/") ? "chain" : "schedule");
	const pathParts = window.location.pathname.split("/").filter(Boolean);
	const id = initial?.id ?? pathParts[pathParts.length - 1] ?? "";
	const { value: campaign, connected, error } = useLiveResource({
		initial,
		load: (signal) => fetchJSON<Campaign>(`/api/v1/campaigns/${source}/${encodeURIComponent(id)}`, signal),
		reduce: campaignEvent<Campaign | null>,
	});
	if (!campaign) return <p>{error ?? "Loading campaign…"}</p>;
	const points = campaign.stages.map(({ index, kind, circles, bestCost, hasBestCost }) => ({ index, kind, circles, bestCost, hasBestCost }));
	const latest = [...campaign.stages].reverse().find((stage) => stage.state === "completed" && stage.jobId);
	return <div>
		<div style={{ marginBottom: "1.5rem" }}><div style={{ display: "flex", gap: "0.75rem", alignItems: "center" }}><h1>{campaignTitle(campaign)}</h1><Badge state={campaign.state} /></div><code>{campaign.id}</code>
			<div style={{ color: "var(--text-muted)", fontSize: "0.875rem", marginTop: "0.25rem" }}>{campaignProvenance(campaign)}</div></div>
		{campaign.error ? <div className="card" style={{ color: "var(--error-text)" }}>{campaign.error}</div> : null}
		{campaign.warnings && campaign.warnings.length > 0 ? <CampaignWarnings warnings={campaign.warnings} /> : null}
		<div className="card"><h2>Cost against circle count</h2><CampaignCostChart points={points} palette={palette} variant="full" /></div>
		{campaign.projection ? <CampaignProjectionCard projection={campaign.projection} /> : null}
		{latest ? <ImageViewer jobId={latest.jobId} revision={latest.iterations} /> : <div className="card">No completed stage has produced image artifacts yet.</div>}
		<div className="card"><h2>Stages</h2><div style={{ overflowX: "auto" }}><table style={{ width: "100%", borderCollapse: "collapse" }}><thead><tr><th>#</th><th>Stage</th><th>State</th><th>Circles</th><th>Cost</th><th>PSNR</th><th>Elapsed</th><th title="Accepted polishing sweeps">Accepted</th><th>Job</th></tr></thead><tbody>
			{campaign.stages.map((stage) => <Fragment key={stage.index}>
				<tr style={{ borderTop: "1px solid var(--border-color)" }}><td>{stage.index}</td><td>{stage.kind}</td><td><Badge state={stage.state} /></td><td>{stage.circles}</td><td>{formatCost(stage)}</td><td>{formatPsnr(stage)}</td><td title={stage.elapsedAbsent}>{formatElapsed(stage)}</td><td title={acceptedSweepsTitle(stage)}>{formatAcceptedSweeps(stage)}</td><td>{stage.jobId ? <a href={`/jobs/${stage.jobId}`}>{stage.jobId.slice(0, 8)}</a> : "—"}</td></tr>
				{stage.note ? <tr><td colSpan={9} style={{ color: "var(--text-muted)", fontSize: "0.8125rem" }}>{stage.note}</td></tr> : null}
			</Fragment>)}
		</tbody></table></div></div>
		<p style={{ color: "var(--text-muted)", fontSize: "0.875rem" }}>Live updates: {connected ? "connected" : "reconnecting"}{error ? ` · ${error}` : ""}</p>
	</div>;
}

// CampaignWarnings mirrors the advisory card in schedule.templ. The document
// runs exactly as authored, so it is coloured as a warning rather than as the
// error card above it.
function CampaignWarnings({ warnings }: { warnings: string[] }) {
	return <div className="card" style={{ backgroundColor: "var(--warning-bg)", color: "var(--warning-text)" }}>
		<strong>{campaignWarningHeading(warnings)}</strong>
		<ul style={{ margin: "0.5rem 0 0 1.25rem" }}>{warnings.map((warning) => <li key={warning} style={{ marginBottom: "0.25rem" }}>{warning}</li>)}</ul>
	</div>;
}

// CampaignProjectionCard mirrors ui.CampaignProjectionCard: two columns side by
// side, because a reader is short of circles or short of time and the answers
// differ once the rates diverge.
function CampaignProjectionCard({ projection }: { projection: CampaignProjection }) {
	return <div className="card">
		<h2 style={{ fontSize: "1.125rem", fontWeight: 600, marginBottom: "0.25rem" }}>Where the fit lands</h2>
		<p style={{ color: "var(--text-muted)", fontSize: "0.875rem", marginBottom: "1rem" }}>{campaignProjectionBasis(projection)}</p>
		<div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(18rem, 1fr))", gap: "1.5rem" }}>
			<div><h3 style={{ fontSize: "0.9375rem", fontWeight: 600, marginBottom: "0.5rem" }}>Against a circle ceiling</h3>
				<ProjectionRow label="Measured" value={campaignPerCircleRate(projection)} />
				<ProjectionRow label="Remaining" value={campaignRemainingCircles(projection)} />
				<ProjectionRow label="Projected" value={campaignProjectedPlanEnd(projection)} /></div>
			<div><h3 style={{ fontSize: "0.9375rem", fontWeight: 600, marginBottom: "0.5rem" }}>Against a time budget</h3>
				<ProjectionRow label="Measured" value={campaignPerHourRate(projection)} />
				<ProjectionRow label="Remaining" value={campaignRemainingElapsed(projection)} />
				<ProjectionRow label="Projected" value={campaignProjectedFinish(projection)} /></div>
		</div>
		{projection.note ? <p style={{ color: "var(--text-muted)", fontSize: "0.8125rem", marginTop: "1rem" }}>{projection.note}</p> : null}
		<p style={{ color: "var(--text-muted)", fontSize: "0.8125rem", marginTop: "0.5rem" }}>
			The two answers differ because the objective does: while the circle budget is open, gain per hour
			decides; against a fixed ceiling, gain per circle does. See <code>docs/schedule-format.md</code>.
		</p>
	</div>;
}

function ProjectionRow({ label, value }: { label: string; value: string }) {
	return <div style={{ display: "flex", gap: "0.75rem", fontSize: "0.875rem", marginBottom: "0.25rem" }}>
		<span style={{ color: "var(--text-muted)", minWidth: "5.5rem" }}>{label}</span><span>{value}</span>
	</div>;
}

function Badge({ state }: { state: string }) {
	const kind = state === "completed" ? "badge-success" : state === "failed" ? "badge-error" : state === "paused" || state === "cancelled" ? "badge-warning" : "badge-info";
	return <span className={`badge ${kind}`}>{state}</span>;
}
