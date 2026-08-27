package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestJobDetailPageViewModes(t *testing.T) {
	psnr, ssim := 31.25, 0.9123
	job := JobDetail{
		ID:          "12345678-1234-1234-1234-123456789abc",
		State:       "pending",
		StartTime:   time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC),
		RefWidth:    640,
		RefHeight:   480,
		RefSize:     2048,
		PSNR:        &psnr,
		SSIM:        &ssim,
		SSIMEnabled: true,
		Iterations:  25, MaxIters: 100, Evaluations: 12_345,
		Circles:       64,
		BestCost:      12.5,
		BestRevision:  7,
		Parameters:    []CircleParameter{{Number: 1}},
		MetricHistory: []MetricSample{{Iteration: 1, Cost: 10, PSNR: &psnr, SSIM: &ssim}},
	}

	var output bytes.Buffer

	err := JobDetailPage(job).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render job detail: %v", err)
	}

	body := output.String()

	for _, marker := range []string{
		// The viewer is an island now: the page ships the side-by-side fallback
		// and the props the island mounts with, not five modes of inert markup.
		`data-view-mode="side-by-side"`,
		`data-island="image-viewer"`,
		`data-default-mode="side-by-side"`,
		`data-colormap="turbo"`,
		`data-best-revision="7"`,
		`data-circle-count="64"`,
		`const circleCount = Number.parseInt(imageViewer.dataset.circleCount, 10) || 0;`,
		`data-view-panel="reference"`,
		`data-view-panel="best"`,
		`best.png?v=7`,
		`selectedHeatmapColormap()`,
		`let lastRenderedBestRevision =`,
		`data-island="job-controls"`,
		`mayflycirclefit:job-status`,
		`mayflycirclefit:job-metrics`,
		`640 × 480 px`,
		`title="2048 bytes">2.0 KiB`,
		`data-metric="psnr">31.25`,
		`data-metric="ssim">0.9123`,
		`id="metric-history-series"`,
		`id="metric-history-window"`,
		`<option value="all" selected>All samples</option>`,
		`<option value="psnr">PSNR</option>`,
		`<option value="ssim">SSIM</option>`,
		`id="metric-history-data"`,
		`id="sparkline-grid"`,
		`id="sparkline-axes"`,
		`selectedMetricPoints()`,
		`"Iteration"`,
		`font: inherit`,
		`class="card detail-summary"`, `class="card image-viewer detail-images"`,
		`class="card detail-history"`, `class="card detail-downloads download-card"`,
		`.detail-images {`, `order: 2;`, `data-metric="evaluations"`,
		`RGB mean squared error · committed and checkpoint-safe · lower is better`, `Peak signal-to-noise ratio · higher is better`,
		`Objective function calls`, `id="sparkline-hover-readout"`,
		`let sparklineVisible = metricHistory.length > 0`,
		`Best PNG`, `Parameters JSON`, `Difference PNG`, `HTML Report`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered detail page missing %q", marker)
		}
	}

	if strings.Contains(body, `{ fmt.Sprintf`) {
		t.Fatal("rendered detail page contains an unexpanded Go expression in JavaScript")
	}
}

func TestJobDetailPageDistinguishesCandidateFromAuditedBest(t *testing.T) {
	candidate := 95.25
	job := JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "running", StartTime: time.Now(),
		BestCost: 100, CandidateCost: &candidate,
	}

	var output bytes.Buffer

	err := JobDetailPage(job).Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}

	body := output.String()
	for _, marker := range []string{
		"Audited Best Cost", "In-flight Candidate", "95.2500", "4.7500 (4.75%) provisional gain",
		"pending full-image usefulness audit", `data-metric="candidate-psnr"`, "updateCandidateMetrics(data)",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered detail page missing %q", marker)
		}
	}
}

func TestJobDetailPageOmitsSSIMControlsWhenDisabled(t *testing.T) {
	job := JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "pending", StartTime: time.Now(),
	}

	var output bytes.Buffer

	err := JobDetailPage(job).Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}

	body := output.String()
	if strings.Contains(body, `<option value="ssim">`) {
		t.Fatal("disabled SSIM was offered as a history series")
	}

	if !strings.Contains(body, `id="metric-history-empty" style="display: block;`) {
		t.Fatal("empty history state was not visible")
	}
}

