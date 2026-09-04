//nolint:testpackage // reaches the unexported dirty-region session, its switch and its telemetry
package renderer

import (
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg" // registered for image.Decode
	_ "image/png"  // the committed reference is a PNG
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/circlefit/internal/fit"
	"github.com/cwbudde/circlefit/internal/opt"
)

// The end-to-end acceptance check for the dirty-region candidate evaluator
// (Task 4 of ../../../PLAN.md). Everything else that pins the evaluator scores
// candidates directly against a synthetic vector; this drives one complete
// polishing sweep of a real fitted 2,111-circle solution through
// PolishCircleBatchContext, twice, and compares the two evaluators on the
// answer they reach rather than on the cost of a single candidate.
//
// The fixture is an immutable committed checkpoint, testdata/README.md records
// where it came from. It is a different vector from the one behind the 599 s
// sweep this check originally referred to: that run fitted a reference image
// the repository does not carry, so its wall clock is not reproducible here and
// is not claimed. What this establishes instead is self-contained -- both
// evaluators, same budget, same seed, one host, one revision.
const (
	polishFixturePath      = "testdata/polish-fixture-2111.json"
	polishFixtureRepoRoot  = "../../.."
	polishFixtureCircles   = 2111
	polishFixtureSweepCost = 85.12514114379883
)

const (
	polishFixtureMaxSweeps  = 1
	polishFixtureEpochs     = 2
	polishFixtureMaxWorkers = 12
)

// polishFixtureShape is one sweep configuration to run through both evaluators.
type polishFixtureShape struct {
	name            string
	strategy        BatchPolishStrategy
	activeSetSize   int
	iters           int
	popSize         int
	stagnationIters int
	minImprovement  float64
}

// Three shapes, because what decides whether the dirty-region evaluator can
// score a candidate is the active set's canvas coverage, and both the size and
// the selector move it. "default" is what a job gets when the caller sets
// nothing (app.DefaultPolishing*, active set 5, merit selection); "wide" is the
// production-shaped sweep the original 2,111-circle calibration ran; "window"
// keeps the default budget and changes only the selector, because
// TestPolishFixtureActiveSetCoverage shows positional windows of five clear the
// gate where merit-selected sets of five do not. One sweep each: a sweep is the
// unit under test, and repeating it measures the same thing again.
var polishFixtureShapes = []polishFixtureShape{
	{
		name: "default", strategy: BatchPolishWeakestReplacement,
		activeSetSize: 5, iters: 200, popSize: 30, stagnationIters: 100, minImprovement: 0.001,
	},
	{
		name: "wide", strategy: BatchPolishWeakestReplacement,
		activeSetSize: 100, iters: 400, popSize: 60, stagnationIters: 300, minImprovement: 0.005,
	},
	{
		name: "window", strategy: BatchPolishContiguousWindow,
		activeSetSize: 5, iters: 200, popSize: 30, stagnationIters: 100, minImprovement: 0.001,
	},
}

// polishFixture is a minimal view of the committed checkpoint. It deliberately
// does not decode through internal/store: this package sits below store in the
// dependency order and the harness needs the parameter vector, not the store's
// validation or lineage rules.
type polishFixture struct {
	JobID         string    `json:"jobId"`
	BestParams    []float64 `json:"bestParams"`
	BestCost      float64   `json:"bestCost"`
	ActualCircles int       `json:"actualCircles"`
	EffectiveSeed int64     `json:"effectiveSeed"`
	Config        struct {
		RefPath string `json:"refPath"`
	} `json:"config"`
}

func loadPolishFixture(t *testing.T) polishFixture {
	t.Helper()

	raw, err := os.ReadFile(polishFixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var fixture polishFixture

	err = json.Unmarshal(raw, &fixture)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	if fixture.ActualCircles != polishFixtureCircles {
		t.Fatalf("fixture circles = %d, want %d", fixture.ActualCircles, polishFixtureCircles)
	}

	if len(fixture.BestParams) != polishFixtureCircles*paramsPerCircle {
		t.Fatalf("fixture parameters = %d, want %d",
			len(fixture.BestParams), polishFixtureCircles*paramsPerCircle)
	}

	if fixture.BestCost != polishFixtureSweepCost {
		t.Fatalf("fixture cost = %v, want %v", fixture.BestCost, polishFixtureSweepCost)
	}

	return fixture
}

// loadPolishFixtureReference resolves the fixture's own repo-relative refPath,
// so the reference the sweep scores against is the one the checkpoint names
// rather than one this file picks independently.
func loadPolishFixtureReference(t *testing.T, fixture polishFixture) *image.NRGBA {
	t.Helper()

	path := filepath.Join(polishFixtureRepoRoot, filepath.FromSlash(fixture.Config.RefPath))

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open reference %s: %v", path, err)
	}
	defer file.Close()

	decoded, _, err := image.Decode(file)
	if err != nil {
		t.Fatalf("decode reference %s: %v", path, err)
	}

	bounds := decoded.Bounds()

	ref := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ref.Set(x, y, decoded.At(x, y))
		}
	}

	return ref
}

