package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

func renderJobDetail(t *testing.T, job JobDetail) string {
	t.Helper()

	var output bytes.Buffer

	if err := JobDetailPage(job).Render(context.Background(), &output); err != nil {
		t.Fatalf("render job detail: %v", err)
	}

	return output.String()
}

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

	body := renderJobDetail(t, job)

	for _, marker := range []string{
		// The viewer's fallback: the side-by-side pair and the props the React
		// component is rendered with, and no mount point of its own.
		`data-view-mode="side-by-side"`,
		`data-default-mode="side-by-side"`,
		`data-colormap="turbo"`,
		`data-best-revision="7"`,
		`data-circle-count="64"`,
		`data-view-panel="reference"`,
		`data-view-panel="best"`,
		`best.png?v=7`,
		// One island over the whole body, and one JSON seed feeding it.
		`data-island="job-detail"`,
		`data-island-label="job detail"`,
		`id="job-detail-data"`,
		`640 × 480 px`,
		`title="2048 bytes">2.0 KiB`,
		`data-metric="psnr">31.25`,
		`data-metric="ssim">0.9123`,
		`class="card detail-summary"`, `class="card image-viewer detail-images"`,
		`class="card detail-history"`, `class="card detail-downloads download-card"`,
		`.detail-images {`, `order: 2;`, `data-metric="evaluations"`,
		`RGB mean squared error · committed and checkpoint-safe · lower is better`, `Peak signal-to-noise ratio · higher is better`,
		`Objective function calls`,
		`Best PNG`, `Parameters JSON`, `Difference PNG`, `HTML Report`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered detail page missing %q", marker)
		}
	}

	if strings.Contains(body, `{ fmt.Sprintf`) {
		t.Fatal("rendered detail page contains an unexpanded Go expression")
	}
}

// TestJobDetailPageCarriesNoInlineScript is Task 18.1's closing condition, and
// the one this whole port exists for: the 865 lines that used to live in this
// page are gone, not moved. Two script tags remain in the body and both are
// allowed -- the island bundle, which is a src reference with no body, and the
// JSON seed, which is data.
//
// Only the document body is scanned. Layout puts one more inline script in the
// head, the pre-paint theme IIFE, and that one stays by design: it has to run
// before the first paint, which means before the bundle exists.
func TestJobDetailPageCarriesNoInlineScript(t *testing.T) {
	t.Parallel()

	page := renderJobDetail(t, JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "running", StartTime: time.Now(),
		MetricHistory: []MetricSample{{Iteration: 1, Cost: 10}},
		Parameters:    []CircleParameter{{Number: 1}},
	})

	start := strings.Index(page, "<body")
	if start < 0 {
		t.Fatal("the rendered page has no body")
	}

	body := page[start:]

	for _, tag := range regexp.MustCompile(`(?s)<script\b[^>]*>(.*?)</script>`).FindAllStringSubmatch(body, -1) {
		opening := tag[0][:strings.Index(tag[0], ">")+1]

		switch {
		case strings.Contains(opening, `src=`):
			if strings.TrimSpace(tag[1]) != "" {
				t.Errorf("the bundle reference carries a body: %q", tag[1])
			}
		case strings.Contains(opening, `type="application/json"`):
			if !json.Valid([]byte(tag[1])) {
				t.Errorf("the JSON seed is not valid JSON: %q", tag[1])
			}
		default:
			t.Errorf("the detail page carries an inline script again: <script %s", opening)
		}
	}
}

