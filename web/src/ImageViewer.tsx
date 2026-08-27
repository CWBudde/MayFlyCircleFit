import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import {
	browserStorage,
	COLORMAPS,
	normalizeAutoRefresh,
	normalizeColormap,
	normalizeOverlayOpacity,
	normalizeViewMode,
	OVERLAY_OPACITY_KEY,
	PREFERENCE_KEYS,
	readPreference,
	VIEW_MODES,
	writeOverlayOpacity,
	writePreference,
} from "./prefs";
import type { Colormap, ViewMode } from "./prefs";

// The one image viewer. Both pages that show reference/best/difference/overlay
// panels resolve here: the job detail page mounts ImageViewerIsland over the
// server-rendered card, and the campaign page renders ImageViewer directly from
// Campaigns.tsx, inside the campaign-detail island that owns that whole subtree.
// There is deliberately no second implementation -- ImageViewer.island.test.ts
// fails if one reappears.
//
// The class names and the data-view-mode contract come from layout.templ, which
// is also where the CSS lives: mounting an island replaces every child of its
// root, so a component-local <style> block would be destroyed on the campaign
// page. Panels are all rendered and CSS decides which are visible, exactly as
// the templ fallback does, so the server markup and the mounted markup show the
// same thing.

const MODES: Array<{ value: ViewMode; label: string }> = [
	{ value: "reference", label: "Reference" },
	{ value: "best", label: "Best" },
	{ value: "side-by-side", label: "Side-by-Side" },
	{ value: "difference", label: "Difference" },
	{ value: "overlay", label: "Overlay" },
];

const COLORMAP_LABELS: Record<Colormap, string> = { turbo: "Turbo", magma: "Magma" };

// The two gradients the difference endpoint renders with. The turbo stops are
// also the CSS default of .heatmap-legend-gradient in layout.templ, so a page
// that never mounts this component still shows a legend that matches its image.
const GRADIENTS: Record<Colormap, string> = {
	turbo: "linear-gradient(90deg, #23171b, #4145ab, #2aa7d6, #49df75, #d5e21a, #f68513, #900c00)",
	magma:
		"linear-gradient(90deg, #000004, #180f3d, #3d0f70, #65156e, #8c2961, #b7374b, " +
		"#de492f, #f67019, #fda50a, #f9dc5c, #fcfdbf)",
};

// States a job can still leave, which is what makes an image worth re-fetching.
const ACTIVE_STATES = ["running", "pending", "paused"];

export type ReferenceMetadata = {
	/** Pre-formatted "W × H px", or "" when the server has no dimensions. */
	dimensions: string;
	/** Pre-formatted file size, or "" when the server has no size. */
	fileSize: string;
	/** Exact byte count for the size tooltip, or "". */
	bytes: string;
};

export type ImageViewerProps = {
	jobId: string;
	/** Cache-busting best-image revision; 0 means "no revision known yet". */
	revision?: number;
	/** Drives the placeholder wording only; a pending job has different reasons. */
	jobState?: string;
	/** The server's preferred mode. A stored preference outranks it. */
	defaultMode?: string;
	referenceURL?: string;
	bestURL?: string;
	diffURL?: string;
	metadata?: ReferenceMetadata;
	/**
	 * The island mount point, when there is one. The templ partial renders the
	 * `.image-viewer` card itself and keeps the ids and data attributes the job
	 * detail script still reads, so mounting has to fill that element rather
	 * than nest a second card inside it.
	 */
	root?: HTMLElement;
	/** Extra classes for the standalone wrapper; ignored when `root` is set. */
	extraClass?: string;
	/** Fires with the resolved colormap on mount and on every change. */
	onColormap?: (colormap: Colormap) => void;
};

// initialViewMode is the precedence rule the inline script had: a valid stored
// preference wins, otherwise the server's default, otherwise side-by-side. It
// is not normalizeViewMode(stored) -- that would collapse an unset preference
// onto side-by-side and throw the caller's default away.
export function initialViewMode(stored: string | null, serverDefault: string | null): ViewMode {
	if (VIEW_MODES.includes(stored as ViewMode)) return stored as ViewMode;
	return normalizeViewMode(serverDefault);
}

// initialColormap follows the same rule for the difference heatmap.
export function initialColormap(stored: string | null, serverDefault: string | null): Colormap {
	if (COLORMAPS.includes(stored as Colormap)) return stored as Colormap;
	return normalizeColormap(serverDefault);
}

