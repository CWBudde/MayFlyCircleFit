package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestLayoutIncludesThemeSwitcher(t *testing.T) {
	var output bytes.Buffer

	err := Layout("Theme test").Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render layout: %v", err)
	}

	body := output.String()
	for _, marker := range []string{
		`@media (prefers-color-scheme: dark)`,
		`:root[data-theme="dark"]`,
		`role="group" aria-label="Color theme"`,
		`data-theme-value="auto"`,
		`data-theme-value="light"`,
		`data-theme-value="dark"`,
		`mayflycirclefit.theme`,
		`localStorage.removeItem(theme.storageKey)`,
		`aria-label="Use system theme" aria-pressed="true"`,
		`>Dashboard<`,
		`>Jobs<`,
		`/jobs`,
		`>Campaigns<`,
		`>Create Job<`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered layout missing %q", marker)
		}
	}
}

// imageFrameRule is the marker two files assert on: layout_test checks Layout
// supplies it, image_viewer_test checks the viewer no longer declares it.
const imageFrameRule = ".image-frame {"

// TestLayoutCarriesTheAccessibilityContract pins the shared vocabulary every
// other page and every React island builds on. These are markers rather than a
// golden file on purpose: the point is that a named utility keeps existing, not
// that the stylesheet stays byte-identical.
func TestLayoutCarriesTheAccessibilityContract(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	err := Layout("Contract").Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render layout: %v", err)
	}

	body := output.String()
	for _, marker := range []string{
		// Bypass block: the nav puts nine controls before the content.
		`<a class="skip-link" href="#main-content">`,
		// tabindex is what makes following the skip link actually move focus.
		`<main id="main-content" tabindex="-1">`,
		`<nav aria-label="Primary">`,
		`.sr-only {`,
		// One focus ring for everything, plus the two-tone variant that stays
		// visible on a filled button.
		`a:focus-visible,`,
		`.btn:focus-visible {`,
		`box-shadow: 0 0 0 2px var(--surface-color), 0 0 0 4px var(--primary-color);`,
		// Wrap helpers that replace the unwrapped space-between rows.
		`.row-between {`,
		`.meta-row {`,
		`.action-row {`,
		`.table-scroll {`,
		`.card-link {`,
		// Image-viewer vocabulary has to live outside every island root.
		`.view-mode-option label {`,
		imageFrameRule,
		`grid-template-columns: repeat(auto-fit, minmax(min(260px, 100%), 1fr));`,
		`@media (prefers-reduced-motion: reduce) {`,
		`@media (max-width: 480px) {`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered layout missing %q", marker)
		}
	}

	// The decorative brand mark must not join the link's accessible name.
	if !strings.Contains(body, `<svg aria-hidden="true" width="24"`) {
		t.Error("brand logo is not hidden from assistive technology")
	}
}

// TestLayoutPairsEveryAccentBackgroundWithItsOwnForeground guards the defect
// this contract exists to fix. The accent tokens stay light in both themes
// while --text-color flips, so a button that set an accent background and
// inherited the ordinary foreground measured 2.54:1 (white on #60a5fa) and
// 1.51:1 (--text-color on #fbbf24) in dark mode. Every accent background now
// has a matching foreground token, declared in both palettes.
func TestLayoutPairsEveryAccentBackgroundWithItsOwnForeground(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	err := Layout("Contrast").Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render layout: %v", err)
	}

	body := output.String()

	if strings.Contains(body, "color: white;") {
		t.Error("a button still hard-codes white, which inverts against the dark palette's light accents")
	}

	for _, token := range []string{
		"--on-primary:", "--on-danger:", "--on-warning:",
		"--danger-bg:", "--warning-btn-bg:", "--success-text-strong:",
	} {
		// Five times: the bare :root light block, the two selectors
		// darkThemeTokens is rendered into -- the explicit data-theme="dark"
		// opt-in and the system-preference media query, which plain CSS cannot
		// share one declaration block between -- and both palettes again inside
		// the preload script, which emits whichever one was stored before the
		// first paint.
		if got := strings.Count(body, token); got != 5 {
			t.Errorf("token %q declared %d times, want 5 (light, explicit dark, system dark, both preload)",
				token, got)
		}
	}

	for _, pairing := range []string{
		`background-color: var(--primary-color);`,
		`color: var(--on-primary);`,
		`background-color: var(--danger-bg);`,
		`color: var(--on-danger);`,
		`background-color: var(--warning-btn-bg);`,
		`color: var(--on-warning);`,
	} {
		if !strings.Contains(body, pairing) {
			t.Errorf("rendered layout missing %q", pairing)
		}
	}
}
