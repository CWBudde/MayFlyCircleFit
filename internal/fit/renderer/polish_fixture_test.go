//nolint:testpackage // reaches the unexported dirty-region session, its switch and its telemetry
package renderer

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg" // registered for image.Decode
	_ "image/png"  // the committed reference is a PNG
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
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

	// polishFixtureEnvVar opts into the long harness. It is a measurement, not
	// a gate: it runs for about twenty minutes, which is past the point where
	// Go's default 600 s panic timeout kills the whole package, and the CI
	// rows that run this package natively do not pass -short. So the skip
	// cannot rely on -short alone -- it has to be off unless asked for.
	polishFixtureEnvVar = "CIRCLEFIT_POLISH_FIXTURE"
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

	// perturbPixels nudges every circle centre before the sweep starts, so the
	// sweep has something to recover and can be accepted. A shape that leaves
	// it zero polishes the converged fixture, where no sweep is accepted.
	perturbPixels float64
}

// Four shapes. What decides whether the dirty-region evaluator can
// score a candidate is the active set's canvas coverage, and both the size and
// the selector move it. "default" is what a job gets when the caller sets
// nothing (app.DefaultPolishing*, active set 5, merit selection); "wide" is the
// production-shaped sweep the original 2,111-circle calibration ran; "window"
// keeps the default budget and changes only the selector, because
// TestPolishFixtureActiveSetCoverage shows positional windows of five clear the
// gate where merit-selected sets of five do not. One sweep each: a sweep is the
// unit under test, and repeating it measures the same thing again.
//
// "window-headroom" exists for a different reason. The fixture is converged, so
// no sweep on it is accepted, and a rejected sweep rolls back to exactly the
// vector it started from -- which would let both arms return identical results
// no matter what the evaluator did. This shape perturbs the vector first so the
// sweep has room to improve and commits, which is the only configuration in
// which BestParams is the optimizer's output rather than the input.
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
	{
		name: "window-headroom", strategy: BatchPolishContiguousWindow,
		activeSetSize: 5, iters: 200, popSize: 30, stagnationIters: 100, minImprovement: 0.001,
		perturbPixels: 1.5,
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
	startCost float64
	result    *BatchPolishResult
	elapsed   time.Duration
	sessions  []*polishDirtySession
	decisions []polishSweepDecision
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

	// Restored when the arm returns, not when the test ends: the second arm
	// installs its own recorder, and a t.Cleanup would leave the first one in
	// place to collect the second arm's sweeps as well.
	capture, restoreLogger := capturePolishSweeps(t)
	defer restoreLogger()

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

	start := perturbPolishFixtureParams(fixture.BestParams, shape.perturbPixels)
	startCost := cpu.Cost(start)
	started := time.Now()

	result, err := PolishCircleBatchContext(context.Background(), cpu,
		opt.WithEpochs(optimizer, polishFixtureEpochs),
		start, BatchPolishOptions{
			ActiveSetSize: shape.activeSetSize,
			MaxSweeps:     polishFixtureMaxSweeps,
			Strategy:      shape.strategy,
		})
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("polish sweep: %v", err)
	}

	return polishFixtureArm{
		startCost: startCost,
		result:    result,
		elapsed:   elapsed,
		sessions:  sessions,
		decisions: capture.records(),
	}
}

// perturbPolishFixtureParams nudges every circle centre by a fixed, seedless
// offset so the sweep that follows has something to recover. Deterministic by
// construction -- the offset is a function of the circle index alone -- because
// the two arms must start from bit-identical vectors or the comparison means
// nothing. A zero offset returns the fixture untouched.
func perturbPolishFixtureParams(params []float64, pixels float64) []float64 {
	if pixels == 0 {
		return params
	}

	perturbed := append([]float64(nil), params...)
	for i := 0; i+paramsPerCircle <= len(perturbed); i += paramsPerCircle {
		circle := float64(i / paramsPerCircle)
		perturbed[i] += pixels * math.Sin(circle)
		perturbed[i+1] += pixels * math.Cos(circle)
	}

	return perturbed
}