// withImageParams appends the colormap and the cache-busting revision to an
// image URL. It mirrors ui.imageViewerSrc on the Go side, including its rule
// for a base path that already carries a query string, so the src the server
// renders and the src this component renders for the same inputs agree.
export function withImageParams(
	base: string,
	params: { colormap?: string; revision?: number } = {},
): string {
	let url = base;
	const append = (pair: string) => {
		url += (url.includes("?") ? "&" : "?") + pair;
	};
	if (params.colormap) append(`colormap=${encodeURIComponent(params.colormap)}`);
	const revision = params.revision;
	if (typeof revision === "number" && Number.isFinite(revision) && revision > 0) {
		append(`v=${encodeURIComponent(String(revision))}`);
	}
	return url;
}

// heatmapGradient is the legend's fill for a colormap.
export function heatmapGradient(colormap: Colormap): string {
	return GRADIENTS[colormap];
}

// shortcutMode maps a KeyboardEvent.key to the mode its digit selects. The
// order is the order of the radios, so "1" is the first and "5" the last.
export function shortcutMode(key: string): ViewMode | null {
	const index = Number.parseInt(key, 10);
	if (!Number.isInteger(index)) return null;
	return MODES[index - 1]?.value ?? null;
}

// missingImageMessage is what a frame says when its image will not load. A
// pending job has never rendered anything, which is a different fact from a
// running job that has not produced a result yet.
export function missingImageMessage(jobState: string | undefined, kind: "best" | "difference"): string {
	if (jobState !== "pending") return "No results yet";
	return kind === "best" ? "Optimization not started yet" : "Not available yet";
}

