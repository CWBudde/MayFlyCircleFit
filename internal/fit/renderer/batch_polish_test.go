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
	"testing"

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

	selection, err := selectResidualRegionActiveSet(base, params, 2, nil)
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

func TestSelectContiguousWindowCirclesPrefersLatestUnvisitedWindow(t *testing.T) {
	visits := make(map[int]int)
	active := selectContiguousWindowCircles(10, 3, visits)
	if !reflect.DeepEqual(active, []int{7, 8, 9}) {
		t.Fatalf("first window = %v, want the last three draw slots", active)
	}
	// Baking depends on the window being consecutive and starting as late as
	// possible: a start of 7 leaves seven circles bakeable instead of none.
	for _, circle := range active {
		visits[circle]++
	}
	if next := selectContiguousWindowCircles(10, 3, visits); !reflect.DeepEqual(next, []int{4, 5, 6}) {
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
		active := selectContiguousWindowCircles(circleCount, activeSetSize, visits)
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
	active := selectContiguousWindowCircles(3, 5, make(map[int]int))
	if !reflect.DeepEqual(active, []int{0, 1, 2}) {
		t.Fatalf("oversized active set = %v, want every circle", active)
	}
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
	// Active sets are one-based. The first sweep must sit at the end of the draw
	// order, which is the whole point of the strategy.
	if !reflect.DeepEqual(bakedSets[0], []int{circleCount - 1, circleCount}) {
		t.Fatalf("first active set = %v, want the last two draw slots", bakedSets[0])
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

// TestPolishCircleBatchRejectsImprovementWhileAnUntouchedCircleIsHarmful pins the
// acceptance rule that dominates every strategy's usefulness.
//
// A sweep is committed only when allCirclesUseful holds for the complete
// candidate, so a circle outside the active set whose MSEContribution has gone
// negative rejects the sweep even when the sweep strictly lowers the cost of the
// whole vector. Fitted vectors do contain such circles: PruneCircleBatch runs
// per batch stage against that stage's canvas, later stages composite over what
// an earlier stage judged useful, and nothing re-audits the assembled result.
//
// The consequence measured in docs/contiguous-window-polish-report.md is that
// polishing a real batch fit can accept nothing at all while spending its whole
// optimizer budget. Change this rule deliberately or not at all.
func TestPolishCircleBatchRejectsImprovementWhileAnUntouchedCircleIsHarmful(t *testing.T) {
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
	if result.AcceptedSweeps != 0 || result.Sweeps != 1 {
		t.Fatalf("accepted/sweeps = %d/%d, want 0/1: a strict improvement was committed despite a harmful circle",
			result.AcceptedSweeps, result.Sweeps)
	}
	if result.BestCost != initialCost || !reflect.DeepEqual(result.BestParams, initial) {
		t.Fatalf("rejected sweep changed the result: cost %v params %v, want %v and the initial vector",
			result.BestCost, result.BestParams, initialCost)
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
			// Every sweep is rejected here, which is the common case for a fitted
			// vector: the whole-vector usefulness gate vetoes improvements.
			name:      "rejected-sweeps",
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
					5639.8125, 16916.385416666668, 21602.385416666668, 21916.28125, 22048.005208333332,
				},
				params:      polishParityParams(),
				cost:        5639.8125,
				accepted:    0,
				sweeps:      3,
				evaluations: 22,
			},
		},
		{
			// Every sweep commits here, so the accepted-sweep count is a real
			// assertion rather than a constant zero.
			name:      "accepted-sweeps",
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
func TestPolishCircleBatchPoolServesConcurrentEvaluations(t *testing.T) {
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
		ActiveSetSize: 2,
		MaxSweeps:     3,
		Strategy:      BatchPolishHybridOverlap,
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
