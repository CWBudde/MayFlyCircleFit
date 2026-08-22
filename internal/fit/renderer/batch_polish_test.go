package renderer

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

type fixedPolishOptimizer struct {
	params  []float64
	calls   int
	options []opt.RunOptions
}

type incumbentPolishOptimizer struct {
	options []opt.RunOptions
}

func (o *incumbentPolishOptimizer) Run(func([]float64) float64, []float64, []float64, int) ([]float64, float64) {
	return nil, 0
}

func (o *incumbentPolishOptimizer) RunContext(_ context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	o.options = append(o.options, options)
	params := append([]float64(nil), options.Initial.Params...)
	return opt.Result{BestParams: params, BestCost: problem.Eval(params), Iterations: 1, Evaluations: 1}, nil
}

func (o *fixedPolishOptimizer) Run(eval func([]float64) float64, _, _ []float64, _ int) ([]float64, float64) {
	params := append([]float64(nil), o.params...)
	return params, eval(params)
}

func (o *fixedPolishOptimizer) RunContext(_ context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	o.calls++
	o.options = append(o.options, options)
	params := append([]float64(nil), o.params...)
	cost := problem.Eval(params)
	if options.Observer != nil {
		options.Observer(opt.Progress{Iterations: 3, Evaluations: 7, BestParams: params, BestCost: cost})
	}
	return opt.Result{
		BestParams:  params,
		BestCost:    cost,
		Iterations:  3,
		Evaluations: 7,
		Termination: opt.TerminationCompleted,
	}, nil
}

