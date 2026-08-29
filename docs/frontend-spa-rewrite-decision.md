# Why the frontend is islands, not a single-page application

A decision record, not a commitment to revisit. Read it before proposing a Vite
+ React Router + Tailwind + shadcn/ui rewrite; the benefit it names is real and
is obtainable without it.

## The state it was proposed against

A 2026-08-22 audit found the island transition half-finished: five mount points
across four of nine templ pages, against **1,481 lines of hand-written inline
JavaScript inside `.templ` files** and 2,136 lines of TypeScript under
`web/src`. Phase 18 finished the transition instead of replacing it. Every
`.templ` file outside `layout.templ` is now script-free, `layout.templ` carries
only the pre-paint theme IIFE, and `internal/ui/inline_script_gate_test.go`
holds that line by allowlisting the exception by position rather than by
filename.

## The debt the rewrite named is real

Measured in the same tree: **403 inline `style=` attributes across
`internal/ui/*.templ` against 147 `class=` usages, and 136 across `web/src/*.tsx`
against 27 `className=` usages.** There is no CSS file at all; every rule lives
in the `<style>` block in `internal/ui/layout.templ`, and the 27 custom
properties defined there already map onto shadcn's CSS-variable theming, with
dark mode working the way shadcn expects. shadcn would supply Table with real
sorting, Badge, Card, Dialog, and Form+zod for the roughly thirty-field create
page.

**Tailwind and shadcn/ui may be adopted inside the islands.** An island owns its
DOM entirely, so it captures that benefit without paying any of the five costs
below.

## The five costs a rewrite would have to pay

1. **It deletes a documented invariant.**
   [`behavior-invariants.md`](behavior-invariants.md) guarantees every page stays
   readable with JavaScript off or the bundle broken. Phase 18 asserts both
   degraded modes in 22 Playwright cases. That guarantee would have to be
   revised deliberately, not worked around.
2. **The asset namespace is flat.** `internal/ui/static.go` 404s any asset name
   containing `/`, so a stock Vite build emitting `assets/index-*.js` does not
   serve. Output would have to be flattened or the handler reworked.
3. **There is no catch-all route.** `internal/server/server.go` registers
   `mux.HandleFunc("/", s.handleDashboardPage)`, which 404s every path but
   exactly `/`. Deep links need an HTML fallback that shadows neither
   `/api/v1/` nor `/static/`.
4. **Same-origin is enforced, not advisory.** The CORS middleware 403s any
   request whose `Origin` host differs from `Host`, and there are no CSRF tokens
   anywhere: that check plus the loopback bind is the entire defense. A Vite dev
   server on another port only works if its proxy rewrites `Origin`.
5. **No CDN, no external assets, and no node in `go build`.**
   [`behavior-invariants.md`](behavior-invariants.md) and
   `internal/ui/static.go` are binding on any new pipeline.

## Conditions to revisit

Client-side routing or retiring the dual rendering path becoming goals in
themselves. Nothing else — the estimate was 4-8 weeks, and it is mutually
exclusive with the island work that shipped.

One note that holds in both directions: **SSE cannot carry a client-rendered UI
on its own.** `/api/v1/events` is deliberately an invalidation channel —
`campaign.changed` carries no payload — so the refetch loop in `web/src/live.ts`
stays either way.

See also
[`typescript-read-model-generation.md`](typescript-read-model-generation.md) for
the related decision not to generate the `web/src` read models from the Go
structs, and [`architecture.md`](architecture.md) for the island inventory.