func TestJobDetailPageShowsPolishingSchedule(t *testing.T) {
	job := JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "pending", StartTime: time.Now(),
		PolishingEnabled: true, PolishingActiveSetSize: 5, PolishingMaxSweeps: 3,
		PolishingEpochs: 2, PolishingIters: 1000, PolishingStagnationIters: 500,
		PolishingMinImprovement: 0.001, CanPolish: true,
	}

	var output bytes.Buffer

	err := JobDetailPage(job).Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}

	body := output.String()
	for _, marker := range []string{"Active-set Polishing", "Enabled · up to 3 sweeps of 5 circles", "2 × 1000 iterations", "progress threshold 0.001", "Polish weak circles", `data-can-polish="true"`} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered detail page missing %q", marker)
		}
	}
}

// TestJobDetailFallbackMutationsAreDisabled pins the honesty of the fallback.
// The job-controls island replaces these buttons with working ones on mount, so
// the server-rendered copies are what a reader sees only when the bundle fails
// to load or JavaScript is off. They mutate a job over the JSON API and have
// never worked without script, so they must not look clickable. Refresh is
// deliberately excluded: its inline handler works whenever script runs at all.
func TestJobDetailFallbackMutationsAreDisabled(t *testing.T) {
	for _, tc := range []struct {
		state  string
		button string
	}{
		{"running", "pause-job"},
		{"running", "cancel-job"},
		{"paused", "resume-job"},
		{"completed", "delete-job"},
	} {
		t.Run(tc.button+"/"+tc.state, func(t *testing.T) {
			job := JobDetail{
				ID: "12345678-1234-1234-1234-123456789abc", State: tc.state,
				StartTime: time.Now(),
			}

			var output bytes.Buffer

			err := JobDetailPage(job).Render(context.Background(), &output)
			if err != nil {
				t.Fatal(err)
			}

			body := output.String()

			start := strings.Index(body, `id="`+tc.button+`"`)
			if start < 0 {
				t.Fatalf("state %q renders no %s button", tc.state, tc.button)
			}

			tag := body[start : start+strings.Index(body[start:], ">")]
			if !strings.Contains(tag, `aria-disabled="true"`) || !strings.Contains(tag, "disabled ") {
				t.Errorf("%s is offered as clickable without the bundle: <button %s>", tc.button, tag)
			}
		})
	}
}

func TestJobDetailPageMetadataUnavailable(t *testing.T) {
	job := JobDetail{
		ID:        "12345678-1234-1234-1234-123456789abc",
		State:     "pending",
		StartTime: time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC),
	}

	var output bytes.Buffer

	err := JobDetailPage(job).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render job detail: %v", err)
	}

	if !strings.Contains(output.String(), "Metadata unavailable") {
		t.Error("rendered detail page should report unavailable reference metadata")
	}
}

func TestJobDetailPageParameterViewerCircleCounts(t *testing.T) {
	tests := []struct {
		name       string
		parameters []CircleParameter
		wantText   string
	}{
		{name: "none"},
		{
			name: "one",
			parameters: []CircleParameter{{
				Number: 1, X: 12.345, Y: 67.891, Radius: 4.567,
				Red: 1, Green: 0.5, Blue: 0, Opacity: 0.75,
			}},
			wantText: "Circle 1: (12.35, 67.89, 4.57) RGB(255, 128, 0) α=0.750",
		},
		{name: "many", parameters: makeCircleParameters(64)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := JobDetail{
				ID: "12345678-1234-1234-1234-123456789abc", State: "completed",
				StartTime: time.Now(), Circles: 64, Parameters: test.parameters,
			}

			var output bytes.Buffer

			err := JobDetailPage(job).Render(context.Background(), &output)
			if err != nil {
				t.Fatal(err)
			}

			body := output.String()
			for _, marker := range []string{
				`id="parameter-viewer"`, `id="parameter-list"`, `id="parameter-data"`,
				`params.json`, `refreshParameterViewer()`, `formatParameter(circle)`,
				`Best PNG`, `Parameters JSON`, `Difference PNG`,
				`id="download-report"`, `Generating report…`, `URL.createObjectURL(blob)`,
				// The colormap now travels on the viewer element the island keeps
				// current; the report render reads it there at click time.
				`imageViewer.dataset.colormap`, `role="status" aria-live="polite"`,
			} {
				if !strings.Contains(body, marker) {
					t.Errorf("rendered detail page missing %q", marker)
				}
			}

			if got := strings.Count(body, `<li title="Circle `); got != len(test.parameters) {
				t.Errorf("rendered circle rows = %d, want %d", got, len(test.parameters))
			}

			if test.wantText != "" && !strings.Contains(body, test.wantText) {
				t.Errorf("rendered detail page missing %q", test.wantText)
			}

			exportStart := strings.Index(body, `id="parameter-export"`)
			if exportStart < 0 {
				t.Fatal("rendered detail page missing parameter export control")
			}

			exportEnd := strings.Index(body[exportStart:], ">")
			if exportEnd < 0 {
				t.Fatal("parameter export control has no closing bracket")
			}

			exportTag := body[exportStart : exportStart+exportEnd]

			wantDisabled := len(test.parameters) == 0
			if gotDisabled := strings.Contains(exportTag, `aria-disabled="true"`); gotDisabled != wantDisabled {
				t.Errorf("export disabled = %v, want %v", gotDisabled, wantDisabled)
			}
		})
	}
}

