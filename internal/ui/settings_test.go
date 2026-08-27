package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func renderSettings(t *testing.T) string {
	t.Helper()

	var output bytes.Buffer

	if err := SettingsPage().Render(context.Background(), &output); err != nil {
		t.Fatalf("render settings page: %v", err)
	}

	return output.String()
}

// The editor is SettingsIsland's; the page's job is to render a mount point and
// a fallback complete enough to read. Mounting replaces everything inside the
// mount point, so the two have to describe the same controls.
func TestSettingsPageMountsTheIsland(t *testing.T) {
	t.Parallel()

	body := renderSettings(t)

	if !strings.Contains(body, `data-island="settings"`) {
		t.Error("settings page renders no mount point for SettingsIsland")
	}

	if got := strings.Count(bodyWithoutLayout(body), "data-island="); got != 1 {
		t.Errorf("settings page renders %d island mount points, want exactly 1", got)
	}

	if !strings.Contains(body, BundleURL()) {
		t.Error("settings page renders an island but never loads the bundle that mounts it")
	}

	for _, control := range []string{
		`id="settings-image-refresh"`,
		`id="settings-default-view-mode"`,
		`id="settings-default-colormap"`,
		`id="settings-visible-metrics"`,
		`id="settings-metric-cost"`,
		`id="settings-metric-psnr"`,
		`id="settings-metric-ssim"`,
		`id="settings-metric-cps"`,
		`id="settings-reset"`,
		`id="settings-feedback"`,
	} {
		if !strings.Contains(body, control) {
			t.Errorf("settings fallback is missing %s, so the page is incomplete without the bundle", control)
		}
	}
}

// Task 18.3's acceptance check: the page has to render without JavaScript and
// say why nothing it offers can be remembered in that state. <noscript> is the
// only element the browser shows exactly then, and mounting removes it along
// with the rest of the fallback.
func TestSettingsPageExplainsThatPreferencesNeedJavaScript(t *testing.T) {
	t.Parallel()

	body := renderSettings(t)

	start := strings.Index(body, "<noscript>")
	end := strings.Index(body, "</noscript>")
	if start < 0 || end < 0 {
		t.Fatal("settings page has no <noscript> explanation")
	}

	notice := body[start:end]
	for _, phrase := range []string{"local storage", "JavaScript"} {
		if !strings.Contains(notice, phrase) {
			t.Errorf("the no-JavaScript notice does not mention %q: %s", phrase, notice)
		}
	}
}

// The fallback controls are inert, so whatever they show is what the reader
// will believe a job page uses. They therefore have to show the defaults
// DEFAULT_PREFERENCES declares in web/src/prefs.ts, not the first option in
// each list: the default view mode is side-by-side, and every metric card is
// visible.
func TestSettingsFallbackShowsTheDefaults(t *testing.T) {
	t.Parallel()

	body := renderSettings(t)

	for _, marker := range []string{
		`<option value="0">SSE-driven (default)</option>`,
		`<option value="side-by-side" selected>`,
		`<option value="turbo">Turbo</option>`,
		`id="settings-metric-cost" type="checkbox" value="cost" checked`,
		`id="settings-metric-psnr" type="checkbox" value="psnr" checked`,
		`id="settings-metric-ssim" type="checkbox" value="ssim" checked`,
		`id="settings-metric-cps" type="checkbox" value="cps" checked`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("settings fallback is missing %q", marker)
		}
	}

	if strings.Contains(body, `<option value="reference" selected`) {
		t.Error("the fallback preselects reference, but the default view mode is side-by-side")
	}
}

// The 177 lines this page used to carry are the point of the task. Scoped to
// the page's own markup: the layout still writes the pre-paint theme IIFE, and
// layout_test guards that one.
func TestSettingsPageCarriesNoInlineScript(t *testing.T) {
	t.Parallel()

	page := bodyWithoutLayout(renderSettings(t))

	if strings.Contains(page, "<script") {
		t.Error("settings page still carries an inline script; the preference handling belongs to SettingsIsland")
	}

	for _, key := range []string{
		"mayflycirclefit.imageRefreshInterval",
		"mayflycirclefit.viewMode",
		"mayflycirclefit.diffColormap",
		"mayflycirclefit.visibleMetrics",
	} {
		if strings.Contains(page, key) {
			t.Errorf("settings page names the storage key %q; web/src/prefs.ts owns those now", key)
		}
	}
}