// polishSweepDecision is what a sweep produced before the transaction decided
// its fate: the cost of the merged candidate the optimizer actually proposed,
// and the reason the sweep was rejected or accepted.
//
// It is the load-bearing observation of this test. BatchPolishResult carries
// only the committed incumbent, and a rejected sweep rolls back to exactly the
// vector it started from -- so on a converged fixture the two arms would return
// identical costs and identical parameters even if the dirty evaluator had sent
// the optimizer somewhere else entirely. Comparing the candidate cost compares
// the optimizer's output instead of the rollback's.
type polishSweepDecision struct {
	sweep         int64
	message       string
	reason        string
	candidateCost float64
	bestCost      float64
	hasCandidate  bool
}

func (d polishSweepDecision) String() string {
	if !d.hasCandidate {
		return fmt.Sprintf("sweep %d: %s (%s), no candidate cost", d.sweep, d.message, d.reason)
	}

	return fmt.Sprintf("sweep %d: %s (%s), candidate %.17g, incumbent %.17g",
		d.sweep, d.message, d.reason, d.candidateCost, d.bestCost)
}

// polishSweepCapture records the sweep decisions PolishCircleBatchContext logs.
// Every path out of the acceptance decision reports at Info with a "sweep"
// attribute, which is the only place the pre-rollback candidate cost is
// observable from outside the package's own call.
type polishSweepCapture struct {
	slog.Handler

	mu   sync.Mutex
	seen []polishSweepDecision
}

// Enabled overrides the discarding delegate, which reports every level off. The
// recorder has to see the records it exists to record.
func (c *polishSweepCapture) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

func (c *polishSweepCapture) Handle(ctx context.Context, record slog.Record) error {
	decision := polishSweepDecision{message: record.Message}

	var (
		isSweep      bool
		candidate    float64
		best         float64
		previous     float64
		hasCandidate bool
		hasPrevious  bool
	)

	record.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "sweep":
			isSweep = true
			decision.sweep = attr.Value.Int64()
		case "reason":
			decision.reason = attr.Value.String()
		case "candidate_cost":
			candidate, hasCandidate = attr.Value.Float64(), true
		case "best_cost":
			best = attr.Value.Float64()
		case "previous_cost":
			previous, hasPrevious = attr.Value.Float64(), true
		}

		return true
	})

	// An accepted sweep names the same two numbers under different keys: the
	// candidate it committed becomes "best_cost" and the incumbent it replaced
	// becomes "previous_cost". Normalize, so an accepted and a rejected sweep
	// are compared on the same pair.
	switch {
	case hasPrevious:
		decision.reason = "accepted"
		decision.candidateCost, decision.bestCost, decision.hasCandidate = best, previous, true
	case hasCandidate:
		decision.candidateCost, decision.bestCost, decision.hasCandidate = candidate, best, true
	}

	if isSweep {
		c.mu.Lock()
		c.seen = append(c.seen, decision)
		c.mu.Unlock()
	}

	return c.Handler.Handle(ctx, record)
}

func (c *polishSweepCapture) records() []polishSweepDecision {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]polishSweepDecision(nil), c.seen...)
}

// capturePolishSweeps redirects the default logger through a recorder for the
// length of one arm. The harness holds package state already and never runs
// beside another test, so replacing the global logger costs nothing extra here.
//
// The recorder delegates to a discarding handler rather than to the previous
// one. Chaining is the obvious thing and it hangs: the standard library's
// default handler writes through the log package, and slog.SetDefault routes
// the log package back into slog, so a wrapper that forwards to it recurses.
// A sweep logs once, and the decisions are printed from the record below, so
// nothing is lost.
func capturePolishSweeps(t *testing.T) (*polishSweepCapture, func()) {
	t.Helper()

	previous := slog.Default()
	capture := &polishSweepCapture{
		Handler: slog.DiscardHandler,
	}

	slog.SetDefault(slog.New(capture))

	return capture, func() { slog.SetDefault(previous) }
}