// polishFixtureArm is one complete sweep and what it cost to run.
type polishFixtureArm struct {
	result   *BatchPolishResult
	elapsed  time.Duration
	sessions []*polishDirtySession
}

func runPolishFixtureArm(
	t *testing.T,
	ref *image.NRGBA,
	fixture polishFixture,
	shape polishFixtureShape,
	dirty bool,
	workers int,
) polishFixtureArm {
	t.Helper()

	var (
		mu       sync.Mutex
		sessions []*polishDirtySession
	)

	// The hook and the switch are package state, so restore both before
	// returning; a leaked value would silently change every later test.
	previousEnabled := polishDirtyEnabled
	previousHook := polishDirtySessionHook

	defer func() {
		polishDirtyEnabled = previousEnabled
		polishDirtySessionHook = previousHook
	}()

	polishDirtyEnabled = dirty
	polishDirtySessionHook = func(session *polishDirtySession) {
		mu.Lock()
		defer mu.Unlock()

		sessions = append(sessions, session)
	}

	cpu := NewCPURenderer(ref, fixture.ActualCircles)
	cpu.SetThreads(1)

	optimizer, err := opt.NewMayflyVariant("standard",
		shape.iters, shape.popSize, fixture.EffectiveSeed,
		opt.WithEarlyStop(opt.Stop{
			MinImprovement:  shape.minImprovement,
			StagnationIters: shape.stagnationIters,
		}),
		opt.WithParallelEvaluation(workers),
	)
	if err != nil {
		t.Fatalf("build optimizer: %v", err)
	}

	started := time.Now()

	result, err := PolishCircleBatchContext(context.Background(), cpu,
		opt.WithEpochs(optimizer, polishFixtureEpochs),
		fixture.BestParams, BatchPolishOptions{
			ActiveSetSize: shape.activeSetSize,
			MaxSweeps:     polishFixtureMaxSweeps,
			Strategy:      shape.strategy,
		})
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("polish sweep: %v", err)
	}

	return polishFixtureArm{result: result, elapsed: elapsed, sessions: sessions}
}

// TestPolishFixtureDirtyVsFull runs the same production-shaped polishing sweep
// through the dirty-region evaluator and through the full-canvas evaluator and
// requires that they reach exactly the same answer.
//
// Bit-identical is the right bar rather than a tolerance. The dirty evaluator
// returns exactly equal floats by construction, which
// TestPolishDirtySessionMatchesFullCanvas pins per candidate; equal costs mean
// the optimizer sees an identical landscape and takes an identical trajectory,
// so a whole sweep must agree to the last bit. A tolerance here would hide the
// only failure mode worth catching.
//
//nolint:paralleltest // flips the package-level dirty-region switch, which no two tests may do at once
func TestPolishFixtureDirtyVsFull(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end polishing sweep on a 2,111-circle fixture; runs for minutes")
	}

	fixture := loadPolishFixture(t)
	ref := loadPolishFixtureReference(t, fixture)

	workers := min(polishFixtureMaxWorkers, runtime.GOMAXPROCS(0))
	t.Logf("host: GOMAXPROCS=%d, evaluation workers=%d, render threads=1",
		runtime.GOMAXPROCS(0), workers)
	t.Logf("fixture: job %s, %d circles, cost %.12f, reference %s",
		fixture.JobID, fixture.ActualCircles, fixture.BestCost, fixture.Config.RefPath)

	maskedTotal := 0

	//nolint:paralleltest // each shape owns the package switch for the length of its two arms
	for _, shape := range polishFixtureShapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Logf("sweep: strategy=%s activeSet=%d sweeps=%d epochs=%d "+
				"iters=%d pop=%d stagnation=%d minImprovement=%v",
				shape.strategy, shape.activeSetSize, polishFixtureMaxSweeps, polishFixtureEpochs,
				shape.iters, shape.popSize, shape.stagnationIters, shape.minImprovement)

			dirtyArm := runPolishFixtureArm(t, ref, fixture, shape, true, workers)
			fullArm := runPolishFixtureArm(t, ref, fixture, shape, false, workers)

			// A vacuous pass is the trap worth guarding: if the dirty
			// evaluator was never installed, both arms ran the same path and
			// would agree for a reason that says nothing about the evaluator.
			if len(dirtyArm.sessions) == 0 {
				t.Fatal("dirty arm installed no dirty-region sessions, so the comparison is vacuous")
			}

			if len(fullArm.sessions) != 0 {
				t.Fatalf("full arm installed %d dirty-region sessions, want 0", len(fullArm.sessions))
			}

			if dirtyArm.result.BestCost != fullArm.result.BestCost {
				t.Errorf("cost differs: dirty %.17g, full %.17g (delta %g)",
					dirtyArm.result.BestCost, fullArm.result.BestCost,
					dirtyArm.result.BestCost-fullArm.result.BestCost)
			}

			for i := range fullArm.result.BestParams {
				if dirtyArm.result.BestParams[i] != fullArm.result.BestParams[i] {
					t.Fatalf("parameter %d differs: dirty %.17g, full %.17g",
						i, dirtyArm.result.BestParams[i], fullArm.result.BestParams[i])
				}
			}

			reportPolishFixtureArm(t, "dirty", dirtyArm, fixture)
			reportPolishFixtureArm(t, "full", fullArm, fixture)

			if dirtyArm.elapsed > 0 {
				t.Logf("wall clock: dirty %v, full %v, speedup %.2fx",
					dirtyArm.elapsed.Round(time.Millisecond),
					fullArm.elapsed.Round(time.Millisecond),
					fullArm.elapsed.Seconds()/dirtyArm.elapsed.Seconds())
			}

			maskedTotal += reportPolishFixtureFractions(t, dirtyArm)
		})
	}

	// Parity across a sweep whose every candidate fell back to the full canvas
	// proves only that the fallback is exact. At least one shape has to have
	// scored candidates through the dirty path, or the suite says nothing about
	// the evaluator it exists to check.
	if maskedTotal == 0 {
		t.Error("no shape scored a candidate through the dirty path; the parity result is vacuous")
	}
}

