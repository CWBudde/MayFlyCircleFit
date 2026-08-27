//nolint:testpackage // checks the unexported view helpers the templ output calls
package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The Go half of Task 18.1's before-and-after-mount check.
//
// The job detail page is rendered twice for every reader who has JavaScript:
// once by JobDetailPage, which is the fallback and the island's hydration seed,
// and again by web/src/JobDetail.tsx the moment the bundle mounts over it. The
// acceptance check is that the two agree on every metric, ETA, throughput and
// parameter value, which is not something either side can assert on its own.
// Both are therefore checked against web/src/job-detail-parity.json instead:
// this file for Go, web/src/JobDetail.test.ts for TypeScript. Neither language
// is the source of truth; the fixture is.

type jobDetailParityExpectation struct {
	BestCost            string   `json:"bestCost"`
	PSNR                string   `json:"psnr"`
	SSIM                string   `json:"ssim"`
	Iterations          string   `json:"iterations"`
	IterationProgress   string   `json:"iterationProgress"`
	Evaluations         string   `json:"evaluations"`
	AverageCPS          string   `json:"averageCps"`
	CurrentCPS          string   `json:"currentCps"`
	ETA                 string   `json:"eta"`
	CostImprovementRate string   `json:"costImprovementRate"`
	Elapsed             string   `json:"elapsed"`
	StartTime           string   `json:"startTime"`
	ReferenceDimensions string   `json:"referenceDimensions"`
	ReferenceFileSize   string   `json:"referenceFileSize"`
	Termination         string   `json:"termination"`
	Parameters          []string `json:"parameters"`
}

type jobDetailParityCase struct {
	Name     string                     `json:"name"`
	Job      JobDetail                  `json:"job"`
	Expected jobDetailParityExpectation `json:"expected"`
}

// loadJobDetailParity reads the shared fixture. The job in each case is a
// serialized JobDetail, which is byte for byte the seed JobDetailPage writes
// into #job-detail-data, so unmarshalling it here also exercises the wire tags
// the island reads.
func loadJobDetailParity(t *testing.T) []jobDetailParityCase {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "job-detail-parity.json"))
	if err != nil {
		t.Fatalf("read job-detail-parity.json: %v", err)
	}

	var contract struct {
		Cases []jobDetailParityCase `json:"cases"`
	}

	err = json.Unmarshal(raw, &contract)
	if err != nil {
		t.Fatalf("parse job-detail-parity.json: %v", err)
	}

	if len(contract.Cases) < 2 {
		t.Fatalf("the fixture names %d cases; a running job and a terminal job are both required", len(contract.Cases))
	}

	return contract.Cases
}

// TestJobDetailHelpersMatchSharedContract pins each derived figure to the exact
// string the fixture names. It is the half that catches a formatting change: a
// rounding mode, a suffix, an arrow.
func TestJobDetailHelpersMatchSharedContract(t *testing.T) {
	t.Parallel()

	for _, testCase := range loadJobDetailParity(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			t.Parallel()

			job := testCase.Job
			want := testCase.Expected

			for _, check := range []struct {
				what string
				got  string
				want string
			}{
				{"best cost", fmt.Sprintf("%.4f", job.BestCost), want.BestCost},
				{"psnr", auditedPSNR(job), want.PSNR},
				{"ssim", auditedSSIM(job), want.SSIM},
				{"iterations", strconv.Itoa(job.Iterations), want.Iterations},
				{
					"iteration progress",
					fmt.Sprintf("%.1f%%", progressPercent(job.Iterations, job.MaxIters)),
					want.IterationProgress,
				},
				{"evaluations", formatNumber(float64(job.Evaluations)), want.Evaluations},
				{"average cps", formatNumber(averageCPS(job.MetricHistory, job.CPS)), want.AverageCPS},
				{"current cps", formatNumber(currentCPS(job.MetricHistory, job.Circles, job.CPS)), want.CurrentCPS},
				{"eta", etaLabel(job.MetricHistory, job.MaxIters), want.ETA},
				{"cost improvement rate", costImprovementRate(job.MetricHistory), want.CostImprovementRate},
				{"elapsed", formatDuration(job.ElapsedSec), want.Elapsed},
				{"start time", formatTimestamp(job.StartTime), want.StartTime},
				{"termination", job.Termination, want.Termination},
			} {
				if check.got != check.want {
					t.Errorf("%s = %q, want %q", check.what, check.got, check.want)
				}
			}

			viewer := ImageViewerData{
				ReferenceWidth:  job.RefWidth,
				ReferenceHeight: job.RefHeight,
				ReferenceSize:   job.RefSize,
			}
			if got := imageViewerDimensions(viewer); got != want.ReferenceDimensions {
				t.Errorf("reference dimensions = %q, want %q", got, want.ReferenceDimensions)
			}

			if got := imageViewerFileSize(viewer); got != want.ReferenceFileSize {
				t.Errorf("reference file size = %q, want %q", got, want.ReferenceFileSize)
			}

			if len(job.Parameters) != len(want.Parameters) {
				t.Fatalf("fixture describes %d circles but supplies %d", len(want.Parameters), len(job.Parameters))
			}

			for i, circle := range job.Parameters {
				if got := parameterDescription(circle); got != want.Parameters[i] {
					t.Errorf("circle %d = %q, want %q", i, got, want.Parameters[i])
				}
			}
		})
	}
}