export function ImageViewer(props: ImageViewerProps) {
	const {
		jobId,
		revision = 0,
		jobState,
		defaultMode,
		metadata,
		root,
		extraClass,
		onColormap,
	} = props;
	const ids = useId();
	const storage = browserStorage();
	const [mode, setMode] = useState<ViewMode>(() =>
		initialViewMode(readPreference(storage, PREFERENCE_KEYS.viewMode), defaultMode ?? null),
	);
	const [colormap, setColormap] = useState<Colormap>(() =>
		initialColormap(readPreference(storage, PREFERENCE_KEYS.colormap), null),
	);
	const [opacity, setOpacity] = useState(() =>
		normalizeOverlayOpacity(readPreference(storage, OVERLAY_OPACITY_KEY)),
	);

	const base = `/api/v1/jobs/${encodeURIComponent(jobId)}`;
	const referenceURL = props.referenceURL ?? `${base}/ref.png`;
	const bestURL = props.bestURL ?? `${base}/best.png`;
	const diffURL = props.diffURL ?? `${base}/diff.png`;

	// The panels are hidden and shown by CSS keyed on this attribute, so on the
	// island path it has to land on the mount point rather than on an element
	// this component renders. A layout effect keeps that from being visible: it
	// runs after the children are committed and before the browser paints, so
	// there is no frame where the markup says one mode and the root says another.
	useLayoutEffect(() => {
		if (root) root.dataset.viewMode = mode;
	}, [root, mode]);

	// Persisted on change, never on mount. Resolving a preference is not the
	// reader choosing one: writing the resolved value here would materialize a
	// key that was deliberately absent, and an absent key and a key holding the
	// default have to stay indistinguishable -- the settings page reads the same
	// entries and would start showing this page's default as the reader's.
	const chooseMode = useCallback((next: ViewMode) => {
		setMode(next);
		writePreference(PREFERENCE_KEYS.viewMode, next, browserStorage());
	}, []);
	const chooseColormap = useCallback((next: Colormap) => {
		setColormap(next);
		writePreference(PREFERENCE_KEYS.colormap, next, browserStorage());
	}, []);
	const chooseOpacity = useCallback((next: number) => {
		setOpacity(next);
		writeOverlayOpacity(next, browserStorage());
	}, []);

	// Reported on mount as well as on change: the job detail page keeps the
	// difference-artifact download link in step with the heatmap on screen, and
	// the stored preference may already differ from the turbo the server wrote.
	const colormapRef = useRef(onColormap);
	colormapRef.current = onColormap;
	useEffect(() => {
		colormapRef.current?.(colormap);
	}, [colormap]);

	useEffect(() => {
		const chooseByKeyboard = (event: KeyboardEvent) => {
			// A modified digit belongs to the browser -- tab switching, among
			// others -- a handled key belongs to whoever handled it, and a held
			// key should select once rather than once per repeat tick.
			if (event.defaultPrevented || event.repeat) return;
			if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
			const target = event.target as HTMLElement | null;
			// The same three tags the inline script skipped: a digit typed into a
			// form control is text, not a shortcut.
			if (target?.isContentEditable) return;
			if (target && ["INPUT", "SELECT", "TEXTAREA"].includes(target.tagName)) return;
			const selected = shortcutMode(event.key);
			if (!selected) return;
			event.preventDefault();
			chooseMode(selected);
		};
		document.addEventListener("keydown", chooseByKeyboard);
		return () => document.removeEventListener("keydown", chooseByKeyboard);
	}, [chooseMode]);

	const body = (
		<>
			<div className="row-between" style={{ marginBottom: "1rem" }}>
				<h2 style={{ fontSize: "1.25rem", fontWeight: 600 }}>Images</h2>
				<fieldset className="view-mode-selector" aria-label="Image view mode">
					<legend className="sr-only">Image view mode</legend>
					{MODES.map((item, index) => (
						<div className="view-mode-option" key={item.value}>
							<input
								type="radio"
								id={`${ids}-view-mode-${item.value}`}
								name={`${ids}-view-mode`}
								value={item.value}
								aria-keyshortcuts={String(index + 1)}
								checked={mode === item.value}
								onChange={() => chooseMode(item.value)}
							/>
							<label htmlFor={`${ids}-view-mode-${item.value}`}>
								{item.label} <span className="view-mode-shortcut" aria-hidden="true">{index + 1}</span>
							</label>
						</div>
					))}
				</fieldset>
			</div>
			<div className="image-view-panels">
				<div className="image-view-panel" data-view-panel="reference">
					<PanelHeading>Reference</PanelHeading>
					<ImageFrame src={referenceURL} alt="Reference Image" message="Reference image not available" />
					{metadata ? <ReferenceMetadataRow metadata={metadata} /> : null}
				</div>
				<div className="image-view-panel" data-view-panel="best">
					<PanelHeading>Current Best</PanelHeading>
					<ImageFrame
						src={withImageParams(bestURL, { revision })}
						alt="Current Best Image"
						message={missingImageMessage(jobState, "best")}
					/>
				</div>
				<div className="image-view-panel" data-view-panel="difference">
					<div className="heatmap-heading">
						<PanelHeading inline>Difference Heatmap</PanelHeading>
						<label className="heatmap-colormap-control" htmlFor={`${ids}-heatmap-colormap`}>
							Colormap
							<select
								id={`${ids}-heatmap-colormap`}
								value={colormap}
								onChange={(event) => chooseColormap(normalizeColormap(event.target.value))}
							>
								{COLORMAPS.map((option) => (
									<option value={option} key={option}>{COLORMAP_LABELS[option]}</option>
								))}
							</select>
						</label>
					</div>
					<ImageFrame
						className="image-frame-difference"
						src={withImageParams(diffURL, { colormap, revision })}
						alt="False-color difference heatmap"
						message={missingImageMessage(jobState, "difference")}
						muted
					/>
					<div
						className="heatmap-legend"
						role="img"
						aria-label="Mean absolute RGB error scale from 0 to 255"
					>
						<span>0</span>
						<div className="heatmap-legend-gradient" style={{ background: heatmapGradient(colormap) }} />
						<span>255</span>
						<span className="heatmap-legend-description">Mean absolute RGB error per pixel</span>
					</div>
				</div>
				<div className="image-view-panel" data-view-panel="overlay">
					<div className="overlay-heading">
						<PanelHeading inline>Best over Reference</PanelHeading>
						<div className="overlay-opacity-control">
							<label htmlFor={`${ids}-overlay-opacity`}>Best opacity</label>
							<input
								type="range"
								id={`${ids}-overlay-opacity`}
								min="0"
								max="100"
								step="1"
								value={opacity}
								aria-label="Best image opacity in percent"
								onChange={(event) => chooseOpacity(normalizeOverlayOpacity(event.target.value))}
							/>
							<output
								className="overlay-opacity-value"
								htmlFor={`${ids}-overlay-opacity`}
								aria-live="polite"
							>
								{opacity}%
							</output>
						</div>
					</div>
					<OverlayFrame
						referenceURL={referenceURL}
						bestURL={withImageParams(bestURL, { revision })}
						opacity={opacity}
						message={missingImageMessage(jobState, "best")}
					/>
					<p style={{ marginTop: "0.75rem", fontSize: "0.75rem", color: "var(--text-muted)" }}>
						Drag the slider to blend the current best render over the reference. 0% shows the
						reference alone, 100% the best render alone.
					</p>
				</div>
			</div>
		</>
	);

	if (root) return body;
	return (
		<div className={["card", "image-viewer", extraClass].filter(Boolean).join(" ")} data-view-mode={mode}>
			{body}
		</div>
	);
}