func reportPolishFixtureArm(t *testing.T, name string, arm polishFixtureArm, fixture polishFixture) {
	t.Helper()

	t.Logf("%s: cost %.12f (gain %.12f), sweeps %d accepted %d, iterations %d, evaluations %d, elapsed %v",
		name, arm.result.BestCost, fixture.BestCost-arm.result.BestCost,
		arm.result.Sweeps, arm.result.AcceptedSweeps,
		arm.result.Iterations, arm.result.Evaluations,
		arm.elapsed.Round(time.Millisecond))
}

// incumbentUnionFraction is the share of the canvas the session's active
// circles really cover in the incumbent, computed the same way the evaluator
// builds its mask. It is the measured counterpart to the preflight's summed
// disc area, which ignores overlap and canvas clipping, so the two together say
// whether a fallback was necessary or merely conservative.
func incumbentUnionFraction(session *polishDirtySession) (float64, float64) {
	vector := fit.ParamVector{
		Data:   session.incumbent,
		K:      session.k,
		Width:  session.width,
		Height: session.height,
	}

	var (
		union    dirtySpanSet
		discArea float64
	)

	union.reset(session.height, max(1, 2*len(session.activeCircles)))

	for _, circle := range session.activeCircles {
		decoded := vector.DecodeCircle(circle)
		session.collectCircleSpans(decoded, &union)

		if decoded.Opacity != 0 && decoded.R > 0 {
			discArea += math.Pi * decoded.R * decoded.R
		}
	}

	pixels, _ := union.metrics()
	canvas := float64(session.width * session.height)

	return float64(pixels) / canvas, discArea / canvas
}