// TestPolishFixtureDirtyVsFull runs the same production-shaped polishing sweep
// through the dirty-region evaluator and through the full-canvas evaluator and
// requires that they reach exactly the same answer.
//
// Bit-identical is the right bar rather than a tolerance. The dirty evaluator
// returns exactly equal floats by construction, which
// TestPolishDirtySessionMatchesFullCanvas pins per candidate; equal costs mean
// the optimizer saw an identical landscape, so a whole sweep must agree to the
// last bit. A tolerance here would hide the only failure mode worth catching.
//
// The comparison deliberately does not rest on BestCost alone. A sweep that is
// rejected rolls back to the vector it started from, and on a converged fixture
// every sweep is rejected -- so both arms would return the input unchanged even
// if the dirty evaluator had sent the optimizer somewhere else. Three things
// close that gap: the per-sweep candidate cost the optimizer proposed before
// the transaction ruled on it, the iteration and evaluation counts, and the
// "window-headroom" shape, which perturbs the fixture so its sweep commits and
// BestParams is an output rather than an echo of the input.
//
//nolint:paralleltest // flips the package-level dirty-region switch, which no two tests may do at once
func TestPolishFixtureDirtyVsFull(t *testing.T) {
	if testing.Short() || os.Getenv(polishFixtureEnvVar) != "1" {
		t.Skipf("end-to-end polishing sweep on a 2,111-circle fixture; set %s=1 to run it (~21 min)",
			polishFixtureEnvVar)
	}

	// The switch this test flips is package-level, and the rest of the package
	// runs in parallel and assumes the dirty session is installed. So the
	// environment variable is not enough on its own: require that the run was
	// also narrowed with -run, which is what keeps those tests out of the
	// window in which the switch is off.
	requirePolishFixtureSelected(t)

	fixture := loadPolishFixture(t)
	ref := loadPolishFixtureReference(t, fixture)

	workers := min(polishFixtureMaxWorkers, runtime.GOMAXPROCS(0))
	t.Logf("host: GOMAXPROCS=%d, evaluation workers=%d, render threads=1",
		runtime.GOMAXPROCS(0), workers)
	t.Logf("fixture: job %s, %d circles, cost %.12f, reference %s",
		fixture.JobID, fixture.ActualCircles, fixture.BestCost, fixture.Config.RefPath)

	scoredTotal := 0

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

			// The sweep decisions come before the transaction rolls a rejected
			// candidate back, so they are what says the two evaluators sent the
			// optimizer to the same place. On a converged fixture no sweep is
			// accepted and the assertions below reduce to "the rollback works",
			// which is true whatever the evaluator did.
			comparePolishSweepDecisions(t, dirtyArm.decisions, fullArm.decisions)

			if dirtyArm.result.Iterations != fullArm.result.Iterations ||
				dirtyArm.result.Evaluations != fullArm.result.Evaluations {
				t.Errorf("budget differs: dirty %d iterations / %d evaluations, full %d / %d",
					dirtyArm.result.Iterations, dirtyArm.result.Evaluations,
					fullArm.result.Iterations, fullArm.result.Evaluations)
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

			// The shape that perturbs the fixture exists to make the sweep
			// commit; if it stops committing, its parity result has quietly
			// become as weak as the converged shapes' and must not pass
			// silently.
			if shape.perturbPixels != 0 && dirtyArm.result.AcceptedSweeps == 0 {
				t.Error("no sweep was accepted, so BestParams is the input vector " +
					"and the comparison never sees the optimizer's output")
			}

			reportPolishFixtureArm(t, "dirty", dirtyArm)
			reportPolishFixtureArm(t, "full", fullArm)

			if dirtyArm.elapsed > 0 {
				t.Logf("wall clock: dirty %v, full %v, speedup %.2fx",
					dirtyArm.elapsed.Round(time.Millisecond),
					fullArm.elapsed.Round(time.Millisecond),
					fullArm.elapsed.Seconds()/dirtyArm.elapsed.Seconds())
			}

			scoredTotal += reportPolishFixtureFractions(t, dirtyArm)
		})
	}

	// Parity across a sweep whose every candidate fell back to the full canvas
	// proves only that the fallback is exact. At least one shape has to have
	// scored candidates through the dirty path, or the suite says nothing about
	// the evaluator it exists to check.
	if scoredTotal == 0 {
		t.Error("no shape scored a candidate through the dirty path; the parity result is vacuous")
	}
}