// TestJobDetailPageMountsOneIslandOverTheBody pins the shape the port depends
// on. The action row and the image viewer are both inside this island's root
// now -- job-controls and image-viewer are gone as separate mount points -- and
// mounting replaces every child of the root, so the root has to open before the
// first thing the island renders and close before the seed it reads.
func TestJobDetailPageMountsOneIslandOverTheBody(t *testing.T) {
	t.Parallel()

	body := renderJobDetail(t, JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "running", StartTime: time.Now(),
	})

	for _, gone := range []string{`data-island="job-controls"`, `data-island="image-viewer"`} {
		if strings.Contains(body, gone) {
			t.Errorf("the detail page still advertises %s beside the job-detail island", gone)
		}
	}

	if got := strings.Count(body, `data-island="job-detail"`); got != 1 {
		t.Fatalf("job-detail mount points = %d, want 1", got)
	}

	root := strings.Index(body, `data-island="job-detail"`)
	seed := strings.Index(body, `id="job-detail-data"`)

	for _, inside := range []string{`class="action-row"`, `class="detail-stack"`, `id="image-viewer"`, `id="parameter-viewer"`} {
		at := strings.Index(body, inside)
		if at < root {
			t.Errorf("%s is rendered before the island root and would survive mounting", inside)
		}

		if at > seed {
			t.Errorf("%s is rendered after the seed, which is outside the island root", inside)
		}
	}

	// The seed has to sit outside the root: mounting sweeps every child away,
	// and a seed the island reads cannot be one of them.
	if seed < root {
		t.Fatal("the JSON seed is rendered before the island root")
	}
}

// TestJobDetailPageSeedsTheIsland covers what the island actually reads. The
// page hands it one JSON blob rather than forty formatted data attributes, so
// the blob has to round-trip: what comes back out has to be the job that went
// in, under the wire names web/src/JobDetail.tsx declares.
func TestJobDetailPageSeedsTheIsland(t *testing.T) {
	t.Parallel()

	psnr := 31.25
	job := JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "running", StartTime: time.Now(),
		Circles: 64, Iterations: 25, MaxIters: 100, Evaluations: 12_345,
		BestCost: 12.5, InitialCost: 40.5, BestRevision: 7, CPS: 2.5, CanPolish: true,
		SSIMEnabled: true, PSNR: &psnr, RefWidth: 640, RefHeight: 480, RefSize: 2048,
		MetricHistory: []MetricSample{{Iteration: 1, Cost: 10}},
		Parameters:    []CircleParameter{{Number: 1}},
	}

	seed := map[string]any{}
	if err := json.Unmarshal([]byte(jobDetailSeedJSON(t, renderJobDetail(t, job))), &seed); err != nil {
		t.Fatalf("parse the island seed: %v", err)
	}

	for name, want := range map[string]any{
		"id": job.ID, "state": job.State, "circles": 64.0, "iterations": 25.0,
		"maxIterations": 100.0, "evaluations": 12345.0, "bestCost": 12.5,
		"initialCost": 40.5, "bestRevision": 7.0, "cps": 2.5, "canPolish": true,
		"ssimEnabled": true, "refWidth": 640.0, "refHeight": 480.0, "refSize": 2048.0,
	} {
		if got, ok := seed[name]; !ok || got != want {
			t.Errorf("seed[%q] = %v, want %v", name, got, want)
		}
	}

	for _, name := range []string{"metricHistory", "parameters"} {
		list, ok := seed[name].([]any)
		if !ok || len(list) != 1 {
			t.Errorf("seed[%q] = %v, want one entry", name, seed[name])
		}
	}
}

// jobDetailSeedJSON returns the contents of the page's #job-detail-data script.
func jobDetailSeedJSON(t *testing.T, body string) string {
	t.Helper()

	match := regexp.MustCompile(`(?s)<script id="job-detail-data"[^>]*>(.*?)</script>`).FindStringSubmatch(body)
	if match == nil {
		t.Fatal("the detail page renders no #job-detail-data seed")
	}

	return match[1]
}

func TestJobDetailPageDistinguishesCandidateFromAuditedBest(t *testing.T) {
	candidate := 95.25
	body := renderJobDetail(t, JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "running", StartTime: time.Now(),
		BestCost: 100, CandidateCost: &candidate,
	})

	for _, marker := range []string{
		"Audited Best Cost", "In-flight Candidate", "95.2500", "4.7500 (4.75%) provisional gain",
		"pending full-image usefulness audit", `data-metric="candidate-psnr"`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered detail page missing %q", marker)
		}
	}
}

