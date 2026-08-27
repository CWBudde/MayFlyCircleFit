import { useState } from "react";
import {
	browserStorage,
	METRIC_IDS,
	normalizeMetricSelection,
	readPreferences,
	resetPreferences,
	writePreferences,
} from "./prefs";
import type { Colormap, MetricID, Preferences, ViewMode } from "./prefs";

// The browser-local preference editor. Nothing here talks to the server: every
// value lands in localStorage under the keys prefs.ts pins, which the job detail
// page and the image viewer read on their own.
//
// The templ page renders the same controls as the mount point's fallback, so
// the page is complete and readable without this bundle; what it cannot do
// there is remember anything, which the fallback says in a <noscript>.

const SAVED = "Saved. Open a job to see your new defaults.";
const SAVE_FAILED = "Could not save preferences. Local storage is not available.";
const RESET = "Reset to defaults.";
const RESET_FAILED = "Could not reset preferences. Local storage is not available.";

const REFRESH_OPTIONS: Array<{ value: number; label: string }> = [
	{ value: 0, label: "SSE-driven (default)" },
	{ value: 1000, label: "1 second" },
	{ value: 2000, label: "2 seconds" },
	{ value: 5000, label: "5 seconds" },
	{ value: 10000, label: "10 seconds" },
	{ value: 30000, label: "30 seconds" },
];

const VIEW_MODE_OPTIONS: Array<{ value: ViewMode; label: string }> = [
	{ value: "reference", label: "Reference" },
	{ value: "best", label: "Best" },
	{ value: "side-by-side", label: "Side-by-Side" },
	{ value: "difference", label: "Difference" },
	{ value: "overlay", label: "Overlay" },
];

const COLORMAP_OPTIONS: Array<{ value: Colormap; label: string }> = [
	{ value: "turbo", label: "Turbo" },
	{ value: "magma", label: "Magma" },
];

const METRIC_LABELS: Record<MetricID, string> = {
	cost: "Cost",
	psnr: "PSNR",
	ssim: "SSIM",
	cps: "Throughput (cps)",
};

const headingStyle = { fontSize: "1.125rem", fontWeight: 600, margin: 0 } as const;

export function SettingsIsland() {
	const [prefs, setPrefs] = useState<Preferences>(() => readPreferences(browserStorage()));
	const [status, setStatus] = useState("");

	// Every control commits immediately; there is no save button, and there was
	// none before the port either.
	const commit = (next: Preferences) => {
		setPrefs(next);
		setStatus(writePreferences(next, browserStorage()) ? SAVED : SAVE_FAILED);
	};

	const toggleMetric = (metric: MetricID, checked: boolean) => {
		const selected = checked
			? [...prefs.visibleMetrics, metric]
			: prefs.visibleMetrics.filter((entry) => entry !== metric);
		// Unchecking the last box restores all four rather than hiding every
		// metric card, which would leave no way back from the job page.
		commit({ ...prefs, visibleMetrics: normalizeMetricSelection(selected) });
	};

	const reset = () => {
		const storage = browserStorage();
		const removed = resetPreferences(storage);
		setPrefs(readPreferences(storage));
		setStatus(removed ? RESET : RESET_FAILED);
	};

	return (
		<>
			<div className="setting-group">
				<h2 style={headingStyle}>Image refresh</h2>
				<div className="setting-control">
					<label htmlFor="settings-image-refresh">Auto image refresh</label>
					<select
						id="settings-image-refresh"
						aria-describedby="settings-image-refresh-note"
						value={String(prefs.autoRefresh)}
						onChange={(event) => commit({ ...prefs, autoRefresh: Number(event.target.value) })}
					>
						{REFRESH_OPTIONS.map((option) => (
							<option key={option.value} value={String(option.value)}>{option.label}</option>
						))}
					</select>
				</div>
				<p id="settings-image-refresh-note" className="settings-note">SSE-driven updates keep metrics and images responsive while a job runs. Pick an interval only if the event stream is blocked, for example behind a buffering proxy; active jobs then poll the status endpoint instead.</p>
			</div>
			<div className="setting-group">
				<h2 style={headingStyle}>Image defaults</h2>
				<div className="setting-control">
					<label htmlFor="settings-default-view-mode">Default image view mode</label>
					<select
						id="settings-default-view-mode"
						aria-describedby="settings-default-view-mode-note"
						value={prefs.viewMode}
						onChange={(event) => commit({ ...prefs, viewMode: event.target.value as ViewMode })}
					>
						{VIEW_MODE_OPTIONS.map((option) => (
							<option key={option.value} value={option.value}>{option.label}</option>
						))}
					</select>
				</div>
				<p id="settings-default-view-mode-note" className="settings-note">Applies to every job page. Switching modes on a job page updates this value too.</p>
				<div className="setting-control">
					<label htmlFor="settings-default-colormap">Default difference colormap</label>
					<select
						id="settings-default-colormap"
						aria-describedby="settings-default-colormap-note"
						value={prefs.colormap}
						onChange={(event) => commit({ ...prefs, colormap: event.target.value as Colormap })}
					>
						{COLORMAP_OPTIONS.map((option) => (
							<option key={option.value} value={option.value}>{option.label}</option>
						))}
					</select>
				</div>
				<p id="settings-default-colormap-note" className="settings-note">Used for the difference heatmap and its artifact download link.</p>
			</div>
			<div className="setting-group">
				<h2 style={headingStyle}>Metric visibility</h2>
				<div className="settings-metric-list" id="settings-visible-metrics">
					{METRIC_IDS.map((metric) => (
						<div className="settings-metric-option" key={metric}>
							<input
								id={`settings-metric-${metric}`}
								type="checkbox"
								value={metric}
								checked={prefs.visibleMetrics.includes(metric)}
								onChange={(event) => toggleMetric(metric, event.target.checked)}
							/>
							<label htmlFor={`settings-metric-${metric}`}>{METRIC_LABELS[metric]}</label>
						</div>
					))}
				</div>
				<p id="settings-visible-metrics-note" className="settings-note">Controls which summary metric cards are visible on job detail pages. Clearing every box restores all of them.</p>
			</div>
			<div className="settings-actions">
				<button id="settings-reset" type="button" className="btn" style={{ background: "var(--border-color)" }} onClick={reset}>Reset to Defaults</button>
				<span id="settings-feedback" aria-live="polite" className="settings-note" role="status">{status}</span>
			</div>
		</>
	);
}