// TestJobDetailPageRendersTheContractValues is the other half: the strings the
// helpers produce have to actually reach the page. Presence is what matters
// here -- an exact position would pin templ's whitespace rather than the
// contract -- because the island renders the same strings into the same slots.
func TestJobDetailPageRendersTheContractValues(t *testing.T) {
	t.Parallel()

	for _, testCase := range loadJobDetailParity(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer

			err := JobDetailPage(testCase.Job).Render(context.Background(), &output)
			if err != nil {
				t.Fatalf("render job detail: %v", err)
			}

			body := output.String()
			want := testCase.Expected

			markers := []string{
				want.BestCost, want.PSNR, want.IterationProgress, want.Evaluations,
				want.AverageCPS, want.CurrentCPS, want.ETA, want.CostImprovementRate,
				want.Elapsed, want.StartTime,
			}
			markers = append(markers, want.Parameters...)

			for _, optional := range []string{want.SSIM, want.ReferenceDimensions, want.ReferenceFileSize, want.Termination} {
				if optional != "" {
					markers = append(markers, optional)
				}
			}

			for _, marker := range markers {
				if !strings.Contains(body, marker) {
					t.Errorf("rendered detail page does not show %q", marker)
				}
			}
		})
	}
}

// auditedPSNR and auditedSSIM restate the two conditionals the page's markup
// spells out inline, so the fixture can pin what those branches print.
func auditedPSNR(job JobDetail) string {
	switch {
	case job.PSNRInfinite:
		return "∞"
	case job.PSNR != nil:
		return fmt.Sprintf("%.2f", *job.PSNR)
	default:
		return "—"
	}
}

func auditedSSIM(job JobDetail) string {
	if !job.SSIMEnabled {
		return ""
	}

	if job.SSIM == nil {
		return "Calculating…"
	}

	return fmt.Sprintf("%.4f", *job.SSIM)
}

// The cases below cover the branches the two-job fixture cannot reach on its
// own. web/src/JobDetail.test.ts has the same table, because a derived figure
// that disagrees between the fallback and the island is exactly the failure
// Task 18.1's acceptance check exists to prevent.

func at(iteration, evaluations int, cost, cps float64, offsetMilliseconds int) MetricSample {
	sample := MetricSample{Iteration: iteration, Evaluations: evaluations, Cost: cost, CPS: cps}
	if offsetMilliseconds >= 0 {
		sample.Timestamp = time.UnixMilli(int64(offsetMilliseconds)).UTC()
	}

	return sample
}