func TestJobDetailPageOmitsSSIMControlsWhenDisabled(t *testing.T) {
	psnr, ssim := 31.25, 0.9123
	body := renderJobDetail(t, JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "pending", StartTime: time.Now(),
		MetricHistory: []MetricSample{{Iteration: 1, Cost: 10, PSNR: &psnr, SSIM: &ssim}},
	})

	if strings.Contains(body, `data-metric-card="ssim"`) {
		t.Error("disabled SSIM was given a metric card")
	}

	// The fallback table columns follow the same flag: a job that never
	// computed SSIM would otherwise get a column of em dashes.
	if strings.Contains(body, ">SSIM<") {
		t.Error("disabled SSIM was offered as a history column")
	}

	if !strings.Contains(body, `"ssimEnabled":false`) {
		t.Error("the island seed does not record that SSIM is disabled")
	}
}

func TestJobDetailPageShowsPolishingSchedule(t *testing.T) {
	body := renderJobDetail(t, JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "pending", StartTime: time.Now(),
		PolishingEnabled: true, PolishingActiveSetSize: 5, PolishingMaxSweeps: 3,
		PolishingEpochs: 2, PolishingIters: 1000, PolishingStagnationIters: 500,
		PolishingMinImprovement: 0.001, CanPolish: true,
	})

	for _, marker := range []string{
		"Active-set Polishing", "Enabled · up to 3 sweeps of 5 circles", "2 × 1000 iterations",
		"progress threshold 0.001", "Polish weak circles", `"canPolish":true`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered detail page missing %q", marker)
		}
	}
}

// TestJobDetailFallbackMutationsAreDisabled pins the honesty of the fallback.
// The job-detail island replaces these buttons with working ones on mount, so
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
		// The report is assembled in the browser from a blob, so unlike the
		// other three downloads it has no URL to fall back to at all.
		{"completed", "download-report"},
	} {
		t.Run(tc.button+"/"+tc.state, func(t *testing.T) {
			body := renderJobDetail(t, JobDetail{
				ID: "12345678-1234-1234-1234-123456789abc", State: tc.state,
				StartTime: time.Now(),
			})

			start := strings.Index(body, `id="`+tc.button+`"`)
			if start < 0 {
				t.Fatalf("state %q renders no %s button", tc.state, tc.button)
			}

			tag := body[start : start+strings.Index(body[start:], ">")]
			if !strings.Contains(tag, `aria-disabled="true"`) || !strings.Contains(tag, "disabled") {
				t.Errorf("%s is offered as clickable without the bundle: <button %s>", tc.button, tag)
			}
		})
	}
}

// TestJobDetailFallbackIsCompleteWithoutScript is Task 18.1's third acceptance
// check, asserted by rendering the page rather than by reading the source. With
// JavaScript disabled the reader still gets the state, the metrics, the images,
// the parameters and the artifact links -- everything except the four things
// that genuinely need a script, which the noscript notice names.
func TestJobDetailFallbackIsCompleteWithoutScript(t *testing.T) {
	t.Parallel()

	psnr := 31.25
	body := renderJobDetail(t, JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "completed",
		StartTime: time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC),
		Mode:      "batch", Optimizer: "mayfly", Variant: "standard", RefPath: "assets/reference.png",
		Circles: 64, PopSize: 40, Iterations: 100, MaxIters: 100, Evaluations: 4000,
		BestCost: 12.5, InitialCost: 40.5, PSNR: &psnr, ElapsedSec: 95.5, CPS: 2500,
		RefWidth: 640, RefHeight: 480, RefSize: 2048,
		MetricHistory: []MetricSample{
			{Iteration: 50, Cost: 20, PSNR: &psnr, CPS: 2400, Timestamp: time.Date(2026, time.August, 13, 9, 1, 0, 0, time.UTC)},
			{Iteration: 100, Cost: 12.5, PSNR: &psnr, CPS: 2500, Timestamp: time.Date(2026, time.August, 13, 9, 1, 20, 0, time.UTC)},
		},
		Parameters: []CircleParameter{{Number: 1, X: 12.345, Y: 67.891, Radius: 4.567, Red: 1, Green: 0.5, Opacity: 0.75}},
	})

	for _, present := range []string{
		// State.
		`class="badge badge-success"`, "Completed",
		// Metrics, including the four figures that used to be computed only in
		// the browser and left as em dashes here.
		"12.5000", "31.25", "100.0%", "4.0K", "1m 35s", "Aug 13, 2026 9:00 AM",
		`data-metric="cost-improvement-rate"`, `data-metric="eta"`, `data-metric="cps-current"`,
		// Images.
		`alt="Reference Image"`, `alt="Current Best Image"`, "640 × 480 px",
		// Metric history, as a table rather than as an empty canvas.
		`id="metric-history-table"`, "Iteration", "Cost", "PSNR",
		// Configuration and parameters.
		"assets/reference.png", "Circle 1: (12.35, 67.89, 4.57) RGB(255, 128, 0) α=0.750",
		// Artifact links, which are plain hrefs and work with no script at all.
		"/best.png?download=1", "/params.json", "/diff.png?colormap=turbo&amp;download=1",
		"Download params.json",
		"<noscript>",
	} {
		if !strings.Contains(body, present) {
			t.Errorf("no-JavaScript fallback is missing %q", present)
		}
	}
}

