# Browser support and manual validation

This document declares which browsers and viewport sizes the web UI is
supported on, which of those claims CI enforces, and which one has to be
checked by hand and why.

It exists because the repository previously declared nothing. `docs/support-matrix.md`
covers operating systems, CPUs and SIMD tiers; the only implicit statement
about browsers was the single `chromium` project in `web/playwright.config.ts`.

## Supported engines

The UI targets the three current engines. The bundle is compiled to `es2020`
(`scripts/bundle-web.sh`), which every listed version reaches natively.

| Engine | Minimum | Enforced by |
| --- | --- | --- |
| Chromium (Chrome, Edge) | 111 | `ci-web`, every browser spec |
| WebKit (Safari) | 16.4 | `ci-web`, via Playwright's bundled WebKit |
| Gecko (Firefox) | 113 | Not enforced — see below |

The minimum is set by `:has()` and container-query-era CSS baselines, not by
anything the UI uses today; it is the floor we are willing to test against, not
a cliff. The server is a trusted-local tool, so this list is about what a
developer's own workstation is likely to run.

**Firefox is expected to work and is not tested.** It shares no rendering
engine with either tested one, so that is a real gap rather than an implied
pass. It is recorded in `docs/known-limitations.md`.

## Supported viewports

| Class | Width | Layout contract |
| --- | --- | --- |
| Phone | 320-480px | Single column. Every row wraps; no page scrolls sideways. |
| Tablet | 481-768px | Navigation stacks; image comparison modes may still pair. |
| Desktop | 769px and up | Full layout; `main` is capped at 1200px. |

320px is the narrowest width any spec asserts against, because it is the
narrowest viewport in real use. The contract every page must hold at every
width is a single line: `document.documentElement.scrollWidth` must not exceed
`clientWidth`. Wide data tables are the deliberate exception — they scroll
inside their own labelled, focusable `.table-scroll` region rather than
reflowing, because they are dense numeric columns people scan vertically and
one of them is sortable.

## What CI enforces

`ci-web` runs the Playwright matrix on every pull request and blocks releases.
It covers Chromium and WebKit on desktop, plus iPhone and iPad viewports, and
runs an `@axe-core/playwright` sweep at WCAG 2.1 A/AA over every page in both
the light and dark themes.

## What CI cannot enforce

**Playwright's WebKit is not Safari.** It is the same engine, built for Linux,
without Safari's browser chrome, its download pipeline, its print pipeline, or
its default keyboard-navigation settings. Two differences matter enough to name:

- Playwright's WebKit moves focus to links on `Tab`. Real Safari does not
  unless *Full Keyboard Access* is enabled. So the automated keyboard spec
  proves focus **order**, not real-Safari **reachability**.
- Downloads, printing and `100vh` behaviour all differ.

So the checklist below is not ceremony; it is the part of the claim automation
cannot make. Record a completed run in the results table so a pass is evidence
rather than an assertion.

## Manual checklist

Run against a release candidate, with the server started as
`./bin/circlefit serve --addr 127.0.0.1 --port 8080`.

### Results

| Date | Tester | App version | OS / browser version | Result |
| --- | --- | --- | --- | --- |
| | | | | |

### Safari on macOS (current, and one prior major)

> **Check this first, and check it with the bundle blocked.** Open `/settings`
> and `/create` with the theme set to dark. If the text is near-black on the
> dark background, Safari shares the WebKit style-caching defect recorded in
> [`known-limitations.md`](known-limitations.md), and that is a release blocker
> rather than a harness artifact.
>
> Blocking the bundle is what makes this check meaningful now. Phase 18 turned
> both pages into React islands, and mounting one replaces the server-rendered
> markup with freshly created elements, which inherit correctly -- so the defect
> is hidden on a healthy page even where it still exists. Repeat the check with
> JavaScript disabled (Develop → Disable JavaScript), which is what a reader
> sees before the bundle arrives and if it ever fails to. Report both results.

- [ ] All seven pages render: `/`, `/jobs`, `/jobs/{id}`, `/create`,
      `/schedules`, `/schedules/{id}`, `/settings`.
- [ ] Enable *Full Keyboard Access* (System Settings → Keyboard). The skip link
      is the first tab stop and moves focus into the content.
- [ ] Focus is visible on every control, including buttons with a filled
      background and the image-viewer mode selector.
- [ ] All five image comparison modes work by click and by keys `1`-`5`; the
      keys do nothing while focus is in a text field.
- [ ] Overlay opacity slider and difference colormap select both respond, and
      the numeric readout tracks the slider.
- [ ] Downloads save with correct filenames: `best.png`, `params.json`,
      `diff.png`, `report.html`.
- [ ] The downloaded `report.html` opens offline with all three images intact,
      and prints to PDF sensibly.
- [ ] Theme switch (auto/light/dark) applies immediately and survives a reload.
- [ ] Settings preferences persist across a reload.
- [ ] Live metrics update on a running job without the page reloading.
- [ ] Sleep the machine for a minute; on wake the connection line returns to
      "connected" without a reload.

### Safari on a real iPhone and iPad

- [ ] No page scrolls sideways at any orientation.
- [ ] Every control is tappable without zooming; nothing needs hover to reach.
- [ ] Image comparison modes stack on the phone and pair on the iPad.
- [ ] Wide tables scroll within their own region, not the page.
- [ ] Pinch-zoom works on the image viewer.

### Screen reader (VoiceOver)

- [ ] Rotor lists sensible headings and landmarks on all seven pages.
- [ ] Every form control on `/create` announces its own label, and required
      fields announce as required.
- [ ] The live-updates line announces when the stream drops and recovers.
- [ ] Charts announce their name and a text summary rather than "canvas".
- [ ] Progress bars announce a percentage.

### Zoom and high contrast

- [ ] 200% and 400% browser zoom reflow without loss of content or function
      (WCAG 1.4.10).
- [ ] Windows High Contrast / `forced-colors` keeps every control visible.
- [ ] With *Reduce Motion* on, the spinner and running-badge pulse stop
      animating.