// requirePolishFixtureSelected skips unless -run names this test and nothing
// else. Turning the dirty-region evaluator off is safe only while no other
// test in the package is running, and -run is the only lever the test binary
// gives us over that.
func requirePolishFixtureSelected(t *testing.T) {
	t.Helper()

	pattern := ""
	if f := flag.Lookup("test.run"); f != nil {
		pattern = f.Value.String()
	}

	if pattern == "" {
		t.Skipf("flips the package-level dirty-region switch; select it explicitly, "+
			"for example -run '^%s$'", t.Name())
	}

	for _, name := range polishFixtureSiblingTests {
		matched, err := regexp.MatchString(pattern, name)
		if err != nil {
			t.Fatalf("-run pattern %q: %v", pattern, err)
		}

		if matched {
			t.Skipf("-run %q also selects %s, which reads the dirty-region switch "+
				"this test turns off; narrow it, for example -run '^%s$'",
				pattern, name, t.Name())
		}
	}
}

// polishFixtureSiblingTests are the tests in this package that assume the
// dirty-region evaluator is installed. Any -run pattern that reaches one of
// them may run it beside the harness, so the harness stands down instead.
var polishFixtureSiblingTests = []string{
	"TestPolishDirtySessionMatchesFullCanvas",
	"TestPolishDirtySessionFallsBackForLargeAffectedRegion",
	"TestPolishDirtyEnabledByDefault",
	"TestPolishFixtureActiveSetCoverage",
}

// comparePolishSweepDecisions requires the two arms to have produced the same
// sweep outcomes, candidate cost included, to the last bit.
func comparePolishSweepDecisions(t *testing.T, dirty, full []polishSweepDecision) {
	t.Helper()

	if len(dirty) == 0 {
		t.Fatal("no sweep decision was captured, so the pre-rollback comparison is vacuous")
	}

	if len(dirty) != len(full) {
		t.Fatalf("sweep count differs: dirty %d, full %d", len(dirty), len(full))
	}

	proposals := 0

	for i := range dirty {
		if dirty[i].hasCandidate {
			proposals++
		}

		if dirty[i] != full[i] {
			t.Errorf("sweep decision %d differs:\n  dirty %s\n  full  %s", i, dirty[i], full[i])
		}
	}

	// A run in which no sweep ever reached the cost comparison -- every
	// candidate invalid, say -- would pass the loop above without comparing a
	// single optimizer output.
	if proposals == 0 {
		t.Error("no sweep produced a candidate cost; the two arms were never compared on optimizer output")
	}

	t.Logf("sweep decisions: %d captured, %d with a candidate cost, all identical across arms",
		len(dirty), proposals)

	for _, decision := range dirty {
		t.Logf("  %s", decision)
	}
}

func reportPolishFixtureArm(t *testing.T, name string, arm polishFixtureArm) {
	t.Helper()

	t.Logf("%s: cost %.12f (start %.12f, gain %.12f), sweeps %d accepted %d, "+
		"iterations %d, evaluations %d, elapsed %v",
		name, arm.result.BestCost, arm.startCost, arm.startCost-arm.result.BestCost,
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
//
// It returns the number of evaluations the dirty path actually carried to a
// cost, not the number that built a mask: a candidate whose mask cleared the
// preflight and then lost to the 5% gate still fell back to the full canvas,
// so counting it would let the suite's non-vacuity guard pass on a sweep the
// evaluator never scored.
func reportPolishFixtureFractions(t *testing.T, arm polishFixtureArm) int {
	t.Helper()

	var (
		counts     [len(polishDirtyFractionEdges)]int
		total      int
		masked     int
		scored     int
		fallbacks  int
		preflights int
		sum        float64
		maximum    float64
	)

	for _, session := range arm.sessions {
		total += session.evaluations
		masked += session.maskedEvaluations()
		scored += session.scoredEvaluations()
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

	t.Logf("dirty sessions: %d, evaluations %d, masked %d, scored %d, fallbacks %d (preflight %d)",
		len(arm.sessions), total, masked, scored, fallbacks, preflights)

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

	return scored
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

		var (
			windows   int
			underGate int
			sum       float64
			minimum   = math.Inf(1)
			maximum   float64
		)

		// Every window, not a stride sample: the report quotes these minima,
		// maxima and under-gate rates as the complete distribution, and a
		// skipped start could hold any of them.
		for start := 0; start+size <= fixture.ActualCircles; start++ {
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