func TestJobDetailPageMetadataUnavailable(t *testing.T) {
	body := renderJobDetail(t, JobDetail{
		ID:        "12345678-1234-1234-1234-123456789abc",
		State:     "pending",
		StartTime: time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC),
	})

	if !strings.Contains(body, "Metadata unavailable") {
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
			body := renderJobDetail(t, JobDetail{
				ID: "12345678-1234-1234-1234-123456789abc", State: "completed",
				StartTime: time.Now(), Circles: 64, Parameters: test.parameters,
			})

			for _, marker := range []string{
				`id="parameter-viewer"`, `id="parameter-list"`, `id="job-detail-data"`,
				`params.json`, `Best PNG`, `Parameters JSON`, `Difference PNG`,
				`id="download-report"`, `role="status" aria-live="polite"`,
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

// TestJobDetailPageAnnouncesIterationProgress pins the progress bar the
// fallback renders. Its live half moved to the island, which drives the width
// and aria-valuenow from one number for the same reason: a fill that advances
// beside a frozen aria-valuenow leaves a screen reader on the figure the page
// was served with.
func TestJobDetailPageAnnouncesIterationProgress(t *testing.T) {
	t.Parallel()

	body := renderJobDetail(t, JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "running",
		StartTime: time.Now(), Iterations: 25, MaxIters: 100,
	})

	for _, marker := range []string{
		`id="iteration-progress-track"`,
		`role="progressbar"`,
		`aria-label="Optimizer iteration progress"`,
		`aria-valuemin="0"`,
		`aria-valuemax="100"`,
		`aria-valuenow="25.0"`,
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

			body := renderJobDetail(t, JobDetail{
				ID: "12345678-1234-1234-1234-123456789abc", State: test.state,
				StartTime: time.Now(),
			})

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

// TestJobDetailPageHidesDecorativeRefreshGlyph keeps the glyph out of the
// button's accessible name, which would otherwise read "⟳ Refresh".
func TestJobDetailPageHidesDecorativeRefreshGlyph(t *testing.T) {
	t.Parallel()

	body := renderJobDetail(t, JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "completed", StartTime: time.Now(),
	})

	if !strings.Contains(body, `<span aria-hidden="true">⟳</span> Refresh`) {
		t.Error("refresh glyph is still part of the button's accessible name")
	}
}

// TestJobDetailPageWrapsHeaderRows pins the wrap helpers. Both rows used to be
// a bare space-between flex with no flex-wrap, so on a narrow viewport the
// heading and its control cluster overlapped instead of stacking.
func TestJobDetailPageWrapsHeaderRows(t *testing.T) {
	t.Parallel()

	body := renderJobDetail(t, JobDetail{
		ID: "12345678-1234-1234-1234-123456789abc", State: "running", StartTime: time.Now(),
		MetricHistory: []MetricSample{{Iteration: 1, Cost: 10}},
	})

	if got := strings.Count(body, `class="row-between"`); got < 2 {
		t.Errorf("row-between headers = %d, want at least 2", got)
	}

	if !strings.Contains(body, `class="action-row"`) {
		t.Error("the job action cluster is not an action-row")
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