function PanelHeading({ children, inline }: { children: string; inline?: boolean }) {
	return (
		<h3
			style={{
				fontSize: "1rem",
				fontWeight: 600,
				marginBottom: inline ? undefined : "0.75rem",
				color: "var(--text-muted)",
			}}
		>
			{children}
		</h3>
	);
}

function ReferenceMetadataRow({ metadata }: { metadata: ReferenceMetadata }) {
	const empty = !metadata.dimensions && !metadata.fileSize;
	return (
		<div className="image-metadata" aria-label="Reference image metadata">
			{metadata.dimensions ? <span>{metadata.dimensions}</span> : null}
			{metadata.fileSize ? <span title={metadata.bytes || undefined}>{metadata.fileSize}</span> : null}
			{empty ? <span>Metadata unavailable</span> : null}
		</div>
	);
}

// ImageFrame carries the load/error state the inline script hand-rolled with
// four ids per image. Tracking which src succeeded rather than a boolean is
// what makes a revision bump behave the way refreshImage did: the previous
// render stays on screen, dimmed, with the spinner over it, until the new one
// decodes -- and a frame that has never loaded shows the placeholder instead of
// a broken-image icon.
function ImageFrame({
	src,
	alt,
	message,
	className,
	muted,
}: {
	src: string;
	alt: string;
	message: string;
	className?: string;
	muted?: boolean;
}) {
	const load = useImageLoad(src);

	return (
		<div className={["image-frame", className].filter(Boolean).join(" ")}>
			<img src={src} alt={alt} {...load.imgProps} />
			<ImageFrameStates load={load} message={message} muted={muted} />
		</div>
	);
}

// OverlayFrame is the one panel whose state does not belong to the image that
// fills the frame: the reference is the underlay and always there, while the
// best render is the layer that may not exist yet, so the spinner and the
// placeholder follow the best image, as they did before the port.
function OverlayFrame({
	referenceURL,
	bestURL,
	opacity,
	message,
}: {
	referenceURL: string;
	bestURL: string;
	opacity: number;
	message: string;
}) {
	const load = useImageLoad(bestURL);

	return (
		<div className="image-frame image-frame-overlay">
			<img src={referenceURL} alt="Reference image underlay" />
			<div className="overlay-best-layer" style={{ opacity: opacity / 100 }}>
				<img
					src={bestURL}
					alt="Current best image blended over the reference"
					{...load.imgProps}
				/>
			</div>
			<ImageFrameStates load={load} message={message} />
		</div>
	);
}

type ImageLoad = {
	stale: boolean;
	errored: boolean;
	imgProps: {
		onLoad: () => void;
		onError: () => void;
		style: { display: string; opacity: number };
	};
};

// useImageLoad carries the load/error state the inline script hand-rolled with
// four element ids per image. Tracking which src succeeded rather than a
// boolean is what makes a revision bump behave the way refreshImage did: the
// previous render stays on screen, dimmed, with the spinner over it, until the
// new one decodes -- and a frame that has never loaded shows the placeholder
// instead of a broken-image icon.
function useImageLoad(src: string): ImageLoad {
	const [loaded, setLoaded] = useState<string | null>(null);
	const [failed, setFailed] = useState<string | null>(null);
	const errored = failed === src;
	const stale = loaded !== src && !errored && loaded !== null;

	return {
		stale,
		errored,
		imgProps: {
			onLoad: () => setLoaded(src),
			onError: () => setFailed(src),
			style: { display: errored ? "none" : "block", opacity: stale ? 0.5 : 1 },
		},
	};
}

function ImageFrameStates({
	load,
	message,
	muted,
}: {
	load: ImageLoad;
	message: string;
	muted?: boolean;
}) {
	const color = muted ? "#cccccc" : undefined;
	if (load.errored) {
		return <div className="image-state" style={{ color }}>{message}</div>;
	}
	if (!load.stale) return null;
	return (
		<div className="image-state image-loading" role="status" style={{ color }}>
			<div className="spinner" />
			<p style={{ marginTop: "0.5rem", fontSize: "0.875rem" }}>Loading...</p>
		</div>
	);
}

type JobStatus = { state?: string; bestRevision?: number | string };

