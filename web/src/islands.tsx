import { createRoot } from "react-dom/client";
import type { ComponentType } from "react";

// An island is a server-rendered element that React takes over after load.
// The server always renders usable content inside the mount point, so a page
// stays readable when this bundle fails to load or JavaScript is disabled;
// mounting replaces that content rather than supplying it for the first time.
export type Island = ComponentType<{ root: HTMLElement }>;

// mountIslands attaches every registered component to the elements carrying
// its data-island name. Unknown names are ignored rather than treated as
// errors: a page may be served by a binary whose bundle predates it.
export function mountIslands(registry: Record<string, Island>): void {
  for (const [name, Component] of Object.entries(registry)) {
    const roots = document.querySelectorAll<HTMLElement>(
      `[data-island="${name}"]`,
    );
    for (const root of roots) {
      createRoot(root).render(<Component root={root} />);
    }
  }
}
