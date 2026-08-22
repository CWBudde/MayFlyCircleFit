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
		`data-view-mode="side-by-side"`,
		`data-best-revision="7"`,
		`data-circle-count="64"`,
		`const circleCount = Number.parseInt(imageViewer.dataset.circleCount, 10) || 0;`,
		`name="view-mode" value="reference" aria-keyshortcuts="1"`,
		`name="view-mode" value="best" aria-keyshortcuts="2"`,
		`name="view-mode" value="side-by-side" aria-keyshortcuts="3" checked`,
		`name="view-mode" value="difference" aria-keyshortcuts="4"`,
		`name="view-mode" value="overlay" aria-keyshortcuts="5"`,
		`data-view-panel="reference"`,
		`data-view-panel="best"`,
		`data-view-panel="difference"`,
		`data-view-panel="overlay"`,
		`mayflycirclefit.viewMode`,
		`mayflycirclefit.overlayOpacity`,
		`initializeImageState(`,
		`initializeOverlayOpacity()`,
		`id="best-image-error"`,
		`id="diff-image-error"`,
		`id="overlay-best-image-error"`,
		`id="overlay-opacity"`,
		`id="overlay-best-layer" class="overlay-best-layer" style="opacity: 0.5;"`,
		`id="overlay-best-image"`,
		`best.png?v=7`,
		`id="heatmap-colormap"`,
		`<option value="turbo" selected>Turbo</option>`,
		`<option value="magma">Magma</option>`,
		`diff.png?colormap=turbo&amp;v=7`,
		`id="heatmap-legend-gradient"`,
		`Mean absolute RGB error per pixel`,
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
				`syncArtifactDownloadColormap()`, `role="status" aria-live="polite"`,
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