func TestProgressPercent(t *testing.T) {
	for _, test := range []struct {
		iterations, maximum int
		want                float64
	}{
		{25, 100, 25}, {125, 100, 100}, {-5, 100, 0}, {10, 0, 0},
	} {
		if got := progressPercent(test.iterations, test.maximum); got != test.want {
			t.Errorf("progressPercent(%d, %d) = %v, want %v", test.iterations, test.maximum, got, test.want)
		}
	}
}

func makeCircleParameters(count int) []CircleParameter {
	parameters := make([]CircleParameter, count)
	for i := range parameters {
		parameters[i] = CircleParameter{
			Number: i + 1, X: float64(i), Y: float64(i + 1), Radius: float64(i + 2),
			Red: 0.1, Green: 0.2, Blue: 0.3, Opacity: 0.4,
		}
	}

	return parameters
}

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		size int64
		want string
	}{
		{size: 0, want: "0 B"},
		{size: 1023, want: "1023 B"},
		{size: 1024, want: "1.0 KiB"},
		{size: 2 * 1024 * 1024, want: "2.0 MiB"},
		{size: 3 * 1024 * 1024 * 1024, want: "3.0 GiB"},
	}

	for _, test := range tests {
		if got := formatFileSize(test.size); got != test.want {
			t.Errorf("formatFileSize(%d) = %q, want %q", test.size, got, test.want)
		}
	}
}

// TestJobDetailPageAnnouncesIterationProgress pins both halves of the progress
// bar. Marking up the track as a progressbar is only half a fix: the live
// updater moves the fill's width, so an aria-valuenow it never touches would
// freeze at the server-rendered figure for the rest of the run.
func TestJobDetailPageAnnouncesIterationProgress(t *testing.T) {
	t.Parallel()

	job := JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "running",
		StartTime: time.Now(), Iterations: 25, MaxIters: 100,
	}

	var output bytes.Buffer

	err := JobDetailPage(job).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render job detail: %v", err)
	}

	body := output.String()
	for _, marker := range []string{
		`id="iteration-progress-track"`,
		`role="progressbar"`,
		`aria-label="Optimizer iteration progress"`,
		`aria-valuemin="0"`,
		`aria-valuemax="100"`,
		`aria-valuenow="25.0"`,
		`track.setAttribute("aria-valuenow", percent.toFixed(1))`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered detail page missing %q", marker)
		}
	}
}

// TestJobDetailPageAccentButtonsCarryTheirForeground guards the contrast fix.
// Setting only an accent background inline left the theme foreground in place,
// which inverts in dark mode: --text-color on --warning-color is 1.51:1. The
// .btn-danger/.btn-warning pairs ship a matching foreground, so the inline
// accents must not come back.
const cancelButton = `id="cancel-job" class="btn btn-danger"`

