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
//
// Task 18.1 then removed the viewer's own island wrapper: the job detail page's
// island root is the whole detail body, so the viewer sits inside it exactly as
// it sits inside campaign-detail on the campaign page, and both pages render
// the component directly.
describe("one image viewer implementation", () => {
	it("is declared in exactly one module", () => {
		expect(modulesContaining("export function ImageViewer(")).toEqual(["./ImageViewer.tsx"]);
	});

	it("is the only module that renders the viewer's markup", () => {
		// Class names from the shared vocabulary in layout.templ. A second
		// component rendering the panels or the mode selector would show up here
		// long before it showed up as a visual difference between the two pages.
		for (const marker of ["image-view-panels", "view-mode-option", "overlay-best-layer", "heatmap-legend"]) {
			expect(modulesContaining(marker)).toEqual(["./ImageViewer.tsx"]);
		}
	});

	it("mounts no island of its own", () => {
		// A mount point inside another island's root is a React root over a node
		// that root is about to discard. internal/ui/image_viewer_test.go pins
		// the same fact from the templ side.
		expect(modulesContaining("ImageViewerIsland")).toEqual([]);
		expect(modulesContaining('"image-viewer": ')).toEqual([]);
	});

	it("is what both pages render", () => {
		for (const page of ["Campaigns.tsx", "JobDetail.tsx"]) {
			const source = sourceOf(page);
			expect(source).toContain('from "./ImageViewer"');
			expect(source).toContain("<ImageViewer");
		}
	});
});