func TestPolishCircleBatchCommitsStrictImprovement(t *testing.T) {
	black := color.NRGBA{A: 255}
	ref := solidImage(5, 5, black)
	base := NewCPURenderer(ref, 1)
	initial := circleParams(2, 2, 5, color.NRGBA{R: 128, G: 128, B: 128, A: 255}, 1)
	replacement := circleParams(2, 2, 5, black, 1)
	optimizer := &fixedPolishOptimizer{params: replacement}

	var observed opt.Progress
	var boundary BatchPolishProgress
	result, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     2,
		Observer:      func(progress opt.Progress) { observed = progress },
		OnSweep:       func(progress BatchPolishProgress) error { boundary = progress; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AcceptedSweeps != 1 || result.Sweeps != 2 || optimizer.calls != 2 {
		t.Fatalf("accepted/sweeps/calls = %d/%d/%d, want 1/2/2", result.AcceptedSweeps, result.Sweeps, optimizer.calls)
	}
	if result.BestCost != 0 || !reflect.DeepEqual(result.BestParams, replacement) {
		t.Fatalf("best result = cost %v params %v, want zero-cost replacement %v", result.BestCost, result.BestParams, replacement)
	}
	if observed.Iterations != 6 || len(observed.BestParams) != len(initial) {
		t.Fatalf("observer progress = %+v, want cumulative iterations and complete params", observed)
	}
	if boundary.Accepted || boundary.Sweep != 2 || !reflect.DeepEqual(boundary.BestParams, replacement) {
		t.Fatalf("rejected boundary did not preserve committed solution: %+v", boundary)
	}
	if got := result.BestImage.NRGBAAt(2, 2); got != black {
		t.Fatalf("best image center = %#v, want %#v", got, black)
	}
}

func TestPolishCircleBatchRollsBackRejectedSweepExactly(t *testing.T) {
	black := color.NRGBA{A: 255}
	ref := solidImage(5, 5, black)
	base := NewCPURenderer(ref, 1)
	initial := circleParams(2, 2, 5, color.NRGBA{R: 96, G: 96, B: 96, A: 255}, 1)
	worse := circleParams(2, 2, 5, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, 1)
	initialCost := base.Cost(initial)

	result, err := PolishCircleBatchContext(context.Background(), base, &fixedPolishOptimizer{params: worse}, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Sweeps != 3 || result.AcceptedSweeps != 0 {
		t.Fatalf("sweeps/accepted = %d/%d, want 3/0", result.Sweeps, result.AcceptedSweeps)
	}
	if result.BestCost != initialCost || !reflect.DeepEqual(result.BestParams, initial) {
		t.Fatalf("rejected sweep changed result: cost %v params %v", result.BestCost, result.BestParams)
	}
	if got, want := result.BestImage.NRGBAAt(2, 2), base.Render(initial).NRGBAAt(2, 2); got != want {
		t.Fatalf("rollback image center = %#v, want %#v", got, want)
	}
}

func TestPolishCircleBatchRejectedSweepsRotateAcrossCircleGroups(t *testing.T) {
	ref := solidImage(12, 4, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	base := NewCPURenderer(ref, 3)
	initial := append(circleParams(1, 1, 1, color.NRGBA{R: 32, A: 255}, 0.2), circleParams(5, 1, 1, color.NRGBA{R: 64, A: 255}, 0.3)...)
	initial = append(initial, circleParams(9, 1, 1, color.NRGBA{R: 96, A: 255}, 0.4)...)
	optimizer := &incumbentPolishOptimizer{}

	result, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     3,
		Strategy:      BatchPolishHybridOverlap,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Sweeps != 3 || result.AcceptedSweeps != 0 || len(optimizer.options) != 3 {
		t.Fatalf("rotating rejected sweeps = %d/%d options=%d, want 3/0/3", result.Sweeps, result.AcceptedSweeps, len(optimizer.options))
	}
	seenX := make(map[float64]bool)
	for _, options := range optimizer.options {
		seenX[options.Initial.Params[0]] = true
	}
	if len(seenX) != 3 {
		t.Fatalf("rejected active groups used x coordinates %v, want all three circles", seenX)
	}
}

func TestPolishCircleBatchMapsEpochBoundariesToCompleteVector(t *testing.T) {
	black := color.NRGBA{A: 255}
	ref := solidImage(5, 5, black)
	base := NewCPURenderer(ref, 1)
	initial := circleParams(2, 2, 5, color.NRGBA{R: 128, G: 128, B: 128, A: 255}, 1)
	replacement := circleParams(2, 2, 5, black, 1)
	optimizer := opt.WithEpochs(&fixedPolishOptimizer{params: replacement}, 2)
	var boundaries []BatchPolishEpoch

	_, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
		OnEpoch: func(boundary BatchPolishEpoch) error {
			boundaries = append(boundaries, boundary)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(boundaries) != 2 {
		t.Fatalf("epoch boundaries = %d, want 2", len(boundaries))
	}
	for epoch, boundary := range boundaries {
		if boundary.Epoch != epoch+1 || len(boundary.BestParams) != len(initial) {
			t.Fatalf("boundary %d = %+v, want complete parameter vector", epoch+1, boundary)
		}
	}
	if boundaries[1].Iterations != 6 || boundaries[1].Evaluations <= boundaries[0].Evaluations {
		t.Fatalf("epoch work is not cumulative: %+v", boundaries)
	}
}

func TestMergeActiveCircleParamsPreservesOriginalDrawSlots(t *testing.T) {
	full := make([]float64, 4*paramsPerCircle)
	for circle := range 4 {
		for parameter := range paramsPerCircle {
			full[circle*paramsPerCircle+parameter] = float64(circle*10 + parameter)
		}
	}
	before := append([]float64(nil), full...)
	active := append(circleParams(1, 1, 1, color.NRGBA{R: 10, A: 255}, 0.5), circleParams(2, 2, 2, color.NRGBA{B: 20, A: 255}, 0.75)...)

	mergeActiveCircleParams(full, []int{1, 3}, active)

	if !reflect.DeepEqual(full[:paramsPerCircle], before[:paramsPerCircle]) ||
		!reflect.DeepEqual(full[2*paramsPerCircle:3*paramsPerCircle], before[2*paramsPerCircle:3*paramsPerCircle]) {
		t.Fatal("inactive circle draw slots changed")
	}
	if !reflect.DeepEqual(full[paramsPerCircle:2*paramsPerCircle], active[:paramsPerCircle]) ||
		!reflect.DeepEqual(full[3*paramsPerCircle:], active[paramsPerCircle:]) {
		t.Fatal("active circles were not restored to their original draw slots")
	}
}

func TestSelectHybridOverlapCirclesCombinesWeakAnchorsAndPartners(t *testing.T) {
	params := append(circleParams(2, 2, 2, color.NRGBA{A: 255}, 1), circleParams(17, 17, 2, color.NRGBA{A: 255}, 1)...)
	params = append(params, circleParams(2, 2, 4, color.NRGBA{A: 255}, 1)...)
	params = append(params, circleParams(17, 17, 4, color.NRGBA{A: 255}, 1)...)
	params = append(params, circleParams(10, 10, 1, color.NRGBA{A: 255}, 1)...)
	audit := BatchAudit{Circles: []CircleAudit{
		{Circle: 1, OriginalCircle: 1, MSEContribution: 1, FinalChangedPixels: 10},
		{Circle: 2, OriginalCircle: 2, MSEContribution: 2, FinalChangedPixels: 10},
		{Circle: 3, OriginalCircle: 3, MSEContribution: 20, FinalChangedPixels: 10},
		{Circle: 4, OriginalCircle: 4, MSEContribution: 30, FinalChangedPixels: 10},
		{Circle: 5, OriginalCircle: 5, MSEContribution: 3, FinalChangedPixels: 10},
	}}

	got := selectHybridOverlapCircles(params, audit, 4, 20, 20, nil)
	want := []int{0, 1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hybrid active set = %v, want weak anchors plus overlap partners %v", got, want)
	}
	retained := removeActiveCircleParams(params, got)
	if !reflect.DeepEqual(retained, params[4*paramsPerCircle:]) {
		t.Fatal("hybrid removal did not preserve the retained draw order")
	}
}

func TestSelectHybridOverlapCirclesRotatesAwayFromVisitedGroup(t *testing.T) {
	params := make([]float64, 6*paramsPerCircle)
	audit := BatchAudit{Circles: make([]CircleAudit, 6)}
	for circle := range 6 {
		copy(params[circle*paramsPerCircle:], circleParams(float64(circle*3), 1, 1, color.NRGBA{A: 255}, 1))
		audit.Circles[circle] = CircleAudit{
			Circle: circle + 1, OriginalCircle: circle + 1,
			MSEContribution: float64(circle + 1), FinalChangedPixels: 1,
		}
	}

	got := selectHybridOverlapCircles(params, audit, 3, 20, 5, map[int]int{0: 1, 1: 1, 2: 1})
	for _, circle := range got {
		if circle < 3 {
			t.Fatalf("rotated active set = %v, want only previously unvisited circles", got)
		}
	}
}

func TestPolishCircleBatchHybridSeedsIncumbentAndResidualAlternative(t *testing.T) {
	ref := solidImage(7, 7, color.NRGBA{A: 255})
	base := NewCPURenderer(ref, 1)
	initial := circleParams(3, 3, 3, color.NRGBA{R: 96, G: 96, B: 96, A: 255}, 1)
	optimizer := &fixedPolishOptimizer{params: initial}

	result, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
		Strategy:      BatchPolishHybridOverlap,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AcceptedSweeps != 0 || len(optimizer.options) != 1 {
		t.Fatalf("hybrid result/options = %+v/%d", result, len(optimizer.options))
	}
	options := optimizer.options[0]
	if options.Initial == nil || !reflect.DeepEqual(options.Initial.Params, initial) {
		t.Fatalf("hybrid incumbent seed = %+v, want current active parameters", options.Initial)
	}
	if len(options.AdditionalSeeds) != 1 || reflect.DeepEqual(options.AdditionalSeeds[0].Params, initial) {
		t.Fatalf("hybrid residual alternatives = %+v, want distinct seed", options.AdditionalSeeds)
	}
	if options.Continuation == nil || options.Continuation.LocalFraction != 1 ||
		options.Continuation.Sigma != 0.02 || options.Continuation.CoordinateRate != 0.2 ||
		options.Continuation.MaxVelocity != 0.02 {
		t.Fatalf("hybrid continuation profile = %+v, want local polishing profile", options.Continuation)
	}
}

func TestHighestResidualRegionSkipsVisitedTiles(t *testing.T) {
	reference := solidImage(8, 8, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	canvas := cloneNRGBA(reference)
	canvas.SetNRGBA(1, 1, color.NRGBA{A: 255})
	canvas.SetNRGBA(6, 6, color.NRGBA{R: 128, G: 128, B: 128, A: 255})

	first, firstIndex, err := highestResidualRegion(canvas, reference, nil)
	if err != nil {
		t.Fatal(err)
	}
	if firstIndex != 0 || first != image.Rect(0, 0, 2, 2) {
		t.Fatalf("first residual region = %v index %d, want top-left tile", first, firstIndex)
	}
	second, secondIndex, err := highestResidualRegion(canvas, reference, map[int]bool{firstIndex: true})
	if err != nil {
		t.Fatal(err)
	}
	if secondIndex != 15 || second != image.Rect(6, 6, 8, 8) {
		t.Fatalf("second residual region = %v index %d, want bottom-right tile", second, secondIndex)
	}
}

func TestSelectResidualRegionActiveSetCombinesWeakSlotAndInfluencer(t *testing.T) {
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	black := color.NRGBA{A: 255}
	canvasRef := solidImage(8, 8, white)
	canvasRenderer := NewCPURenderer(canvasRef, 3)
	params := append(circleParams(7, 7, 1, black, 0.1), circleParams(1, 1, 3, black, 0.5)...)
	params = append(params, circleParams(6, 6, 3, black, 0.8)...)
	reference := cloneNRGBA(canvasRenderer.Render(params))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			reference.SetNRGBA(x, y, black)
		}
	}
	base := NewCPURenderer(reference, 3)

	selection, err := selectResidualRegionActiveSet(base, &incumbentAuditCache{session: base}, params, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if selection.RegionIndex != 0 || !reflect.DeepEqual(selection.ReplacementCircles, []int{0}) {
		t.Fatalf("regional replacement selection = %+v, want weak circle 1 in top-left region", selection)
	}
	if !reflect.DeepEqual(selection.Circles, []int{0, 1}) {
		t.Fatalf("regional active circles = %v, want weak slot and top-left influencer [0 1]", selection.Circles)
	}
}

func TestPolishCircleBatchResidualRegionContinuesAfterRejectedTile(t *testing.T) {
	black := color.NRGBA{A: 255}
	ref := solidImage(5, 5, black)
	base := NewCPURenderer(ref, 1)
	initial := circleParams(2, 2, 5, color.NRGBA{R: 96, G: 96, B: 96, A: 255}, 1)
	optimizer := &fixedPolishOptimizer{params: initial}

	result, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     2,
		Strategy:      BatchPolishResidualRegion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Sweeps != 2 || result.AcceptedSweeps != 0 || optimizer.calls != 2 {
		t.Fatalf("regional sweeps/accepted/calls = %d/%d/%d, want 2/0/2", result.Sweeps, result.AcceptedSweeps, optimizer.calls)
	}
}

func TestPolishCircleBatchValidatesOptions(t *testing.T) {
	ref := solidImage(3, 3, color.NRGBA{A: 255})
	base := NewCPURenderer(ref, 1)
	params := circleParams(1, 1, 1, color.NRGBA{A: 255}, 1)
	tests := []BatchPolishOptions{
		{ActiveSetSize: 0, MaxSweeps: 1},
		{ActiveSetSize: 2, MaxSweeps: 1},
		{ActiveSetSize: 1, MaxSweeps: -1},
		{ActiveSetSize: 1, MaxSweeps: 1, Strategy: "unsupported"},
	}
	for _, options := range tests {
		if _, err := PolishCircleBatchContext(context.Background(), base, &fixedPolishOptimizer{params: params}, params, options); err == nil {
			t.Fatalf("PolishCircleBatchContext options %+v returned nil error", options)
		}
	}
}

// stagedOnlyRenderer forwards staged session creation and in-place compositing
// but hides canvas-seeded sessions, so polishing evaluates the complete parameter
// vector for every candidate the way it did before the fixed prefix was baked.
// Everything else keeps working, which isolates baking in comparisons.
type stagedOnlyRenderer struct {
	Renderer
	rendererSessionFactory
	compositor inPlaceCompositor
}

func newStagedOnlyRenderer(cpu *CPURenderer) stagedOnlyRenderer {
	return stagedOnlyRenderer{Renderer: cpu, rendererSessionFactory: cpu, compositor: cpu}
}

func (s stagedOnlyRenderer) compositeParams(img *image.NRGBA, params []float64, count int) {
	s.compositor.compositeParams(img, params, count)
}

func (s stagedOnlyRenderer) initialCanvas() *image.NRGBA {
	return s.compositor.initialCanvas()
}

// recordingPolishOptimizer evaluates a deterministic spread of candidates around
// the incumbent and keeps every cost it saw, so two runs can be compared
// evaluation by evaluation.
type recordingPolishOptimizer struct {
	steps int
	costs []float64
}

func (o *recordingPolishOptimizer) Run(func([]float64) float64, []float64, []float64, int) ([]float64, float64) {
	return nil, 0
}

func (o *recordingPolishOptimizer) RunContext(_ context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	steps := o.steps
	if steps == 0 {
		steps = 4
	}
	best := append([]float64(nil), options.Initial.Params...)
	bestCost := problem.Eval(best)
	o.costs = append(o.costs, bestCost)
	for step := 1; step <= steps; step++ {
		candidate := append([]float64(nil), options.Initial.Params...)
		for i := range candidate {
			candidate[i] += float64(step) * 0.75 * float64(i%3-1)
		}
		cost := problem.Eval(candidate)
		o.costs = append(o.costs, cost)
		if cost < bestCost {
			best, bestCost = candidate, cost
		}
	}
	return opt.Result{
		BestParams:  best,
		BestCost:    bestCost,
		Iterations:  1,
		Evaluations: len(o.costs),
		Termination: opt.TerminationCompleted,
	}, nil
}

func TestBakedSuffixSessionMatchesFullVector(t *testing.T) {
	const width, height = 20, 16
	ref := solidImage(width, height, color.NRGBA{R: 200, G: 40, B: 90, A: 255})
	canvas := solidImage(width, height, color.NRGBA{R: 20, G: 220, B: 40, A: 255})
	params := polishParityParams()
	circleCount := len(params) / paramsPerCircle

	for _, custom := range []bool{false, true} {
		for prefixCircles := 1; prefixCircles < circleCount; prefixCircles++ {
			var base *CPURenderer
			if custom {
				base = NewCPURendererWithCanvas(ref, canvas, circleCount)
			} else {
				base = NewCPURenderer(ref, circleCount)
			}
			wantImage := cloneNRGBA(base.Render(params))
			wantCost := base.Cost(params)

			suffix, cleanup, ok := bakedSuffixSession(base, params, prefixCircles, circleCount)
			if !ok {
				t.Fatalf("bakedSuffixSession(prefix %d) not applied", prefixCircles)
			}
			gotCost := suffix.Cost(params[prefixCircles*paramsPerCircle:])
			gotImage := suffix.Render(params[prefixCircles*paramsPerCircle:])
			if gotCost != wantCost {
				t.Errorf("custom canvas %v prefix %d: cost = %v, want %v", custom, prefixCircles, gotCost, wantCost)
			}
			if !bytes.Equal(gotImage.Pix, wantImage.Pix) {
				t.Errorf("custom canvas %v prefix %d: baked image differs from full render", custom, prefixCircles)
			}
			cleanup()
		}
	}
}

func TestBakedSuffixSessionSkipsUnsupportedInput(t *testing.T) {
	ref := solidImage(6, 6, color.NRGBA{A: 255})
	params := polishParityParams()
	circleCount := len(params) / paramsPerCircle
	base := NewCPURenderer(ref, circleCount)

	if _, _, ok := bakedSuffixSession(base, params, 0, circleCount); ok {
		t.Error("bakedSuffixSession(prefix 0) applied, want fallback")
	}
	if _, _, ok := bakedSuffixSession(base, params, circleCount, circleCount); ok {
		t.Error("bakedSuffixSession(full prefix) applied, want fallback")
	}
	hidden := newStagedOnlyRenderer(base)
	if _, _, ok := bakedSuffixSession(hidden, params, 1, circleCount); ok {
		t.Error("bakedSuffixSession(unsupported backend) applied, want fallback")
	}
}

func TestPolishCircleBatchBakedPrefixMatchesFullVector(t *testing.T) {
	const width, height = 20, 16
	ref := solidImage(width, height, color.NRGBA{R: 200, G: 40, B: 90, A: 255})
	params := polishParityParams()
	circleCount := len(params) / paramsPerCircle

	run := func(bake bool) (*BatchPolishResult, []float64, [][]int) {
		t.Helper()
		cpu := NewCPURenderer(ref, circleCount)
		cpu.SetThreads(1)
		var base Renderer = cpu
		if !bake {
			base = newStagedOnlyRenderer(cpu)
		}
		optimizer := &recordingPolishOptimizer{}
		var activeSets [][]int
		result, err := PolishCircleBatchContext(context.Background(), base, optimizer, params, BatchPolishOptions{
			ActiveSetSize: 2,
			MaxSweeps:     3,
			Strategy:      BatchPolishHybridOverlap,
			OnSweep: func(progress BatchPolishProgress) error {
				activeSets = append(activeSets, progress.ActiveSet)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("PolishCircleBatchContext(bake %v) error = %v", bake, err)
		}
		return result, optimizer.costs, activeSets
	}

	baked, bakedCosts, bakedSets := run(true)
	full, fullCosts, fullSets := run(false)

	if !reflect.DeepEqual(bakedSets, fullSets) {
		t.Fatalf("active sets = %v, want %v", bakedSets, fullSets)
	}
	// Active sets are one-based, so a minimum above one means circles ahead of the
	// first active slot were baked instead of replayed.
	if !slices.ContainsFunc(bakedSets, func(set []int) bool { return slices.Min(set) > 1 }) {
		t.Fatalf("active sets %v never left a bakeable prefix", bakedSets)
	}
	// A sweep that touches the first circle must still exercise the fallback.
	if !slices.ContainsFunc(bakedSets, func(set []int) bool { return slices.Min(set) == 1 }) {
		t.Fatalf("active sets %v never exercised the unbaked path", bakedSets)
	}
	if !reflect.DeepEqual(bakedCosts, fullCosts) {
		t.Fatalf("evaluated costs = %v, want %v", bakedCosts, fullCosts)
	}
	if baked.BestCost != full.BestCost || !reflect.DeepEqual(baked.BestParams, full.BestParams) {
		t.Fatalf("baked result = cost %v params %v, want cost %v params %v",
			baked.BestCost, baked.BestParams, full.BestCost, full.BestParams)
	}
	if !bytes.Equal(baked.BestImage.Pix, full.BestImage.Pix) {
		t.Fatal("baked polishing image differs from full-vector polishing image")
	}
}

func TestSelectContiguousWindowCirclesPrefersLatestUnvisitedWindowForPartialBudget(t *testing.T) {
	visits := make(map[int]int)
	active := selectContiguousWindowCircles(10, 3, visits, false)
	if !reflect.DeepEqual(active, []int{7, 8, 9}) {
		t.Fatalf("first window = %v, want the last three draw slots", active)
	}
	// Baking depends on the window being consecutive and starting as late as
	// possible: a start of 7 leaves seven circles bakeable instead of none.
	for _, circle := range active {
		visits[circle]++
	}
	if next := selectContiguousWindowCircles(10, 3, visits, false); !reflect.DeepEqual(next, []int{4, 5, 6}) {
		t.Fatalf("second window = %v, want the next unvisited run below it", next)
	}
}

func TestSelectContiguousWindowCirclesCoversEveryCircle(t *testing.T) {
	const circleCount, activeSetSize = 10, 3
	visits := make(map[int]int)
	seen := make(map[int]bool, circleCount)
	// ceil(10/3) sweeps is the point at which every draw slot must have been
	// offered to the optimizer at least once.
	for sweep := 0; sweep < (circleCount+activeSetSize-1)/activeSetSize; sweep++ {
		active := selectContiguousWindowCircles(circleCount, activeSetSize, visits, false)
		for i, circle := range active {
			if i > 0 && circle != active[i-1]+1 {
				t.Fatalf("sweep %d selected non-contiguous window %v", sweep, active)
			}
			seen[circle] = true
			visits[circle]++
		}
	}
	for circle := range circleCount {
		if !seen[circle] {
			t.Fatalf("circle %d was never polished, seen = %v", circle, seen)
		}
	}
}

func TestSelectContiguousWindowCirclesClampsOversizedActiveSet(t *testing.T) {
	active := selectContiguousWindowCircles(3, 5, make(map[int]int), true)
	if !reflect.DeepEqual(active, []int{0, 1, 2}) {
		t.Fatalf("oversized active set = %v, want every circle", active)
	}
}

func TestPlanContiguousWindowsFullCoverageStartsEarlyWithoutCostRegression(t *testing.T) {
	const circleCount, activeSetSize, maxSweeps = 1000, 32, 32
	activeSets, visits, err := PlanContiguousWindows(circleCount, activeSetSize, maxSweeps, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(activeSets[0], integerRange(0, activeSetSize)) {
		t.Fatalf("first full-coverage window = %v, want slots 0-%d", activeSets[0], activeSetSize-1)
	}
	rasterizations := 0
	for sweep, active := range activeSets {
		if len(active) != activeSetSize {
			t.Fatalf("sweep %d active set size = %d, want %d", sweep, len(active), activeSetSize)
		}
		for i := 1; i < len(active); i++ {
			if active[i] != active[i-1]+1 {
				t.Fatalf("sweep %d selected non-contiguous window %v", sweep, active)
			}
		}
		rasterizations += circleCount - active[0]
	}
	for circle, count := range visits {
		if count == 0 {
			t.Fatalf("circle %d was never covered", circle)
		}
	}
	if rasterizations != 16_152 {
		t.Fatalf("rasterizations per candidate = %d, want 16152 (latest-first baseline 16872)", rasterizations)
	}
}

func TestPlanContiguousWindowsDefaultPartialBudgetStaysLatestFirst(t *testing.T) {
	const circleCount, activeSetSize = 1000, 32
	activeSets, _, err := PlanContiguousWindows(circleCount, activeSetSize, app.DefaultPolishingMaxSweeps, nil)
	if err != nil {
		t.Fatal(err)
	}
	rasterizations := 0
	for sweep, active := range activeSets {
		wantStart := circleCount - (sweep+1)*activeSetSize
		if active[0] != wantStart {
			t.Fatalf("sweep %d starts at %d, want current latest-first start %d", sweep, active[0], wantStart)
		}
		rasterizations += circleCount - active[0]
	}
	wantRasterizations := activeSetSize * app.DefaultPolishingMaxSweeps * (app.DefaultPolishingMaxSweeps + 1) / 2
	if rasterizations != wantRasterizations {
		t.Fatalf("partial-budget rasterizations = %d, want current cost %d", rasterizations, wantRasterizations)
	}
}

func TestPlanContiguousWindowsContinuationStartsOnParentUnvisitedSlots(t *testing.T) {
	parentSets, parentVisits, err := PlanContiguousWindows(10, 3, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	childSets, _, err := PlanContiguousWindows(10, 3, 1, parentVisits)
	if err != nil {
		t.Fatal(err)
	}
	parentCovered := make(map[int]bool)
	for _, active := range parentSets {
		for _, circle := range active {
			parentCovered[circle] = true
		}
	}
	for _, circle := range childSets[0] {
		if parentCovered[circle] {
			t.Fatalf("child active set %v revisits parent sets %v", childSets[0], parentSets)
		}
	}
}

func TestPolishCircleBatchContiguousWindowUsesInitialVisitCounts(t *testing.T) {
	const width, height, circleCount = 20, 16, 3
	ref := solidImage(width, height, color.NRGBA{R: 200, G: 40, B: 90, A: 255})
	params := deterministicParams(circleCount, width, height, 1608)
	initialVisits := []int{0, 0, 1}
	var selected []int
	_, err := PolishCircleBatchContext(
		context.Background(),
		NewCPURenderer(ref, circleCount),
		&fixedPolishOptimizer{params: params},
		params,
		BatchPolishOptions{
			ActiveSetSize:      1,
			MaxSweeps:          1,
			Strategy:           BatchPolishContiguousWindow,
			InitialVisitCounts: initialVisits,
			OnSweep: func(progress BatchPolishProgress) error {
				selected = append([]int(nil), progress.ActiveSet...)
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, []int{2}) {
		t.Fatalf("selected active set = %v, want the latest unvisited draw slot 2", selected)
	}
	if !reflect.DeepEqual(initialVisits, []int{0, 0, 1}) {
		t.Fatalf("initial visit counts were mutated: %v", initialVisits)
	}
}

func TestPlanContiguousWindowsRejectsInvalidVisitCounts(t *testing.T) {
	for _, initial := range [][]int{{0, 0}, {0, -1, 0}} {
		if _, _, err := PlanContiguousWindows(3, 1, 1, initial); !errors.Is(err, ErrInvalidOptimizationInput) {
			t.Fatalf("PlanContiguousWindows(initial %v) error = %v, want invalid input", initial, err)
		}
	}
}

func integerRange(start, count int) []int {
	values := make([]int, count)
	for i := range values {
		values[i] = start + i
	}
	return values
}

func TestPolishCircleBatchContiguousWindowBakesPrefixAndMatchesFullVector(t *testing.T) {
	const width, height = 20, 16
	ref := solidImage(width, height, color.NRGBA{R: 200, G: 40, B: 90, A: 255})
	params := polishParityParams()
	circleCount := len(params) / paramsPerCircle

	run := func(bake bool) (*BatchPolishResult, []float64, [][]int) {
		t.Helper()
		cpu := NewCPURenderer(ref, circleCount)
		cpu.SetThreads(1)
		var base Renderer = cpu
		if !bake {
			base = newStagedOnlyRenderer(cpu)
		}
		optimizer := &recordingPolishOptimizer{}
		var activeSets [][]int
		result, err := PolishCircleBatchContext(context.Background(), base, optimizer, params, BatchPolishOptions{
			ActiveSetSize: 2,
			MaxSweeps:     3,
			Strategy:      BatchPolishContiguousWindow,
			OnSweep: func(progress BatchPolishProgress) error {
				activeSets = append(activeSets, progress.ActiveSet)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("PolishCircleBatchContext(bake %v) error = %v", bake, err)
		}
		return result, optimizer.costs, activeSets
	}

	baked, bakedCosts, bakedSets := run(true)
	full, fullCosts, fullSets := run(false)

	if !reflect.DeepEqual(bakedSets, fullSets) {
		t.Fatalf("active sets = %v, want %v", bakedSets, fullSets)
	}
	// Active sets are one-based. Three two-circle sweeps cover this five-circle
	// vector, so the value-first traversal starts at the beginning.
	if !reflect.DeepEqual(bakedSets[0], []int{1, 2}) {
		t.Fatalf("first active set = %v, want the first two draw slots", bakedSets[0])
	}
	for _, set := range bakedSets {
		for i := 1; i < len(set); i++ {
			if set[i] != set[i-1]+1 {
				t.Fatalf("active set %v is not contiguous", set)
			}
		}
	}
	// Sliding the window eventually reaches circle one, which has no bakeable
	// prefix, so both the baked and the fallback path are exercised here.
	if !slices.ContainsFunc(bakedSets, func(set []int) bool { return slices.Min(set) > 1 }) {
		t.Fatalf("active sets %v never left a bakeable prefix", bakedSets)
	}
	if !slices.ContainsFunc(bakedSets, func(set []int) bool { return slices.Min(set) == 1 }) {
		t.Fatalf("active sets %v never exercised the unbaked path", bakedSets)
	}
	// Baking must be a pure speed optimization: every evaluated cost, the final
	// parameters, and the rendered image have to match the unbaked replay.
	if !reflect.DeepEqual(bakedCosts, fullCosts) {
		t.Fatalf("evaluated costs = %v, want %v", bakedCosts, fullCosts)
	}
	if baked.BestCost != full.BestCost || !reflect.DeepEqual(baked.BestParams, full.BestParams) {
		t.Fatalf("baked result = cost %v params %v, want cost %v params %v",
			baked.BestCost, baked.BestParams, full.BestCost, full.BestParams)
	}
	if !bytes.Equal(baked.BestImage.Pix, full.BestImage.Pix) {
		t.Fatal("baked and unbaked polishing produced different images")
	}
}

// TestPolishCircleBatchCommitsImprovementBesideAnUntouchedHarmfulCircle pins the
// acceptance rule that dominates every strategy's usefulness.
//
// A circle outside the active set is copied through a sweep unchanged, so
// demanding that it be useful demands that the sweep repair something it never
// touched. Fitted vectors routinely carry such circles: PruneCircleBatch runs
// per batch stage against that stage's canvas, later stages composite over what
// an earlier stage judged useful, and nothing re-audits the assembled result.
// The absolute gate therefore vetoed every sweep of a long incremental run --
// measured at 12 of 12 rejections with a net cost gain of 0.00 at both 64 and 96
// circles -- no matter how much the candidate improved the cost.
//
// The gate is a non-regression rule instead: the sweep must keep every active
// circle useful and must not add a non-useful circle outside the active set. A
// pre-existing harmful circle that the sweep leaves exactly as it found it no
// longer blocks acceptance. Change this rule deliberately or not at all.
func TestPolishCircleBatchCommitsImprovementBesideAnUntouchedHarmfulCircle(t *testing.T) {
	ref := solidImage(8, 8, color.NRGBA{R: 200, G: 200, B: 200, A: 255})
	base := NewCPURenderer(ref, 2)

	// Circle one is worse than the white background it covers, so removing it
	// improves the image and its contribution is negative. Circle two is the
	// one the sweep repairs.
	harmful := circleParams(2, 2, 2, color.NRGBA{A: 255}, 1)
	initial := append(append([]float64(nil), harmful...),
		circleParams(6, 6, 2, color.NRGBA{R: 100, G: 100, B: 100, A: 255}, 1)...)
	repaired := circleParams(6, 6, 2, color.NRGBA{R: 200, G: 200, B: 200, A: 255}, 1)

	initialCost := base.Cost(initial)
	improved := append(append([]float64(nil), harmful...), repaired...)
	improvedCost := base.Cost(improved)
	if improvedCost >= initialCost {
		t.Fatalf("test setup is vacuous: repaired cost %v is not below %v", improvedCost, initialCost)
	}
	audit, err := AuditCircleBatch(base, improved)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Circles[0].MSEContribution > minBatchMSEContribution {
		t.Fatalf("test setup is vacuous: circle one contributes %v, want it to be harmful",
			audit.Circles[0].MSEContribution)
	}

	// contiguous-window with one active slot selects the last circle, leaving
	// the harmful one untouched.
	result, err := PolishCircleBatchContext(context.Background(), base, &fixedPolishOptimizer{params: repaired}, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
		Strategy:      BatchPolishContiguousWindow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AcceptedSweeps != 1 || result.Sweeps != 1 {
		t.Fatalf("accepted/sweeps = %d/%d, want 1/1: a strict improvement was vetoed by an untouched harmful circle",
			result.AcceptedSweeps, result.Sweeps)
	}
	if result.BestCost != improvedCost || !reflect.DeepEqual(result.BestParams, improved) {
		t.Fatalf("committed sweep = cost %v params %v, want %v and the improved vector",
			result.BestCost, result.BestParams, improvedCost)
	}
}

// TestPolishCircleBatchRejectsImprovementThatKillsAnUntouchedCircle is the other
// half of the non-regression rule: a sweep may inherit a harmful circle, but it
// may not create one outside the active set either. Circle one is useful in the
// incumbent and stays outside the active set, so the sweep never touches its
// parameters; growing the active circle two until it buries circle one lowers
// the cost of the whole vector and still has to be rejected.
func TestPolishCircleBatchRejectsImprovementThatKillsAnUntouchedCircle(t *testing.T) {
	ref := solidImage(16, 16, color.NRGBA{R: 200, G: 200, B: 200, A: 255})
	base := NewCPURenderer(ref, 2)

	// Circle one is a correct patch, so it changes pixels and helps. Circle two
	// is a small, badly coloured blob far from it, and is the circle the sweep
	// optimizes.
	initial := append(
		circleParams(4, 4, 2, color.NRGBA{R: 200, G: 200, B: 200, A: 255}, 1),
		circleParams(11, 11, 3, color.NRGBA{A: 255}, 1)...,
	)
	// Grown circle two paints the reference colour across the whole canvas, which
	// both lowers the cost and buries circle one: circle one is drawn first, so it
	// changes no pixel of the final image once circle two covers it opaquely.
	grown := circleParams(8, 8, 24, color.NRGBA{R: 200, G: 200, B: 200, A: 255}, 1)

	improved := append(append([]float64(nil), initial[:paramsPerCircle]...), grown...)
	initialCost := base.Cost(initial)
	improvedCost := base.Cost(improved)
	if improvedCost >= initialCost {
		t.Fatalf("test setup is vacuous: grown cost %v is not below %v", improvedCost, initialCost)
	}
	incumbentAudit, err := AuditCircleBatch(base, initial)
	if err != nil {
		t.Fatal(err)
	}
	if !circleUseful(incumbentAudit.Circles[0], minBatchMSEContribution) {
		t.Fatalf("test setup is vacuous: circle one is already not useful in the incumbent: %+v",
			incumbentAudit.Circles[0])
	}
	candidateAudit, err := AuditCircleBatch(base, improved)
	if err != nil {
		t.Fatal(err)
	}
	if circleUseful(candidateAudit.Circles[0], minBatchMSEContribution) {
		t.Fatalf("test setup is vacuous: circle one survives the sweep: %+v", candidateAudit.Circles[0])
	}

	// A one-slot contiguous window prefers the latest unvisited window, which is
	// circle two, so circle one is genuinely outside the active set and the
	// optimizer only ever returns the one grown slot.
	result, err := PolishCircleBatchContext(context.Background(), base, &fixedPolishOptimizer{params: grown}, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
		Strategy:      BatchPolishContiguousWindow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AcceptedSweeps != 0 || result.Sweeps != 1 {
		t.Fatalf("accepted/sweeps = %d/%d, want 0/1: the sweep killed an untouched circle outside the active set",
			result.AcceptedSweeps, result.Sweeps)
	}
	if result.BestCost != initialCost || !reflect.DeepEqual(result.BestParams, initial) {
		t.Fatalf("rejected sweep changed the result: cost %v params %v, want %v and the initial vector",
			result.BestCost, result.BestParams, initialCost)
	}
}

// TestIncumbentAuditCacheAdoptsTheCommittedCandidateAudit pins the reason a
// committing sweep does not drop the cache. The acceptance gate audits the
// candidate immediately before it commits, and that candidate is the next
// incumbent, so re-auditing it would be one wasted full render per circle after
// every accepted sweep. The nil session is the assertion: a cache that fell back
// to AuditCircleBatch here would panic.
func TestIncumbentAuditCacheAdoptsTheCommittedCandidateAudit(t *testing.T) {
	cache := &incumbentAuditCache{}
	committed := auditOf(usefulAuditCircle(0), harmfulAuditCircle(1))

	cache.adopt(committed)
	got, err := cache.get(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Circles, committed.Circles) {
		t.Fatalf("cached audit = %+v, want the adopted candidate audit %+v", got.Circles, committed.Circles)
	}
}

// allCirclesUsefulReference is the absolute acceptance predicate the
// non-regression gate replaced. It exists so the equivalence case below can
// assert that the two agree exactly whenever the incumbent carries no
// pre-existing blockers, which is the only situation the old rule described
// correctly.
func allCirclesUsefulReference(audit BatchAudit, minContribution float64) bool {
	for _, circle := range audit.Circles {
		if !circle.Valid || circle.FinalChangedPixels < 1 || circle.MSEContribution <= minContribution {
			return false
		}
	}
	return true
}

func usefulAuditCircle(index int) CircleAudit {
	return CircleAudit{
		Circle:             index + 1,
		OriginalCircle:     index + 1,
		FinalChangedPixels: 100,
		MSEContribution:    1,
		Valid:              true,
	}
}

func harmfulAuditCircle(index int) CircleAudit {
	circle := usefulAuditCircle(index)
	circle.MSEContribution = -0.4
	return circle
}

func auditOf(circles ...CircleAudit) BatchAudit {
	return BatchAudit{Circles: circles}
}

func TestSweepKeepsCirclesUsefulIsANonRegressionRule(t *testing.T) {
	cases := []struct {
		name          string
		incumbent     BatchAudit
		candidate     BatchAudit
		activeCircles []int
		wantOK        bool
		wantBlockers  []int
	}{
		{
			name:          "all useful commits",
			incumbent:     auditOf(usefulAuditCircle(0), usefulAuditCircle(1), usefulAuditCircle(2)),
			candidate:     auditOf(usefulAuditCircle(0), usefulAuditCircle(1), usefulAuditCircle(2)),
			activeCircles: []int{1},
			wantOK:        true,
		},
		{
			name:          "pre-existing blocker outside the active set is excused",
			incumbent:     auditOf(usefulAuditCircle(0), harmfulAuditCircle(1), usefulAuditCircle(2)),
			candidate:     auditOf(usefulAuditCircle(0), harmfulAuditCircle(1), usefulAuditCircle(2)),
			activeCircles: []int{2},
			wantOK:        true,
		},
		{
			name:          "a blocker the sweep introduces outside the active set rejects",
			incumbent:     auditOf(usefulAuditCircle(0), usefulAuditCircle(1), usefulAuditCircle(2)),
			candidate:     auditOf(usefulAuditCircle(0), harmfulAuditCircle(1), usefulAuditCircle(2)),
			activeCircles: []int{2},
			wantOK:        false,
			wantBlockers:  []int{2},
		},
		{
			name:          "a pre-existing blocker inside the active set still rejects",
			incumbent:     auditOf(usefulAuditCircle(0), harmfulAuditCircle(1)),
			candidate:     auditOf(usefulAuditCircle(0), harmfulAuditCircle(1)),
			activeCircles: []int{1},
			wantOK:        false,
			wantBlockers:  []int{2},
		},
		{
			name: "repairing one blocker while inheriting another commits",
			incumbent: auditOf(harmfulAuditCircle(0), harmfulAuditCircle(1),
				harmfulAuditCircle(2), usefulAuditCircle(3)),
			candidate: auditOf(usefulAuditCircle(0), harmfulAuditCircle(1),
				harmfulAuditCircle(2), usefulAuditCircle(3)),
			activeCircles: []int{0, 3},
			wantOK:        true,
		},
		{
			name:          "a zero-pixel circle blocks like a harmful one",
			incumbent:     auditOf(usefulAuditCircle(0), usefulAuditCircle(1)),
			candidate:     auditOf(usefulAuditCircle(0), CircleAudit{Circle: 2, OriginalCircle: 2, Valid: true, MSEContribution: 1}),
			activeCircles: []int{0},
			wantOK:        false,
			wantBlockers:  []int{2},
		},
		{
			name:          "an out-of-bounds circle blocks like a harmful one",
			incumbent:     auditOf(usefulAuditCircle(0), usefulAuditCircle(1)),
			candidate:     auditOf(usefulAuditCircle(0), CircleAudit{Circle: 2, OriginalCircle: 2, FinalChangedPixels: 5, MSEContribution: 1}),
			activeCircles: []int{0},
			wantOK:        false,
			wantBlockers:  []int{2},
		},
		{
			name:          "an incumbent audit of the wrong length excuses nothing",
			incumbent:     auditOf(usefulAuditCircle(0)),
			candidate:     auditOf(usefulAuditCircle(0), harmfulAuditCircle(1)),
			activeCircles: []int{0},
			wantOK:        false,
			wantBlockers:  []int{2},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ok, blockers := sweepKeepsCirclesUseful(
				testCase.incumbent, testCase.candidate, testCase.activeCircles, minBatchMSEContribution)
			if ok != testCase.wantOK || !slices.Equal(blockers, testCase.wantBlockers) {
				t.Fatalf("gate = %v blockers %v, want %v blockers %v",
					ok, blockers, testCase.wantOK, testCase.wantBlockers)
			}
			// Wherever the incumbent carries no blockers at all, the new rule must
			// reproduce the old absolute predicate exactly.
			if allCirclesUsefulReference(testCase.incumbent, minBatchMSEContribution) {
				if want := allCirclesUsefulReference(testCase.candidate, minBatchMSEContribution); ok != want {
					t.Fatalf("gate = %v on a blocker-free incumbent, want the absolute predicate %v", ok, want)
				}
			}
		})
	}
}

// polishParityParams places a large, strong circle first so weak-circle selection
// leaves a non-empty fixed prefix to bake.
func polishParityParams() []float64 {
	params := circleParams(10, 8, 9, color.NRGBA{R: 190, G: 45, B: 85, A: 255}, 1)
	params = append(params, circleParams(4, 4, 4, color.NRGBA{R: 210, G: 60, B: 100, A: 255}, 0.9)...)
	params = append(params, circleParams(15, 5, 3, color.NRGBA{R: 120, G: 30, B: 60, A: 255}, 0.6)...)
	params = append(params, circleParams(6, 12, 3, color.NRGBA{R: 80, G: 200, B: 120, A: 255}, 0.4)...)
	params = append(params, circleParams(16, 12, 2, color.NRGBA{R: 30, G: 90, B: 220, A: 255}, 0.3)...)
	return params
}

// BenchmarkPolishCircleBatchStrategy contrasts a scattered active set against a
// contiguous one on an otherwise identical sweep. Both bake, so the only
// difference is how many circles the selected active set leaves bakeable.
func BenchmarkPolishCircleBatchStrategy(b *testing.B) {
	const width, height = 128, 128
	for _, circleCount := range []int{64, 256} {
		ref := solidImage(width, height, color.NRGBA{R: 60, G: 120, B: 180, A: 255})
		params := benchmarkPolishParams(circleCount, width, height)
		for _, strategy := range []BatchPolishStrategy{BatchPolishHybridOverlap, BatchPolishContiguousWindow} {
			b.Run(string(strategy)+"-"+strconv.Itoa(circleCount), func(b *testing.B) {
				cpu := NewCPURenderer(ref, circleCount)
				cpu.SetThreads(1)
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					_, err := PolishCircleBatchContext(context.Background(), cpu, &recordingPolishOptimizer{steps: 200}, params, BatchPolishOptions{
						ActiveSetSize: 3,
						MaxSweeps:     1,
						Strategy:      strategy,
					})
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func benchmarkPolishParams(circleCount, width, height int) []float64 {
	params := make([]float64, 0, circleCount*paramsPerCircle)
	for i := 0; i < circleCount; i++ {
		params = append(params, circleParams(
			float64((i*37)%width)+0.5,
			float64((i*53)%height)+0.5,
			4+float64(i%7),
			color.NRGBA{R: uint8(i * 3), G: uint8(i * 5), B: uint8(i * 7), A: 255},
			0.4+float64(i%4)/10,
		)...)
	}
	return params
}

func BenchmarkPolishCircleBatch(b *testing.B) {
	const width, height = 128, 128
	for _, circleCount := range []int{64, 256} {
		ref := solidImage(width, height, color.NRGBA{R: 60, G: 120, B: 180, A: 255})
		params := make([]float64, 0, circleCount*paramsPerCircle)
		for i := 0; i < circleCount; i++ {
			params = append(params, circleParams(
				float64((i*37)%width)+0.5,
				float64((i*53)%height)+0.5,
				4+float64(i%7),
				color.NRGBA{R: uint8(i * 3), G: uint8(i * 5), B: uint8(i * 7), A: 255},
				0.4+float64(i%4)/10,
			)...)
		}

		newBase := func(bake bool) Renderer {
			cpu := NewCPURenderer(ref, circleCount)
			cpu.SetThreads(1)
			if bake {
				return cpu
			}
			return newStagedOnlyRenderer(cpu)
		}
		for _, bake := range []bool{true, false} {
			name := "baked-"
			if !bake {
				name = "full-vector-"
			}
			b.Run(name+strconv.Itoa(circleCount), func(b *testing.B) {
				base := newBase(bake)
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					_, err := PolishCircleBatchContext(context.Background(), base, &recordingPolishOptimizer{steps: 200}, params, BatchPolishOptions{
						ActiveSetSize: 3,
						MaxSweeps:     1,
						Strategy:      BatchPolishHybridOverlap,
					})
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func circleParams(x, y, radius float64, c color.NRGBA, opacity float64) []float64 {
	return []float64{
		x,
		y,
		radius,
		float64(c.R) / 255,
		float64(c.G) / 255,
		float64(c.B) / 255,
		opacity,
	}
}

// parallelPolishOptimizer reports a concurrent evaluation width the way a
// MayflyAdapter configured with opt.WithParallelEvaluation does.
type parallelPolishOptimizer struct {
	fixedPolishOptimizer
	workers int
}

func (o *parallelPolishOptimizer) ParallelEvaluationWorkers() int { return o.workers }

// poollessRenderer hides both session factories of a working CPU renderer, so
// nothing can lease an independent session from it while everything else keeps
// behaving. It is the backend shape -- OpenCL today -- for which polishing has
// no pool to build and must keep refusing a concurrent optimizer.
type poollessRenderer struct {
	Renderer
}

// TestPolishCircleBatchRejectsParallelOptimizerWithoutSessionPool guards the
// case that stays unsafe now that polishing pools sessions. A backend that
// cannot hand out independent sessions leaves the sweep evaluator sharing one
// vector and one canvas, and a parallel optimizer would race both plus the
// evaluation counter. Every one of those failures is silent -- a plausible
// wrong cost and a corrupt image, with no error -- so the run must be refused
// rather than degraded to a one-slot pool.
func TestPolishCircleBatchRejectsParallelOptimizerWithoutSessionPool(t *testing.T) {
	ref := solidImage(5, 5, color.NRGBA{A: 255})
	initial := circleParams(2, 2, 5, color.NRGBA{R: 128, G: 128, B: 128, A: 255}, 1)
	base := poollessRenderer{Renderer: NewCPURenderer(ref, 1)}
	optimizer := &parallelPolishOptimizer{workers: 4}

	_, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
	})
	if err == nil {
		t.Fatal("polishing accepted a concurrent optimizer over a renderer that cannot pool sessions")
	}
	if !errors.Is(err, ErrInvalidOptimizationInput) {
		t.Fatalf("error = %v, want ErrInvalidOptimizationInput", err)
	}
	if optimizer.calls != 0 {
		t.Fatalf("optimizer ran %d times, want 0; the guard must refuse before evaluating", optimizer.calls)
	}

	// A serially configured optimizer of the same shape must still run on that
	// same pool-less renderer. The strategy is residual-region because it is the
	// one that never asks the backend for a staged session at all.
	serial := &parallelPolishOptimizer{workers: 1}
	serial.params = circleParams(2, 2, 5, color.NRGBA{A: 255}, 1)
	if _, err := PolishCircleBatchContext(context.Background(), base, serial, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
		Strategy:      BatchPolishResidualRegion,
	}); err != nil {
		t.Fatalf("polishing rejected a serial optimizer: %v", err)
	}

	// A backend that can hand out sessions but does not advertise concurrent
	// evaluation must be refused just as firmly. That is the OpenCL shape: it
	// implements the session factory, so a factory-only guard would let several
	// device sessions evaluate at once, which the backend has never been
	// validated for.
	unadvertised := newStagedOnlyRenderer(NewCPURenderer(ref, 1))
	silent := &parallelPolishOptimizer{workers: 4}
	_, err = PolishCircleBatchContext(context.Background(), unadvertised, silent, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
	})
	if !errors.Is(err, ErrInvalidOptimizationInput) {
		t.Fatalf("error = %v, want ErrInvalidOptimizationInput for a backend without a parallel marker", err)
	}
	if silent.calls != 0 {
		t.Fatalf("optimizer ran %d times, want 0; the guard must refuse before evaluating", silent.calls)
	}

	// So must a concurrent optimizer over a renderer that can pool sessions.
	pooled := &parallelPolishOptimizer{workers: 4}
	pooled.params = circleParams(2, 2, 5, color.NRGBA{A: 255}, 1)
	if _, err := PolishCircleBatchContext(context.Background(), NewCPURenderer(ref, 1), pooled, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
	}); err != nil {
		t.Fatalf("polishing rejected a concurrent optimizer over a poolable renderer: %v", err)
	}
}

// sessionCounts records how a run created its sessions: sessions counts the
// ones rendered from scratch, which is what baking a fixed prefix costs, and
// canvasSessions the ones started from an already-baked canvas.
type sessionCounts struct {
	sessions       atomic.Int64
	canvasSessions atomic.Int64
}

// sessionCountingRenderer is a working CPU renderer that counts how its
// sessions were created, so a test can tell one bake per sweep from one bake
// per pooled worker.
type sessionCountingRenderer struct {
	*CPURenderer
	counts *sessionCounts
}

func (r sessionCountingRenderer) newSession(circleCount int) (Renderer, func(), error) {
	r.counts.sessions.Add(1)
	return r.CPURenderer.newSession(circleCount)
}

func (r sessionCountingRenderer) newSessionWithCanvas(canvas *image.NRGBA, circleCount int) (Renderer, func(), error) {
	r.counts.canvasSessions.Add(1)
	return r.CPURenderer.newSessionWithCanvas(canvas, circleCount)
}

// TestPolishCircleBatchBakesThePrefixOncePerSweep pins the cost of widening the
// pool. Every slot needs a canvas of its own, but the fixed prefix painted onto
// it is the same for all of them, so it must be rasterized once per sweep. A
// bake per slot would redraw it once per worker during setup -- for
// contiguous-window nearly the whole circle vector -- and eat the throughput the
// pool exists to win.
func TestPolishCircleBatchBakesThePrefixOncePerSweep(t *testing.T) {
	const sweeps = 2
	ref := solidImage(20, 16, color.NRGBA{R: 200, G: 40, B: 90, A: 255})
	params := polishParityParams()
	circleCount := len(params) / paramsPerCircle

	run := func(workers int) (int, int) {
		t.Helper()
		cpu := NewCPURenderer(ref, circleCount)
		cpu.SetThreads(1)
		counts := &sessionCounts{}
		base := sessionCountingRenderer{CPURenderer: cpu, counts: counts}
		optimizer := &widthPolishOptimizer{workers: workers}
		if _, err := PolishCircleBatchContext(context.Background(), base, optimizer, params, BatchPolishOptions{
			ActiveSetSize: 2,
			MaxSweeps:     sweeps,
			// The window slides toward the front of the vector, so with this many
			// sweeps every one of them keeps a non-empty prefix and bakes. A sweep
			// that cannot bake would open a full session per slot instead, which
			// would blur the very counts this test compares.
			Strategy: BatchPolishContiguousWindow,
		}); err != nil {
			t.Fatalf("PolishCircleBatchContext(width %d) error = %v", workers, err)
		}
		return int(counts.sessions.Load()), int(counts.canvasSessions.Load())
	}

	serialRendered, serialBaked := run(1)
	if serialBaked != sweeps {
		t.Fatalf("serial run started %d sessions from a baked canvas, want one per sweep (%d)", serialBaked, sweeps)
	}
	pooledRendered, pooledBaked := run(4)
	if pooledRendered != serialRendered {
		t.Errorf("width 4 rendered %d sessions, want the serial run's %d: the fixed prefix must be baked once per sweep, not once per worker",
			pooledRendered, serialRendered)
	}
	if pooledBaked <= serialBaked {
		t.Errorf("width 4 started %d sessions from the baked canvas, want more than the serial run's %d: every slot needs its own canvas",
			pooledBaked, serialBaked)
	}
}

// widthPolishOptimizer is recordingPolishOptimizer with a declared concurrent
// evaluation width. It still evaluates serially, so any difference between two
// widths is the pool's doing and not the optimizer's.
type widthPolishOptimizer struct {
	recordingPolishOptimizer
	workers int
}

func (o *widthPolishOptimizer) ParallelEvaluationWorkers() int { return o.workers }

// polishParityOptimizer is a deterministic optimizer that reports the pool
// width it wants and keeps every cost it evaluated, so two runs can be compared
// evaluation by evaluation.
type polishParityOptimizer interface {
	opt.Optimizer
	evaluatedCosts() []float64
}

func (o *recordingPolishOptimizer) evaluatedCosts() []float64 { return o.costs }
func (o *improvingPolishOptimizer) evaluatedCosts() []float64 { return o.costs }

// polishSerialResult is what the pre-pool serial implementation produced for a
// parity fixture. The numbers were captured by running the same fixture against
// the serial evaluator at b0185a0, the commit before the session pool, so a
// pooled run at width one has something to be byte-identical to that does not
// come from the pooled code itself.
type polishSerialResult struct {
	costs       []float64
	params      []float64
	cost        float64
	accepted    int
	sweeps      int
	evaluations int
}

// TestPolishCircleBatchPoolWidthParity is the parity check for the session
// pool. The evaluator now merges into a leased scratch vector and evaluates on
// a leased baked-suffix session instead of one shared pair, so two properties
// have to hold. Width one must reproduce the recorded serial run exactly, and
// for an optimizer that calls the objective in a fixed order every wider pool
// must reproduce width one exactly: the same evaluated costs, the same
// committed parameters and cost, the same accepted-sweep count, and the same
// rendered image.
//
// How the goldens in the `serial` fields were produced, so that no reader has
// to take a commit message on trust: a scratch worktree was checked out at
// b0185a0 -- the commit before the session pool, where the sweep evaluator
// still merged into one shared vector and evaluated on one shared session --
// and a throwaway `_test.go` file was added there that runs exactly the
// fixtures below (the same reference images, the same parameter vectors, the
// same `BatchPolishOptions`, the same optimizers, declaring width one, which
// the pre-pool refusal at `batch_polish.go:125` still permits) and prints the
// evaluated costs, best parameters, best cost, accepted-sweep count, sweep
// count and evaluation count as Go literals. Those literals were pasted here
// unchanged. The two `hybrid-overlap` cases already existed and their recorded
// numbers reproduced byte-for-byte in that capture, which is the evidence that
// the capture harness matches the one the original goldens came from.
//
// Three fixtures are an explicit exception to that provenance, and the reader
// should know it. The `inherited-blocker-sweeps-*` cases run on
// polishParityParams, which carries circles that are already not useful, so
// their recorded serial runs changed when the acceptance gate became the
// non-regression rule sweepKeepsCirclesUseful: sweep one now commits a strict
// improvement the old absolute gate vetoed. The pre-pool commit cannot produce
// those numbers, because it predates the gate, so their literals were
// re-captured from the current width-one run and are regression pins rather
// than a pre-pool comparison. What makes that safe to do is that the divergence
// is confined to exactly where the gate acts: sweep one's evaluated costs are
// still bit-identical to the pre-pool capture in all three, and every later
// value differs only because the incumbent the next sweep starts from is now
// the committed candidate. The pool property itself is never asserted against
// this table -- widths two, four, and eight are compared to the live width-one
// run below -- so a stale golden cannot mask a pool regression.
//
// Task 16.8 also re-captured the two contiguous-window cases from the live
// width-one run. Their budgets cover the fixture vectors, so their deterministic
// traversal intentionally changed from latest-first to earliest-first; the
// wider pool widths remain compared against that live serial result below.
//
// Every strategy is covered because they reach the pooled `evaluate` closure by
// different routes: `replacement` and `hybrid-overlap` seed once through
// SeedParamsFromResidual, `residual-region` additionally builds and evaluates a
// merged alternative through mergeReplacementSeedParams before the optimizer
// starts, and `contiguous-window` is the only strategy that guarantees a large
// baked prefix. `residual-region` is the strategy the production chain runs and
// it scatters its active set through the draw order, so it is also covered with
// an active set that contains the first circle and therefore bakes nothing.
func TestPolishCircleBatchPoolWidthParity(t *testing.T) {
	cases := []struct {
		name         string
		reference    *image.NRGBA
		params       []float64
		options      BatchPolishOptions
		newOptimizer func(workers int) polishParityOptimizer
		serial       polishSerialResult
	}{
		{
			// This fixture carries pre-existing blockers, which is the common case
			// for a fitted vector: circles 3, 4, and 5 contribute -89.7, -221.6, and
			// -65.5 against a threshold of 0.01. Under the old absolute gate every
			// sweep was rejected. The non-regression gate lets sweep 2 commit, whose
			// active set [3 5] repairs two of the three blockers while inheriting
			// circle 4 untouched; sweeps 1 and 3 still lose on cost.
			name:      "inherited-blocker-sweeps-hybrid-overlap",
			reference: solidImage(20, 16, color.NRGBA{R: 200, G: 40, B: 90, A: 255}),
			params:    polishParityParams(),
			options: BatchPolishOptions{
				ActiveSetSize: 2,
				MaxSweeps:     3,
				Strategy:      BatchPolishHybridOverlap,
			},
			newOptimizer: func(workers int) polishParityOptimizer {
				return &widthPolishOptimizer{workers: workers}
			},
			serial: polishSerialResult{
				costs: []float64{
					5639.8125, 14598.391666666666, 19012.595833333333, 19172.329166666666, 19118.3375,
					5639.8125, 5545.864583333333, 5503.739583333333, 5470.673958333334, 5456.420833333334,
					5456.420833333334, 18332.851041666665, 23794.814583333333, 24108.763541666667, 24240.4875,
				},
				params: []float64{
					10, 8, 9, 0.7450980392156863, 0.17647058823529413, 0.3333333333333333, 1,
					4, 4, 4, 0.8235294117647058, 0.23529411764705882, 0.39215686274509803, 0.9,
					12, 5, 6, 0, 0.11764705882352941, 1, 0.00392156862745098,
					6, 12, 3, 0.3137254901960784, 0.7843137254901961, 0.47058823529411764, 0.4,
					16, 15, 1, 0.11764705882352941, 1, 0, 0.3,
				},
				cost:        5456.420833333334,
				accepted:    1,
				sweeps:      3,
				evaluations: 22,
			},
		},
		{
			// Every sweep commits here, so the accepted-sweep count is a real
			// assertion rather than a constant zero.
			name:      "accepted-sweeps-hybrid-overlap",
			reference: solidImage(12, 6, color.NRGBA{A: 255}),
			params:    polishAcceptingParams(),
			options: BatchPolishOptions{
				ActiveSetSize: 1,
				MaxSweeps:     4,
				Strategy:      BatchPolishHybridOverlap,
			},
			newOptimizer: func(workers int) polishParityOptimizer {
				return &improvingPolishOptimizer{workers: workers}
			},
			serial: polishSerialResult{
				costs: []float64{
					44018.72222222222, 42125.625, 40754.88888888889, 39749.5,
					39749.5, 38675.375, 37794.61111111111, 37107.208333333336,
					37107.208333333336, 36427.208333333336, 35832.541666666664, 35323.208333333336,
					35323.208333333336, 35209.208333333336, 35100.541666666664, 34997.208333333336,
				},
				params: []float64{
					2, 3, 2, 0.1568627450980392, 0.1568627450980392, 0.1568627450980392, 0.9,
					6, 3, 2, 0.11764705882352941, 0.11764705882352941, 0.11764705882352941, 0.8,
					10, 3, 2, 0.022058823529411766, 0.022058823529411766, 0.022058823529411766, 0.7,
				},
				cost:        34997.208333333336,
				accepted:    4,
				sweeps:      4,
				evaluations: 25,
			},
		},
		{
			// `replacement` seeds the active set from the residual once, the same
			// route `hybrid-overlap` takes, but it selects different circles, so it
			// bakes a different prefix. Like the hybrid-overlap fixture above it
			// runs on the blocker-carrying parity vector, so sweep 1's active set
			// [3 4] commits a strict improvement that the old absolute gate vetoed;
			// sweeps 2 and 3 still lose on cost.
			name:      "inherited-blocker-sweeps-replacement",
			reference: solidImage(20, 16, color.NRGBA{R: 200, G: 40, B: 90, A: 255}),
			params:    polishParityParams(),
			options: BatchPolishOptions{
				ActiveSetSize: 2,
				MaxSweeps:     3,
				Strategy:      BatchPolishWeakestReplacement,
			},
			newOptimizer: func(workers int) polishParityOptimizer {
				return &widthPolishOptimizer{workers: workers}
			},
			serial: polishSerialResult{
				costs: []float64{
					4969.403125, 5292.009375, 5358.133333333333, 5358.133333333333, 5405.978125,
					5472.492708333333, 5786.842708333334, 5849.786458333333, 5847.935416666666, 5894.261458333333,
					20995.696875, 21268.529166666667, 21340.423958333333, 21385, 21385.5375,
				},
				params: []float64{
					10, 8, 9, 0.7450980392156863, 0.17647058823529413, 0.3333333333333333, 1,
					4, 4, 4, 0.8235294117647058, 0.23529411764705882, 0.39215686274509803, 0.9,
					0, 0, 1, 0.5686274509803921, 0, 0, 0.5,
					2, 0, 1, 0.5686274509803921, 0, 0, 0.5,
					16, 12, 2, 0.11764705882352941, 0.35294117647058826, 0.8627450980392157, 0.3,
				},
				cost:        4969.403125,
				accepted:    1,
				sweeps:      3,
				evaluations: 22,
			},
		},
		{
			// The accepting fixture run under `replacement`. It rejects every sweep
			// rather than committing -- `replacement` seeds a different active set
			// than `hybrid-overlap` does on the same three circles -- so the golden
			// records accepted 0. The value of the case is the evaluated-cost
			// sequence, not the acceptance count.
			name:      "accepting-fixture-replacement",
			reference: solidImage(12, 6, color.NRGBA{A: 255}),
			params:    polishAcceptingParams(),
			options: BatchPolishOptions{
				ActiveSetSize: 1,
				MaxSweeps:     4,
				Strategy:      BatchPolishWeakestReplacement,
			},
			newOptimizer: func(workers int) polishParityOptimizer {
				return &improvingPolishOptimizer{workers: workers}
			},
			serial: polishSerialResult{
				costs: []float64{
					48078.25, 48078.25, 48078.25, 48078.25,
					48904.88888888889, 48904.88888888889, 48904.88888888889, 48904.88888888889,
					49073.055555555555, 49073.055555555555, 49073.055555555555, 49073.055555555555,
					48078.25, 48078.25, 48078.25, 48078.25,
				},
				params:      polishAcceptingParams(),
				cost:        44018.72222222222,
				accepted:    0,
				sweeps:      4,
				evaluations: 25,
			},
		},
		{
			// `residual-region` is the strategy the production chain runs. It builds
			// a merged alternative through mergeReplacementSeedParams and evaluates
			// that on the pool before the optimizer starts, which no other strategy
			// does. Its active set here is circles 0 and 3 in draw order, so the
			// baked prefix is one circle of five.
			name:      "rejected-sweeps-residual-region",
			reference: solidImage(20, 16, color.NRGBA{R: 200, G: 40, B: 90, A: 255}),
			params:    polishParityParams(),
			options: BatchPolishOptions{
				ActiveSetSize: 2,
				MaxSweeps:     3,
				Strategy:      BatchPolishResidualRegion,
			},
			newOptimizer: func(workers int) polishParityOptimizer {
				return &widthPolishOptimizer{workers: workers}
			},
			serial: polishSerialResult{
				costs: []float64{
					5639.8125, 14598.391666666666, 19012.595833333333, 19172.329166666666, 19118.3375,
					5639.8125, 14598.391666666666, 19012.595833333333, 19172.329166666666, 19118.3375,
					5639.8125, 14598.391666666666, 19012.595833333333, 19172.329166666666, 19118.3375,
				},
				params:      polishParityParams(),
				cost:        5639.8125,
				accepted:    0,
				sweeps:      3,
				evaluations: 22,
			},
		},
		{
			// `residual-region` with an active set that spans the whole vector, so
			// the first circle is active and the baked prefix is empty. That is the
			// bake path the production strategy reaches most often -- measured
			// min(activeCircles) on the live 512x512 vector is 7 of 256 and 11 of
			// 512 -- and it is the one `hybrid-overlap` never exercised here.
			name:      "empty-prefix-residual-region",
			reference: solidImage(20, 16, color.NRGBA{R: 200, G: 40, B: 90, A: 255}),
			params:    polishParityParams(),
			options: BatchPolishOptions{
				ActiveSetSize: 5,
				MaxSweeps:     2,
				Strategy:      BatchPolishResidualRegion,
			},
			newOptimizer: func(workers int) polishParityOptimizer {
				return &widthPolishOptimizer{workers: workers}
			},
			serial: polishSerialResult{
				costs: []float64{
					5639.8125, 17280.785416666666, 22345.555208333335, 23095.908333333333, 23473.733333333334,
					5639.8125, 17280.785416666666, 22345.555208333335, 23095.908333333333, 23473.733333333334,
				},
				params:      polishParityParams(),
				cost:        5639.8125,
				accepted:    0,
				sweeps:      2,
				evaluations: 15,
			},
		},
		{
			// `residual-region` on the accepting fixture, so the strategy that
			// carries the extra pre-optimizer evaluation is also covered on a run
			// that really commits every sweep.
			name:      "accepted-sweeps-residual-region",
			reference: solidImage(12, 6, color.NRGBA{A: 255}),
			params:    polishAcceptingParams(),
			options: BatchPolishOptions{
				ActiveSetSize: 1,
				MaxSweeps:     4,
				Strategy:      BatchPolishResidualRegion,
			},
			newOptimizer: func(workers int) polishParityOptimizer {
				return &improvingPolishOptimizer{workers: workers}
			},
			serial: polishSerialResult{
				costs: []float64{
					44018.72222222222, 42125.625, 40754.88888888889, 39749.5,
					39749.5, 38675.375, 37794.61111111111, 37107.208333333336,
					37107.208333333336, 36427.208333333336, 35832.541666666664, 35323.208333333336,
					35323.208333333336, 35209.208333333336, 35100.541666666664, 34997.208333333336,
				},
				params: []float64{
					2, 3, 2, 0.1568627450980392, 0.1568627450980392, 0.1568627450980392, 0.9,
					6, 3, 2, 0.11764705882352941, 0.11764705882352941, 0.11764705882352941, 0.8,
					10, 3, 2, 0.022058823529411766, 0.022058823529411766, 0.022058823529411766, 0.7,
				},
				cost:        34997.208333333336,
				accepted:    4,
				sweeps:      4,
				evaluations: 25,
			},
		},
		{
			// `contiguous-window` is the only strategy that guarantees a contiguous
			// active set, so it is the one whose baked prefix is large and whose
			// suffix session therefore carries most of the work. Its full-coverage
			// budget uses the value-first order [1 2], [3 4], [4 5]; the final
			// window commits a strict improvement while the first two are rejected.
			name:      "inherited-blocker-sweeps-contiguous-window",
			reference: solidImage(20, 16, color.NRGBA{R: 200, G: 40, B: 90, A: 255}),
			params:    polishParityParams(),
			options: BatchPolishOptions{
				ActiveSetSize: 2,
				MaxSweeps:     3,
				Strategy:      BatchPolishContiguousWindow,
			},
			newOptimizer: func(workers int) polishParityOptimizer {
				return &widthPolishOptimizer{workers: workers}
			},
			serial: polishSerialResult{
				costs: []float64{
					5639.8125, 16916.385416666668, 21602.385416666668, 21916.28125, 22048.005208333332,
					5639.8125, 5545.21875, 5430.99375, 5353.877083333334, 5378.975,
					5639.8125, 5413.929166666667, 5370.835416666667, 5337.932291666667, 5323.808333333333,
				},
				params: []float64{
					10, 8, 9, 0.7450980392156863, 0.17647058823529413, 0.3333333333333333, 1,
					4, 4, 4, 0.8235294117647058, 0.23529411764705882, 0.39215686274509803, 0.9,
					15, 5, 3, 0.47058823529411764, 0.11764705882352941, 0.23529411764705882, 0.6,
					3, 12, 6, 0, 0.7843137254901961, 1, 0.00392156862745098,
					16, 15, 1, 0.11764705882352941, 1, 0, 0.3,
				},
				cost:        5323.808333333333,
				accepted:    1,
				sweeps:      3,
				evaluations: 22,
			},
		},
		{
			name:      "accepted-sweeps-contiguous-window",
			reference: solidImage(12, 6, color.NRGBA{A: 255}),
			params:    polishAcceptingParams(),
			options: BatchPolishOptions{
				ActiveSetSize: 1,
				MaxSweeps:     4,
				Strategy:      BatchPolishContiguousWindow,
			},
			newOptimizer: func(workers int) polishParityOptimizer {
				return &improvingPolishOptimizer{workers: workers}
			},
			serial: polishSerialResult{
				costs: []float64{
					44018.72222222222, 42125.625, 40754.88888888889, 39749.5,
					39749.5, 38675.375, 37794.61111111111, 37107.208333333336,
					37107.208333333336, 36427.208333333336, 35832.541666666664, 35323.208333333336,
					35323.208333333336, 35170.22222222222, 35008.333333333336, 34891.055555555555,
				},
				params: []float64{
					2, 3, 2, 0.0392156862745098, 0.0392156862745098, 0.0392156862745098, 0.9,
					6, 3, 2, 0.11764705882352941, 0.11764705882352941, 0.11764705882352941, 0.8,
					10, 3, 2, 0.08823529411764706, 0.08823529411764706, 0.08823529411764706, 0.7,
				},
				cost:        34891.055555555555,
				accepted:    4,
				sweeps:      4,
				evaluations: 25,
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			circleCount := len(testCase.params) / paramsPerCircle
			run := func(workers int) (*BatchPolishResult, []float64) {
				t.Helper()
				cpu := NewCPURenderer(testCase.reference, circleCount)
				cpu.SetThreads(1)
				optimizer := testCase.newOptimizer(workers)
				result, err := PolishCircleBatchContext(
					context.Background(), cpu, optimizer, testCase.params, testCase.options)
				if err != nil {
					t.Fatalf("PolishCircleBatchContext(width %d) error = %v", workers, err)
				}
				return result, optimizer.evaluatedCosts()
			}

			serial, serialCosts := run(1)
			want := testCase.serial
			if !reflect.DeepEqual(serialCosts, want.costs) {
				t.Errorf("width 1 evaluated costs = %v, want the serial run's %v", serialCosts, want.costs)
			}
			if !reflect.DeepEqual(serial.BestParams, want.params) {
				t.Errorf("width 1 best params = %v, want the serial run's %v", serial.BestParams, want.params)
			}
			if serial.BestCost != want.cost {
				t.Errorf("width 1 best cost = %v, want the serial run's %v", serial.BestCost, want.cost)
			}
			if serial.AcceptedSweeps != want.accepted || serial.Sweeps != want.sweeps {
				t.Errorf("width 1 accepted/sweeps = %d/%d, want the serial run's %d/%d",
					serial.AcceptedSweeps, serial.Sweeps, want.accepted, want.sweeps)
			}
			if serial.Evaluations != want.evaluations {
				t.Errorf("width 1 evaluations = %d, want the serial run's %d", serial.Evaluations, want.evaluations)
			}

			for _, workers := range []int{2, 4, 8} {
				pooled, pooledCosts := run(workers)
				if !reflect.DeepEqual(pooledCosts, serialCosts) {
					t.Errorf("width %d evaluated costs = %v, want %v", workers, pooledCosts, serialCosts)
				}
				if !reflect.DeepEqual(pooled.BestParams, serial.BestParams) {
					t.Errorf("width %d best params = %v, want %v", workers, pooled.BestParams, serial.BestParams)
				}
				if pooled.BestCost != serial.BestCost {
					t.Errorf("width %d best cost = %v, want %v", workers, pooled.BestCost, serial.BestCost)
				}
				if pooled.AcceptedSweeps != serial.AcceptedSweeps || pooled.Sweeps != serial.Sweeps {
					t.Errorf("width %d accepted/sweeps = %d/%d, want %d/%d",
						workers, pooled.AcceptedSweeps, pooled.Sweeps, serial.AcceptedSweeps, serial.Sweeps)
				}
				if pooled.Evaluations != serial.Evaluations {
					t.Errorf("width %d evaluations = %d, want %d", workers, pooled.Evaluations, serial.Evaluations)
				}
				if !bytes.Equal(pooled.BestImage.Pix, serial.BestImage.Pix) {
					t.Errorf("width %d best image differs from the serial image", workers)
				}
			}
		})
	}
}

// concurrentPolishOptimizer calls the objective from workers goroutines at
// once, the way MayFly's parallel generation loop does. It exists so a polish
// sweep can be driven the way the pool claims to support and checked under
// -race.
type concurrentPolishOptimizer struct {
	workers    int
	candidates int

	costs []float64
}

func (o *concurrentPolishOptimizer) ParallelEvaluationWorkers() int { return o.workers }

func (o *concurrentPolishOptimizer) Run(func([]float64) float64, []float64, []float64, int) ([]float64, float64) {
	return nil, 0
}

func (o *concurrentPolishOptimizer) RunContext(_ context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	candidates := o.candidates
	if candidates == 0 {
		candidates = 4 * o.workers
	}
	best := append([]float64(nil), options.Initial.Params...)
	bestCost := problem.Eval(best)

	var mu sync.Mutex
	var wg sync.WaitGroup
	for worker := range o.workers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for step := worker; step < candidates; step += o.workers {
				candidate := append([]float64(nil), options.Initial.Params...)
				for i := range candidate {
					candidate[i] += float64(step+1) * 0.5 * float64(i%3-1)
				}
				cost := problem.Eval(candidate)
				mu.Lock()
				o.costs = append(o.costs, cost)
				if cost < bestCost {
					best, bestCost = candidate, cost
				}
				mu.Unlock()
			}
		}(worker)
	}
	wg.Wait()
	return opt.Result{
		BestParams:  best,
		BestCost:    bestCost,
		Iterations:  1,
		Evaluations: candidates + 1,
		Termination: opt.TerminationCompleted,
	}, nil
}

// TestPolishCircleBatchPoolServesConcurrentEvaluations drives several sweeps
// with an optimizer that really does call the objective from many goroutines.
// Run under -race it is the check that the leased vectors and sessions are the
// only mutable state an evaluation touches; without a pool this raced the
// shared candidate vector, the session canvas, and the evaluation counter.
//
// Every strategy is driven, not just one, because each reaches the pooled
// evaluator differently: `residual-region` -- the strategy production runs --
// evaluates a merged alternative through mergeReplacementSeedParams before the
// optimizer starts, and its scattered active set leaves a small or empty baked
// prefix, so it leases slots whose suffix session covers nearly the whole
// vector. `contiguous-window` is the opposite extreme.
func TestPolishCircleBatchPoolServesConcurrentEvaluations(t *testing.T) {
	strategies := []BatchPolishStrategy{
		BatchPolishWeakestReplacement,
		BatchPolishHybridOverlap,
		BatchPolishResidualRegion,
		BatchPolishContiguousWindow,
	}
	// Active set size 5 is the whole fixture vector, so the first circle is
	// active and nothing is baked; the small sizes leave a prefix.
	activeSetSizes := []int{2, 5}
	for _, strategy := range strategies {
		for _, activeSetSize := range activeSetSizes {
			name := string(strategy) + "-active-" + strconv.Itoa(activeSetSize)
			t.Run(name, func(t *testing.T) {
				const width, height = 20, 16
				ref := solidImage(width, height, color.NRGBA{R: 200, G: 40, B: 90, A: 255})
				params := polishParityParams()
				circleCount := len(params) / paramsPerCircle
				cpu := NewCPURenderer(ref, circleCount)
				cpu.SetThreads(1)
				initialCost := cpu.Cost(params)

				optimizer := &concurrentPolishOptimizer{workers: 4, candidates: 32}
				sweeps := 0
				result, err := PolishCircleBatchContext(context.Background(), cpu, optimizer, params, BatchPolishOptions{
					ActiveSetSize: activeSetSize,
					MaxSweeps:     3,
					Strategy:      strategy,
					OnSweep:       func(BatchPolishProgress) error { sweeps++; return nil },
				})
				if err != nil {
					t.Fatalf("PolishCircleBatchContext error = %v", err)
				}
				if sweeps != 3 || result.Sweeps != 3 {
					t.Fatalf("sweeps = %d/%d, want 3/3", sweeps, result.Sweeps)
				}
				if len(optimizer.costs) < optimizer.candidates {
					t.Fatalf("optimizer recorded %d costs, want at least %d", len(optimizer.costs), optimizer.candidates)
				}
				// Polishing is transactional, so the committed cost can only improve, and it
				// must be the cost of the committed vector evaluated on the full session.
				if result.BestCost > initialCost {
					t.Fatalf("best cost = %v, want no worse than the initial %v", result.BestCost, initialCost)
				}
				if got := cpu.Cost(result.BestParams); got != result.BestCost {
					t.Fatalf("committed cost = %v, want the full-session cost %v", result.BestCost, got)
				}
				if result.Evaluations < len(optimizer.costs) {
					t.Fatalf("evaluations = %d, want at least the %d the optimizer made", result.Evaluations, len(optimizer.costs))
				}
			})
		}
	}
}

// BenchmarkPolishCircleBatchEvaluationWidth measures one polish sweep at
// several pool widths on one fixture. The optimizer makes the same number of
// evaluations at every width, so the only variable is how many of them run at
// once. Widths above GOMAXPROCS are reported for the record; they cannot
// overlap further on the machine that runs them.
//
// The candidate count is deliberately large. A sweep pays for its pool up
// front -- one baked-suffix session, and one canvas, per slot -- and pays a
// serial active-set selection that this change does not touch, so a sweep with
// only a few hundred candidates measures that fixed cost rather than the
// evaluation throughput. A real sweep runs iters*popSize candidates, where the
// fixed cost is noise.
func BenchmarkPolishCircleBatchEvaluationWidth(b *testing.B) {
	const width, height = 128, 128
	const circleCount = 256
	ref := solidImage(width, height, color.NRGBA{R: 60, G: 120, B: 180, A: 255})
	params := benchmarkPolishParams(circleCount, width, height)
	for _, workers := range []int{1, 8, 48} {
		b.Run("width-"+strconv.Itoa(workers), func(b *testing.B) {
			cpu := NewCPURenderer(ref, circleCount)
			cpu.SetThreads(1)
			b.ReportAllocs()
			for range b.N {
				_, err := PolishCircleBatchContext(context.Background(), cpu,
					&concurrentPolishOptimizer{workers: workers, candidates: 1920}, params, BatchPolishOptions{
						ActiveSetSize: 3,
						MaxSweeps:     1,
						Strategy:      BatchPolishHybridOverlap,
					})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// improvingPolishOptimizer walks the active circles' colors toward black in
// fixed steps, so on a black reference every candidate it proposes is a strict
// improvement that leaves the geometry -- and therefore every circle's
// usefulness -- intact. It is what makes a parity fixture commit sweeps
// instead of rejecting all of them.
type improvingPolishOptimizer struct {
	workers int
	costs   []float64
}

func (o *improvingPolishOptimizer) ParallelEvaluationWorkers() int {
	return max(o.workers, 1)
}

func (o *improvingPolishOptimizer) Run(func([]float64) float64, []float64, []float64, int) ([]float64, float64) {
	return nil, 0
}

func (o *improvingPolishOptimizer) RunContext(_ context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	best := append([]float64(nil), options.Initial.Params...)
	bestCost := problem.Eval(best)
	o.costs = append(o.costs, bestCost)
	for step := 1; step <= 3; step++ {
		candidate := append([]float64(nil), options.Initial.Params...)
		for circle := range len(candidate) / paramsPerCircle {
			for channel := 3; channel < 6; channel++ {
				candidate[circle*paramsPerCircle+channel] *= 1 - 0.25*float64(step)
			}
		}
		cost := problem.Eval(candidate)
		o.costs = append(o.costs, cost)
		if cost < bestCost {
			best, bestCost = candidate, cost
		}
	}
	return opt.Result{
		BestParams:  best,
		BestCost:    bestCost,
		Iterations:  1,
		Evaluations: len(o.costs),
		Termination: opt.TerminationCompleted,
	}, nil
}

// polishAcceptingParams is a fixture whose sweeps really commit: three
// separated circles that are each individually useful against a black
// reference, so the whole-vector usefulness gate does not veto an improvement
// the way it does for polishParityParams.
func polishAcceptingParams() []float64 {
	params := circleParams(2, 3, 2, color.NRGBA{R: 160, G: 160, B: 160, A: 255}, 0.9)
	params = append(params, circleParams(6, 3, 2, color.NRGBA{R: 120, G: 120, B: 120, A: 255}, 0.8)...)
	params = append(params, circleParams(10, 3, 2, color.NRGBA{R: 90, G: 90, B: 90, A: 255}, 0.7)...)
	return params
}
