import { useEffect, useState } from "react";

type ViewMode = "reference" | "best" | "side-by-side" | "difference" | "overlay";
const modes: Array<{ value: ViewMode; label: string }> = [
	{ value: "reference", label: "Reference" },
	{ value: "best", label: "Best" },
	{ value: "side-by-side", label: "Side-by-Side" },
	{ value: "difference", label: "Difference" },
	{ value: "overlay", label: "Overlay" },
];

function storedMode(): ViewMode {
	try {
		const value = window.localStorage.getItem("mayflycirclefit.viewMode") as ViewMode | null;
		if (modes.some((mode) => mode.value === value)) return value as ViewMode;
	} catch { /* storage is optional */ }
	return "side-by-side";
}

function storedNumber(key: string, fallback: number): number {
	try {
		const stored = window.localStorage.getItem(key);
		if (stored === null) return fallback;
		const value = Number(stored);
		if (Number.isFinite(value)) return value;
	} catch { /* storage is optional */ }
	return fallback;
}

export function ImageViewer({ jobId, revision }: { jobId: string; revision: number }) {
	const [mode, setMode] = useState<ViewMode>(storedMode);
	const [opacity, setOpacity] = useState(() => Math.min(100, Math.max(0, storedNumber("mayflycirclefit.overlayOpacity", 50))));
	const [colormap, setColormap] = useState(() => {
		try { return window.localStorage.getItem("mayflycirclefit.diffColormap") === "magma" ? "magma" : "turbo"; }
		catch { return "turbo"; }
	});

	useEffect(() => {
		const chooseByKeyboard = (event: KeyboardEvent) => {
			const target = event.target as HTMLElement | null;
			if (target?.matches("input, select, textarea, button, [contenteditable='true']")) return;
			const selected = modes[Number(event.key) - 1];
			if (selected) setMode(selected.value);
		};
		window.addEventListener("keydown", chooseByKeyboard);
		return () => window.removeEventListener("keydown", chooseByKeyboard);
	}, []);

	useEffect(() => {
		try { window.localStorage.setItem("mayflycirclefit.viewMode", mode); } catch { /* storage is optional */ }
	}, [mode]);
	useEffect(() => {
		try { window.localStorage.setItem("mayflycirclefit.overlayOpacity", String(opacity)); } catch { /* storage is optional */ }
	}, [opacity]);
	useEffect(() => {
		try { window.localStorage.setItem("mayflycirclefit.diffColormap", colormap); } catch { /* storage is optional */ }
	}, [colormap]);

	const suffix = `v=${encodeURIComponent(String(revision))}`;
	const reference = `/api/v1/jobs/${encodeURIComponent(jobId)}/ref.png`;
	const best = `/api/v1/jobs/${encodeURIComponent(jobId)}/best.png?${suffix}`;
	const difference = `/api/v1/jobs/${encodeURIComponent(jobId)}/diff.png?colormap=${colormap}&${suffix}`;
	const showReference = mode === "reference" || mode === "side-by-side";
	const showBest = mode === "best" || mode === "side-by-side";

	return <div className="card image-viewer" data-view-mode={mode}>
		<div style={{ display: "flex", flexWrap: "wrap", justifyContent: "space-between", alignItems: "center", gap: "1rem", marginBottom: "1rem" }}>
			<h2>Images</h2>
			<fieldset aria-label="Image view mode" style={{ display: "flex", flexWrap: "wrap", gap: "0.5rem", border: 0 }}>
				{modes.map((item, index) => <label key={item.value} className="btn" style={mode === item.value ? { borderColor: "var(--primary-color)", background: "var(--info-bg)" } : undefined}>
					<input type="radio" name="campaign-view-mode" value={item.value} checked={mode === item.value} onChange={() => setMode(item.value)} style={{ position: "absolute", opacity: 0 }} aria-keyshortcuts={String(index + 1)} />
					{item.label} <kbd>{index + 1}</kbd>
				</label>)}
			</fieldset>
		</div>
		<div style={{ display: "grid", gridTemplateColumns: mode === "side-by-side" ? "repeat(auto-fit, minmax(260px, 1fr))" : "minmax(0, 1fr)", gap: "2rem" }}>
			{showReference ? <ImagePanel title="Reference" src={reference} alt="Reference Image" /> : null}
			{showBest ? <ImagePanel title="Current Best" src={best} alt="Current Best Image" /> : null}
			{mode === "difference" ? <section>
				<div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: "1rem", marginBottom: "0.75rem" }}><h3>Difference Heatmap</h3>
					<label>Colormap <select value={colormap} onChange={(event) => setColormap(event.target.value)}><option value="turbo">Turbo</option><option value="magma">Magma</option></select></label>
				</div>
				<ImageFrame src={difference} alt="Difference Heatmap" background="#000" />
				<div style={{ display: "flex", justifyContent: "space-between", color: "var(--text-muted)", fontSize: "0.75rem", marginTop: "0.5rem" }}><span>Low difference</span><span>High difference</span></div>
			</section> : null}
			{mode === "overlay" ? <section>
				<div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: "1rem", marginBottom: "0.75rem" }}><h3>Overlay Comparison</h3>
					<label>Best opacity <input type="range" min="0" max="100" value={opacity} onChange={(event) => setOpacity(Number(event.target.value))} /> <output>{opacity}%</output></label>
				</div>
				<div style={{ position: "relative", overflow: "hidden", border: "1px solid var(--border-color)", borderRadius: "0.375rem" }}>
					<img src={reference} alt="Reference beneath overlay" style={{ width: "100%", display: "block" }} />
					<img src={best} alt="Best result overlay" style={{ position: "absolute", inset: 0, width: "100%", height: "100%", opacity: opacity / 100 }} />
				</div>
			</section> : null}
		</div>
	</div>;
}

function ImagePanel({ title, src, alt }: { title: string; src: string; alt: string }) {
	return <section><h3 style={{ marginBottom: "0.75rem", color: "var(--text-muted)" }}>{title}</h3><ImageFrame src={src} alt={alt} /></section>;
}

function ImageFrame({ src, alt, background }: { src: string; alt: string; background?: string }) {
	return <div style={{ overflow: "hidden", border: "1px solid var(--border-color)", borderRadius: "0.375rem", background: background ?? "var(--surface-color)" }}>
		<img src={src} alt={alt} style={{ width: "100%", height: "auto", display: "block" }} />
	</div>;
}
