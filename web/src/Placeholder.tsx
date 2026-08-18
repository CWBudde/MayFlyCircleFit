// Placeholder proves the build pipeline end to end: TSX source, esbuild
// bundling through a Go tool, a committed and embedded bundle, and the
// /static/ route serving it. Task 17.6 replaces it with the real dashboard
// island; until then it is the only thing the bundle renders.
export function Placeholder({ root }: { root: HTMLElement }) {
  const label = root.dataset.islandLabel ?? "island";
  return <span data-island-mounted="true">{label} mounted</span>;
}
