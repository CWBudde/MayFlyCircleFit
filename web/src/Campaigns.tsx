import { useMemo } from "react";
import { CampaignCostChart } from "./CampaignCostChart";
import { useChartTheme } from "./charts";
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
type Campaign = {
	id: string; name: string; state: string; source: "schedule" | "chain";
	campaignSeed: number; hasSeed: boolean; plannedStages: number; stages: CampaignStage[]; error: string;
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
		<div style={{ marginBottom: "1.5rem" }}><div style={{ display: "flex", gap: "0.75rem", alignItems: "center" }}><h1>{campaign.name || `Campaign ${campaign.id.slice(0, 8)}`}</h1><Badge state={campaign.state} /></div><code>{campaign.id}</code></div>
		{campaign.error ? <div className="card" style={{ color: "var(--error-text)" }}>{campaign.error}</div> : null}
		<div className="card"><h2>Cost against circle count</h2><CampaignCostChart points={points} palette={palette} variant="full" /></div>
		{latest ? <ImageViewer jobId={latest.jobId} revision={latest.iterations} /> : <div className="card">No completed stage has produced image artifacts yet.</div>}
		<div className="card"><h2>Stages</h2><div style={{ overflowX: "auto" }}><table style={{ width: "100%", borderCollapse: "collapse" }}><thead><tr><th>#</th><th>Stage</th><th>State</th><th>Circles</th><th>Cost</th><th>PSNR</th><th>Job</th></tr></thead><tbody>
			{campaign.stages.map((stage) => <tr key={stage.index} style={{ borderTop: "1px solid var(--border-color)" }}><td>{stage.index}</td><td>{stage.kind}</td><td><Badge state={stage.state} /></td><td>{stage.circles}</td><td>{stage.hasBestCost ? stage.bestCost.toFixed(3) : "—"}</td><td>{stage.psnrInfinite ? "∞" : stage.hasPsnr ? stage.psnr.toFixed(2) : "—"}</td><td>{stage.jobId ? <a href={`/jobs/${stage.jobId}`}>{stage.jobId.slice(0, 8)}</a> : "—"}</td></tr>)}
		</tbody></table></div></div>
		<p style={{ color: "var(--text-muted)", fontSize: "0.875rem" }}>Live updates: {connected ? "connected" : "reconnecting"}{error ? ` · ${error}` : ""}</p>
	</div>;
}

function Badge({ state }: { state: string }) {
	const kind = state === "completed" ? "badge-success" : state === "failed" ? "badge-error" : state === "paused" || state === "cancelled" ? "badge-warning" : "badge-info";
	return <span className={`badge ${kind}`}>{state}</span>;
}
