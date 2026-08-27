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
	stateBadgeStyle,
	stateClass,
	stateLabel,
} from "./format";
import { fetchJSON, useLiveResource } from "./live";
import type { UIEvent } from "./live";
import { LiveStatus } from "./LiveStatus";
import { SkeletonBar } from "./Skeleton";
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
	const { value, status, error } = useLiveResource({
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
		<LiveStatus state={status} error={error} />
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
	return <a href={href} className="card-link"><div className="card">
		<div className="row-between row-between-top"><div style={{ minWidth: 0 }}>
			<div style={{ display: "flex", flexWrap: "wrap", gap: "0.75rem", alignItems: "center" }}><h3 style={{ fontFamily: "monospace" }}>{item.id.slice(0, 8)}</h3><span className={stateClass(item.state)} style={stateBadgeStyle(item.state)}>{stateLabel(item.state)}</span>{item.name ? <span>{item.name}</span> : null}</div>
			<div style={{ color: "var(--text-muted)", fontSize: "0.875rem" }}><strong>Stages:</strong> {item.recordedStages}{item.plannedStages ? ` / ${item.plannedStages}` : ""} · <strong>Circles:</strong> {item.circles}</div>
		</div>{item.hasBestCost ? <div className="row-end"><small>Best cost</small><div style={{ fontSize: "1.25rem", fontWeight: 600 }}>{item.bestCost.toFixed(2)}</div></div> : null}</div>
	</div></a>;
}

// The bare "Loading campaign…" paragraph told a screen reader nothing — it was
// not a live region, so it was never announced — and it left the page a blank
// sheet that jumped once the payload landed. The blocks stand in for the cards
// that replace them; the sentence stays, in a region that speaks.
function CampaignSkeleton({ error }: { error: string | null }) {
	return <div>
		<p role="status" aria-live="polite" style={{ color: "var(--text-muted)" }}>{error ?? "Loading campaign…"}</p>
		{error ? null : <>
			<div className="card" style={{ display: "grid", gap: "0.75rem" }}>
				<SkeletonBar width="45%" height="1.5rem" /><SkeletonBar width="25%" />
			</div>
			<div className="card"><SkeletonBar width="100%" height="17rem" /></div>
			<div className="card" style={{ display: "grid", gap: "0.75rem" }}>
				<SkeletonBar width="30%" height="1.25rem" /><SkeletonBar width="100%" height="2rem" /><SkeletonBar width="100%" height="2rem" />
			</div>
		</>}
	</div>;
}

export function CampaignDetailIsland({ root }: { root: HTMLElement }) {
	const palette = useChartTheme();
	const initial = useMemo(() => seed<Campaign | null>(root, "campaign-detail-page", null), [root]);
	const source = initial?.source ?? (window.location.pathname.startsWith("/chains/") ? "chain" : "schedule");
	const pathParts = window.location.pathname.split("/").filter(Boolean);
	const id = initial?.id ?? pathParts[pathParts.length - 1] ?? "";
	const { value: campaign, status, error } = useLiveResource({
		initial,
		load: (signal) => fetchJSON<Campaign>(`/api/v1/campaigns/${source}/${encodeURIComponent(id)}`, signal),
		reduce: campaignEvent<Campaign | null>,
	});
	if (!campaign) return <CampaignSkeleton error={error} />;
	const points = campaign.stages.map(({ index, kind, circles, bestCost, hasBestCost }) => ({ index, kind, circles, bestCost, hasBestCost }));
	const latest = [...campaign.stages].reverse().find((stage) => stage.state === "completed" && stage.jobId);
	return <div>
		<div style={{ marginBottom: "1.5rem" }}><div style={{ display: "flex", flexWrap: "wrap", gap: "0.75rem", alignItems: "center" }}><h1>{campaignTitle(campaign)}</h1><span className={stateClass(campaign.state)} style={stateBadgeStyle(campaign.state)}>{stateLabel(campaign.state)}</span></div><code style={{ overflowWrap: "anywhere" }}>{campaign.id}</code>
			<div style={{ color: "var(--text-muted)", fontSize: "0.875rem", marginTop: "0.25rem" }}>{campaignProvenance(campaign)}</div></div>
		{campaign.error ? <div className="card" style={{ color: "var(--error-text)" }}>{campaign.error}</div> : null}
		{campaign.warnings && campaign.warnings.length > 0 ? <CampaignWarnings warnings={campaign.warnings} /> : null}
		<div className="card"><h2>Cost against circle count</h2><CampaignCostChart points={points} palette={palette} variant="full" /></div>
		{campaign.projection ? <CampaignProjectionCard projection={campaign.projection} /> : null}
		{latest ? <ImageViewer jobId={latest.jobId} revision={latest.iterations} jobState={latest.state} /> : <div className="card">No completed stage has produced image artifacts yet.</div>}
		<div className="card"><h2>Stages</h2>
			{/* Focusable named region: the nine columns scroll sideways on a phone,
			    and a keyboard has no other way to reach the ones off-screen. */}
			<div className="table-scroll" role="region" aria-label="Campaign stages" tabIndex={0}><table style={{ width: "100%", borderCollapse: "collapse" }}><thead><tr><th scope="col">#</th><th scope="col">Stage</th><th scope="col">State</th><th scope="col">Circles</th><th scope="col">Cost</th><th scope="col">PSNR</th><th scope="col">Elapsed</th><th scope="col" title="Accepted polishing sweeps">Accepted</th><th scope="col">Job</th></tr></thead><tbody>
			{campaign.stages.map((stage) => <Fragment key={stage.index}>
				<tr style={{ borderTop: "1px solid var(--border-color)" }}><td>{stage.index}</td><td>{stage.kind}</td><td><span className={stateClass(stage.state)} style={stateBadgeStyle(stage.state)}>{stateLabel(stage.state)}</span></td><td>{stage.circles}</td><td>{formatCost(stage)}</td><td>{formatPsnr(stage)}</td><td title={stage.elapsedAbsent}>{formatElapsed(stage)}</td><td title={acceptedSweepsTitle(stage)}>{formatAcceptedSweeps(stage)}</td><td>{stage.jobId ? <a href={`/jobs/${stage.jobId}`}>{stage.jobId.slice(0, 8)}</a> : "—"}</td></tr>
				{stage.note ? <tr><td colSpan={9} style={{ color: "var(--text-muted)", fontSize: "0.8125rem" }}>{stage.note}</td></tr> : null}
			</Fragment>)}
		</tbody></table></div></div>
		<LiveStatus state={status} error={error} />
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
		{/* 18rem is 288px, so without min() this grid alone overflowed a 320px
		    viewport once the card's padding was counted. */}
		<div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(min(18rem, 100%), 1fr))", gap: "1.5rem" }}>
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