func TestJobDetailPageAccentButtonsCarryTheirForeground(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		state string
		want  []string
	}{
		{state: "running", want: []string{
			`id="pause-job" class="btn btn-warning"`,
			cancelButton,
		}},
		{state: "paused", want: []string{cancelButton}},
		{state: "pending", want: []string{cancelButton}},
		{state: "completed", want: []string{`id="delete-job" class="btn btn-danger"`}},
	} {
		t.Run(test.state, func(t *testing.T) {
			t.Parallel()

			job := JobDetail{
				ID: "12345678-1234-1234-1234-123456789abc", State: test.state,
				StartTime: time.Now(),
			}

			var output bytes.Buffer

			err := JobDetailPage(job).Render(context.Background(), &output)
			if err != nil {
				t.Fatalf("render job detail: %v", err)
			}

			body := output.String()
			for _, marker := range test.want {
				if !strings.Contains(body, marker) {
					t.Errorf("rendered detail page missing %q", marker)
				}
			}

			for _, banned := range []string{
				"background-color: var(--error-color)",
				"background-color: var(--warning-color)",
				"margin-left: 0.5rem; background-color",
			} {
				if strings.Contains(body, banned) {
					t.Errorf("rendered detail page still sets an inline accent: %q", banned)
				}
			}
		})
	}
}

// TestJobDetailPageSparklineIsKeyboardTraversable pins the keyboard route into
// the chart. The SVG is focusable and role="img", so before the keydown handler
// a keyboard reader could land on it and be told nothing at all: the live
// readout beside it was driven by pointer events alone.
func TestJobDetailPageSparklineIsKeyboardTraversable(t *testing.T) {
	t.Parallel()

	psnr := 31.25
	job := JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "running", StartTime: time.Now(),
		MetricHistory: []MetricSample{{Iteration: 1, Cost: 10, PSNR: &psnr}, {Iteration: 2, Cost: 9, PSNR: &psnr}},
	}

	var output bytes.Buffer

	err := JobDetailPage(job).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render job detail: %v", err)
	}

	body := output.String()
	for _, marker := range []string{
		`sparkline.addEventListener("keydown", moveSparklineSelection)`,
		`function moveSparklineSelection(event)`,
		`case "ArrowLeft":`, `case "ArrowRight":`, `case "Home":`, `case "End":`,
		// The keyboard path must drive the same readout the pointer path does,
		// rather than a second copy that can drift out of step with it.
		`renderSparklineHover(points[index])`,
		`positionSparklineTooltip()`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered detail page missing %q", marker)
		}
	}
}

// TestJobDetailPageHidesDecorativeRefreshGlyph keeps the glyph out of the
// button's accessible name, which would otherwise read "⟳ Refresh".
func TestJobDetailPageHidesDecorativeRefreshGlyph(t *testing.T) {
	t.Parallel()

	job := JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "completed", StartTime: time.Now(),
	}

	var output bytes.Buffer

	err := JobDetailPage(job).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render job detail: %v", err)
	}

	if !strings.Contains(output.String(), `<span aria-hidden="true">⟳</span> Refresh`) {
		t.Error("refresh glyph is still part of the button's accessible name")
	}
}

// TestJobDetailPageWrapsHeaderRows pins the wrap helpers. Both rows used to be
// a bare space-between flex with no flex-wrap, so on a narrow viewport the
// heading and its control cluster overlapped instead of stacking.
func TestJobDetailPageWrapsHeaderRows(t *testing.T) {
	t.Parallel()

	job := JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "running", StartTime: time.Now(),
		MetricHistory: []MetricSample{{Iteration: 1, Cost: 10}},
	}

	var output bytes.Buffer

	err := JobDetailPage(job).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render job detail: %v", err)
	}

	body := output.String()
	if got := strings.Count(body, `class="row-between"`); got < 2 {
		t.Errorf("row-between headers = %d, want at least 2", got)
	}

	if !strings.Contains(body, `class="action-row"`) {
		t.Error("job-controls cluster is not an action-row")
	}

	// A track wider than its container is what makes a 320px viewport scroll
	// sideways; min() clamps it.
	for _, marker := range []string{
		"repeat(auto-fit, minmax(min(160px, 100%), 1fr))",
		"repeat(auto-fit, minmax(min(200px, 100%), 1fr))",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered detail page missing clamped grid %q", marker)
		}
	}
}