// reportPolishFixtureFractions prints the per-candidate affected-pixel
// distribution the sweep actually produced. This is the production counterpart
// to BenchmarkPolishCandidateCost's single synthetic fraction: the radii here
// come from a real fit, so the distribution says where a real sweep sits
// relative to the 5% fallback gate instead of assuming it.
func reportPolishFixtureFractions(t *testing.T, arm polishFixtureArm) int {
	t.Helper()

	var (
		counts     [len(polishDirtyFractionEdges)]int
		total      int
		masked     int
		fallbacks  int
		preflights int
		sum        float64
		maximum    float64
	)

	for _, session := range arm.sessions {
		total += session.evaluations
		masked += session.maskedEvaluations()
		fallbacks += session.fallbacks
		preflights += session.preflightFallbacks
		sum += session.fractionSum

		if session.fractionMax > maximum {
			maximum = session.fractionMax
		}

		for i, count := range session.fractionCounts {
			counts[i] += count
		}
	}

	t.Logf("dirty sessions: %d, evaluations %d, masked %d, fallbacks %d (preflight %d)",
		len(arm.sessions), total, masked, fallbacks, preflights)

	// Every session in a sweep shares one active set, so one reading of the
	// incumbent union describes the whole sweep.
	if len(arm.sessions) > 0 {
		fraction, estimate := incumbentUnionFraction(arm.sessions[0])
		t.Logf("incumbent active-set union: %.4f%% of canvas; preflight estimate %.2f%% (gate %.0f%%)",
			100*fraction, 100*estimate, 100*polishDirtyPreflightMaxFraction)
	}

	if masked == 0 {
		t.Logf("no evaluation built a scanline mask: every candidate was rejected by the preflight")

		return 0
	}

	t.Logf("affected fraction over masked evaluations: mean %.4f%%, max %.4f%%",
		100*sum/float64(masked), 100*maximum)

	lower := 0.0

	for i, edge := range polishDirtyFractionEdges {
		if counts[i] == 0 {
			lower = edge

			continue
		}

		t.Logf("  [%7.4f%%, %7.4f%%) %8d  %6.2f%%",
			100*lower, 100*edge, counts[i], 100*float64(counts[i])/float64(masked))

		lower = edge
	}

	return masked
}

// TestPolishFixtureActiveSetCoverage measures how much canvas a polishing
// active set covers on the fixture, over every contiguous window of the draw
// order. It runs no optimizer: coverage is a property of the fitted vector and
// the active-set size alone, and it is what decides whether the dirty-region
// evaluator can score a candidate at all.
//
// It exists because TestPolishFixtureDirtyVsFull measures one active set --
// the one weakest-replacement selects -- and one set is an anecdote. This turns
// it into a distribution, so the report can say how often any selector could
// stay under the gate rather than how often this one did.
//
//nolint:paralleltest // reuses one session across windows, so it owns that session exclusively
func TestPolishFixtureActiveSetCoverage(t *testing.T) {
	fixture := loadPolishFixture(t)
	ref := loadPolishFixtureReference(t, fixture)

	cpu := NewCPURenderer(ref, fixture.ActualCircles)
	cpu.SetThreads(1)

	baseline := cloneNRGBA(cpu.Render(fixture.BestParams))

	baselineSSD, exact := fit.ExactSSD(baseline, ref)
	if !exact {
		t.Fatal("baseline SSD is not exact")
	}

	for _, size := range []int{5, 20, 100} {
		// One session is reused across windows: only its active set changes,
		// and the union it measures depends on nothing else. Building 2,000 of
		// them would measure the same thing far more slowly.
		session, ok := newPolishDirtySession(
			cpu, baseline, baselineSSD, fixture.BestParams, makeRange(size)).(*polishDirtySession)
		if !ok {
			t.Fatal("dirty session not installed")
		}

		step := max(1, size/5)

		var (
			windows   int
			underGate int
			sum       float64
			minimum   = math.Inf(1)
			maximum   float64
		)

		for start := 0; start+size <= fixture.ActualCircles; start += step {
			session.activeCircles = makeRangeFrom(start, size)

			fraction, _ := incumbentUnionFraction(session)
			windows++
			sum += fraction

			if fraction < polishDirtyMaxFraction {
				underGate++
			}

			minimum = math.Min(minimum, fraction)
			maximum = math.Max(maximum, fraction)
		}

		t.Logf("active set %3d: %d windows, union min %.4f%% mean %.4f%% max %.4f%%, under the %.0f%% gate in %d (%.1f%%)",
			size, windows, 100*minimum, 100*sum/float64(windows), 100*maximum,
			100*polishDirtyMaxFraction, underGate, 100*float64(underGate)/float64(windows))
	}
}

func makeRange(size int) []int {
	return makeRangeFrom(0, size)
}

func makeRangeFrom(start, size int) []int {
	circles := make([]int, size)
	for i := range circles {
		circles[i] = start + i
	}

	return circles
}

// TestPolishDirtyEnabledByDefault pins the test-only switch shut. It is package
// state that the fixture harness flips, and a leaked false would turn the
// evaluator off for every job without failing anything else.
//
//nolint:paralleltest // reads the package switch another test in this file writes
func TestPolishDirtyEnabledByDefault(t *testing.T) {
	if !polishDirtyEnabled {
		t.Fatal("polishDirtyEnabled is false; the dirty-region evaluator is off in production")
	}

	if polishDirtySessionHook != nil {
		t.Fatal("polishDirtySessionHook is set; production must leave it nil")
	}
}