// ImageViewerIsland adapts the shared component to the job detail page: it
// reads its props off the mount point's data attributes, keeps that element's
// dataset current for the detail script that still reads it, refreshes the
// images when the best revision advances, and owns the difference-artifact
// download link's colormap.
export function ImageViewerIsland({ root }: { root: HTMLElement }) {
	const data = root.dataset;
	const jobId = data.jobId ?? "";
	const [revision, setRevision] = useState(() => Number.parseInt(data.bestRevision ?? "", 10) || 0);
	const [jobState, setJobState] = useState(data.jobState ?? "");

	const apply = useCallback((status: JobStatus | null) => {
		if (!status) return;
		const next = Number.parseInt(String(status.bestRevision ?? ""), 10);
		// Monotonic: two producers publish this status -- the job-controls
		// island's live stream and the poll below -- and a slow response must
		// not walk the images back to an older render.
		if (Number.isFinite(next)) setRevision((current) => (next > current ? next : current));
		if (status.state) setJobState(status.state);
	}, []);

	useEffect(() => {
		const onStatus = (event: Event) => apply((event as CustomEvent<JobStatus>).detail);
		document.addEventListener("mayflycirclefit:job-status", onStatus);
		return () => document.removeEventListener("mayflycirclefit:job-status", onStatus);
	}, [apply]);

	// The detail page reads these off the same element. Writing them back keeps
	// that channel exactly as the inline script left it.
	useEffect(() => {
		root.dataset.bestRevision = String(revision);
		root.dataset.jobState = jobState;
	}, [root, revision, jobState]);

	const onColormap = useCallback(
		(colormap: Colormap) => {
			root.dataset.colormap = colormap;
			// The download link lives in the detail page's Downloads card, outside
			// this island. The inline script reached for it the same way; the
			// alternative is a second copy of the colormap preference over there.
			const link = document.getElementById("download-difference");
			if (link instanceof HTMLAnchorElement && jobId) {
				link.href =
					`/api/v1/jobs/${encodeURIComponent(jobId)}/diff.png` +
					`?colormap=${encodeURIComponent(colormap)}&download=1`;
			}
		},
		[root, jobId],
	);

	// Opt-in polling, for a reader whose event stream is blocked by a buffering
	// proxy. Zero -- the default -- leaves the images on the live stream alone.
	// A terminal state re-runs this effect and takes the early return, which is
	// how the loop stops.
	useEffect(() => {
		if (!jobId || !ACTIVE_STATES.includes(jobState)) return;
		const interval = normalizeAutoRefresh(
			readPreference(browserStorage(), PREFERENCE_KEYS.autoRefresh),
		);
		if (interval <= 0) return;

		let inFlight = false;
		let stopped = false;
		// One request at a time: a slow network must not let ticks pile up into
		// overlapping fetches that apply their updates out of order.
		const tick = async () => {
			if (inFlight || stopped) return;
			inFlight = true;
			try {
				const response = await fetch(`/api/v1/jobs/${encodeURIComponent(jobId)}/status`, {
					cache: "no-store",
				});
				if (!response.ok) return;
				const status = (await response.json()) as JobStatus;
				if (stopped) return;
				// Republish for the job detail page, which owns the metric panel.
				document.dispatchEvent(
					new CustomEvent("mayflycirclefit:job-status", { detail: status }),
				);
				apply(status);
			} catch {
				// Keep polling and let errors self-recover.
			} finally {
				inFlight = false;
			}
		};

		const timer = window.setInterval(() => void tick(), interval);
		void tick();
		const stop = () => {
			stopped = true;
			window.clearInterval(timer);
		};
		window.addEventListener("pagehide", stop);
		return () => {
			stop();
			window.removeEventListener("pagehide", stop);
		};
	}, [jobId, jobState, apply]);

	return (
		<ImageViewer
			root={root}
			jobId={jobId}
			revision={revision}
			jobState={jobState}
			// data-view-mode is what the fallback shows -- always the
			// side-by-side pair, because those are the only two panels that need
			// no script. The mode this island should open in is its own
			// attribute, and a stored preference outranks both.
			defaultMode={data.defaultMode}
			referenceURL={data.referenceUrl}
			bestURL={data.bestUrl}
			diffURL={data.diffUrl}
			metadata={
				data.showMetadata === "true"
					? {
						dimensions: data.refDimensions ?? "",
						fileSize: data.refFilesize ?? "",
						bytes: data.refBytes ?? "",
					}
					: undefined
			}
			onColormap={onColormap}
		/>
	);
}