func TestLatestHistorySamplePrefersAStampedSample(t *testing.T) {
	t.Parallel()

	stamped := at(1, 10, 5, 100, 1000)
	unstamped := at(2, 20, 4, 110, -1)

	if got, ok := latestHistorySample([]MetricSample{stamped, unstamped}); !ok || got.Iteration != 1 {
		t.Errorf("latest = %+v (ok=%v), want the stamped sample", got, ok)
	}

	// With nothing stamped there is still a newest sample; it just cannot take
	// part in a rate.
	if got, ok := latestHistorySample([]MetricSample{unstamped}); !ok || got.Iteration != 2 {
		t.Errorf("latest = %+v (ok=%v), want the only sample", got, ok)
	}

	if _, ok := latestHistorySample(nil); ok {
		t.Error("an empty history reported a latest sample")
	}
}

func TestPreviousHistorySampleOrdersAnEqualInstantByIteration(t *testing.T) {
	t.Parallel()

	// A clock coarser than the loop stamps two samples identically.
	older := at(1, 10, 5, 100, 1000)
	target := at(2, 20, 4, 110, 1000)

	if got, ok := previousHistorySample([]MetricSample{older, target}, 0, target); !ok || got.Iteration != 1 {
		t.Errorf("previous = %+v (ok=%v), want the lower iteration", got, ok)
	}

	if _, ok := previousHistorySample([]MetricSample{target}, 0, target); ok {
		t.Error("a sample was reported as its own predecessor")
	}
}

func TestDerivedThroughputFallsBackToTheJobFigure(t *testing.T) {
	t.Parallel()

	only := []MetricSample{at(5, 10, 3, 3, 1000)}

	for _, test := range []struct {
		name    string
		history []MetricSample
		circles int
		want    float64
	}{
		{name: "one sample", history: only, circles: 64, want: 42},
		{name: "no circles", history: only, circles: 0, want: 42},
		{name: "no history", history: nil, circles: 64, want: 42},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := currentCPS(test.history, test.circles, 42); got != test.want {
				t.Errorf("currentCPS = %v, want %v", got, test.want)
			}
		})
	}

	if got := averageCPS(nil, 42); got != 42 {
		t.Errorf("averageCPS on an empty history = %v, want the job figure 42", got)
	}
}

func TestFormatETAResolution(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		seconds float64
		want    string
	}{
		{0, "0s"}, {45.4, "45s"}, {300, "5m 0s"}, {3725, "1h 2m"}, {-1, "—"},
	} {
		if got := formatETA(test.seconds); got != test.want {
			t.Errorf("formatETA(%v) = %q, want %q", test.seconds, got, test.want)
		}
	}
}

func TestETALabelWithoutABasis(t *testing.T) {
	t.Parallel()

	history := []MetricSample{at(10, 100, 5, 50, 1000), at(20, 200, 4, 50, 2000)}

	for _, test := range []struct {
		name    string
		history []MetricSample
		maximum int
		want    string
	}{
		{name: "no planned count", history: history, maximum: 0, want: "—"},
		{name: "no history", history: nil, maximum: 100, want: "—"},
		{name: "already there", history: []MetricSample{at(100, 100, 5, 50, 1)}, maximum: 100, want: "0s"},
		{name: "projected", history: history, maximum: 40, want: "2s"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := etaLabel(test.history, test.maximum); got != test.want {
				t.Errorf("etaLabel = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCostImprovementRateDistinguishesStalledFromUnmeasurable(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		history []MetricSample
		want    string
	}{
		{name: "stalled", history: []MetricSample{at(1, 0, 5, 0, 0), at(2, 0, 5, 0, 1)}, want: "→ 0.0000 / iter"},
		{name: "one sample", history: []MetricSample{at(1, 0, 5, 0, 0)}, want: "—"},
		{name: "same iteration", history: []MetricSample{at(1, 0, 5, 0, 0), at(1, 0, 4, 0, 1)}, want: "—"},
		{name: "improving", history: []MetricSample{at(0, 0, 5, 0, 0), at(10, 0, 4, 0, 1)}, want: "↓ 0.1000 / iter"},
		{name: "worsening", history: []MetricSample{at(0, 0, 4, 0, 0), at(10, 0, 5, 0, 1)}, want: "↑ 0.1000 / iter"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := costImprovementRate(test.history); got != test.want {
				t.Errorf("costImprovementRate = %q, want %q", got, test.want)
			}
		})
	}
}
