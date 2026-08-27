import { describe, expect, it } from "vitest";

// Vite's raw glob, declared here because @types/node is not a dependency of
// this package and a source-reading test should not become the reason it
// becomes one. The transform is syntactic, so the call has to stay literal.
declare global {
	interface ImportMeta {
		glob(
			pattern: string,
			options: { query: "?raw"; import: "default"; eager: true },
		): Record<string, string>;
	}
}

const sources = import.meta.glob("./*.tsx", { query: "?raw", import: "default", eager: true });

function sourceOf(name: string): string {
	const found = sources[`./${name}`];
	if (found === undefined) throw new Error(`no source read for ${name}`);
	return found;
}

function modulesContaining(marker: string): string[] {
	return Object.entries(sources)
		.filter(([, source]) => source.includes(marker))
		.map(([path]) => path)
		.sort();
}

// Task 18.2's acceptance check. Before the port there were two implementations
// of one component: a 442-line inline script in internal/ui/image_viewer.templ
// driving the job detail page, and this React component driving the campaign
// pages. Both pages now resolve here. These cases fail if a second one grows
// back, or if either page stops being wired to this one.
describe("one image viewer implementation", () => {
	it("is declared in exactly one module", () => {
		expect(modulesContaining("export function ImageViewer(")).toEqual(["./ImageViewer.tsx"]);
		expect(modulesContaining("export function ImageViewerIsland(")).toEqual(["./ImageViewer.tsx"]);
	});

	it("is the only module that renders the viewer's markup", () => {
		// Class names from the shared vocabulary in layout.templ. A second
		// component rendering the panels or the mode selector would show up here
		// long before it showed up as a visual difference between the two pages.
		for (const marker of ["image-view-panels", "view-mode-option", "overlay-best-layer", "heatmap-legend"]) {
			expect(modulesContaining(marker)).toEqual(["./ImageViewer.tsx"]);
		}
	});

	it("is registered as the image-viewer island", () => {
		const dashboard = sourceOf("dashboard.tsx");
		expect(dashboard).toContain('import { ImageViewerIsland } from "./ImageViewer";');
		// The name pairs with data-island="image-viewer" in
		// internal/ui/image_viewer.templ, which its own Go test pins.
		expect(dashboard).toContain('"image-viewer": ImageViewerIsland,');
		expect(modulesContaining('"image-viewer": ')).toEqual(["./dashboard.tsx"]);
	});

	it("is what the campaign page renders", () => {
		const campaigns = sourceOf("Campaigns.tsx");
		expect(campaigns).toContain('import { ImageViewer } from "./ImageViewer";');
		expect(campaigns).toContain("<ImageViewer ");
	});
});
